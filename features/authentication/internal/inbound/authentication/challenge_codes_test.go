package authentication

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// errorCode decodes the "code" field of a JSON error body (the §5.8 machine code the
// transport emits).
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Code
}

// TestRespondDomainErrorNamedChallengeCodes is the direct IX-15 mapper regression: the
// three stable challenge sentinels emit the named §5.8 machine codes and their pinned
// statuses through the single JSON error seam, and any non-challenge error still falls
// back to the generic sdk-kind mapping (no code leak, no reclassification).
func TestRespondDomainErrorNamedChallengeCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"expired", authsvc.ErrChallengeExpired, http.StatusGone, "challenge_expired"},
		{"invalid", authsvc.ErrChallengeInvalid, http.StatusBadRequest, "challenge_invalid"},
		{"locked out", authsvc.ErrTooManyAttempts, http.StatusForbidden, "too_many_attempts"},
		{"non-challenge falls back", sdk.ErrNotFound, http.StatusNotFound, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			respondDomainError(rec, tt.err)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body)
			}
			if got := errorCode(t, rec); got != tt.wantCode {
				t.Fatalf("code = %q, want %q", got, tt.wantCode)
			}
		})
	}
}

// TestVerifyJSONChallengeInvalidCode proves the JSON verify arm surfaces the named
// challenge_invalid code (400) for an unknown account — indistinguishable from a wrong
// code (enumeration protection), now with the machine-readable §5.8 code.
func TestVerifyJSONChallengeInvalidCode(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "POST", "/auth/verify", `{"email":"ghost@example.com","code":"123456"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
	if got := errorCode(t, rec); got != "challenge_invalid" {
		t.Fatalf("code = %q, want challenge_invalid", got)
	}
}

// TestVerifyJSONChallengeLockedOutCode proves that exhausting the wrong-code budget on
// a real registration challenge surfaces the named too_many_attempts code (403).
func TestVerifyJSONChallengeLockedOutCode(t *testing.T) {
	h := newTestHandler(t, nil)
	if reg := do(t, h, "POST", "/auth/register",
		`{"email":"lock@example.com","password":"password123456789","display_name":"L"}`); reg.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", reg.Code, reg.Body)
	}
	var last *httptest.ResponseRecorder
	for i := 0; i < challenge.MaxAttempts; i++ {
		last = do(t, h, "POST", "/auth/verify", `{"email":"lock@example.com","code":"000001"}`)
	}
	if last.Code != http.StatusForbidden {
		t.Fatalf("final wrong-code status = %d, want 403; body=%s", last.Code, last.Body)
	}
	if got := errorCode(t, last); got != "too_many_attempts" {
		t.Fatalf("code = %q, want too_many_attempts", got)
	}
}

// TestVerifyJSONChallengeExpiredCode proves an expired registration challenge surfaces
// the named challenge_expired code (410). The service clock is advanced past the
// challenge TTL so the atomic consume reports expiry.
func TestVerifyJSONChallengeExpiredCode(t *testing.T) {
	clk := &testClock{t: time.Now().UTC()}
	h := newVerifyClockHandler(t, clk.now)
	if reg := do(t, h, "POST", "/auth/register",
		`{"email":"stale@example.com","password":"password123456789","display_name":"S"}`); reg.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", reg.Code, reg.Body)
	}
	clk.advance(16 * time.Minute) // registration verify TTL is 15m
	rec := do(t, h, "POST", "/auth/verify", `{"email":"stale@example.com","code":"000001"}`)
	if rec.Code != http.StatusGone {
		t.Fatalf("expired verify status = %d, want 410; body=%s", rec.Code, rec.Body)
	}
	if got := errorCode(t, rec); got != "challenge_expired" {
		t.Fatalf("code = %q, want challenge_expired", got)
	}
}

// TestVerifyFormChallengeParityNoCodeLeak proves transport parity: the SAME service
// error (unknown account → challenge_invalid) that emits challenge_invalid (400) on the
// JSON arm re-renders the form arm at the same 400 with generic copy, and the machine
// code never leaks into the user-facing HTML.
func TestVerifyFormChallengeParityNoCodeLeak(t *testing.T) {
	h := newHTMLTestHandler(t, stubViews{})

	jsonRec := do(t, h, "POST", "/auth/verify", `{"email":"ghost@example.com","code":"123456"}`)
	if jsonRec.Code != http.StatusBadRequest || errorCode(t, jsonRec) != "challenge_invalid" {
		t.Fatalf("JSON arm = %d/%q, want 400/challenge_invalid; body=%s", jsonRec.Code, errorCode(t, jsonRec), jsonRec.Body)
	}

	form := "email=ghost@example.com&code=123456"
	r := httptest.NewRequest("POST", "/auth/verify", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formRec := httptest.NewRecorder()
	h.ServeHTTP(formRec, r)

	if formRec.Code != jsonRec.Code {
		t.Fatalf("form arm status = %d, want parity with JSON arm %d", formRec.Code, jsonRec.Code)
	}
	if body := formRec.Body.String(); strings.Contains(body, "challenge_invalid") ||
		strings.Contains(body, "too_many_attempts") || strings.Contains(body, "challenge_expired") {
		t.Fatalf("form re-render leaked a machine code: %q", body)
	}
}

// testClock is a mutable clock for the challenge-expiry regression.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newVerifyClockHandler builds the JSON route table over the in-memory rail with an
// injected clock, so a test can advance time past a challenge TTL to drive the expired
// disposition end-to-end.
func newVerifyClockHandler(t *testing.T, clock func() time.Time) http.Handler {
	t.Helper()
	users := newMemUsers()
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	svc := authsvc.NewService(authsvc.Deps{
		Users:       users,
		Identifiers: newMemIdentifiers(users),
		Passwords:   &memPasswords{m: map[string]string{}},
		Sessions:    &memSessions{m: map[string]session.Session{}},
		Challenges:  challenges,
		Protector:   memProtector{},
		Hasher:      fakeHasher{},
		Deliver:     router,
		Queue:       stubQueue{},
		Limiter:     ratelimiter.NewMemory(),
		Cookie:      authsvc.CookieConfig{},
		TokenSigner: newFakeSigner(),
		Clock:       clock,
	})
	h := web.NewWebHandler()
	Mount(h, svc, nil, nil, "", MutationSecurity{}, nil, nil)
	return h
}
