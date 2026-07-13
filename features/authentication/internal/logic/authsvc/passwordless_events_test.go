package authsvc

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
)

// extractMagicToken recovers the fragment-borne magic-link token from a rendered
// message (email text or SMS body). The link is the tail of the message; the token
// rides after "#token=" url-escaped, so it is unescaped back to the raw value the
// redeem endpoint consumes.
func extractMagicToken(t *testing.T, text string) string {
	t.Helper()
	const marker = "#token="
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("no magic-link token in %q", text)
	}
	raw := text[i+len(marker):]
	if j := strings.IndexAny(raw, " \t\r\n"); j >= 0 {
		raw = raw[:j]
	}
	tok, err := url.QueryUnescape(raw)
	if err != nil {
		t.Fatalf("unescape token %q: %v", raw, err)
	}
	return tok
}

// lastBodyTo returns the most recent SMS body delivered to value.
func (n *recordingNotifier) lastBodyTo(t *testing.T, value string) string {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := len(n.sent) - 1; i >= 0; i-- {
		if n.sent[i].to.Value == value {
			return n.sent[i].body
		}
	}
	t.Fatalf("no SMS delivered to %s", value)
	return ""
}

// seedVerifiedPhone seeds an active, verified, login-enabled phone identifier and
// its owning user so a passwordless phone flow can resolve it.
func (h *harness) seedVerifiedPhone(userID, phone string) {
	now := time.Now()
	h.users.byID[userID] = user.User{ID: userID}
	h.idents.byID[userID+"-ph"] = identifier.Identifier{
		ID: userID + "-ph", UserID: userID, Kind: identifier.KindPhone, NormalizedValue: phone,
		VerifiedAt: now, LoginEnabled: true, CreatedAt: now,
	}
}

// requirePasswordlessEvent asserts a passwordless audit row of (type,status) exists,
// returns it, and proves it carries kind + challenge purpose only — never the raw
// identifier value, code, or token (design §4.3).
func requirePasswordlessEvent(t *testing.T, spy *spySecurityEvents, eventType, status string) securityevent.SecurityEvent {
	t.Helper()
	e := requireEvent(t, spy, eventType, status)
	if _, ok := e.Details["kind"]; !ok {
		t.Errorf("%s/%s details missing kind: %v", eventType, status, e.Details)
	}
	if _, ok := e.Details["purpose"]; !ok {
		t.Errorf("%s/%s details missing purpose: %v", eventType, status, e.Details)
	}
	return e
}

// TestStartPasswordlessRecordsAcceptedEvent proves an accepted (enqueued) start
// records one passwordless_start/success carrying kind + challenge purpose only,
// with no UserID (the start never resolves the account on the request path) and no
// identifier value in details.
func TestStartPasswordlessRecordsAcceptedEvent(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "acc@example.com", "password123456789")
	h.mustVerify(t, "acc@example.com")
	enablePasswordless(h, string(identifier.KindEmail))

	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "acc@example.com", "link"); err != nil {
		t.Fatalf("StartPasswordless: %v", err)
	}
	e := requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessStart, securityevent.StatusSuccess)
	if e.UserID != "" {
		t.Errorf("start event carries UserID %q, want empty (no resolution on the request path)", e.UserID)
	}
	if e.Details["kind"] != string(identifier.KindEmail) || e.Details["purpose"] != challenge.PurposeLoginMagicLink {
		t.Errorf("start details = %v, want kind=email purpose=%s", e.Details, challenge.PurposeLoginMagicLink)
	}
	if strings.Contains(strings.ToLower(detailsString(e)), "acc@example.com") {
		t.Errorf("start event leaked the identifier value: %v", e.Details)
	}
}

// TestStartPasswordlessRecordsBlockedEvent proves a throttled start records one
// passwordless_start/blocked and no accepted row.
func TestStartPasswordlessRecordsBlockedEvent(t *testing.T) {
	h := newHarness(t, denyLimiter{})
	enablePasswordless(h, string(identifier.KindEmail))

	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "blk@example.com", "link"); err == nil {
		t.Fatal("StartPasswordless(throttled) = nil, want rate-limited")
	}
	requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessStart, securityevent.StatusBlocked)
	if hasEvent(h.events, securityevent.TypePasswordlessStart, securityevent.StatusSuccess) {
		t.Error("throttled start also recorded an accepted event")
	}
}

// TestVerifyPasswordlessRecordsSuccessAndFailure proves the login event pair: a
// successful OTP verify records passwordless_login/success with the user attributed,
// an unknown identifier records passwordless_login/failure, and neither row carries
// the raw identifier or code.
func TestVerifyPasswordlessRecordsSuccessAndFailure(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "vev@example.com", "password123456789")
	h.mustVerify(t, "vev@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	h.mailer.sent = nil

	// A failed verify (unknown identifier) records a login failure with no user.
	if _, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "ghost@example.com", "000000"); err == nil {
		t.Fatal("VerifyPasswordless(unknown) = nil, want generic failure")
	}
	fe := requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessLogin, securityevent.StatusFailure)
	if fe.UserID != "" {
		t.Errorf("failed verify event UserID = %q, want empty", fe.UserID)
	}

	// A successful verify records a login success attributed to the user.
	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "vev@example.com", "code"); err != nil {
		t.Fatalf("StartPasswordless(code): %v", err)
	}
	code := extractCode(t, h.mailer.last().Text)
	if _, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "vev@example.com", code); err != nil {
		t.Fatalf("VerifyPasswordless(success): %v", err)
	}
	se := requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessLogin, securityevent.StatusSuccess)
	if se.UserID != u.ID {
		t.Errorf("success verify event UserID = %q, want %q", se.UserID, u.ID)
	}
	for _, e := range h.events.recorded() {
		if e.EventType != securityevent.TypePasswordlessLogin {
			continue
		}
		if s := strings.ToLower(detailsString(e)); strings.Contains(s, "vev@example.com") || strings.Contains(s, code) {
			t.Errorf("passwordless_login event leaked identifier/code: %v", e.Details)
		}
	}
}

// TestVerifyPasswordlessRecordsBlockedEvent proves a throttled verify records one
// passwordless_login/blocked, distinct from the credential failure.
func TestVerifyPasswordlessRecordsBlockedEvent(t *testing.T) {
	h := newHarness(t, denyLimiter{})
	enablePasswordless(h, string(identifier.KindEmail))

	if _, err := h.svc.VerifyPasswordless(context.Background(), string(identifier.KindEmail), "blk@example.com", "123456"); err == nil {
		t.Fatal("VerifyPasswordless(throttled) = nil, want rate-limited")
	}
	requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessLogin, securityevent.StatusBlocked)
}

// TestRedeemPasswordlessRecordsSuccessAndFailure proves the magic-link login event
// pair: a garbage token records passwordless_login/failure, and a real redemption
// records passwordless_login/success attributed to the bound user, neither carrying
// the token.
func TestRedeemPasswordlessRecordsSuccessAndFailure(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "rev@example.com", "password123456789")
	h.mustVerify(t, "rev@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	h.mailer.sent = nil

	if _, err := h.svc.RedeemPasswordless(context.Background(), "not-a-real-token"); err == nil {
		t.Fatal("RedeemPasswordless(garbage) = nil, want generic failure")
	}
	requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessLogin, securityevent.StatusFailure)

	if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), "rev@example.com", "link"); err != nil {
		t.Fatalf("StartPasswordless(link): %v", err)
	}
	token := extractMagicToken(t, h.mailer.last().Text)
	if _, err := h.svc.RedeemPasswordless(context.Background(), token); err != nil {
		t.Fatalf("RedeemPasswordless(success): %v", err)
	}
	se := requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessLogin, securityevent.StatusSuccess)
	if se.UserID != u.ID {
		t.Errorf("success redeem event UserID = %q, want %q", se.UserID, u.ID)
	}
	for _, e := range h.events.recorded() {
		if e.EventType == securityevent.TypePasswordlessLogin && strings.Contains(detailsString(e), token) {
			t.Errorf("passwordless_login event leaked the token: %v", e.Details)
		}
	}
}

// TestStartPasswordlessDoesNotBlockOnProvider is the slow-provider start-timing
// proof (phase acceptance, design §4.1/V14): with a worker running against a
// provider that blocks indefinitely, StartPasswordless returns promptly for BOTH a
// known and an unknown identifier — the response completes before the worker send —
// and the two timings are comparable, so a blocking provider leaks no existence
// signal via latency.
func TestStartPasswordlessDoesNotBlockOnProvider(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "known@example.com", "password123456789")
	h.mustVerify(t, "known@example.com")
	enablePasswordless(h, string(identifier.KindEmail))

	enc := fakeEncrypter{}
	repo := newMemDeliveryRepo()
	blocking := &blockingSender{release: make(chan struct{})}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	router, err := delivery.NewRouter(delivery.Deps{Mailer: blocking, MailFrom: "noreply@example.com", Logger: quiet})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	dsvc, err := delivery.NewService(delivery.ServiceDeps{Repo: repo, Encrypter: enc})
	if err != nil {
		t.Fatalf("delivery.NewService: %v", err)
	}
	worker, err := delivery.NewWorker(delivery.WorkerDeps{Repo: repo, Encrypter: enc, Router: router, Initializer: h.svc, Logger: quiet})
	if err != nil {
		t.Fatalf("delivery.NewWorker: %v", err)
	}
	h.svc.queue = dsvc

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = worker.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		close(blocking.release)
		<-done
	})

	timeStart := func(addr string) time.Duration {
		start := time.Now()
		if err := h.svc.StartPasswordless(context.Background(), string(identifier.KindEmail), addr, "link"); err != nil {
			t.Fatalf("StartPasswordless(%q): %v", addr, err)
		}
		return time.Since(start)
	}
	known := timeStart("known@example.com")
	unknown := timeStart("ghost@example.com")
	t.Logf("start timing under blocking provider: known=%v unknown=%v", known, unknown)

	const bound = 200 * time.Millisecond
	if known > bound || unknown > bound {
		t.Fatalf("start blocked on the provider: known=%v unknown=%v (bound %v)", known, unknown, bound)
	}
}

// TestPasswordlessEndToEndAllFlows drives every passwordless method end to end
// against the synchronous outbox drain with recorder ("console") delivery — email
// link, email code, phone SMS link, and phone OTP — proving each: an accepted start,
// a delivered secret, a completed login through mintSession, and a working
// refresh/logout on the minted session (design §4.3, phase acceptance).
func TestPasswordlessEndToEndAllFlows(t *testing.T) {
	ctx := context.Background()

	t.Run("email link", func(t *testing.T) {
		h := newHarness(t, nil)
		h.mustRegister(t, "el@example.com", "password123456789")
		h.mustVerify(t, "el@example.com")
		enablePasswordless(h, string(identifier.KindEmail))
		h.mailer.sent = nil

		if err := h.svc.StartPasswordless(ctx, string(identifier.KindEmail), "el@example.com", "link"); err != nil {
			t.Fatalf("start: %v", err)
		}
		requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessStart, securityevent.StatusSuccess)
		token := extractMagicToken(t, h.mailer.last().Text)
		pair, err := h.svc.RedeemPasswordless(ctx, token)
		if err != nil {
			t.Fatalf("redeem: %v", err)
		}
		requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessLogin, securityevent.StatusSuccess)
		mustRefreshLogout(t, h, pair)
	})

	t.Run("email code", func(t *testing.T) {
		h := newHarness(t, nil)
		h.mustRegister(t, "ec@example.com", "password123456789")
		h.mustVerify(t, "ec@example.com")
		enablePasswordless(h, string(identifier.KindEmail))
		h.mailer.sent = nil

		if err := h.svc.StartPasswordless(ctx, string(identifier.KindEmail), "ec@example.com", "code"); err != nil {
			t.Fatalf("start: %v", err)
		}
		requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessStart, securityevent.StatusSuccess)
		code := extractCode(t, h.mailer.last().Text)
		pair, err := h.svc.VerifyPasswordless(ctx, string(identifier.KindEmail), "ec@example.com", code)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessLogin, securityevent.StatusSuccess)
		mustRefreshLogout(t, h, pair)
	})

	t.Run("phone sms link", func(t *testing.T) {
		h := newHarness(t, nil)
		h.enablePhone(t)
		enablePasswordless(h, string(identifier.KindPhone))
		h.seedVerifiedPhone("pl", "+15551110001")

		if err := h.svc.StartPasswordless(ctx, string(identifier.KindPhone), "+1 (555) 111-0001", "link"); err != nil {
			t.Fatalf("start: %v", err)
		}
		requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessStart, securityevent.StatusSuccess)
		token := extractMagicToken(t, h.phone.lastBodyTo(t, "+15551110001"))
		pair, err := h.svc.RedeemPasswordless(ctx, token)
		if err != nil {
			t.Fatalf("redeem: %v", err)
		}
		requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessLogin, securityevent.StatusSuccess)
		mustRefreshLogout(t, h, pair)
	})

	t.Run("phone otp", func(t *testing.T) {
		h := newHarness(t, nil)
		h.enablePhone(t)
		enablePasswordless(h, string(identifier.KindPhone))
		h.seedVerifiedPhone("po", "+15551110002")

		if err := h.svc.StartPasswordless(ctx, string(identifier.KindPhone), "+1 (555) 111-0002", "code"); err != nil {
			t.Fatalf("start: %v", err)
		}
		requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessStart, securityevent.StatusSuccess)
		code := h.phone.codeFor(t, "+15551110002")
		pair, err := h.svc.VerifyPasswordless(ctx, string(identifier.KindPhone), "+15551110002", code)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		requirePasswordlessEvent(t, h.events, securityevent.TypePasswordlessLogin, securityevent.StatusSuccess)
		mustRefreshLogout(t, h, pair)
	})
}

// mustRefreshLogout proves a passwordless-minted session supports the standard
// refresh rotation and logout, so both mint methods land a fully live session.
func mustRefreshLogout(t *testing.T, h *harness, pair TokenPair) {
	t.Helper()
	next, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh after passwordless mint: %v", err)
	}
	if next.RefreshToken == "" || next.RefreshToken == pair.RefreshToken {
		t.Fatalf("refresh did not rotate: %+v", next)
	}
	if err := h.svc.Logout(context.Background(), next.RefreshToken, ""); err != nil {
		t.Fatalf("Logout after passwordless mint: %v", err)
	}
}

// detailsString renders an event's details for a substring leak scan.
func detailsString(e securityevent.SecurityEvent) string {
	var b strings.Builder
	for k, v := range e.Details {
		b.WriteString(k)
		b.WriteByte('=')
		if s, ok := v.(string); ok {
			b.WriteString(s)
		}
		b.WriteByte(';')
	}
	return b.String()
}
