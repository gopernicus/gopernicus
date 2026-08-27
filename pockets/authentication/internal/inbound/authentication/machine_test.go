package authentication

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
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
		return serviceaccount.ServiceAccount{}, sdk.ErrNotFound
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
		return serviceaccount.ServiceAccount{}, sdk.ErrNotFound
	}
	s.m[id] = sa
	return sa, nil
}
func (s *memServiceAccounts) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[id]; !ok {
		return sdk.ErrNotFound
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
	return apikey.APIKey{}, sdk.ErrNotFound
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
		return sdk.ErrNotFound
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
		return sdk.ErrNotFound
	}
	key.LastUsedAt = at
	k.m[id] = key
	return nil
}

// newMachineHandler builds a real authsvc.Service with the machine repos wired,
// mounts the full route table behind an allow-all host gate, and returns the
// handler. The gate is what makes the lifecycle routes mount at all (D1); the
// cases below exercise the routes themselves, not the host's policy.
func newMachineHandler(t *testing.T) http.Handler {
	t.Helper()
	return newMachineFixture(t, allowMachineGate).h
}

// sessionFor registers and logs in a user, returning its session cookie.
func sessionFor(t *testing.T, h http.Handler, email string) *http.Cookie {
	t.Helper()
	do(t, h, "POST", "/auth/register", `{"email":"`+email+`","password":"password123456789","display_name":"M"}`)
	login := do(t, h, "POST", "/auth/login", `{"email":"`+email+`","password":"password123456789"}`)
	c := sessionCookie(login)
	if c == nil {
		t.Fatal("no session cookie from login")
	}
	return &http.Cookie{Name: c.Name, Value: c.Value}
}

// machinePOST issues a cookie-authenticated lifecycle mutation the way a browser
// client must from v0.6.0 on: application/json (the routes decode strictly) plus
// the __Host-auth_csrf double-submit pair the browser-safe gate compares. The
// gate short-circuits for bearer-only callers, so only the cookie lane needs it.
func machinePOST(t *testing.T, h http.Handler, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return machinePOSTContentType(t, h, path, body, "application/json", c)
}

// machinePOSTContentType is machinePOST with the media type under the test's
// control, so a case can drive the 415 branch.
func machinePOSTContentType(t *testing.T, h http.Handler, path, body, contentType string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest("POST", path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest("POST", path, nil)
	}
	r.Header.Set("Content-Type", contentType)
	r.Header.Set(csrfHeaderName, "tok")
	r.AddCookie(c)
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "tok"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
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

// --- transport-edge page params (crud.ParseListQuery / crud.ParseOrder) ---

// TestListServiceAccountsRejectsBadPageParams pins the wire contract of the
// shared parseListRequest helper on a real list route: a bad page or order param
// answers 400 bad_request carrying the framework parser's own sentence, sentinel
// last, instead of a fixed message that discards which param was wrong.
func TestListServiceAccountsRejectsBadPageParams(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "pageparams@example.com")

	for _, tc := range []struct{ name, query, message string }{
		{"limit not a number", "?limit=zero", `page limit conversion: strconv.Atoi: parsing "zero": invalid syntax: invalid input`},
		{"limit too small", "?limit=0", "rows value too small, must be larger than 0: invalid input"},
		{"cursor with offset", "?cursor=x&offset=1", "cursor and offset are mutually exclusive: invalid input"},
		{"unknown order field", "?order=nope:asc", "unknown order field: nope: invalid input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, "GET", "/auth/service-accounts"+tc.query, "", c)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("GET %s status = %d, want 400; body=%s", tc.query, rec.Code, rec.Body)
			}
			var body struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
			}
			if body.Code != "bad_request" {
				t.Errorf("code = %q, want bad_request", body.Code)
			}
			if body.Message != tc.message {
				t.Errorf("message = %q, want %q", body.Message, tc.message)
			}
		})
	}
}

func TestCreateServiceAccountStrictDecode(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "sd@example.com")
	rec := machinePOST(t, h, "/auth/service-accounts", `{"name":"bot","extra":1}`, c)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown-field body status = %d, want 400", rec.Code)
	}
}

// --- happy paths ---

func TestCreateServiceAccountHappyPath(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "hp@example.com")
	rec := machinePOST(t, h, "/auth/service-accounts", `{"name":"deployer","description":"CI"}`, c)
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

	create := machinePOST(t, h, "/auth/service-accounts", `{"name":"bot"}`, c)
	var sa map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &sa)
	saID, _ := sa["id"].(string)

	mint := machinePOST(t, h, "/auth/service-accounts/"+saID+"/keys", `{"name":"deploy"}`, c)
	if mint.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want 201; body=%s", mint.Code, mint.Body)
	}
	// The one response that ever carries a live credential is never cacheable.
	if got := mint.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("mint Cache-Control = %q, want no-store", got)
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
	rec := machinePOST(t, h, "/auth/service-accounts/nope/keys", `{"name":"k"}`, c)
	if rec.Code != http.StatusNotFound {
		t.Errorf("mint for unknown sa status = %d, want 404", rec.Code)
	}
}

func TestMintAPIKeyBadExpiresAt(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "exp@example.com")
	create := machinePOST(t, h, "/auth/service-accounts", `{"name":"bot"}`, c)
	var sa map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &sa)
	saID, _ := sa["id"].(string)
	rec := machinePOST(t, h, "/auth/service-accounts/"+saID+"/keys", `{"name":"k","expires_at":"not-a-time"}`, c)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad expires_at status = %d, want 400", rec.Code)
	}
}

func TestRevokeAPIKeyUnknown(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "rev@example.com")
	rec := machinePOST(t, h, "/auth/api-keys/nope/revoke", "", c)
	if rec.Code != http.StatusNotFound {
		t.Errorf("revoke unknown key status = %d, want 404", rec.Code)
	}
}

func TestRevokeAPIKeyHappyPath(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "revok@example.com")
	create := machinePOST(t, h, "/auth/service-accounts", `{"name":"bot"}`, c)
	var sa map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &sa)
	saID, _ := sa["id"].(string)
	mint := machinePOST(t, h, "/auth/service-accounts/"+saID+"/keys", `{"name":"k"}`, c)
	var minted map[string]any
	_ = json.Unmarshal(mint.Body.Bytes(), &minted)
	keyID, _ := minted["id"].(string)

	rec := machinePOST(t, h, "/auth/api-keys/"+keyID+"/revoke", "", c)
	if rec.Code != http.StatusOK {
		t.Errorf("revoke status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}

// TestMachineMutationsRequireJSONContentType pins the strict media type on the
// two body-carrying mutations: a text/plain body is 415 before any decode, the
// same posture the administrative user surface takes.
func TestMachineMutationsRequireJSONContentType(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "media@example.com")

	create := machinePOSTContentType(t, h, "/auth/service-accounts", `{"name":"bot"}`, "text/plain", c)
	if create.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("create with text/plain = %d, want 415; body=%s", create.Code, create.Body)
	}
	if code, _ := errorEnvelope(t, create); code != "unsupported_media_type" {
		t.Errorf("create 415 code = %q, want unsupported_media_type", code)
	}

	ok := machinePOST(t, h, "/auth/service-accounts", `{"name":"bot"}`, c)
	var sa map[string]any
	_ = json.Unmarshal(ok.Body.Bytes(), &sa)
	saID, _ := sa["id"].(string)

	mint := machinePOSTContentType(t, h, "/auth/service-accounts/"+saID+"/keys", `{"name":"k"}`, "text/plain", c)
	if mint.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("mint with text/plain = %d, want 415; body=%s", mint.Code, mint.Body)
	}
	if code, _ := errorEnvelope(t, mint); code != "unsupported_media_type" {
		t.Errorf("mint 415 code = %q, want unsupported_media_type", code)
	}
}

// --- ownership: the caller is the owner; owner_user_id is refused by name ---

// errorEnvelope decodes the {code,message} pair of a JSON error body so a case
// can pin the machine code AND the copy a client is told to act on.
func errorEnvelope(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Code, body.Message
}

// TestCreateServiceAccountRefusesOwnerUserIDByName pins the one-release rename
// grace: a v0.5.x client still sending owner_user_id is told the field's name
// and its replacement, not strict decode's generic "invalid request body".
func TestCreateServiceAccountRefusesOwnerUserIDByName(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "owner-field@example.com")
	rec := machinePOST(t, h, "/auth/service-accounts", `{"name":"bot","act_as_user":true,"owner_user_id":"someone-else"}`, c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("owner_user_id status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	code, message := errorEnvelope(t, rec)
	if code != "bad_request" {
		t.Errorf("code = %q, want bad_request", code)
	}
	if message != "owner_user_id is no longer accepted; the caller is the owner, or name act_as_user_id" {
		t.Errorf("message = %q", message)
	}
}

// TestCreateServiceAccountEmptyOwnerUserIDIgnored keeps the v0.5.x fixtures that
// send "owner_user_id":"" working for exactly one release.
func TestCreateServiceAccountEmptyOwnerUserIDIgnored(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "owner-empty@example.com")
	rec := machinePOST(t, h, "/auth/service-accounts", `{"name":"bot","owner_user_id":""}`, c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("empty owner_user_id status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
}

// TestCreateServiceAccountActAsUserIDRequiresActAsUser refuses the half-stated
// request rather than silently dropping the named owner.
func TestCreateServiceAccountActAsUserIDRequiresActAsUser(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "half@example.com")
	rec := machinePOST(t, h, "/auth/service-accounts", `{"name":"bot","act_as_user_id":"u-1"}`, c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("act_as_user_id without act_as_user status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	code, message := errorEnvelope(t, rec)
	if code != "bad_request" || message != "act_as_user_id requires act_as_user" {
		t.Errorf("error envelope = (%q, %q)", code, message)
	}
}

// TestCreateServiceAccountActAsSelf proves act-as-self needs no field: the owner
// is the caller, taken from the credential and never from the body.
func TestCreateServiceAccountActAsSelf(t *testing.T) {
	fx := newMachineFixture(t, allowMachineGate)
	userID, c := userIDFor(t, fx.h, "self@example.com")
	rec := machinePOST(t, fx.h, "/auth/service-accounts", `{"name":"bot","act_as_user":true}`, c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("act-as-self status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var resp serviceAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OwnerUserID != userID {
		t.Errorf("owner_user_id = %q, want the caller %q", resp.OwnerUserID, userID)
	}
	if resp.CreatedBy != userID {
		t.Errorf("created_by = %q, want the caller %q", resp.CreatedBy, userID)
	}
}

// TestCreateServiceAccountDelegated proves the gate-protected delegation path:
// act_as_user_id names another EXISTING user, who becomes the stored owner while
// the caller stays the creator.
func TestCreateServiceAccountDelegated(t *testing.T) {
	fx := newMachineFixture(t, allowMachineGate)
	otherID, _ := userIDFor(t, fx.h, "delegate-target@example.com")
	callerID, c := userIDFor(t, fx.h, "delegate-caller@example.com")

	rec := machinePOST(t, fx.h, "/auth/service-accounts", `{"name":"bot","act_as_user":true,"act_as_user_id":"`+otherID+`"}`, c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("delegated create status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var resp serviceAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OwnerUserID != otherID {
		t.Errorf("owner_user_id = %q, want the named user %q", resp.OwnerUserID, otherID)
	}
	if resp.CreatedBy != callerID {
		t.Errorf("created_by = %q, want the caller %q", resp.CreatedBy, callerID)
	}
}

// TestCreateServiceAccountUnknownActAsUserID closes the ghost-principal hole at
// the transport: an owner nobody can deactivate is 400 invalid reference, and
// the offending id stays out of the response body.
func TestCreateServiceAccountUnknownActAsUserID(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "ghost@example.com")
	rec := machinePOST(t, h, "/auth/service-accounts", `{"name":"bot","act_as_user":true,"act_as_user_id":"no-such-user"}`, c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown act_as_user_id status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	code, message := errorEnvelope(t, rec)
	if code != "bad_request" || message != "invalid reference" {
		t.Errorf("error envelope = (%q, %q)", code, message)
	}
	if strings.Contains(rec.Body.String(), "no-such-user") {
		t.Errorf("response echoed the rejected id: %s", rec.Body)
	}
}

// TestCreateServiceAccountOversizedBody proves the administrative routes decode
// through strictJSONBody's byte bound instead of buffering an arbitrary body.
func TestCreateServiceAccountOversizedBody(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "big@example.com")
	body := `{"name":"bot","description":"` + strings.Repeat("x", maxJSONBodyBytes+1) + `"}`
	rec := machinePOST(t, h, "/auth/service-accounts", body, c)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413; body=%s", rec.Code, rec.Body)
	}
	if code, _ := errorEnvelope(t, rec); code != "payload_too_large" {
		t.Errorf("code = %q, want payload_too_large", code)
	}
}

// TestMintAPIKeyMalformedBody pins the mint route on the same strict decoder.
func TestMintAPIKeyMalformedBody(t *testing.T) {
	h := newMachineHandler(t)
	c := sessionFor(t, h, "mintbad@example.com")
	create := machinePOST(t, h, "/auth/service-accounts", `{"name":"bot"}`, c)
	var sa map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &sa)
	saID, _ := sa["id"].(string)

	rec := machinePOST(t, h, "/auth/service-accounts/"+saID+"/keys", `{"name":`, c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed mint body status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	if code, _ := errorEnvelope(t, rec); code != "bad_request" {
		t.Errorf("code = %q, want bad_request", code)
	}
}
