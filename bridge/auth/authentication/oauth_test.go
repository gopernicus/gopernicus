package authentication

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/bridge/transit/httpmid"
	coreauth "github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/infrastructure/oauth"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// ---------------------------------------------------------------------------
// Stub OAuth account repo (minimal — only the methods httpOAuthGetLinked
// transitively touches via Authenticator.GetLinkedAccounts).
// ---------------------------------------------------------------------------

type stubOAuthRepo struct {
	mu       sync.Mutex
	accounts map[string][]coreauth.OAuthAccount // by user_id
}

func newStubOAuthRepo() *stubOAuthRepo {
	return &stubOAuthRepo{accounts: make(map[string][]coreauth.OAuthAccount)}
}

func (r *stubOAuthRepo) GetByProvider(_ context.Context, provider, providerUserID string) (coreauth.OAuthAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, list := range r.accounts {
		for _, a := range list {
			if a.Provider == provider && a.ProviderUserID == providerUserID {
				return a, nil
			}
		}
	}
	return coreauth.OAuthAccount{}, errs.ErrNotFound
}

func (r *stubOAuthRepo) GetByUserID(_ context.Context, userID string) ([]coreauth.OAuthAccount, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.accounts[userID]
	out := make([]coreauth.OAuthAccount, len(list))
	copy(out, list)
	return out, nil
}

func (r *stubOAuthRepo) Create(_ context.Context, a coreauth.OAuthAccount) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accounts[a.UserID] = append(r.accounts[a.UserID], a)
	return nil
}

func (r *stubOAuthRepo) Delete(_ context.Context, userID, provider string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.accounts[userID]
	out := list[:0]
	for _, a := range list {
		if a.Provider != provider {
			out = append(out, a)
		}
	}
	r.accounts[userID] = out
	return nil
}

// newGetLinkedTestBridge constructs a minimal Bridge wired with just enough
// to drive httpOAuthGetLinked. The returned authenticator only has its OAuth
// repo wired — every other dependency is nil/zero. Do NOT reuse this for
// handlers that touch other authenticator subsystems; build a richer harness
// (see the bridge handler tests roadmap entry) for those.
func newGetLinkedTestBridge(t *testing.T) (*Bridge, *stubOAuthRepo) {
	t.Helper()

	oauthRepo := newStubOAuthRepo()

	// providers map must be non-nil for requireOAuth() to pass; the actual
	// providers don't matter because GetLinkedAccounts only calls oauthRepo.
	auth := coreauth.NewAuthenticator(
		"test",
		coreauth.Repositories{}, // unused for this handler — fine as zero value
		nil,                     // hasher — unused
		nil,                     // signer — unused
		nil,                     // bus — unused
		coreauth.Config{},
		coreauth.WithOAuth(map[string]oauth.Provider{}, oauthRepo),
	)

	b := New(nil /* log */, Config{}, auth, nil /* rateLimiter */)
	return b, oauthRepo
}

// requestWithSubject builds an httptest request with the given user_id
// injected via the same context helper the auth middleware uses in production.
func requestWithSubject(method, path, userID string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	ctx := httpmid.SetSubject(r.Context(), "user:"+userID)
	return r.WithContext(ctx)
}

// ===========================================================================
// Response shape contract: OAuthAccountResponse
// ===========================================================================

// TestOAuthAccountResponse_JSONShape is a golden-file marshaling test that
// asserts the wire format of [OAuthAccountResponse] byte-for-byte.
//
// The doc comment on OAuthAccountResponse declares stability: field names,
// JSON tags, types, and the array-not-envelope choice are all stable.
// Frontends bind directly to this shape.
//
// If this test fails because you intentionally changed the shape, that is a
// breaking change to all clients. Update the test, the doc comment on
// OAuthAccountResponse, and ship a changelog entry — do not just update the
// test to match the new output.
func TestOAuthAccountResponse_JSONShape(t *testing.T) {
	// Frozen timestamp so the test is deterministic and the expected JSON is
	// readable. UTC RFC3339 with second precision.
	linkedAt, err := time.Parse(time.RFC3339, "2026-01-15T10:30:00Z")
	if err != nil {
		t.Fatalf("parse linkedAt: %v", err)
	}

	resp := OAuthAccountResponse{
		Provider:      "google",
		ProviderEmail: "user@example.com",
		LinkedAt:      linkedAt,
	}

	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"provider":"google","provider_email":"user@example.com","linked_at":"2026-01-15T10:30:00Z"}`
	if string(got) != want {
		t.Errorf("OAuthAccountResponse JSON shape changed!\n  got:  %s\n  want: %s\n\n"+
			"This is a public API contract — see the doc comment on OAuthAccountResponse.",
			string(got), want)
	}
}

// TestOAuthAccountResponse_ArrayNotEnvelope asserts that a slice of
// [OAuthAccountResponse] marshals to a top-level JSON array (NOT an envelope
// like {"items": [...]}). Frontends and generated SDKs bind directly to
// []OAuthAccountResponse and would break if this changed.
func TestOAuthAccountResponse_ArrayNotEnvelope(t *testing.T) {
	linkedAt, _ := time.Parse(time.RFC3339, "2026-01-15T10:30:00Z")
	resp := []OAuthAccountResponse{
		{Provider: "google", ProviderEmail: "u@example.com", LinkedAt: linkedAt},
		{Provider: "github", ProviderEmail: "u@example.com", LinkedAt: linkedAt},
	}

	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if len(got) == 0 || got[0] != '[' {
		t.Errorf("response must be a top-level JSON array, got: %s", string(got))
	}

	// Also assert no envelope key sneaked in via a struct change.
	var asMap map[string]any
	if err := json.Unmarshal(got, &asMap); err == nil {
		t.Errorf("response should not decode as a map (envelope detected): %s", string(got))
	}
}

// TestOAuthAccountResponse_EmptyListIsEmptyArray asserts that an empty list
// of linked accounts marshals to "[]", not "null". Empty arrays are the
// universally safe choice for client iteration.
func TestOAuthAccountResponse_EmptyListIsEmptyArray(t *testing.T) {
	// The handler uses make([]OAuthAccountResponse, len(accounts)) which
	// produces an empty (non-nil) slice when there are zero accounts. This
	// test pins that behavior — a future refactor that returned a nil slice
	// would marshal to "null" and break clients that expect to iterate.
	resp := make([]OAuthAccountResponse, 0)
	got, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != "[]" {
		t.Errorf("empty list must marshal to []; got %s", string(got))
	}
}

// ===========================================================================
// Handler: httpOAuthGetLinked
// ===========================================================================

func TestHTTPOAuthGetLinked_ReturnsLinkedAccounts(t *testing.T) {
	b, oauthRepo := newGetLinkedTestBridge(t)

	const userID = "user_42"
	linkedAt, _ := time.Parse(time.RFC3339, "2026-01-15T10:30:00Z")

	// Pre-populate the repo with two linked accounts.
	if err := oauthRepo.Create(context.Background(), coreauth.OAuthAccount{
		UserID:         userID,
		Provider:       "google",
		ProviderUserID: "google_123",
		ProviderEmail:  "user@example.com",
		LinkedAt:       linkedAt,
	}); err != nil {
		t.Fatalf("seed google: %v", err)
	}
	if err := oauthRepo.Create(context.Background(), coreauth.OAuthAccount{
		UserID:         userID,
		Provider:       "github",
		ProviderUserID: "github_456",
		ProviderEmail:  "user@example.com",
		LinkedAt:       linkedAt,
	}); err != nil {
		t.Fatalf("seed github: %v", err)
	}

	w := httptest.NewRecorder()
	r := requestWithSubject(http.MethodGet, "/auth/oauth/linked", userID)

	b.httpOAuthGetLinked(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}

	// Decode against the EXACT response type. If a future refactor breaks the
	// shape, this decode fails (or yields zero values), surfacing the regression.
	var got []OAuthAccountResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, w.Body.String())
	}

	if len(got) != 2 {
		t.Fatalf("got %d accounts, want 2; body: %s", len(got), w.Body.String())
	}

	// Order is not specified by the contract, so check by map.
	byProvider := make(map[string]OAuthAccountResponse, len(got))
	for _, a := range got {
		byProvider[a.Provider] = a
	}
	if _, ok := byProvider["google"]; !ok {
		t.Errorf("missing google in response: %+v", got)
	}
	if _, ok := byProvider["github"]; !ok {
		t.Errorf("missing github in response: %+v", got)
	}
	if g := byProvider["google"]; g.ProviderEmail != "user@example.com" || !g.LinkedAt.Equal(linkedAt) {
		t.Errorf("google account fields wrong: %+v", g)
	}
}

func TestHTTPOAuthGetLinked_EmptyListReturnsArray(t *testing.T) {
	b, _ := newGetLinkedTestBridge(t)

	w := httptest.NewRecorder()
	r := requestWithSubject(http.MethodGet, "/auth/oauth/linked", "user_with_no_links")
	b.httpOAuthGetLinked(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// Must be "[]", not "null". This is the part of the contract that's
	// easiest to break with an innocent-looking refactor (returning a nil
	// slice instead of an empty one).
	body := w.Body.String()
	// Trim trailing newline if any encoder adds one.
	for len(body) > 0 && (body[len(body)-1] == '\n' || body[len(body)-1] == '\r') {
		body = body[:len(body)-1]
	}
	if body != "[]" {
		t.Errorf("empty linked list should be [], got %q", body)
	}
}

func TestHTTPOAuthGetLinked_OnlyReturnsCallersAccounts(t *testing.T) {
	b, oauthRepo := newGetLinkedTestBridge(t)

	const callerID = "user_caller"
	const otherID = "user_other"

	// Seed accounts for both users — the caller has google, the other has github.
	_ = oauthRepo.Create(context.Background(), coreauth.OAuthAccount{
		UserID: callerID, Provider: "google", ProviderUserID: "g1", LinkedAt: time.Now().UTC(),
	})
	_ = oauthRepo.Create(context.Background(), coreauth.OAuthAccount{
		UserID: otherID, Provider: "github", ProviderUserID: "g2", LinkedAt: time.Now().UTC(),
	})

	w := httptest.NewRecorder()
	r := requestWithSubject(http.MethodGet, "/auth/oauth/linked", callerID)
	b.httpOAuthGetLinked(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var got []OAuthAccountResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("got %d accounts, want 1 (only caller's); body: %s", len(got), w.Body.String())
	}
	if got[0].Provider != "google" {
		t.Errorf("returned wrong user's account: %+v", got[0])
	}
}

// Compile-time check that stubOAuthRepo satisfies the core OAuth account
// repository interface, even though the tests only call GetByUserID.
var _ coreauth.OAuthAccountRepository = (*stubOAuthRepo)(nil)
