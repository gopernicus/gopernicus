package authenticationhttp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// fakeGlobalRevoker ends every session of the fixture's session store in one
// batch, standing in for a store's session.GlobalRevoker.
type fakeGlobalRevoker struct {
	sess  *memSessions
	mu    sync.Mutex
	calls int
	// onBatch runs first in every batch; sweepErr fails the final sweep;
	// ctxErr is the batch context's error after onBatch.
	onBatch  func()
	sweepErr error
	ctxErr   error
}

func (f *fakeGlobalRevoker) RevokeAllBatch(ctx context.Context, _ string, _ int, _ time.Time) (session.GlobalRevocationBatch, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.onBatch != nil {
		f.onBatch()
	}
	f.ctxErr = ctx.Err()
	f.sess.mu.Lock()
	defer f.sess.mu.Unlock()
	users := map[string]bool{}
	n := len(f.sess.m)
	for id, s := range f.sess.m {
		users[s.UserID] = true
		delete(f.sess.m, id)
	}
	return session.GlobalRevocationBatch{Users: len(users), Sessions: n, Done: true, LastUserID: "zz"}, nil
}

func (f *fakeGlobalRevoker) SweepOrphans(context.Context, time.Time) (int, int, error) {
	return 0, 0, f.sweepErr
}

type revokeAllFixture struct {
	h       http.Handler
	svc     *testService
	revoker *fakeGlobalRevoker
	events  *spySecurityEvents
	mu      sync.Mutex
	checks  []UserAdminCheckRequest
	deny    bool
}

func newRevokeAllFixture(t *testing.T, optIn bool) *revokeAllFixture {
	t.Helper()
	users := newMemUsers()
	sess := &memSessions{m: map[string]session.Session{}}
	f := &revokeAllFixture{revoker: &fakeGlobalRevoker{sess: sess}, events: newSpySecurityEvents()}
	f.svc = newServiceWithFakes(authenticationFixture{
		Users:           users,
		Identifiers:     newMemIdentifiers(users),
		Passwords:       &memPasswords{m: map[string]string{}},
		Sessions:        sess,
		Hasher:          fakeHasher{},
		Limiter:         ratelimiter.NewMemory(),
		ServiceAccounts: &memServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:         &memAPIKeys{m: map[string]apikey.APIKey{}},
		UserAdmin:       memUserAdmin{},
		SecurityEvents:  f.events,
		GlobalSessions:  f.revoker,
		TokenSigner:     newFakeSigner(),
	})
	deps := mountDeps{Auth: f.svc, ListStrategy: list.StrategyCursor, UserAdminCheck: f.check}
	if optIn {
		deps.GlobalRevoker = f.svc
	}
	h := web.NewWebHandler()
	mount(h, deps)
	f.h = h
	return f
}

func (f *revokeAllFixture) check(_ context.Context, req UserAdminCheckRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks = append(f.checks, req)
	if f.deny {
		return fmt.Errorf("not an operator: %w", sdk.ErrForbidden)
	}
	return nil
}

// revokeAll posts the empty body with the session cookie and the CSRF pair, or
// with a bearer when bearer is set.
func (f *revokeAllFixture) revokeAll(c *http.Cookie, bearer string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/auth/admin/sessions/revoke-all", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	if c != nil {
		r.AddCookie(c)
		r.Header.Set(csrfHeaderName, "tok")
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "tok"})
	}
	return serve(f.h, r)
}

func TestRevokeAllRouteAbsentWithoutTheOptIn(t *testing.T) {
	f := newRevokeAllFixture(t, false)
	c := sessionFor(t, f.h, "absent@example.com")
	if rec := f.revokeAll(c, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("revoke-all without the opt-in = %d, want 404", rec.Code)
	}
	if len(f.checks) != 0 || f.revoker.calls != 0 {
		t.Fatalf("an unmounted route ran: checks=%v calls=%d", f.checks, f.revoker.calls)
	}
}

// TestRevokeAllRouteEndsEverySession pins the wire contract: the host check
// sees the revoke-all-sessions action with no target, the 200 body carries the
// counts and the UTC RFC 3339 nanosecond start, both session cookies are
// cleared, the caller's next request is a 401, and the rail records started
// then success attributed to the caller.
func TestRevokeAllRouteEndsEverySession(t *testing.T) {
	f := newRevokeAllFixture(t, true)
	c := sessionFor(t, f.h, "operator@example.com")
	other := sessionFor(t, f.h, "bystander@example.com")

	rec := f.revokeAll(c, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke-all = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	body := rec.Body.String()
	at := strings.Index(body, `"revoked_at":"`)
	if at < 0 {
		t.Fatalf("body = %s, want revoked_at", body)
	}
	revokedAt := body[at+len(`"revoked_at":"`) : strings.LastIndex(body, `"`)]
	if ts, err := time.Parse(time.RFC3339Nano, revokedAt); err != nil || !strings.HasSuffix(revokedAt, "Z") || ts.IsZero() {
		t.Fatalf("revoked_at = %q, want UTC RFC 3339 nanos (%v)", revokedAt, err)
	}
	want := `{"users":2,"sessions":2,"grants":0,"batches":1,"revoked_at":"` + revokedAt + `"}`
	if body != want {
		t.Fatalf("body = %s\nwant   %s", body, want)
	}
	cleared := map[string]bool{}
	for _, ck := range (&http.Response{Header: rec.Header()}).Cookies() {
		if ck.MaxAge < 0 {
			cleared[ck.Name] = true
		}
	}
	if !cleared["session"] || !cleared["session_refresh"] {
		t.Fatalf("cleared cookies = %v, want session and session_refresh", cleared)
	}
	if len(f.checks) != 1 || f.checks[0].Action != UserAdminRevokeAllSessions || f.checks[0].TargetUserID != "" {
		t.Fatalf("host checks = %+v, want one revoke-all-sessions check without a target", f.checks)
	}
	for _, ck := range []*http.Cookie{c, other} {
		if me := do(t, f.h, "GET", "/auth/me", "", ck); me.Code != http.StatusUnauthorized {
			t.Fatalf("/auth/me after revoke-all = %d, want 401", me.Code)
		}
	}
	var statuses []string
	for _, evt := range f.events.recorded() {
		if evt.EventType == securityevent.TypeAllSessionsRevokedGlobal {
			statuses = append(statuses, evt.EventStatus)
			if evt.Actor.Type != "user" || evt.Actor.ID == "" {
				t.Errorf("row actor = %+v, want the operator", evt.Actor)
			}
		}
	}
	if strings.Join(statuses, ",") != "started,success" {
		t.Fatalf("all_sessions_revoked_global rows = %v, want started then success", statuses)
	}
}

func TestRevokeAllRouteGates(t *testing.T) {
	f := newRevokeAllFixture(t, true)
	c := sessionFor(t, f.h, "gates@example.com")

	f.deny = true
	if rec := f.revokeAll(c, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin = %d, want the host's 403", rec.Code)
	}
	f.deny = false

	ctx := context.Background()
	sa, err := f.svc.CreateServiceAccount(ctx, "admin", "operator", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := f.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rec := f.revokeAll(nil, key); rec.Code != http.StatusUnauthorized {
		t.Fatalf("API key = %d, want 401 whatever the host check says", rec.Code)
	}
	if len(f.checks) != 1 {
		t.Fatalf("the API key reached the host check: %+v", f.checks)
	}

	if rec := f.revokeAll(c, ""); rec.Code != http.StatusOK {
		t.Fatalf("admin = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	again := sessionFor(t, f.h, "gates@example.com")
	if rec := f.revokeAll(again, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second call within a minute = %d, want 429", rec.Code)
	}
	if f.revoker.calls != 1 {
		t.Fatalf("revocations = %d, want one", f.revoker.calls)
	}
}

func TestRevokeAllRouteRequiresTheOptInWiring(t *testing.T) {
	f := newRevokeAllFixture(t, true)
	if _, err := New(f.svc.Service, "development", WithGlobalSessionRevocationRoute(true)); err == nil {
		t.Fatal("the route without UserAdminCheck constructed")
	}
	if _, err := New(f.svc.Service, "development", WithGlobalSessionRevocationRoute(true), WithUserAdminCheck(f.check)); err != nil {
		t.Fatalf("the route beside a check and a wired revocation: %v", err)
	}
}

// TestRevokeAllRunsDetachedFromTheRequest is M2: a client that disconnects
// mid-run does not cancel the fleet revoke.
func TestRevokeAllRunsDetachedFromTheRequest(t *testing.T) {
	f := newRevokeAllFixture(t, true)
	c := sessionFor(t, f.h, "detached@example.com")
	ctx, cancel := context.WithCancel(context.Background())
	f.revoker.onBatch = cancel
	r := httptest.NewRequest("POST", "/auth/admin/sessions/revoke-all", strings.NewReader(`{}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(c)
	r.Header.Set(csrfHeaderName, "tok")
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "tok"})
	rec := serve(f.h, r)
	if f.revoker.ctxErr != nil || rec.Code != http.StatusOK {
		t.Fatalf("a disconnect mid-run = %d, batch ctx err %v; want the run to finish", rec.Code, f.revoker.ctxErr)
	}
}

// TestRevokeAllFailureCarriesPartialCounts is L5: a run that fails after a
// committed batch answers its error with the counts and clears the cookies.
func TestRevokeAllFailureCarriesPartialCounts(t *testing.T) {
	f := newRevokeAllFixture(t, true)
	c := sessionFor(t, f.h, "partial@example.com")
	f.revoker.sweepErr = sdk.ErrUnavailable
	rec := f.revokeAll(c, "")
	body := rec.Body.String()
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(body, `"code":"unavailable"`) || !strings.Contains(body, `"users":1`) || !strings.Contains(body, `"batches":1`) {
		t.Fatalf("failed run = %d %s, want 503 with the partial counts", rec.Code, body)
	}
	cleared := false
	for _, ck := range (&http.Response{Header: rec.Header()}).Cookies() {
		cleared = cleared || (ck.Name == "session" && ck.MaxAge < 0)
	}
	if !cleared {
		t.Fatal("the session cookie was not cleared after a committed batch")
	}
}

// countingRevoker is the route's global revocation, counting runs.
type countingRevoker struct{ runs int }

func (c *countingRevoker) GlobalSessionRevocationEnabled() bool { return true }
func (c *countingRevoker) RevokeAllSessions(context.Context, authlogic.Principal) (authlogic.GlobalRevocation, error) {
	c.runs++
	return authlogic.GlobalRevocation{Users: 1, Batches: 1, RevokedAt: time.Now()}, nil
}

// TestRevokeAllAdmitsExternalAdmins pins the owner's H1 ruling: an external
// token reaches the route (AAL2 → 200, AAL1 → the admins MFA challenge, a
// revoked provider subject → refused under Live), while a delegated access
// token, which names no audience here, and an API key are refused.
func TestRevokeAllAdmitsExternalAdmins(t *testing.T) {
	h := newExternalHarness(t, externalSetup{})
	mfa := h.identity("mfa", func(id *sdkauth.Identity) { id.MFA = true })
	delegated, _ := h.delegatedToken(t, "connection-revoke")
	key, _, _ := h.mintKey(t, "ops", false, "", time.Time{})
	revoker := &countingRevoker{}
	router := web.NewWebHandler()
	mount(router, mountDeps{Auth: h.svc, ListStrategy: list.StrategyCursor, AdministrationMFA: true, GlobalRevoker: revoker,
		UserAdminCheck: func(context.Context, UserAdminCheckRequest) error { return nil }})
	post := func(bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/auth/admin/sessions/revoke-all", strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+bearer)
		return serve(router, r)
	}

	if rec := post(delegated); rec.Code != http.StatusUnauthorized || revoker.runs != 0 {
		t.Fatalf("delegated token = %d runs=%d, want 401", rec.Code, revoker.runs)
	}
	if rec := post(key); rec.Code != http.StatusUnauthorized || revoker.runs != 0 {
		t.Fatalf("API key = %d runs=%d, want 401", rec.Code, revoker.runs)
	}
	if rec := post(h.token); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("WWW-Authenticate"), "insufficient_user_authentication") || revoker.runs != 0 {
		t.Fatalf("AAL1 external admin = %d %q, want the MFA challenge", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if rec := post(mfa); rec.Code != http.StatusOK || revoker.runs != 1 {
		t.Fatalf("AAL2 external admin = %d %s, want 200", rec.Code, rec.Body)
	}

	// A provider-disabled subject fails closed at the Live tier (a fresh
	// harness: the revocation facts are cached per subject).
	gone := newExternalHarness(t, externalSetup{})
	gone.revocation.mu.Lock()
	gone.revocation.facts = sdkauth.RevocationFacts{Disabled: true}
	gone.revocation.mu.Unlock()
	goneRouter := web.NewWebHandler()
	mount(goneRouter, mountDeps{Auth: gone.svc, ListStrategy: list.StrategyCursor, AdministrationMFA: true, GlobalRevoker: revoker,
		UserAdminCheck: func(context.Context, UserAdminCheckRequest) error { return nil }})
	r := httptest.NewRequest("POST", "/auth/admin/sessions/revoke-all", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+gone.identity("mfa", func(id *sdkauth.Identity) { id.MFA = true }))
	if rec := serve(goneRouter, r); rec.Code != http.StatusUnauthorized || revoker.runs != 1 {
		t.Fatalf("a revoked provider subject = %d runs=%d, want 401 under Live", rec.Code, revoker.runs)
	}
}
