package http

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/domain/verification"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// --- minimal machine mem repos (route tests only need a working store) ---

type memServiceAccounts struct {
	mu sync.Mutex
	m  map[string]serviceaccount.ServiceAccount
}

func (s *memServiceAccounts) Create(_ context.Context, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sa.ID] = sa
	return sa, nil
}
func (s *memServiceAccounts) Get(_ context.Context, id string) (serviceaccount.ServiceAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sa, ok := s.m[id]
	if !ok {
		return serviceaccount.ServiceAccount{}, errs.ErrNotFound
	}
	return sa, nil
}
func (s *memServiceAccounts) List(_ context.Context, _ crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]serviceaccount.ServiceAccount, 0, len(s.m))
	for _, sa := range s.m {
		items = append(items, sa)
	}
	return crud.Page[serviceaccount.ServiceAccount]{Items: items}, nil
}
func (s *memServiceAccounts) Update(_ context.Context, id string, sa serviceaccount.ServiceAccount) (serviceaccount.ServiceAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[id]; !ok {
		return serviceaccount.ServiceAccount{}, errs.ErrNotFound
	}
	s.m[id] = sa
	return sa, nil
}
func (s *memServiceAccounts) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[id]; !ok {
		return errs.ErrNotFound
	}
	delete(s.m, id)
	return nil
}

type memAPIKeys struct {
	mu sync.Mutex
	m  map[string]apikey.APIKey
}

func (k *memAPIKeys) Create(_ context.Context, key apikey.APIKey) (apikey.APIKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key.ID] = key
	return key, nil
}
func (k *memAPIKeys) GetByHash(_ context.Context, hash string) (apikey.APIKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, key := range k.m {
		if key.KeyHash == hash {
			return key, nil
		}
	}
	return apikey.APIKey{}, errs.ErrNotFound
}
func (k *memAPIKeys) ListByServiceAccount(_ context.Context, saID string, _ crud.ListRequest) (crud.Page[apikey.APIKey], error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	items := make([]apikey.APIKey, 0)
	for _, key := range k.m {
		if key.ServiceAccountID == saID {
			items = append(items, key)
		}
	}
	return crud.Page[apikey.APIKey]{Items: items}, nil
}
func (k *memAPIKeys) Revoke(_ context.Context, id string, at time.Time) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	key, ok := k.m[id]
	if !ok {
		return errs.ErrNotFound
	}
	key.RevokedAt = at
	k.m[id] = key
	return nil
}
func (k *memAPIKeys) TouchLastUsed(_ context.Context, id string, at time.Time) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	key, ok := k.m[id]
	if !ok {
		return errs.ErrNotFound
	}
	key.LastUsedAt = at
	k.m[id] = key
	return nil
}

// newMachineHandler builds a real authsvc.Service with the machine repos wired,
// mounts the full route table, and returns the handler.
func newMachineHandler(t *testing.T) http.Handler {
	t.Helper()
	svc := authsvc.NewService(authsvc.Deps{
		Users:           &memUsers{byID: map[string]user.User{}},
		Passwords:       &memPasswords{m: map[string]string{}},
		Sessions:        &memSessions{m: map[string]session.Session{}},
		Codes:           &memCodes{m: map[string]verification.Code{}},
		Tokens:          &memTokens{m: map[string]verification.Token{}},
		Hasher:          fakeHasher{},
		Mailer:          nopMailer{},
		MailFrom:        "noreply@example.com",
		Limiter:         ratelimiter.NewMemory(),
		Cookie:          authsvc.CookieConfig{},
		ServiceAccounts: &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:         &memAPIKeys{m: map[string]apikey.APIKey{}},
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil)
	return h
}

// sessionFor registers and logs in a user, returning its session cookie.
func sessionFor(t *testing.T, h http.Handler, email string) *http.Cookie {
	t.Helper()
	do(t, h, "POST", "/auth/register", `{"email":"`+email+`","password":"password123","display_name":"M"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"`+email+`","password":"password123"}`)
	c := sessionCookie(login)
	if c == nil {
		t.Fatal("no session cookie from login")
	}
	return &http.Cookie{Name: c.Name, Value: c.Value}
}

// --- deny-by-absence: machine routes absent when unwired ---

func TestMachineRoutesDenyByAbsence(t *testing.T) {
	h := newTestHandler(t, nil) // no machine repos wired
	for _, tc := range []struct{ method, path string }{
		{"GET", "/auth/service-accounts"},
		{"POST", "/auth/service-accounts"},
		{"POST", "/auth/service-accounts/x/keys"},
		{"GET", "/auth/service-accounts/x/keys"},
		{"POST", "/auth/api-keys/x/revoke"},
	} {
		rec := do(t, h, tc.method, tc.path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s (machine off) status = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}

// --- session gating ---

func TestCreateServiceAccountRequiresSession(t *testing.T) {
	h := newMachineHandler(t)
	rec := do(t, h, "POST", "/auth/service-accounts", `{"name":"bot"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("create without session status = %d, want 401", rec.Code)
	}
}

func TestListServiceAccountsRequiresSession(t *testing.T) {
	h := newMachineHandler(t)
	rec := do(t, h, "GET", "/auth/service-accounts", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("list without session status = %d, want 401", rec.Code)
	}
}

func TestCreateServiceAccountStrictDecode(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "sd@example.com")
	rec := do(t, h, "POST", "/auth/service-accounts", `{"name":"bot","extra":1}`, c)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown-field body status = %d, want 400", rec.Code)
	}
}

// --- happy paths ---

func TestCreateServiceAccountHappyPath(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "hp@example.com")
	rec := do(t, h, "POST", "/auth/service-accounts", `{"name":"deployer","description":"CI"}`, c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["id"] == "" || resp["name"] != "deployer" {
		t.Errorf("create response = %v", resp)
	}
}

func TestMintAPIKeyReturnsPlaintextOnce(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "mint@example.com")

	create := do(t, h, "POST", "/auth/service-accounts", `{"name":"bot"}`, c)
	var sa map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &sa)
	saID, _ := sa["id"].(string)

	mint := do(t, h, "POST", "/auth/service-accounts/"+saID+"/keys", `{"name":"deploy"}`, c)
	if mint.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want 201; body=%s", mint.Code, mint.Body)
	}
	var minted map[string]any
	_ = json.Unmarshal(mint.Body.Bytes(), &minted)
	raw, _ := minted["key"].(string)
	if raw == "" {
		t.Fatal("mint response carried no plaintext key")
	}

	// The listing NEVER re-exposes the plaintext.
	list := do(t, h, "GET", "/auth/service-accounts/"+saID+"/keys", "", c)
	if list.Code != http.StatusOK {
		t.Fatalf("list keys status = %d, want 200", list.Code)
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &page)
	if len(page.Items) != 1 {
		t.Fatalf("listed %d keys, want 1", len(page.Items))
	}
	if _, hasKey := page.Items[0]["key"]; hasKey {
		t.Error("listing re-exposed the plaintext key")
	}
	if page.Items[0]["key_prefix"] == "" {
		t.Error("listing omitted the display key_prefix")
	}
}

func TestMintAPIKeyUnknownServiceAccount(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "unk@example.com")
	rec := do(t, h, "POST", "/auth/service-accounts/nope/keys", `{"name":"k"}`, c)
	if rec.Code != http.StatusNotFound {
		t.Errorf("mint for unknown sa status = %d, want 404", rec.Code)
	}
}

func TestMintAPIKeyBadExpiresAt(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "exp@example.com")
	create := do(t, h, "POST", "/auth/service-accounts", `{"name":"bot"}`, c)
	var sa map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &sa)
	saID, _ := sa["id"].(string)
	rec := do(t, h, "POST", "/auth/service-accounts/"+saID+"/keys", `{"name":"k","expires_at":"not-a-time"}`, c)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad expires_at status = %d, want 400", rec.Code)
	}
}

func TestRevokeAPIKeyUnknown(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "rev@example.com")
	rec := do(t, h, "POST", "/auth/api-keys/nope/revoke", "", c)
	if rec.Code != http.StatusNotFound {
		t.Errorf("revoke unknown key status = %d, want 404", rec.Code)
	}
}

func TestRevokeAPIKeyHappyPath(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "revok@example.com")
	create := do(t, h, "POST", "/auth/service-accounts", `{"name":"bot"}`, c)
	var sa map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &sa)
	saID, _ := sa["id"].(string)
	mint := do(t, h, "POST", "/auth/service-accounts/"+saID+"/keys", `{"name":"k"}`, c)
	var minted map[string]any
	_ = json.Unmarshal(mint.Body.Bytes(), &minted)
	keyID, _ := minted["id"].(string)

	rec := do(t, h, "POST", "/auth/api-keys/"+keyID+"/revoke", "", c)
	if rec.Code != http.StatusOK {
		t.Errorf("revoke status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}
