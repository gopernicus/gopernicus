package authenticationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	listing "github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// filteredKeys adds the optional apikey.FilteredLister to the route tests' key
// store, filtering through the domain predicate.
type filteredKeys struct{ *memAPIKeys }

func (k filteredKeys) ListByServiceAccountFiltered(_ context.Context, saID string, f apikey.Filter, _ listing.Request) (listing.Page[apikey.APIKey], error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	items := []apikey.APIKey{}
	for _, key := range k.m {
		if key.ServiceAccountID == saID && f.Matches(key) {
			items = append(items, key)
		}
	}
	return listing.Page[apikey.APIKey]{Items: items}, nil
}

// memAccountStatus is the route tests' service-account status table.
type memAccountStatus struct {
	sas  *memServiceAccounts
	keys *memAPIKeys
	m    map[string]serviceaccount.Status
}

func (s *memAccountStatus) status(id string) serviceaccount.Status {
	if st, ok := s.m[id]; ok {
		return st
	}
	return serviceaccount.StatusActive
}
func (s *memAccountStatus) GetStatus(_ context.Context, id string) (serviceaccount.Status, error) {
	return s.status(id), nil
}
func (s *memAccountStatus) Statuses(_ context.Context, ids []string) (map[string]serviceaccount.Status, error) {
	out := map[string]serviceaccount.Status{}
	for _, id := range ids {
		out[id] = s.status(id)
	}
	return out, nil
}
func (s *memAccountStatus) Disable(ctx context.Context, id, _ string, now time.Time) (serviceaccount.StatusChange, error) {
	if _, err := s.sas.Get(ctx, id); err != nil {
		return serviceaccount.StatusChange{}, err
	}
	if s.status(id) == serviceaccount.StatusDisabled {
		return serviceaccount.StatusChange{}, nil
	}
	s.m[id] = serviceaccount.StatusDisabled
	change := serviceaccount.StatusChange{Changed: true}
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	for kid, k := range s.keys.m {
		if k.ServiceAccountID == id && !k.Revoked() {
			k.RevokedAt = now
			s.keys.m[kid] = k
			change.KeysRevoked++
		}
	}
	return change, nil
}
func (s *memAccountStatus) Enable(ctx context.Context, id, _ string, _ time.Time) (serviceaccount.StatusChange, error) {
	if _, err := s.sas.Get(ctx, id); err != nil {
		return serviceaccount.StatusChange{}, err
	}
	if s.status(id) == serviceaccount.StatusActive {
		return serviceaccount.StatusChange{}, nil
	}
	s.m[id] = serviceaccount.StatusActive
	return serviceaccount.StatusChange{Changed: true}, nil
}
func (s *memAccountStatus) Delete(_ context.Context, id string) error {
	delete(s.m, id)
	return nil
}
func (s *memAccountStatus) ProbeSchema(context.Context) error { return nil }

type governanceOptions struct {
	filter bool
	status bool
	maxTTL time.Duration
}

type governanceFixture struct {
	h    http.Handler
	svc  *testService
	sas  *memServiceAccounts
	keys *memAPIKeys
	c    *http.Cookie
}

func newGovernanceFixture(t *testing.T, o governanceOptions) *governanceFixture {
	t.Helper()
	users := newMemUsers()
	sas := &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}}
	keys := &memAPIKeys{m: map[string]apikey.APIKey{}}
	var keyStore apikey.APIKeyRepository = keys
	if o.filter {
		keyStore = filteredKeys{keys}
	}
	fixture := authenticationFixture{
		Users:           users,
		Identifiers:     newMemIdentifiers(users),
		Passwords:       &memPasswords{m: map[string]string{}},
		Sessions:        &memSessions{m: map[string]session.Session{}},
		Hasher:          fakeHasher{},
		Limiter:         ratelimiter.NewMemory(),
		ServiceAccounts: sas,
		APIKeys:         keyStore,
		TokenSigner:     newFakeSigner(),
		Machine:         authlogic.MachineConfig{APIKeyMaxTTL: o.maxTTL, ServiceAccountStatus: o.status},
	}
	if o.status {
		fixture.ServiceAccountStatuses = &memAccountStatus{sas: sas, keys: keys, m: map[string]serviceaccount.Status{}}
	}
	svc := newServiceWithFakes(fixture)
	deps := mountDeps{Auth: svc, ListStrategy: listing.StrategyCursor, MachineGate: allowMachineGate, KeyFilter: svc}
	if o.status {
		deps.AccountStatus = svc
	}
	h := web.NewWebHandler()
	mount(h, deps)
	f := &governanceFixture{h: h, svc: svc, sas: sas, keys: keys}
	f.c = sessionFor(t, h, "governance@example.com")
	return f
}

// keyStatus answers a request bearing key at a plain RequirePrincipal() gate.
func (f *governanceFixture) keyStatus(t *testing.T, key string) int {
	t.Helper()
	h := f.svc.RequirePrincipal()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	return bearer(t, h, "GET", "/x", key).Code
}

func (f *governanceFixture) account(t *testing.T) string {
	t.Helper()
	rec := machinePOST(t, f.h, "/auth/service-accounts", `{"name":"bot"}`, f.c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var sa struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sa)
	if sa.Status != "active" {
		t.Fatalf("created account status = %q, want active", sa.Status)
	}
	return sa.ID
}

func (f *governanceFixture) mint(t *testing.T, saID, body string) (int, map[string]any) {
	t.Helper()
	rec := machinePOST(t, f.h, "/auth/service-accounts/"+saID+"/keys", body, f.c)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (f *governanceFixture) listKeys(t *testing.T, saID, query string) (int, map[string]any) {
	t.Helper()
	rec := do(t, f.h, "GET", "/auth/service-accounts/"+saID+"/keys"+query, "", f.c)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestAPIKeyListingFilters(t *testing.T) {
	f := newGovernanceFixture(t, governanceOptions{filter: true})
	saID := f.account(t)
	ids := map[string]string{}
	for _, name := range []string{"live", "gone", "old"} {
		code, out := f.mint(t, saID, `{"name":"`+name+`"}`)
		if code != http.StatusCreated {
			t.Fatalf("mint %s = %d %v", name, code, out)
		}
		ids[name] = out["id"].(string)
	}
	if err := f.svc.RevokeAPIKey(context.Background(), ids["gone"]); err != nil {
		t.Fatal(err)
	}
	f.keys.mu.Lock()
	old := f.keys.m[ids["old"]]
	old.ExpiresAt = time.Now().Add(-time.Minute)
	f.keys.m[ids["old"]] = old
	f.keys.mu.Unlock()

	names := func(out map[string]any) map[string]string {
		got := map[string]string{}
		for _, item := range out["items"].([]any) {
			row := item.(map[string]any)
			got[row["name"].(string)] = row["status"].(string)
		}
		return got
	}
	code, out := f.listKeys(t, saID, "")
	if got := names(out); code != http.StatusOK || got["live"] != "active" || got["gone"] != "revoked" || got["old"] != "expired" {
		t.Fatalf("unfiltered = %d %v, want every row with its status", code, got)
	}
	for query, want := range map[string]string{"?status=expired": "old", "?status=revoked&never_expires=true": "gone", "?status=active&limit=5": "live"} {
		code, out := f.listKeys(t, saID, query)
		if got := names(out); code != http.StatusOK || len(got) != 1 || got[want] == "" {
			t.Errorf("%s = %d %v, want only %s", query, code, got, want)
		}
	}
	for query, wantCode := range map[string]string{
		"?status=bogus":                      "filter_invalid",
		"?never_expires=maybe":               "filter_invalid",
		"?status=expired&never_expires=true": "filter_contradiction",
		"?status=active&status=expired":      "bad_request",
	} {
		code, out := f.listKeys(t, saID, query)
		if code != http.StatusBadRequest || out["code"] != wantCode {
			t.Errorf("%s = %d %v, want 400 %s", query, code, out, wantCode)
		}
	}

	plain := newGovernanceFixture(t, governanceOptions{})
	plainSA := plain.account(t)
	if code, out := plain.listKeys(t, plainSA, "?status=active"); code != http.StatusBadRequest || out["code"] != "filter_unsupported" {
		t.Fatalf("a store without the lister = %d %v, want 400 filter_unsupported", code, out)
	}
	if code, _ := plain.listKeys(t, plainSA, "?unknown=1"); code != http.StatusOK {
		t.Fatalf("an unknown key = %d, want tolerated", code)
	}
}

func TestMintAPIKeyMaxTTL(t *testing.T) {
	f := newGovernanceFixture(t, governanceOptions{maxTTL: 24 * time.Hour})
	saID := f.account(t)

	before := time.Now()
	code, out := f.mint(t, saID, `{"name":"default"}`)
	if code != http.StatusCreated {
		t.Fatalf("mint without expires_at = %d %v", code, out)
	}
	exp, err := time.Parse(time.RFC3339, out["expires_at"].(string))
	if err != nil || exp.Before(before.Add(24*time.Hour-time.Minute)) || exp.After(time.Now().Add(24*time.Hour)) {
		t.Fatalf("defaulted expires_at = %v (%v), want now + 24h", out["expires_at"], err)
	}

	far := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	rec := machinePOST(t, f.h, "/auth/service-accounts/"+saID+"/keys", `{"name":"far","expires_at":"`+far+`"}`, f.c)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusBadRequest || body["code"] != "api_key_ttl_exceeded" || body["max_ttl_seconds"] != float64(86400) || body["message"] == "" {
		t.Fatalf("beyond the bound = %d %s", rec.Code, rec.Body)
	}
	if maxAt, err := time.Parse(time.RFC3339, body["max_expires_at"].(string)); err != nil || maxAt.After(time.Now().Add(24*time.Hour)) {
		t.Fatalf("max_expires_at = %v (%v)", body["max_expires_at"], err)
	}
	if len(body) != 4 {
		t.Fatalf("body keys = %v, want message, code, max_ttl_seconds, max_expires_at", body)
	}

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if code, out := f.mint(t, saID, `{"name":"past","expires_at":"`+past+`"}`); code != http.StatusBadRequest || out["code"] != "api_key_expiry_past" {
		t.Fatalf("past expires_at = %d %v, want 400 api_key_expiry_past", code, out)
	}
}

func TestServiceAccountDisableEnableRoutes(t *testing.T) {
	f := newGovernanceFixture(t, governanceOptions{status: true})
	saID := f.account(t)
	var keys []string
	for range 2 {
		code, out := f.mint(t, saID, `{"name":"k"}`)
		if code != http.StatusCreated {
			t.Fatalf("mint = %d %v", code, out)
		}
		keys = append(keys, out["key"].(string))
	}
	if code := f.keyStatus(t, keys[0]); code != http.StatusOK {
		t.Fatalf("live key before disable = %d", code)
	}

	rec := machinePOST(t, f.h, "/auth/service-accounts/"+saID+"/disable", `{}`, f.c)
	var got struct {
		ServiceAccount struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"service_account"`
		Changed     bool `json:"changed"`
		KeysRevoked int  `json:"keys_revoked"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got.ServiceAccount.ID != saID || got.ServiceAccount.Status != "disabled" || !got.Changed || got.KeysRevoked != 2 {
		t.Fatalf("disable = %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"keys_revoked":2`) {
		t.Fatalf("body = %s", rec.Body)
	}
	for _, k := range keys {
		if code := f.keyStatus(t, k); code != http.StatusUnauthorized {
			t.Fatalf("key of a disabled account = %d, want 401", code)
		}
	}
	if code, out := f.mint(t, saID, `{"name":"k"}`); code != http.StatusConflict || out["code"] != "service_account_disabled" {
		t.Fatalf("mint on a disabled account = %d %v, want 409", code, out)
	}
	rec = machinePOST(t, f.h, "/auth/service-accounts/"+saID+"/disable", `{}`, f.c)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"changed":false`) {
		t.Fatalf("repeat disable = %d %s", rec.Code, rec.Body)
	}
	list := do(t, f.h, "GET", "/auth/service-accounts", "", f.c)
	if !strings.Contains(list.Body.String(), `"status":"disabled"`) {
		t.Fatalf("listing = %s, want the disabled status", list.Body)
	}

	rec = machinePOST(t, f.h, "/auth/service-accounts/"+saID+"/enable", `{}`, f.c)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"changed":true`) || !strings.Contains(rec.Body.String(), `"status":"active"`) {
		t.Fatalf("enable = %d %s", rec.Code, rec.Body)
	}
	code, out := f.mint(t, saID, `{"name":"fresh"}`)
	if code != http.StatusCreated {
		t.Fatalf("mint after enable = %d %v", code, out)
	}
	if code := f.keyStatus(t, keys[0]); code != http.StatusUnauthorized {
		t.Fatalf("an old key after enable = %d, want still revoked", code)
	}
	if rec := machinePOST(t, f.h, "/auth/service-accounts/missing/disable", `{}`, f.c); rec.Code != http.StatusNotFound {
		t.Fatalf("disable of an unknown account = %d, want 404", rec.Code)
	}

	unwired := newGovernanceFixture(t, governanceOptions{})
	other := unwired.account(t)
	if rec := machinePOST(t, unwired.h, "/auth/service-accounts/"+other+"/disable", `{}`, unwired.c); rec.Code != http.StatusNotFound {
		t.Fatalf("disable unwired = %d, want 404", rec.Code)
	}
	if list := do(t, unwired.h, "GET", "/auth/service-accounts", "", unwired.c); !strings.Contains(list.Body.String(), `"status":"active"`) {
		t.Fatalf("unwired listing = %s, want status active", list.Body)
	}
}

// TestServiceAccountStatusReadFailureIs503 proves an unreadable status never
// reads as a bad key: the key's request answers 503, not 401.
func TestServiceAccountStatusReadFailureIs503(t *testing.T) {
	f := newGovernanceFixture(t, governanceOptions{status: true})
	saID := f.account(t)
	_, out := f.mint(t, saID, `{"name":"k"}`)
	key := out["key"].(string)
	broken := &failingStatus{}
	svc := newServiceWithFakes(authenticationFixture{
		ServiceAccounts:        f.sas,
		APIKeys:                f.keys,
		ServiceAccountStatuses: broken,
		Machine:                authlogic.MachineConfig{ServiceAccountStatus: true},
	})
	h := svc.RequirePrincipal()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	if rec := bearer(t, h, "GET", "/x", key); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a status outage = %d, want 503", rec.Code)
	}
}

type failingStatus struct{ memAccountStatus }

func (*failingStatus) GetStatus(context.Context, string) (serviceaccount.Status, error) {
	return "", sdk.ErrUnavailable
}
func (*failingStatus) ProbeSchema(context.Context) error { return nil }

// TestNeverExpiresFalseIsNoFilter is L2: never_expires=false narrows nothing,
// so it needs no filtered lister.
func TestNeverExpiresFalseIsNoFilter(t *testing.T) {
	f := newGovernanceFixture(t, governanceOptions{})
	saID := f.account(t)
	if code, out := f.listKeys(t, saID, "?never_expires=false"); code != http.StatusOK {
		t.Fatalf("never_expires=false = %d %v, want the unfiltered listing", code, out)
	}
}
