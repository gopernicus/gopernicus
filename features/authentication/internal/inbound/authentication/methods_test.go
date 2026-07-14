package authentication

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// memCredentialMutations is the inbound test's credential.MutationRepository over
// the mem stores: Snapshot projects the typed MethodSet from passwords +
// identifiers + the user's auth_revision, and Apply performs a revision-CAS
// mutation against the same mems. The /auth/methods read only exercises Snapshot;
// Apply is faithful so later credential-suite tasks can reuse this fake.
type memCredentialMutations struct {
	users     *memUsers
	idents    *memIdentifiers
	passwords *memPasswords
}

var _ credential.MutationRepository = (*memCredentialMutations)(nil)

func (m *memCredentialMutations) Snapshot(ctx context.Context, userID string) (credential.MethodSet, error) {
	u, err := m.users.Get(ctx, userID)
	if err != nil {
		return credential.MethodSet{}, err
	}
	set := credential.MethodSet{AuthRevision: u.AuthRevision}
	if _, err := m.passwords.Get(ctx, userID); err == nil {
		set.HasPassword = true
	}
	idents, err := m.idents.ListByUser(ctx, userID)
	if err != nil {
		return credential.MethodSet{}, err
	}
	for _, it := range idents {
		set.Identifiers = append(set.Identifiers, credential.IdentifierMethod{
			ID:       it.ID,
			Kind:     string(it.Kind),
			Uses:     credential.IdentifierUses{Login: it.LoginEnabled, Recovery: it.RecoveryEnabled, Notification: it.NotificationEnabled},
			Verified: it.Verified(),
			Primary:  it.IsPrimary,
		})
	}
	return set, nil
}

func (m *memCredentialMutations) Apply(_ context.Context, userID string, expected int64, mut credential.Mutation) error {
	if err := m.users.applyRevision(userID, expected); err != nil {
		return err
	}
	now := time.Now().UTC()
	switch v := mut.(type) {
	case credential.RemovePassword:
		m.passwords.mu.Lock()
		delete(m.passwords.m, userID)
		m.passwords.mu.Unlock()
	case credential.RetireIdentifier:
		m.idents.mu.Lock()
		if it, ok := m.idents.byID[v.IdentifierID]; ok {
			it.Retire(now)
			m.idents.byID[v.IdentifierID] = it
		}
		if v.ReplacementPrimaryID != "" {
			if it, ok := m.idents.byID[v.ReplacementPrimaryID]; ok {
				it.IsPrimary = true
				m.idents.byID[v.ReplacementPrimaryID] = it
			}
		}
		m.idents.mu.Unlock()
	case credential.ChangeIdentifierUses:
		m.idents.mu.Lock()
		if it, ok := m.idents.byID[v.IdentifierID]; ok {
			it.LoginEnabled = v.Uses.Login
			it.RecoveryEnabled = v.Uses.Recovery
			it.NotificationEnabled = v.Uses.Notification
			if v.MakePrimary {
				it.IsPrimary = true
			}
			m.idents.byID[v.IdentifierID] = it
		}
		m.idents.mu.Unlock()
	case credential.UnlinkOAuth:
		// no OAuth store in this harness
	}
	return nil
}

// methodsFixture holds the mem stores and the mounted handler for the /auth/methods
// tests so a test can seed identifiers and revoke the session directly.
type methodsFixture struct {
	h         http.Handler
	users     *memUsers
	idents    *memIdentifiers
	passwords *memPasswords
	sessions  *memSessions
}

// newMethodsHandler wires a real authsvc.Service with the credential-mutation rail
// over the mem stores and mounts the routes.
func newMethodsHandler(t *testing.T) methodsFixture {
	t.Helper()
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	svc := authsvc.NewService(authsvc.Deps{
		Users:               users,
		Identifiers:         idents,
		Passwords:           passwords,
		Sessions:            sessions,
		CredentialMutations: &memCredentialMutations{users: users, idents: idents, passwords: passwords},
		Hasher:              fakeHasher{},
		Limiter:             ratelimiter.NewMemory(),
		Cookie:              authsvc.CookieConfig{},
		TokenSigner:         newFakeSigner(),
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil, "", MutationSecurity{}, nil)
	return methodsFixture{h: h, users: users, idents: idents, passwords: passwords, sessions: sessions}
}

// seedUser inserts a user with a password so login resolves it.
func (f methodsFixture) seedUser(userID string) {
	f.users.mu.Lock()
	f.users.byID[userID] = user.User{ID: userID, DisplayName: "Seed"}
	f.users.mu.Unlock()
	f.passwords.mu.Lock()
	f.passwords.m[userID] = "hash:password123456789"
	f.passwords.mu.Unlock()
}

// seedIdentifier inserts an identifier owned by userID.
func (f methodsFixture) seedIdentifier(it identifier.Identifier) {
	f.idents.insert(it)
}

// login authenticates the seeded email over the cookie lane and returns the live
// session cookie.
func (f methodsFixture) login(t *testing.T, emailAddr string) *http.Cookie {
	t.Helper()
	rec := do(t, f.h, "POST", "/auth/login", `{"email":"`+emailAddr+`","password":"password123456789"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%s", rec.Code, rec.Body)
	}
	c := sessionCookie(rec)
	if c == nil {
		t.Fatal("login set no session cookie")
	}
	return c
}

func verifiedEmail(id, userID, value string) identifier.Identifier {
	now := time.Now().UTC()
	return identifier.Identifier{
		ID: id, UserID: userID, Kind: identifier.KindEmail, NormalizedValue: value,
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		IsPrimary: id == "id-primary", CreatedAt: now, UpdatedAt: now,
	}
}

// TestMethodsRequiresLiveSession proves the read is live-session-gated: no
// credential denies with 401.
func TestMethodsRequiresLiveSession(t *testing.T) {
	f := newMethodsHandler(t)
	rec := do(t, f.h, "GET", "/auth/methods", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-session status = %d, want 401", rec.Code)
	}
}

// TestMethodsRevokedSessionDenied proves a revoked (deleted) session is denied
// within one round-trip — the immediate-revocation tier, not the stale JWT window.
func TestMethodsRevokedSessionDenied(t *testing.T) {
	f := newMethodsHandler(t)
	f.seedUser("u1")
	f.seedIdentifier(verifiedEmail("id-primary", "u1", "alice@example.com"))
	cookie := f.login(t, "alice@example.com")

	// Revoke every session, then the same cookie must be denied.
	if err := f.sessions.DeleteByUser(context.Background(), "u1"); err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	rec := do(t, f.h, "GET", "/auth/methods", "", cookie)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked-session status = %d, want 401; body=%s", rec.Code, rec.Body)
	}
}

// TestMethodsMaskingAndNoStore proves the identifier value is masked by default,
// the response is no-store, and the sole-recovery identifier is not removable.
func TestMethodsMaskingAndNoStore(t *testing.T) {
	f := newMethodsHandler(t)
	f.seedUser("u1")
	f.seedIdentifier(verifiedEmail("id-primary", "u1", "alice@example.com"))
	cookie := f.login(t, "alice@example.com")

	rec := do(t, f.h, "GET", "/auth/methods", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("methods status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	var resp methodsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if !resp.HasPassword {
		t.Error("has_password = false, want true")
	}
	if len(resp.Identifiers) != 1 {
		t.Fatalf("identifiers = %d, want 1", len(resp.Identifiers))
	}
	id := resp.Identifiers[0]
	if id.Value == "alice@example.com" {
		t.Error("identifier value was returned unmasked")
	}
	if id.Value != "a***@example.com" {
		t.Errorf("masked value = %q, want a***@example.com", id.Value)
	}
	if id.VerifiedAt == "" {
		t.Error("verified identifier reported no verified_at")
	}
	if !id.Primary {
		t.Error("primary identifier reported primary=false")
	}
	if len(id.Uses) != 3 {
		t.Errorf("uses = %v, want [login recovery notification]", id.Uses)
	}
	// The sole recovery method cannot be removed: the policy hint is advisory-false.
	if id.Removable {
		t.Error("sole recovery identifier reported removable=true")
	}
}

// TestMethodsReplacedRowOmitted proves a replaced (retired) identifier never
// appears in the inventory.
func TestMethodsReplacedRowOmitted(t *testing.T) {
	f := newMethodsHandler(t)
	f.seedUser("u1")
	f.seedIdentifier(verifiedEmail("id-primary", "u1", "alice@example.com"))
	replaced := verifiedEmail("id-old", "u1", "old@example.com")
	replaced.ReplacedAt = time.Now().UTC()
	f.seedIdentifier(replaced)
	cookie := f.login(t, "alice@example.com")

	rec := do(t, f.h, "GET", "/auth/methods", "", cookie)
	var resp methodsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, id := range resp.Identifiers {
		if id.ID == "id-old" {
			t.Fatalf("replaced identifier appeared in inventory: %+v", id)
		}
	}
	if len(resp.Identifiers) != 1 {
		t.Fatalf("identifiers = %d, want 1 (replaced omitted)", len(resp.Identifiers))
	}
}

// TestMethodsRemovableHintAdvisory proves the removable hint follows the credential
// policy: with a second verified recovery identifier, either becomes removable.
func TestMethodsRemovableHintAdvisory(t *testing.T) {
	f := newMethodsHandler(t)
	f.seedUser("u1")
	f.seedIdentifier(verifiedEmail("id-primary", "u1", "alice@example.com"))
	f.seedIdentifier(verifiedEmail("id-second", "u1", "backup@example.com"))
	cookie := f.login(t, "alice@example.com")

	rec := do(t, f.h, "GET", "/auth/methods", "", cookie)
	var resp methodsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Identifiers) != 2 {
		t.Fatalf("identifiers = %d, want 2", len(resp.Identifiers))
	}
	for _, id := range resp.Identifiers {
		if !id.Removable {
			t.Errorf("identifier %s removable=false; with a redundant recovery method it should be advisory-removable", id.ID)
		}
	}
}

// TestMethodsFailsClosedWithoutCredentialRail proves the read fails closed (403)
// when the credential-mutation rail is unwired.
func TestMethodsFailsClosedWithoutCredentialRail(t *testing.T) {
	users := newMemUsers()
	idents := newMemIdentifiers(users)
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	svc := authsvc.NewService(authsvc.Deps{
		Users:       users,
		Identifiers: idents,
		Passwords:   passwords,
		Sessions:    sessions,
		Hasher:      fakeHasher{},
		Limiter:     ratelimiter.NewMemory(),
		Cookie:      authsvc.CookieConfig{},
		TokenSigner: newFakeSigner(),
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil, "", MutationSecurity{}, nil)
	f := methodsFixture{h: h, users: users, idents: idents, passwords: passwords, sessions: sessions}
	f.seedUser("u1")
	f.seedIdentifier(verifiedEmail("id-primary", "u1", "alice@example.com"))
	cookie := f.login(t, "alice@example.com")

	rec := do(t, f.h, "GET", "/auth/methods", "", cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unwired credential rail status = %d, want 403; body=%s", rec.Code, rec.Body)
	}
}
