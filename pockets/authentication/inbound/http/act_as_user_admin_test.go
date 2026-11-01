package authenticationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
)

// TestDelegatedActAsUserKeyCannotEraseThroughItsOwner drives the whole
// scenario the v0.39.0 default closes, end to end over the bundled routes: an
// administrator mints, through the machine lifecycle routes, an act-as-user
// key owned by ANOTHER administrator, then presents it as a header-only bearer
// (no Origin, so the browser-safe gate is skipped) on the erase route. Under
// the default it is refused at the authenticator: the eraser is never called,
// the host check never runs, and the request leaves exactly the resolution-time
// apikey_auth success row and the blocked refusal row.
func TestDelegatedActAsUserKeyCannotEraseThroughItsOwner(t *testing.T) {
	eraser := &fakeEraser{}
	f := newPostureFixture(t, RouteAuthentication{}, func(d *mountDeps) { d.Eraser = eraser })
	f.seedAccount("admin-a", "alice@example.com")
	f.seedAccount("admin-b", "bob@example.com")
	f.seedAccount("u-target", "carol@example.com")
	adminA := f.bearerAccessToken(t, "alice@example.com")

	created := f.request("POST", "/auth/service-accounts", `{"name":"delegated","act_as_user":true,"act_as_user_id":"admin-b"}`, adminA, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create act-as-user service account = %d; body=%s", created.Code, created.Body)
	}
	var sa serviceAccountResponse
	if err := json.Unmarshal(created.Body.Bytes(), &sa); err != nil {
		t.Fatalf("decode service account: %v", err)
	}
	if !sa.ActAsUser || sa.OwnerUserID != "admin-b" {
		t.Fatalf("service account = %+v, want act-as-user owned by admin-b", sa)
	}
	minted := f.request("POST", "/auth/service-accounts/"+sa.ID+"/keys", `{"name":"k"}`, adminA, nil)
	if minted.Code != http.StatusCreated {
		t.Fatalf("mint key = %d; body=%s", minted.Code, minted.Body)
	}
	var key mintedKeyResponse
	if err := json.Unmarshal(minted.Body.Bytes(), &key); err != nil {
		t.Fatalf("decode minted key: %v", err)
	}
	f.events.reset()

	if rec := f.request("POST", "/auth/admin/users/u-target/erase", `{}`, key.Key, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("delegated act-as-user key on erase = %d, want 401; body=%s", rec.Code, rec.Body)
	}
	if len(eraser.calls) != 0 {
		t.Errorf("eraser calls = %v, want none", eraser.calls)
	}
	if checks := f.checks(); len(checks) != 0 {
		t.Errorf("host checks = %+v, want none", checks)
	}
	requireRefusalRows(t, f.events.recorded(), mintedKey{raw: key.Key, id: key.ID, prefix: key.KeyPrefix, serviceAccountID: sa.ID}, "UserAdministration")
	if _, ok := f.users.byID["u-target"]; !ok {
		t.Error("the target user is gone")
	}

	// The header is authoritative: the same key beside admin A's own valid
	// session cookie is refused too, never quietly replaced by the cookie.
	cookie := f.login(t, "alice@example.com")
	f.events.reset()
	if rec := f.request("POST", "/auth/admin/users/u-target/erase", `{}`, key.Key, cookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("key beside a valid cookie on erase = %d, want 401; body=%s", rec.Code, rec.Body)
	}
	if len(eraser.calls) != 0 || len(f.checks()) != 0 {
		t.Errorf("eraser calls = %v, host checks = %d; want none", eraser.calls, len(f.checks()))
	}
	for _, e := range f.events.recorded() {
		if e.EventType != securityevent.TypeAPIKeyAuth {
			t.Errorf("unexpected %s/%s row beside the refusal", e.EventType, e.EventStatus)
		}
	}
}

// TestUserAdminCheckRequestCarriesTheCredential pins the summary a host's
// UserAdminCheck reads beside the Principal: under an override that admits
// act-as-user keys the check can tell the key from its owner; a session and a
// self-acting key carry their own kind with nothing invented.
func TestUserAdminCheckRequestCarriesTheCredential(t *testing.T) {
	f := newPostureFixture(t, RouteAuthentication{})
	f = newPostureFixtureWith(t, f, RouteAuthentication{UserAdministration: f.svc.RequirePrincipal(Live())})
	f.seedAccount("u1", "alice@example.com")
	cookie := f.login(t, "alice@example.com")
	act := mintKeyFor(t, f.svc, "personal", true, "u1")
	self := mintKeyFor(t, f.svc, "bot", false, "")

	for _, tc := range []struct {
		name   string
		bearer string
		cookie *http.Cookie
		want   CheckCredential
	}{
		{"act-as-user key", act.raw, nil, CheckCredential{Kind: CredentialAPIKey, ActAsUser: true, APIKeyID: act.id, ServiceAccountID: act.serviceAccountID}},
		{"self-acting key", self.raw, nil, CheckCredential{Kind: CredentialAPIKey, APIKeyID: self.id, ServiceAccountID: self.serviceAccountID}},
		{"session", "", cookie, CheckCredential{Kind: CredentialAccessToken}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.checks())
			if rec := f.request("GET", "/auth/admin/users", "", tc.bearer, tc.cookie); rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
			}
			checks := f.checks()
			if len(checks) != before+1 {
				t.Fatalf("host checks = %d, want %d", len(checks), before+1)
			}
			if got := checks[len(checks)-1].Credential; got != tc.want {
				t.Errorf("Credential = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestInviteCheckRequestCarriesTheCredential pins the same summary on an
// invitation create under an override that admits act-as-user keys.
func TestInviteCheckRequestCarriesTheCredential(t *testing.T) {
	var (
		mu  sync.Mutex
		got []InviteCheckRequest
	)
	f := newPostureFixture(t, RouteAuthentication{}, func(d *mountDeps) {
		d.InviteCheck = func(_ context.Context, req InviteCheckRequest) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, req)
			return nil
		}
	})
	f = newPostureFixtureWith(t, f, RouteAuthentication{Invitations: f.svc.RequirePrincipal(Live())})
	f.seedAccount("u1", "alice@example.com")
	act := mintKeyFor(t, f.svc, "personal", true, "u1")

	rec := f.request("POST", "/auth/invitations/project/p1", `{"identifier":"bob@example.com","relation":"member"}`, act.raw, nil)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("act-as-user key under the override = 401; body=%s", rec.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("invite checks = %d, want 1", len(got))
	}
	want := CheckCredential{Kind: CredentialAPIKey, ActAsUser: true, APIKeyID: act.id, ServiceAccountID: act.serviceAccountID}
	if got[0].Credential != want || got[0].Principal.ID != "u1" {
		t.Errorf("invite check = principal %+v credential %+v, want the owner u1 with %+v", got[0].Principal, got[0].Credential, want)
	}
}
