package authsvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// --- compile-time seam assertions ---

var (
	_ serviceaccount.ServiceAccountRepository = (*fakeServiceAccounts)(nil)
	_ apikey.APIKeyRepository                 = (*fakeAPIKeys)(nil)
)

// --- machine fakes ---

type fakeServiceAccounts struct {
	mu sync.Mutex
	m  map[string]serviceaccount.ServiceAccount
}

func newFakeServiceAccounts() *fakeServiceAccounts {
	return &fakeServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}}
}

func (f *fakeServiceAccounts) Create(_ context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[sa.ID]; ok {
		return serviceaccount.ServiceAccount{}, sdk.ErrAlreadyExists
	}
	f.m[sa.ID] = sa
	return sa, nil
}

func (f *fakeServiceAccounts) Get(_ context.Context, id string) (serviceaccount.ServiceAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sa, ok := f.m[id]
	if !ok {
		return serviceaccount.ServiceAccount{}, sdk.ErrNotFound
	}
	return sa, nil
}

func (f *fakeServiceAccounts) List(_ context.Context, _ crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]serviceaccount.ServiceAccount, 0, len(f.m))
	for _, sa := range f.m {
		items = append(items, sa)
	}
	return crud.Page[serviceaccount.ServiceAccount]{Items: items}, nil
}

func (f *fakeServiceAccounts) Update(_ context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[id]; !ok {
		return serviceaccount.ServiceAccount{}, sdk.ErrNotFound
	}
	f.m[id] = sa
	return sa, nil
}

func (f *fakeServiceAccounts) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[id]; !ok {
		return sdk.ErrNotFound
	}
	delete(f.m, id)
	return nil
}

type fakeAPIKeys struct {
	mu       sync.Mutex
	m        map[string]apikey.APIKey // by ID
	touchErr error                    // when set, TouchLastUsed fails (best-effort test)
	touched  int
}

func newFakeAPIKeys() *fakeAPIKeys { return &fakeAPIKeys{m: map[string]apikey.APIKey{}} }

func (f *fakeAPIKeys) Create(_ context.Context, k apikey.APIKey) (apikey.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ex := range f.m {
		if ex.KeyHash == k.KeyHash {
			return apikey.APIKey{}, sdk.ErrAlreadyExists
		}
	}
	f.m[k.ID] = k
	return k, nil
}

func (f *fakeAPIKeys) GetByHash(_ context.Context, keyHash string) (apikey.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range f.m {
		if k.KeyHash == keyHash {
			return k, nil
		}
	}
	return apikey.APIKey{}, sdk.ErrNotFound
}

func (f *fakeAPIKeys) ListByServiceAccount(_ context.Context, serviceAccountID string, _ crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]apikey.APIKey, 0)
	for _, k := range f.m {
		if k.ServiceAccountID == serviceAccountID {
			items = append(items, k)
		}
	}
	return crud.Page[apikey.APIKey]{Items: items}, nil
}

func (f *fakeAPIKeys) Revoke(_ context.Context, id string, revokedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.m[id]
	if !ok {
		return sdk.ErrNotFound
	}
	k.RevokedAt = revokedAt
	f.m[id] = k
	return nil
}

func (f *fakeAPIKeys) TouchLastUsed(_ context.Context, id string, usedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.touchErr != nil {
		return f.touchErr
	}
	k, ok := f.m[id]
	if !ok {
		return sdk.ErrNotFound
	}
	k.LastUsedAt = usedAt
	f.m[id] = k
	f.touched++
	return nil
}

// --- harness ---

type machineHarness struct {
	svc    *Service
	sas    *fakeServiceAccounts
	keys   *fakeAPIKeys
	events *spySecurityEvents
	users  *fakeUsers
}

func newMachineHarness(t *testing.T) *machineHarness {
	t.Helper()
	sas := newFakeServiceAccounts()
	keys := newFakeAPIKeys()
	events := newSpySecurityEvents()
	users := newFakeUsers()
	svc := NewService(Deps{
		Users:           users,
		Identifiers:     newFakeIdentifiers(users),
		Passwords:       newFakePasswords(),
		Sessions:        newFakeSessions(),
		Challenges:      newFakeChallenges(),
		Protector:       newFakeProtector("k1", "k1"),
		Hasher:          &fakeHasher{},
		Limiter:         ratelimiter.NewMemory(),
		Cookie:          CookieConfig{},
		ServiceAccounts: sas,
		APIKeys:         keys,
		SecurityEvents:  events,
		TokenSigner:     newFakeSigner(),
	})
	wireSyncDelivery(t, svc, &recordingMailer{}, nil)
	return &machineHarness{svc: svc, sas: sas, keys: keys, events: events, users: users}
}

// seedUser puts a bare active user in the directory so an act-as-user service
// account has a real owner to name (CreateServiceAccount refuses an unknown one).
func (h *machineHarness) seedUser(id string) string {
	h.users.mu.Lock()
	defer h.users.mu.Unlock()
	h.users.byID[id] = user.User{ID: id}
	return id
}

// --- AuthenticateAPIKey ---

func TestAuthenticateAPIKeyRoundTrip(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, err := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	_, raw, err := h.svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}

	p, err := h.svc.AuthenticateAPIKey(ctx, raw)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}
	if p.Type != PrincipalServiceAccount || p.ID != sa.ID {
		t.Errorf("principal = %+v, want {service_account, %s}", p, sa.ID)
	}
	// TouchLastUsed rode the successful auth.
	if h.keys.touched != 1 {
		t.Errorf("TouchLastUsed calls = %d, want 1", h.keys.touched)
	}
}

func TestMintAPIKeyPlaintextIsDotless(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	k, raw, err := h.svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	if isJWTToken(raw) {
		t.Errorf("minted key is JWT-shaped (two dots): %q", raw)
	}
	// The stored record holds only the hash, never the plaintext.
	if k.KeyHash == raw || k.KeyHash == "" {
		t.Errorf("KeyHash must be the hash of the plaintext, not the plaintext: %q", k.KeyHash)
	}
	if k.KeyPrefix == "" {
		t.Error("KeyPrefix must be stored for display")
	}
}

func TestMintAPIKeyUnknownServiceAccount(t *testing.T) {
	h := newMachineHarness(t)
	if _, _, err := h.svc.MintAPIKey(context.Background(), "nope", "k", time.Time{}); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("MintAPIKey(unknown sa): err=%v, want ErrNotFound", err)
	}
}

func TestAuthenticateAPIKeyActAsUserResolvesToOwner(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, err := h.svc.CreateServiceAccount(ctx, "admin", "personal", "", true, h.seedUser("owner-9"))
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	_, raw, err := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	p, err := h.svc.AuthenticateAPIKey(ctx, raw)
	if err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}
	if p.Type != PrincipalUser || p.ID != "owner-9" {
		t.Errorf("act-as-user principal = %+v, want {user, owner-9}", p)
	}
}

func TestAuthenticateAPIKeyRevokedDenies(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	key, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	if err := h.svc.RevokeAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if _, err := h.svc.AuthenticateAPIKey(ctx, raw); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("revoked key: err=%v, want ErrUnauthorized", err)
	}
}

func TestAuthenticateAPIKeyExpiredDenies(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Now().Add(-time.Hour))
	if _, err := h.svc.AuthenticateAPIKey(ctx, raw); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("expired key: err=%v, want ErrUnauthorized", err)
	}
}

func TestAuthenticateAPIKeyValidNeverExpires(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	// A zero ExpiresAt means never-expires — a far-future clock still authenticates.
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	if _, err := h.svc.AuthenticateAPIKey(ctx, raw); err != nil {
		t.Errorf("never-expiring key: err=%v, want nil", err)
	}
}

func TestAuthenticateAPIKeyUnknownDenies(t *testing.T) {
	h := newMachineHarness(t)
	if _, err := h.svc.AuthenticateAPIKey(context.Background(), "prefix_deadbeef"); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("unknown key: err=%v, want ErrUnauthorized", err)
	}
}

func TestAuthenticateAPIKeyTouchLastUsedBestEffort(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	h.keys.touchErr = errors.New("touch boom")
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	// A failing TouchLastUsed must NOT fail authentication.
	if _, err := h.svc.AuthenticateAPIKey(ctx, raw); err != nil {
		t.Errorf("auth failed on a TouchLastUsed error: %v", err)
	}
}

func TestAuthenticateAPIKeySubsystemOff(t *testing.T) {
	// No machine repos wired → the subsystem is off; auth denies without a lookup.
	svc := NewService(Deps{
		Users:     newFakeUsers(),
		Passwords: newFakePasswords(),
		Sessions:  newFakeSessions(),
		Hasher:    &fakeHasher{},
		Limiter:   ratelimiter.NewMemory(),
	})
	if svc.MachineEnabled() {
		t.Fatal("MachineEnabled reported true with no machine repos")
	}
	if _, err := svc.AuthenticateAPIKey(context.Background(), "prefix_x"); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Errorf("auth with subsystem off: err=%v, want ErrUnauthorized", err)
	}
}

// --- middleware ---

func bearerRequest(token string) *http.Request {
	r := httptest.NewRequest("GET", "/x", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestRequireServiceAccountValidKey(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})

	var got Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := h.svc.CurrentPrincipal(r.Context())
		if !ok {
			t.Error("CurrentPrincipal not set inside RequireServiceAccount")
		}
		got = p
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	h.svc.RequireServiceAccount(next).ServeHTTP(rec, bearerRequest(raw))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if got.Type != PrincipalServiceAccount || got.ID != sa.ID {
		t.Errorf("principal = %+v, want {service_account, %s}", got, sa.ID)
	}
}

func TestRequireServiceAccountNoHeader(t *testing.T) {
	h := newMachineHarness(t)
	rec := httptest.NewRecorder()
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	h.svc.RequireServiceAccount(next).ServeHTTP(rec, bearerRequest(""))
	if rec.Code != http.StatusUnauthorized || called {
		t.Errorf("no header: status=%d called=%v, want 401 not-called", rec.Code, called)
	}
}

func TestRequireServiceAccountJWTShapedDenied(t *testing.T) {
	// A two-dot bearer is classed as a JWT; the JWT path is inert in A3, so the
	// API-key middleware denies rather than treating it as a key.
	h := newMachineHarness(t)
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h.svc.RequireServiceAccount(next).ServeHTTP(rec, bearerRequest("aaa.bbb.ccc"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("jwt-shaped bearer: status = %d, want 401", rec.Code)
	}
}

func TestRequireServiceAccountSubsystemOff(t *testing.T) {
	svc := NewService(Deps{
		Users: newFakeUsers(), Passwords: newFakePasswords(), Sessions: newFakeSessions(),
		Hasher:  &fakeHasher{},
		Limiter: ratelimiter.NewMemory(),
	})
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	svc.RequireServiceAccount(next).ServeHTTP(rec, bearerRequest("prefix_x"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("api-key path with subsystem off: status = %d, want 401", rec.Code)
	}
}

func TestRequirePrincipalAPIKey(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})

	var got Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = h.svc.CurrentPrincipal(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	h.svc.RequirePrincipal(next).ServeHTTP(rec, bearerRequest(raw))
	if rec.Code != http.StatusNoContent || got.Type != PrincipalServiceAccount || got.ID != sa.ID {
		t.Errorf("api-key principal: status=%d principal=%+v", rec.Code, got)
	}
}

func TestRequirePrincipalSession(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	u, err := h.svc.Register(ctx, "sess@example.com", "password123456789", "Sess")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	pair, _, err := h.svc.Login(ctx, "sess@example.com", "password123456789")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	var got Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = h.svc.CurrentPrincipal(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: pair.AccessToken})
	rec := httptest.NewRecorder()
	h.svc.RequirePrincipal(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || got.Type != PrincipalUser || got.ID != u.ID {
		t.Errorf("session principal: status=%d principal=%+v, want {user, %s}", rec.Code, got, u.ID)
	}
}

func TestRequirePrincipalJWTShapedGarbageDenied(t *testing.T) {
	// A two-dot bearer is the JWT path; garbage fails verification → 401, and it
	// must NOT fall through to the session path.
	h := newMachineHarness(t)
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h.svc.RequirePrincipal(next).ServeHTTP(rec, bearerRequest("aaa.bbb.ccc"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("jwt-shaped bearer: status = %d, want 401", rec.Code)
	}
}

func TestRequirePrincipalNoCredential(t *testing.T) {
	h := newMachineHarness(t)
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h.svc.RequirePrincipal(next).ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no credential: status = %d, want 401", rec.Code)
	}
}

func TestCurrentPrincipalAbsent(t *testing.T) {
	h := newMachineHarness(t)
	if _, ok := h.svc.CurrentPrincipal(context.Background()); ok {
		t.Error("CurrentPrincipal reported ok on a bare context")
	}
}

// --- CreateServiceAccount: act-as owner validation ---

// errUsers fails Get with an infrastructure error (not sdk.ErrNotFound), so the
// act-as owner check's propagate-don't-translate lane is exercised.
type errUsers struct {
	*fakeUsers
	err error
}

func (e errUsers) Get(context.Context, string) (user.User, error) { return user.User{}, e.err }

func TestCreateServiceAccountActAsUserWithoutIdentityRail(t *testing.T) {
	// No Users repository: an act-as-user account cannot be validated, so it fails
	// closed rather than minting an unowned impersonation credential.
	svc := NewService(Deps{
		Passwords:       newFakePasswords(),
		Sessions:        newFakeSessions(),
		Hasher:          &fakeHasher{},
		Limiter:         ratelimiter.NewMemory(),
		ServiceAccounts: newFakeServiceAccounts(),
		APIKeys:         newFakeAPIKeys(),
	})
	if _, err := svc.CreateServiceAccount(context.Background(), "admin", "personal", "", true, "owner-1"); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("err = %v, want ErrIdentityUnavailable", err)
	}
}

func TestCreateServiceAccountActAsUserUnknownOwner(t *testing.T) {
	h := newMachineHarness(t)
	_, err := h.svc.CreateServiceAccount(context.Background(), "admin", "personal", "", true, "ghost-7")
	if !errors.Is(err, sdk.ErrInvalidReference) {
		t.Fatalf("err = %v, want sdk.ErrInvalidReference", err)
	}
	if !strings.Contains(err.Error(), "ghost-7") {
		t.Errorf("err = %q, want the refused owner id named", err)
	}
	if page, _ := h.sas.List(context.Background(), crud.ListRequest{}); len(page.Items) != 0 {
		t.Errorf("accounts created = %d, want 0 for an unknown owner", len(page.Items))
	}
	if h.events.count() != 0 {
		t.Errorf("audit rows = %d, want 0 for a refused create", h.events.count())
	}
}

func TestCreateServiceAccountActAsUserOwnerLookupErrorPropagates(t *testing.T) {
	boom := errors.New("users db unavailable")
	svc := NewService(Deps{
		Users:           errUsers{fakeUsers: newFakeUsers(), err: boom},
		Passwords:       newFakePasswords(),
		Sessions:        newFakeSessions(),
		Hasher:          &fakeHasher{},
		Limiter:         ratelimiter.NewMemory(),
		ServiceAccounts: newFakeServiceAccounts(),
		APIKeys:         newFakeAPIKeys(),
	})
	_, err := svc.CreateServiceAccount(context.Background(), "admin", "personal", "", true, "owner-1")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the repository error", err)
	}
	if errors.Is(err, sdk.ErrInvalidReference) {
		t.Error("an unreadable directory must not be reported as an invalid reference")
	}
}

func TestCreateServiceAccountActAsSelfExistingOwner(t *testing.T) {
	h := newMachineHarness(t)
	owner := h.seedUser("owner-1")
	sa, err := h.svc.CreateServiceAccount(context.Background(), owner, "personal", "", true, owner)
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	if sa.OwnerUserID != owner || !sa.ActAsUser {
		t.Errorf("account = %+v, want an act-as account owned by %q", sa, owner)
	}
}

func TestCreateServiceAccountNonActAsSkipsOwnerCheck(t *testing.T) {
	// A plain machine account has no human subject: the owner column stays empty
	// and the directory is never consulted.
	h := newMachineHarness(t)
	users := errUsers{fakeUsers: h.users, err: errors.New("must not be called")}
	h.svc.users = users
	sa, err := h.svc.CreateServiceAccount(context.Background(), "admin", "bot", "", false, "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	if sa.OwnerUserID != "" || sa.ActAsUser {
		t.Errorf("account = %+v, want a self-acting account with no owner", sa)
	}
}

// --- lifecycle audit rail ---

func principalContext(id string) context.Context {
	return identity.WithPrincipal(context.Background(), Principal{Type: PrincipalUser, ID: id})
}

func TestSecurityEventServiceAccountCreatedActor(t *testing.T) {
	h := newMachineHarness(t)
	// The acting human is the context principal; createdBy is a caller-supplied
	// string and must never become the actor.
	ctx := principalContext("admin-1")
	sa, err := h.svc.CreateServiceAccount(ctx, "created-by-string", "bot", "", false, "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeServiceAccountCreated, securityevent.StatusSuccess)
	if e.Actor.Type != PrincipalUser || e.Actor.ID != "admin-1" {
		t.Errorf("actor = %+v, want {user, admin-1}", e.Actor)
	}
	if e.UserID != "" {
		t.Errorf("UserID = %q, want empty for a self-acting account", e.UserID)
	}
	if e.Details["service_account_id"] != sa.ID {
		t.Errorf("Details service_account_id = %v, want %q", e.Details["service_account_id"], sa.ID)
	}
	if e.Details["act_as_user"] != false || e.Details["delegated"] != false {
		t.Errorf("Details = %v, want act_as_user=false delegated=false", e.Details)
	}
}

func TestSecurityEventServiceAccountCreatedActAsSelfNotDelegated(t *testing.T) {
	h := newMachineHarness(t)
	owner := h.seedUser("admin-1")
	ctx := principalContext(owner)
	if _, err := h.svc.CreateServiceAccount(ctx, owner, "personal", "", true, owner); err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeServiceAccountCreated, securityevent.StatusSuccess)
	if e.UserID != owner {
		t.Errorf("UserID = %q, want the act-as owner %q", e.UserID, owner)
	}
	if e.Details["act_as_user"] != true || e.Details["delegated"] != false {
		t.Errorf("Details = %v, want act_as_user=true delegated=false for act-as-self", e.Details)
	}
}

func TestSecurityEventServiceAccountCreatedDelegated(t *testing.T) {
	h := newMachineHarness(t)
	owner := h.seedUser("owner-2")
	ctx := principalContext("admin-1")
	if _, err := h.svc.CreateServiceAccount(ctx, "admin-1", "personal", "", true, owner); err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeServiceAccountCreated, securityevent.StatusSuccess)
	if e.UserID != owner {
		t.Errorf("UserID = %q, want the act-as owner %q", e.UserID, owner)
	}
	if e.Details["delegated"] != true {
		t.Errorf("Details = %v, want delegated=true when the owner is not the creator", e.Details)
	}
}

func TestSecurityEventServiceAccountCreatedAbsentPrincipal(t *testing.T) {
	// A host calling the service outside a request (a seeder, a job) records an
	// empty actor — the row is still written.
	h := newMachineHarness(t)
	if _, err := h.svc.CreateServiceAccount(context.Background(), "admin", "bot", "", false, ""); err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeServiceAccountCreated, securityevent.StatusSuccess)
	if e.Actor != (securityevent.Principal{}) {
		t.Errorf("actor = %+v, want the zero Principal", e.Actor)
	}
}

func TestSecurityEventAPIKeyMinted(t *testing.T) {
	h := newMachineHarness(t)
	ctx := principalContext("admin-1")
	sa, err := h.svc.CreateServiceAccount(ctx, "admin-1", "bot", "", false, "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	key, raw, err := h.svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeAPIKeyMinted, securityevent.StatusSuccess)
	if e.Actor.Type != PrincipalUser || e.Actor.ID != "admin-1" {
		t.Errorf("actor = %+v, want {user, admin-1}", e.Actor)
	}
	if e.UserID != "" {
		t.Errorf("UserID = %q, want empty for a key on a self-acting account", e.UserID)
	}
	if e.Details["key_prefix"] != key.KeyPrefix {
		t.Errorf("Details key_prefix = %v, want %q", e.Details["key_prefix"], key.KeyPrefix)
	}
	if e.Details["service_account_id"] != sa.ID {
		t.Errorf("Details service_account_id = %v, want %q", e.Details["service_account_id"], sa.ID)
	}
	// Content hygiene (design §5.1 WI3): neither the plaintext key nor its stored
	// hash may appear in ANY Details value.
	for k, v := range e.Details {
		got := toString(v)
		if strings.Contains(got, raw) {
			t.Errorf("Details[%q] leaks the raw key", k)
		}
		if key.KeyHash != "" && strings.Contains(got, key.KeyHash) {
			t.Errorf("Details[%q] leaks the key hash", k)
		}
	}
}

// TestSecurityEventAPIKeyMintedActAsUserNamesTheOwner pins the subject of an
// impersonation credential: a key minted on an act-as-user account authenticates
// as its human owner, so the audit row names that human — the same UserID
// convention service_account_created follows.
func TestSecurityEventAPIKeyMintedActAsUserNamesTheOwner(t *testing.T) {
	h := newMachineHarness(t)
	owner := h.seedUser("owner-3")
	ctx := principalContext("admin-1")
	sa, err := h.svc.CreateServiceAccount(ctx, "admin-1", "personal", "", true, owner)
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	if _, _, err := h.svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{}); err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeAPIKeyMinted, securityevent.StatusSuccess)
	if e.UserID != owner {
		t.Errorf("UserID = %q, want the act-as owner %q", e.UserID, owner)
	}
	if e.Actor.ID != "admin-1" {
		t.Errorf("actor = %+v, want the minting human {user, admin-1}", e.Actor)
	}
}

func TestSecurityEventAPIKeyRevoked(t *testing.T) {
	h := newMachineHarness(t)
	ctx := principalContext("admin-1")
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin-1", "bot", "", false, "")
	key, _, _ := h.svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
	if err := h.svc.RevokeAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeAPIKeyRevoked, securityevent.StatusSuccess)
	if e.Actor.ID != "admin-1" {
		t.Errorf("actor = %+v, want {user, admin-1}", e.Actor)
	}
	if e.Details["key_id"] != key.ID {
		t.Errorf("Details key_id = %v, want %q", e.Details["key_id"], key.ID)
	}
}

// TestSecurityEventAPIKeyRevokedRecordsPerCall documents the decision, not an
// accident: the revoke path holds only the key id (APIKeyRepository has no
// Get-by-id), so it cannot tell a first revoke from a replay and records one row
// per successful CALL. A state-aware event waits on the deferred D4 store train.
func TestSecurityEventAPIKeyRevokedRecordsPerCall(t *testing.T) {
	h := newMachineHarness(t)
	ctx := principalContext("admin-1")
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin-1", "bot", "", false, "")
	key, _, _ := h.svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
	for i := 0; i < 2; i++ {
		if err := h.svc.RevokeAPIKey(ctx, key.ID); err != nil {
			t.Fatalf("RevokeAPIKey #%d: %v", i+1, err)
		}
	}
	revocations := 0
	for _, e := range h.events.recorded() {
		if e.EventType == securityevent.TypeAPIKeyRevoked {
			revocations++
		}
	}
	if revocations != 2 {
		t.Errorf("recorded %d api_key_revoked rows for two revoke calls, want 2 (per call, not per state transition)", revocations)
	}
}

func TestSecurityEventAPIKeyRevokeUnknownRecordsNothing(t *testing.T) {
	h := newMachineHarness(t)
	if err := h.svc.RevokeAPIKey(context.Background(), "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("RevokeAPIKey(unknown): err=%v, want ErrNotFound", err)
	}
	for _, e := range h.events.recorded() {
		if e.EventType == securityevent.TypeAPIKeyRevoked {
			t.Error("a failed revoke recorded an api_key_revoked row")
		}
	}
}

func TestMachineLifecycleAuditNilRepoIsNoOp(t *testing.T) {
	users := newFakeUsers()
	svc := NewService(Deps{
		Users:           users,
		Passwords:       newFakePasswords(),
		Sessions:        newFakeSessions(),
		Hasher:          &fakeHasher{},
		Limiter:         ratelimiter.NewMemory(),
		ServiceAccounts: newFakeServiceAccounts(),
		APIKeys:         newFakeAPIKeys(),
	})
	ctx := principalContext("admin-1")
	sa, err := svc.CreateServiceAccount(ctx, "admin-1", "bot", "", false, "")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	key, _, err := svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	if err := svc.RevokeAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
}

func TestMachineLifecycleAuditFailureNeverFailsOp(t *testing.T) {
	h := newMachineHarness(t)
	h.events.createErr = errors.New("audit store unavailable")
	ctx := principalContext("admin-1")
	sa, err := h.svc.CreateServiceAccount(ctx, "admin-1", "bot", "", false, "")
	if err != nil {
		t.Fatalf("CreateServiceAccount failed on an audit-write error: %v", err)
	}
	key, _, err := h.svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey failed on an audit-write error: %v", err)
	}
	if err := h.svc.RevokeAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("RevokeAPIKey failed on an audit-write error: %v", err)
	}
	if h.events.count() != 0 {
		t.Errorf("a failing repo recorded %d events, want 0", h.events.count())
	}
}
