package authsvc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/apikey"
	"github.com/gopernicus/gopernicus/features/auth/logic/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
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
		return serviceaccount.ServiceAccount{}, errs.ErrAlreadyExists
	}
	f.m[sa.ID] = sa
	return sa, nil
}

func (f *fakeServiceAccounts) Get(_ context.Context, id string) (serviceaccount.ServiceAccount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sa, ok := f.m[id]
	if !ok {
		return serviceaccount.ServiceAccount{}, errs.ErrNotFound
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
		return serviceaccount.ServiceAccount{}, errs.ErrNotFound
	}
	f.m[id] = sa
	return sa, nil
}

func (f *fakeServiceAccounts) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.m[id]; !ok {
		return errs.ErrNotFound
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
			return apikey.APIKey{}, errs.ErrAlreadyExists
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
	return apikey.APIKey{}, errs.ErrNotFound
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
		return errs.ErrNotFound
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
		return errs.ErrNotFound
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
}

func newMachineHarness(t *testing.T) *machineHarness {
	t.Helper()
	sas := newFakeServiceAccounts()
	keys := newFakeAPIKeys()
	events := newSpySecurityEvents()
	svc := NewService(Deps{
		Users:           newFakeUsers(),
		Passwords:       newFakePasswords(),
		Sessions:        newFakeSessions(),
		Codes:           newFakeCodes(),
		Tokens:          newFakeTokens(),
		Hasher:          &fakeHasher{},
		Mailer:          &recordingMailer{},
		MailFrom:        "noreply@example.com",
		Limiter:         ratelimiter.NewMemory(),
		Cookie:          CookieConfig{},
		ServiceAccounts: sas,
		APIKeys:         keys,
		SecurityEvents:  events,
	})
	return &machineHarness{svc: svc, sas: sas, keys: keys, events: events}
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
	if _, _, err := h.svc.MintAPIKey(context.Background(), "nope", "k", time.Time{}); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("MintAPIKey(unknown sa): err=%v, want ErrNotFound", err)
	}
}

func TestAuthenticateAPIKeyActAsUserResolvesToOwner(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, err := h.svc.CreateServiceAccount(ctx, "admin", "personal", "", true, "owner-9")
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
	if _, err := h.svc.AuthenticateAPIKey(ctx, raw); !errors.Is(err, errs.ErrUnauthorized) {
		t.Errorf("revoked key: err=%v, want ErrUnauthorized", err)
	}
}

func TestAuthenticateAPIKeyExpiredDenies(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Now().Add(-time.Hour))
	if _, err := h.svc.AuthenticateAPIKey(ctx, raw); !errors.Is(err, errs.ErrUnauthorized) {
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
	if _, err := h.svc.AuthenticateAPIKey(context.Background(), "prefix_deadbeef"); !errors.Is(err, errs.ErrUnauthorized) {
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
		Codes:     newFakeCodes(),
		Tokens:    newFakeTokens(),
		Hasher:    &fakeHasher{},
		Mailer:    &recordingMailer{},
		Limiter:   ratelimiter.NewMemory(),
	})
	if svc.MachineEnabled() {
		t.Fatal("MachineEnabled reported true with no machine repos")
	}
	if _, err := svc.AuthenticateAPIKey(context.Background(), "prefix_x"); !errors.Is(err, errs.ErrUnauthorized) {
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
		Codes: newFakeCodes(), Tokens: newFakeTokens(), Hasher: &fakeHasher{},
		Mailer: &recordingMailer{}, Limiter: ratelimiter.NewMemory(),
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
	u, err := h.svc.Register(ctx, "sess@example.com", "password123", "Sess")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	token, _, err := h.svc.Login(ctx, "sess@example.com", "password123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	var got Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = h.svc.CurrentPrincipal(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest("GET", "/x", nil)
	req.AddCookie(&http.Cookie{Name: h.svc.SessionCookieName(), Value: token})
	rec := httptest.NewRecorder()
	h.svc.RequirePrincipal(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent || got.Type != PrincipalUser || got.ID != u.ID {
		t.Errorf("session principal: status=%d principal=%+v, want {user, %s}", rec.Code, got, u.ID)
	}
}

func TestRequirePrincipalJWTShapedInert(t *testing.T) {
	// A two-dot bearer is the JWT path, inert in A3 (no TokenSigner) → 401, and it
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
