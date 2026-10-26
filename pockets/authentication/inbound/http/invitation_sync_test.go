package authenticationhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// fakeInvitationSyncer records the users it was asked to sync.
type fakeInvitationSyncer struct {
	users    []string
	resolved int
	err      error
}

func (f *fakeInvitationSyncer) SyncInvitations(_ context.Context, userID string) (int, error) {
	f.users = append(f.users, userID)
	return f.resolved, f.err
}

// kindInvitationService records the (kind, value) Mine pages by and fails
// Accept with acceptErr.
type kindInvitationService struct {
	spyInvitationService
	mineKind, mineValue string
	acceptErr           error
}

func (s *kindInvitationService) Mine(_ context.Context, kind, value string, _ list.Request) (list.Page[invitations.Invitation], error) {
	s.mineKind, s.mineValue = kind, value
	return list.Page[invitations.Invitation]{}, nil
}

func (s *kindInvitationService) Accept(context.Context, invitations.AcceptInput) (invitations.AcceptResult, error) {
	return invitations.AcceptResult{}, s.acceptErr
}

// newSyncFixture mounts the invitation surface over inv with syncer (nil
// leaves the sync route unmounted) and returns a logged-in session cookie.
func newSyncFixture(t *testing.T, inv InvitationService, syncer invitationSyncer) (http.Handler, *http.Cookie) {
	t.Helper()
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{"u1": "hash:password123456789"}}
	svc := newServiceWithFakes(authenticationFixture{
		Users: users, Identifiers: idents, Passwords: passwords,
		Sessions: &memSessions{m: map[string]session.Session{}}, Hasher: fakeHasher{},
		Limiter: ratelimiter.NewMemory(), TokenSigner: newFakeSigner(),
	})
	users.mu.Lock()
	users.byID["u1"] = user.User{ID: "u1", DisplayName: "Seed"}
	users.mu.Unlock()
	idents.insert(verifiedEmail("id-primary", "u1", "alice@example.com"))
	h := web.NewWebHandler()
	allow := func(context.Context, InviteCheckRequest) error { return nil }
	mount(h, mountDeps{Auth: svc, Invitations: inv, InviteCheck: allow, InvitationSync: syncer, ListStrategy: list.StrategyCursor})
	f := invitationFixture{h: h}
	return h, f.login(t, "alice@example.com")
}

func postSync(h http.Handler, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/auth/invitations/sync", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "csrf-token"})
	r.Header.Set(csrfHeaderName, "csrf-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestInvitationSyncRoute(t *testing.T) {
	syncer := &fakeInvitationSyncer{resolved: 2}
	h, cookie := newSyncFixture(t, &spyInvitationService{}, syncer)
	rec := postSync(h, cookie)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"resolved":2}` {
		t.Fatalf("sync = %d %s, want 200 {\"resolved\":2}", rec.Code, rec.Body)
	}
	if len(syncer.users) != 1 || syncer.users[0] != "u1" {
		t.Errorf("synced users = %v, want [u1]", syncer.users)
	}

	syncer.err = authlogic.ErrRateLimited
	if rec := postSync(h, cookie); rec.Code != http.StatusTooManyRequests {
		t.Errorf("rate-limited sync = %d, want 429", rec.Code)
	}

	r := httptest.NewRequest("POST", "/auth/invitations/sync", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cookie sync without CSRF = %d, want 403", rec.Code)
	}
}

func TestInvitationSyncRouteAbsentWithoutSyncer(t *testing.T) {
	h, cookie := newSyncFixture(t, &spyInvitationService{}, nil)
	if rec := postSync(h, cookie); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("sync without a syncer = %d, want unmounted", rec.Code)
	}
}

func TestMineIdentifierKind(t *testing.T) {
	inv := &kindInvitationService{}
	h, cookie := newSyncFixture(t, inv, nil)
	if rec := do(t, h, "GET", "/auth/invitations/mine", "", cookie); rec.Code != http.StatusOK || inv.mineKind != sdk.AddressKindEmail || inv.mineValue != "alice@example.com" {
		t.Fatalf("default mine = %d (%s, %s), want 200 by the verified email", rec.Code, inv.mineKind, inv.mineValue)
	}
	if rec := do(t, h, "GET", "/auth/invitations/mine?identifier_kind=email", "", cookie); rec.Code != http.StatusOK {
		t.Errorf("mine?identifier_kind=email = %d, want 200", rec.Code)
	}
	if rec := do(t, h, "GET", "/auth/invitations/mine?identifier_kind=phone", "", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("mine?identifier_kind=phone without a verified phone = %d, want 404", rec.Code)
	}
	if rec := do(t, h, "GET", "/auth/invitations/mine?identifier_kind=chat", "", cookie); rec.Code != http.StatusBadRequest {
		t.Errorf("mine?identifier_kind=chat = %d, want 400", rec.Code)
	}
}

func TestInvitationErrorCodes(t *testing.T) {
	inv := &kindInvitationService{acceptErr: invitations.ErrIdentifierMismatch}
	h, cookie := newSyncFixture(t, inv, nil)
	code := func(rec *httptest.ResponseRecorder) string {
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		c, _ := body["code"].(string)
		return c
	}
	rec := doCSRF(t, h, "POST", "/auth/invitations/accept", `{"token":"t"}`, cookie)
	if rec.Code != http.StatusForbidden || code(rec) != "invitation_identity_mismatch" {
		t.Errorf("mismatched accept = %d %s, want 403 invitation_identity_mismatch", rec.Code, rec.Body)
	}
	rec = doCSRF(t, h, "POST", "/auth/invitations/project/p1", `{"identifier":"bob@example.com","relation":"member","redirect":"https://x.example/#a"}`, cookie)
	if rec.Code != http.StatusBadRequest || code(rec) != "invitation_redirect_fragment" {
		t.Errorf("fragment redirect create = %d %s, want 400 invitation_redirect_fragment", rec.Code, rec.Body)
	}
	if inv.createCalled {
		t.Error("a refused create executed")
	}
}
