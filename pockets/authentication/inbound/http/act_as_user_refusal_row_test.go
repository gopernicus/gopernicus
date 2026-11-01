package authenticationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// The refusal row: an act-as-user key RefuseActAsUserKeys refuses leaves the
// resolution-time apikey_auth success row untouched and adds ONE blocked
// apikey_auth row naming the refusing group and the key's ids — never its
// material.

// mintedKey is a key minted outside the lifecycle routes, with the facts a row
// assertion needs.
type mintedKey struct {
	raw, id, prefix, serviceAccountID string
}

// mintKeyFor mints one key on a fresh service account of svc.
func mintKeyFor(t *testing.T, svc *testService, name string, actAsUser bool, ownerUserID string) mintedKey {
	t.Helper()
	ctx := context.Background()
	sa, err := svc.CreateServiceAccount(ctx, "admin", name, "", actAsUser, ownerUserID)
	if err != nil {
		t.Fatalf("CreateServiceAccount(%s): %v", name, err)
	}
	key, raw, err := svc.MintAPIKey(ctx, sa.ID, name, time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey(%s): %v", name, err)
	}
	return mintedKey{raw: raw, id: key.ID, prefix: key.KeyPrefix, serviceAccountID: sa.ID}
}

// requireRefusalRows asserts rows are EXACTLY the apikey_auth success row and
// one blocked refusal row for key on group, the refusal row carrying ids only.
// The refusal row is written while the key is the request's proof, so it also
// carries the via_ attribution; the success row, written before the proof is
// stashed, carries none.
func requireRefusalRows(t *testing.T, rows []securityevent.SecurityEvent, key mintedKey, group string) {
	t.Helper()
	var kinds []string
	for _, e := range rows {
		kinds = append(kinds, e.EventType+"/"+e.EventStatus)
	}
	if len(rows) != 2 || rows[0].EventType != securityevent.TypeAPIKeyAuth || rows[0].EventStatus != securityevent.StatusSuccess ||
		rows[1].EventType != securityevent.TypeAPIKeyAuth || rows[1].EventStatus != securityevent.StatusBlocked {
		t.Fatalf("rows = %v, want exactly [apikey_auth/success apikey_auth/blocked]", kinds)
	}
	blocked := rows[1]
	if blocked.Actor.Type != authlogic.PrincipalServiceAccount || blocked.Actor.ID != key.serviceAccountID {
		t.Errorf("refusal actor = %+v, want {service_account, %s}", blocked.Actor, key.serviceAccountID)
	}
	want := map[string]any{
		"reason":             securityevent.ReasonActAsUserRefused,
		"group":              group,
		"api_key_id":         key.id,
		"service_account_id": key.serviceAccountID,

		securityevent.DetailViaCredential:       string(authlogic.CredentialAPIKey),
		securityevent.DetailViaAPIKeyID:         key.id,
		securityevent.DetailViaServiceAccountID: key.serviceAccountID,
		securityevent.DetailViaActAsUser:        true,
	}
	if _, ok := rows[0].Details[securityevent.DetailViaCredential]; ok {
		t.Errorf("resolution-time success row carries via_ attribution: %v", rows[0].Details)
	}
	if len(blocked.Details) != len(want) {
		t.Errorf("refusal details = %v, want exactly %v", blocked.Details, want)
	}
	for k, v := range want {
		if blocked.Details[k] != v {
			t.Errorf("refusal details[%q] = %v, want %v", k, blocked.Details[k], v)
		}
	}
	serialized, err := json.Marshal(blocked.Details)
	if err != nil {
		t.Fatalf("marshal details: %v", err)
	}
	for _, secret := range []string{key.raw, key.prefix} {
		if strings.Contains(string(serialized), secret) {
			t.Errorf("refusal details %s carry key material %q", serialized, secret)
		}
	}
}

// TestActAsUserRefusalRecordsOneBlockedRow pins the row for each bundled group
// that refuses act-as-user keys by default.
func TestActAsUserRefusalRecordsOneBlockedRow(t *testing.T) {
	for _, tc := range []struct{ group, method, path string }{
		{"UserAdministration", "GET", "/auth/admin/users"},
		{"Invitations", "GET", "/auth/invitations/mine"},
	} {
		t.Run(tc.group, func(t *testing.T) {
			f := newPostureFixture(t, RouteAuthentication{})
			f.seedAccount("u1", "alice@example.com")
			key := mintKeyFor(t, f.svc, "personal", true, "u1")
			f.events.reset()

			if rec := f.request(tc.method, tc.path, "", key.raw, nil); rec.Code != http.StatusUnauthorized {
				t.Fatalf("act-as-user key on %s %s = %d, want 401; body=%s", tc.method, tc.path, rec.Code, rec.Body)
			}
			requireRefusalRows(t, f.events.recorded(), key, tc.group)
		})
	}
}

// TestActAsUserRefusalRowSkipsASelfActingKey proves a self-acting key the same
// surface admits writes its success row and nothing else.
func TestActAsUserRefusalRowSkipsASelfActingKey(t *testing.T) {
	f := newPostureFixture(t, RouteAuthentication{})
	key := mintKeyFor(t, f.svc, "bot", false, "")
	f.events.reset()

	if rec := f.request("GET", "/auth/admin/users", "", key.raw, nil); rec.Code != http.StatusOK {
		t.Fatalf("self-acting key on /auth/admin/users = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	for _, e := range f.events.recorded() {
		if e.EventType == securityevent.TypeAPIKeyAuth && e.EventStatus == securityevent.StatusBlocked {
			t.Errorf("a self-acting key recorded a blocked row: %+v", e)
		}
	}
}

// TestActAsUserRefusalRowNamesTheGroup pins the group label off the defaults: a
// host override of a bundled slot records the slot's name, and a host route
// carrying the option records "route".
func TestActAsUserRefusalRowNamesTheGroup(t *testing.T) {
	f := newPostureFixture(t, RouteAuthentication{})
	f.seedAccount("u1", "alice@example.com")
	key := mintKeyFor(t, f.svc, "personal", true, "u1")

	override := resolveBundledRouteAuth(BundledRouteAuthentication{
		UserAdministration: PrincipalStrategy(Live(), RefuseActAsUserKeys()),
	}, f.svc.Adapter).UserAdministration
	for _, tc := range []struct {
		group string
		mw    func(http.Handler) http.Handler
	}{
		{"UserAdministration", override},
		{"route", f.svc.RequirePrincipal(RefuseActAsUserKeys())},
	} {
		t.Run(tc.group, func(t *testing.T) {
			f.events.reset()
			r := httptest.NewRequest("GET", "/guarded", nil)
			r.Header.Set("Authorization", "Bearer "+key.raw)
			rec := httptest.NewRecorder()
			tc.mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, r)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			requireRefusalRows(t, f.events.recorded(), key, tc.group)
		})
	}
}

// TestActAsUserRefusalRowIsBudgeted proves the refusal row rides the
// failure-row budget: over budget it is counted, not written, while the
// unbudgeted success row still lands.
func TestActAsUserRefusalRowIsBudgeted(t *testing.T) {
	users := newMemUsers()
	spy := newSpySecurityEvents()
	recorder := securityevent.NewRecorder(spy, sdk.IDGenerator{}, nil, nil,
		securityevent.WithFailureRowBudget(denyLimiter{}, ratelimiter.PerMinute(1)))
	svc := newServiceWithFakes(authenticationFixture{
		Users:                 users,
		Limiter:               ratelimiter.NewMemory(),
		ServiceAccounts:       &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:               &memAPIKeys{m: map[string]apikey.APIKey{}},
		SecurityEventRecorder: recorder,
	})
	users.mu.Lock()
	users.byID["u1"] = user.User{ID: "u1"}
	users.mu.Unlock()
	key := mintKeyFor(t, svc, "personal", true, "u1")
	spy.reset()

	r := httptest.NewRequest("GET", "/guarded", nil)
	r.Header.Set("Authorization", "Bearer "+key.raw)
	rec := httptest.NewRecorder()
	svc.RequirePrincipal(RefuseActAsUserKeys())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	rows := spy.recorded()
	if len(rows) != 1 || rows[0].EventType != securityevent.TypeAPIKeyAuth || rows[0].EventStatus != securityevent.StatusSuccess {
		t.Fatalf("rows = %+v, want the success row alone", rows)
	}
	if got := recorder.SuppressedRows(); got != 1 {
		t.Errorf("suppressed rows = %d, want 1", got)
	}
}
