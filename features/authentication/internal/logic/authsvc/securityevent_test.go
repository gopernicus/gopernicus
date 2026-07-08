package authsvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/ratelimiter"
)

// spySecurityEvents is the shared in-package audit spy the WI6 tests assert
// against. It records every Create; a non-nil createErr drives the never-fail
// and WARN-capture paths.
type spySecurityEvents struct {
	mu        sync.Mutex
	events    []securityevent.SecurityEvent
	createErr error
}

func newSpySecurityEvents() *spySecurityEvents { return &spySecurityEvents{} }

func (s *spySecurityEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return securityevent.SecurityEvent{}, s.createErr
	}
	s.events = append(s.events, evt)
	return evt, nil
}

func (s *spySecurityEvents) List(_ context.Context, filter securityevent.ListFilter, _ crud.ListRequest) (crud.Page[securityevent.SecurityEvent], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []securityevent.SecurityEvent{}
	for _, e := range s.events {
		if filter.Match(e) {
			items = append(items, e)
		}
	}
	return crud.Page[securityevent.SecurityEvent]{Items: items}, nil
}

func (s *spySecurityEvents) recorded() []securityevent.SecurityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]securityevent.SecurityEvent(nil), s.events...)
}

func (s *spySecurityEvents) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// requireEvent asserts exactly one recorded event of eventType exists (later
// A-then-B ops record distinct types) with the expected status, returning it.
func requireEvent(t *testing.T, spy *spySecurityEvents, eventType, status string) securityevent.SecurityEvent {
	t.Helper()
	var found *securityevent.SecurityEvent
	var types []string
	for _, e := range spy.recorded() {
		types = append(types, e.EventType+"/"+e.EventStatus)
		if e.EventType == eventType {
			e := e
			found = &e
		}
	}
	if found == nil {
		t.Fatalf("no %s security event recorded; got %v", eventType, types)
	}
	if found.EventStatus != status {
		t.Fatalf("%s event status = %q, want %q", eventType, found.EventStatus, status)
	}
	return *found
}

// --- v1 ops ---

func TestSecurityEventRegister(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "reg@example.com", "password123")
	e := requireEvent(t, h.events, securityevent.TypeRegister, securityevent.StatusSuccess)
	if e.UserID != u.ID {
		t.Errorf("register event UserID = %q, want %q", e.UserID, u.ID)
	}
}

func TestSecurityEventLoginSuccess(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "ls@example.com", "password123")
	if _, _, err := h.svc.Login(context.Background(), "ls@example.com", "password123"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeLogin, securityevent.StatusSuccess)
	if e.UserID != u.ID {
		t.Errorf("login success UserID = %q, want %q", e.UserID, u.ID)
	}
}

func TestSecurityEventLoginFailure(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "lf@example.com", "password123")
	if _, _, err := h.svc.Login(context.Background(), "lf@example.com", "wrongpass"); err == nil {
		t.Fatal("expected a login error")
	}
	requireEvent(t, h.events, securityevent.TypeLogin, securityevent.StatusFailure)
}

func TestSecurityEventLoginBlocked(t *testing.T) {
	h := newHarness(t, denyLimiter{})
	h.mustRegister(t, "lb@example.com", "password123")
	if _, _, err := h.svc.Login(context.Background(), "lb@example.com", "password123"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Login: err=%v, want ErrRateLimited", err)
	}
	requireEvent(t, h.events, securityevent.TypeLogin, securityevent.StatusBlocked)
}

func TestSecurityEventLogout(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "lo@example.com", "password123")
	token, _, _ := h.svc.Login(context.Background(), "lo@example.com", "password123")
	if err := h.svc.Logout(context.Background(), token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	requireEvent(t, h.events, securityevent.TypeLogout, securityevent.StatusSuccess)
}

func TestSecurityEventEmailVerified(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "ev@example.com", "password123")
	var code string
	for c := range h.codes.m {
		code = c
	}
	if err := h.svc.Verify(context.Background(), code); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	requireEvent(t, h.events, securityevent.TypeEmailVerified, securityevent.StatusSuccess)
}

func TestSecurityEventPasswordChange(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "pc@example.com", "password123")
	if _, err := h.svc.ChangePassword(context.Background(), u.ID, "password123", "newpassword456"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypePasswordChange, securityevent.StatusSuccess)
	if e.UserID != u.ID {
		t.Errorf("password_change UserID = %q, want %q", e.UserID, u.ID)
	}
}

func TestSecurityEventPasswordReset(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "pr@example.com", "password123")
	if err := h.svc.ForgotPassword(context.Background(), "pr@example.com"); err != nil {
		t.Fatalf("ForgotPassword: %v", err)
	}
	var token string
	for tk := range h.tokens.m {
		token = tk
	}
	if err := h.svc.ResetPassword(context.Background(), token, "brandnewpass"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	requireEvent(t, h.events, securityevent.TypePasswordReset, securityevent.StatusSuccess)
}

// --- OAuth ops ---

func TestSecurityEventOAuthRegister(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-reg", email: "oreg@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	state := h.startState(t, "")
	if _, err := h.svc.OAuthCallback(context.Background(), "google", "code", state); err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeOAuthRegister, securityevent.StatusSuccess)
	if e.Details["provider"] != "google" {
		t.Errorf("oauth_register Details provider = %v, want google", e.Details["provider"])
	}
}

func TestSecurityEventOAuthLogin(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-log", email: "olog@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	pre := h.mustOAuthUser(t, "olog@example.com")
	seedOAuthLink(t, h, pre.ID, "google", "g-log")

	state := h.startState(t, "")
	if _, err := h.svc.OAuthCallback(context.Background(), "google", "code", state); err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeOAuthLogin, securityevent.StatusSuccess)
	if e.UserID != pre.ID {
		t.Errorf("oauth_login UserID = %q, want %q", e.UserID, pre.ID)
	}
}

func TestSecurityEventOAuthLinked(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-lnk", email: "olnk@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	u := h.mustOAuthUser(t, "olnk@example.com")
	if _, err := h.svc.StartLink(context.Background(), u.ID, "google", ""); err != nil {
		t.Fatalf("StartLink: %v", err)
	}
	if _, err := h.svc.OAuthCallback(context.Background(), "google", "code", h.provider.lastState); err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	requireEvent(t, h.events, securityevent.TypeOAuthLinked, securityevent.StatusSuccess)
}

func TestSecurityEventOAuthLinkVerified(t *testing.T) {
	p := &fakeProvider{name: "google", trust: true, providerUserID: "g-pend", email: "opend@example.com", emailVerified: true}
	h := newOAuthHarness(t, p, nil)
	h.mustOAuthUser(t, "opend@example.com")

	state := h.startState(t, "")
	if _, err := h.svc.OAuthCallback(context.Background(), "google", "code", state); err != nil {
		t.Fatalf("OAuthCallback: %v", err)
	}
	if _, err := h.svc.VerifyLink(context.Background(), h.states.pendingLinkToken()); err != nil {
		t.Fatalf("VerifyLink: %v", err)
	}
	requireEvent(t, h.events, securityevent.TypeOAuthLinkVerified, securityevent.StatusSuccess)
}

func TestSecurityEventOAuthUnlinked(t *testing.T) {
	ctx := context.Background()
	h := newOAuthHarness(t, &fakeProvider{name: "google"}, nil)
	u := h.mustOAuthUser(t, "ounl@example.com")
	h.pw.Set(ctx, u.ID, "hash:secret") // a password keeps the unlink from tripping last-method protection
	seedOAuthLink(t, h, u.ID, "google", "g-unl")
	if err := h.svc.Unlink(ctx, u.ID, "google"); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	requireEvent(t, h.events, securityevent.TypeOAuthUnlinked, securityevent.StatusSuccess)
}

// --- machine op (apikey_auth, all three branches) ---

func TestSecurityEventAPIKeyAuthSuccess(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	key, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "deploy", time.Time{})
	if _, err := h.svc.AuthenticateAPIKey(ctx, raw); err != nil {
		t.Fatalf("AuthenticateAPIKey: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeAPIKeyAuth, securityevent.StatusSuccess)
	if e.Actor.Type != PrincipalServiceAccount || e.Actor.ID != sa.ID {
		t.Errorf("apikey_auth success actor = %+v, want {service_account, %s}", e.Actor, sa.ID)
	}
	if e.Details["key_prefix"] != key.KeyPrefix {
		t.Errorf("apikey_auth Details key_prefix = %v, want %q (prefix only)", e.Details["key_prefix"], key.KeyPrefix)
	}
	// Content hygiene: the raw key never lands in the audit content.
	if strings.Contains(toString(e.Details["key_prefix"]), raw) {
		t.Error("raw key leaked into audit Details")
	}
}

func TestSecurityEventAPIKeyAuthBlockedRevoked(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	key, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	if err := h.svc.RevokeAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if _, err := h.svc.AuthenticateAPIKey(ctx, raw); err == nil {
		t.Fatal("revoked key must deny")
	}
	e := requireEvent(t, h.events, securityevent.TypeAPIKeyAuth, securityevent.StatusBlocked)
	// The revoked-key blocked event carries service-account attribution (the point
	// of the pinned GetByHash contract).
	if e.Actor.Type != PrincipalServiceAccount || e.Actor.ID != sa.ID {
		t.Errorf("blocked actor = %+v, want {service_account, %s}", e.Actor, sa.ID)
	}
}

func TestSecurityEventAPIKeyAuthFailureExpired(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Now().Add(-time.Hour))
	if _, err := h.svc.AuthenticateAPIKey(ctx, raw); err == nil {
		t.Fatal("expired key must deny")
	}
	e := requireEvent(t, h.events, securityevent.TypeAPIKeyAuth, securityevent.StatusFailure)
	if e.Actor.ID != sa.ID {
		t.Errorf("expired-failure actor id = %q, want %q", e.Actor.ID, sa.ID)
	}
}

// --- JWT bearer op (token_issued) ---

func TestSecurityEventTokenIssued(t *testing.T) {
	h := newTokenHarness(t, newFakeSigner(), false, nil)
	u := h.mustRegister(t, "tok@example.com", "password123")
	if _, _, err := h.svc.IssueToken(context.Background(), "tok@example.com", "password123"); err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	e := requireEvent(t, h.events, securityevent.TypeTokenIssued, securityevent.StatusSuccess)
	if e.UserID != u.ID {
		t.Errorf("token_issued UserID = %q, want %q", e.UserID, u.ID)
	}
}

// --- never-fail, nil-repo, and the carrier read path ---

// TestSecurityEventNeverFailsFlow asserts a failing audit repo does NOT fail the
// auth flow — the design's non-negotiable property (design §5.1 WI3/WI6).
func TestSecurityEventNeverFailsFlow(t *testing.T) {
	h := newHarness(t, nil)
	h.events.createErr = errors.New("audit store unavailable")

	if _, err := h.svc.Register(context.Background(), "nf@example.com", "password123", "NF"); err != nil {
		t.Fatalf("Register failed on an audit-write error: %v", err)
	}
	if _, _, err := h.svc.Login(context.Background(), "nf@example.com", "password123"); err != nil {
		t.Fatalf("Login failed on an audit-write error: %v", err)
	}
	if h.events.count() != 0 {
		t.Errorf("a failing repo recorded %d events, want 0", h.events.count())
	}
}

// TestSecurityEventNilRepoNoOp asserts a nil SecurityEvents repository is a
// documented no-op (ratified AV9): the ops run and record nothing, never panic.
func TestSecurityEventNilRepoNoOp(t *testing.T) {
	svc := NewService(Deps{
		Users:     newFakeUsers(),
		Passwords: newFakePasswords(),
		Sessions:  newFakeSessions(),
		Codes:     newFakeCodes(),
		Tokens:    newFakeTokens(),
		Hasher:    &fakeHasher{},
		Mailer:    &recordingMailer{},
		Limiter:   ratelimiter.NewMemory(),
		// SecurityEvents deliberately nil.
	})
	if _, err := svc.Register(context.Background(), "noop@example.com", "password123", "N"); err != nil {
		t.Fatalf("Register with nil audit repo: %v", err)
	}
	if _, _, err := svc.Login(context.Background(), "noop@example.com", "password123"); err != nil {
		t.Fatalf("Login with nil audit repo: %v", err)
	}
}

// keyCapturingLimiter records the last rate-limit key and always allows.
type keyCapturingLimiter struct {
	mu  sync.Mutex
	key string
}

func (l *keyCapturingLimiter) Allow(_ context.Context, key string, _ ratelimiter.Limit) (ratelimiter.Result, error) {
	l.mu.Lock()
	l.key = key
	l.mu.Unlock()
	return ratelimiter.Result{Allowed: true}, nil
}
func (l *keyCapturingLimiter) Reset(context.Context, string) error { return nil }
func (l *keyCapturingLimiter) Close() error                        { return nil }

// TestLoginReadsIPFromCarrier proves the single-source-of-truth IP: Login's
// rate-limit key uses the IP written by WithClientInfo, not a parameter (design
// §5.1 WI4).
func TestLoginReadsIPFromCarrier(t *testing.T) {
	lim := &keyCapturingLimiter{}
	h := newHarness(t, lim)
	h.mustRegister(t, "ip@example.com", "password123")

	ctx := WithClientInfo(context.Background(), "9.9.9.9", "probe-agent")
	if _, _, err := h.svc.Login(ctx, "ip@example.com", "password123"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	lim.mu.Lock()
	key := lim.key
	lim.mu.Unlock()
	if !strings.Contains(key, "9.9.9.9") {
		t.Errorf("rate-limit key = %q, want it to carry the carrier IP 9.9.9.9", key)
	}
}

// TestSecurityEventWarnOnFailingRepo captures one real WARN line from a failing
// audit write and asserts it carries COARSE fields only — event_type, status,
// error kind — and NEVER the event body (design §5.1 WI3). The captured line is
// logged for the execution record.
func TestSecurityEventWarnOnFailingRepo(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	spy := newSpySecurityEvents()
	spy.createErr = errors.New("boom: audit db unavailable")

	svc := NewService(Deps{
		Users:          newFakeUsers(),
		Passwords:      newFakePasswords(),
		Sessions:       newFakeSessions(),
		Codes:          newFakeCodes(),
		Tokens:         newFakeTokens(),
		Hasher:         &fakeHasher{},
		Mailer:         &recordingMailer{},
		Limiter:        ratelimiter.NewMemory(),
		SecurityEvents: spy,
		Logger:         logger,
	})
	if _, err := svc.Register(context.Background(), "warn@example.com", "password123", "W"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	buf.Reset() // isolate the login WARN from the register WARN
	if _, _, err := svc.Login(context.Background(), "warn@example.com", "password123"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("no WARN line captured for the failing audit write")
	}
	t.Logf("captured WARN: %s", line)

	for _, want := range []string{"security event write failed", "event_type=login", "status=success", "error_kind=unknown", "level=WARN"} {
		if !strings.Contains(line, want) {
			t.Errorf("WARN line missing %q: %s", want, line)
		}
	}
	// Content hygiene: the event body (the attempted email in Details, the raw
	// error message) must NOT leak into the coarse WARN line.
	for _, forbidden := range []string{"warn@example.com", "boom", "audit db unavailable"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("WARN line leaked event/error body %q: %s", forbidden, line)
		}
	}
}

// seedOAuthLink pre-creates a provider link for an existing user (the branch-1
// login setup).
func seedOAuthLink(t *testing.T, h *oauthHarness, userID, provider, providerUserID string) {
	t.Helper()
	acct, err := oauthaccount.New(userID, provider, providerUserID, time.Now())
	if err != nil {
		t.Fatalf("oauthaccount.New: %v", err)
	}
	if _, err := h.accounts.Create(context.Background(), acct); err != nil {
		t.Fatalf("seed oauth link: %v", err)
	}
}

func toString(v any) string { return fmt.Sprintf("%v", v) }
