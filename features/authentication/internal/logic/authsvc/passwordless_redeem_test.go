package authsvc

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
)

// startPasswordlessLink drives a magic-link start through the synchronous-draining
// harness queue so the worker issues the login_magic_link and delivers the link,
// then recovers the plaintext token from the delivered mail (the digest at rest is
// not reversible). The token rides the URL fragment, so it is recovered from the
// mail body, never from any server request the flow made.
func startPasswordlessLink(t *testing.T, h *harness, kind, addr string) string {
	t.Helper()
	h.mailer.sent = nil
	if h.phone != nil {
		h.phone.sent = nil
	}
	if err := h.svc.StartPasswordless(context.Background(), kind, addr, "link"); err != nil {
		t.Fatalf("StartPasswordless(link): %v", err)
	}
	return extractMagicLinkToken(t, h.mailer.last().Text)
}

// extractMagicLinkToken recovers the plaintext magic-link token from a delivered
// link body of the form "…{PublicAuthBaseURL}#token=<url-escaped-token>…".
func extractMagicLinkToken(t *testing.T, text string) string {
	t.Helper()
	const marker = "#token="
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("magic-link token not found in %q", text)
	}
	fields := strings.Fields(text[i+len(marker):])
	if len(fields) == 0 {
		t.Fatalf("magic-link token missing after marker in %q", text)
	}
	tok, err := url.QueryUnescape(fields[0])
	if err != nil {
		t.Fatalf("unescape magic-link token %q: %v", fields[0], err)
	}
	return tok
}

// TestRedeemPasswordlessSuccessMintsSession proves the magic-link happy path mints a
// live session through mintSession (a usable access/refresh pair), stamps the
// email_link method, consumes the token, and that the minted refresh token rotates
// through the standard refresh path afterward (design §4.1/§4.3).
func TestRedeemPasswordlessSuccessMintsSession(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "link@example.com", "password123456789")
	h.mustVerify(t, "link@example.com")
	enablePasswordless(h, string(identifier.KindEmail))

	token := startPasswordlessLink(t, h, string(identifier.KindEmail), "Link@Example.com")
	pair, err := h.svc.RedeemPasswordless(context.Background(), token)
	if err != nil {
		t.Fatalf("RedeemPasswordless: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("minted pair incomplete: access=%q refresh=%q", pair.AccessToken, pair.RefreshToken)
	}
	if n := h.loginChallengeCount(challenge.PurposeLoginMagicLink); n != 0 {
		t.Errorf("login_magic_link challenge count after redeem = %d, want 0 (consumed)", n)
	}
	// The session records how the primary authentication happened (email_link).
	h.sess.mu.Lock()
	var got session.Session
	for _, s := range h.sess.m {
		if s.UserID == u.ID {
			got = s
		}
	}
	h.sess.mu.Unlock()
	if len(got.Authentication.Methods) != 1 || got.Authentication.Methods[0].Kind != session.MethodEmailLink {
		t.Errorf("session methods = %+v, want single email_link", got.Authentication.Methods)
	}
	// The minted refresh token rotates through the standard refresh path.
	rotated, err := h.svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh after passwordless redeem: %v", err)
	}
	if rotated.RefreshToken == "" || rotated.RefreshToken == pair.RefreshToken {
		t.Errorf("refresh did not rotate the token: old=%q new=%q", pair.RefreshToken, rotated.RefreshToken)
	}
}

// TestRedeemPasswordlessGenericFailures proves every non-throttle failure collapses
// to the one generic ErrPasswordlessLogin (design §5.8): an unknown token and an
// empty token are indistinguishable and mint nothing.
func TestRedeemPasswordlessGenericFailures(t *testing.T) {
	h := newHarness(t, nil)
	enablePasswordless(h, string(identifier.KindEmail))
	ctx := context.Background()

	for _, tok := range []string{"", "not-a-real-token"} {
		if _, err := h.svc.RedeemPasswordless(ctx, tok); !errors.Is(err, ErrPasswordlessLogin) {
			t.Fatalf("redeem(%q) err = %v, want ErrPasswordlessLogin", tok, err)
		}
	}
	if len(h.sess.m) != 0 {
		t.Errorf("failed redeems minted %d sessions, want 0", len(h.sess.m))
	}
}

// TestRedeemPasswordlessNoAutoProvision proves a redeem of a token that never
// resolves to a live identifier never creates a user or session — passwordless is
// login-only (design §4.1/V3).
func TestRedeemPasswordlessNoAutoProvision(t *testing.T) {
	h := newHarness(t, nil)
	enablePasswordless(h, string(identifier.KindEmail))

	if _, err := h.svc.RedeemPasswordless(context.Background(), "ghost-token"); !errors.Is(err, ErrPasswordlessLogin) {
		t.Fatalf("err = %v, want ErrPasswordlessLogin", err)
	}
	if len(h.users.byID) != 0 {
		t.Errorf("auto-provisioned %d users, want 0", len(h.users.byID))
	}
	if len(h.sess.m) != 0 {
		t.Errorf("minted %d sessions, want 0", len(h.sess.m))
	}
}

// TestRedeemPasswordlessIdentifierRemoved proves a link cannot log in after its bound
// identifier was removed: the token is spent by the atomic consume, but the reload of
// the bound current identifier resolves nothing → the generic failure, nothing minted
// (design §4.1).
func TestRedeemPasswordlessIdentifierRemoved(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "gone@example.com", "password123456789")
	h.mustVerify(t, "gone@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	token := startPasswordlessLink(t, h, string(identifier.KindEmail), "gone@example.com")

	h.idents.retireAll(u.ID, time.Now())

	if _, err := h.svc.RedeemPasswordless(context.Background(), token); !errors.Is(err, ErrPasswordlessLogin) {
		t.Fatalf("redeem after identifier removed err = %v, want ErrPasswordlessLogin", err)
	}
	if len(h.sess.m) != 0 {
		t.Errorf("redeem after removal minted %d sessions, want 0", len(h.sess.m))
	}
	// The token is spent regardless, so a retry is still the generic failure.
	if n := h.loginChallengeCount(challenge.PurposeLoginMagicLink); n != 0 {
		t.Errorf("magic-link challenge count after removed-identifier redeem = %d, want 0 (spent)", n)
	}
}

// TestRedeemPasswordlessIdentifierReplaced proves a link cannot log in after its bound
// identifier was replaced by a different row claiming the same value: the reloaded
// current row's ID no longer matches the token's stored binding, so redeem is the
// generic failure and mints nothing (design §4.1).
func TestRedeemPasswordlessIdentifierReplaced(t *testing.T) {
	h := newHarness(t, nil)
	u := h.mustRegister(t, "swap@example.com", "password123456789")
	h.mustVerify(t, "swap@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	token := startPasswordlessLink(t, h, string(identifier.KindEmail), "swap@example.com")

	// Replace: retire the bound row and claim the same value with a fresh identifier
	// ID, so the reload resolves a row whose ID differs from the token's binding.
	now := time.Now()
	h.idents.retireAll(u.ID, now)
	h.idents.insert(identifier.Identifier{
		ID: "replacement", UserID: u.ID, Kind: identifier.KindEmail, NormalizedValue: "swap@example.com",
		VerifiedAt: now, LoginEnabled: true, CreatedAt: now,
	})

	if _, err := h.svc.RedeemPasswordless(context.Background(), token); !errors.Is(err, ErrPasswordlessLogin) {
		t.Fatalf("redeem after identifier replaced err = %v, want ErrPasswordlessLogin", err)
	}
	if len(h.sess.m) != 0 {
		t.Errorf("redeem after replacement minted %d sessions, want 0", len(h.sess.m))
	}
}

// TestRedeemPasswordlessDoubleRedemptionSingleWinner proves two concurrent redeems of
// the same token produce exactly one minted session — the atomic delete-returning
// elects one winner and the loser is the generic failure (design §3.2 single-use).
func TestRedeemPasswordlessDoubleRedemptionSingleWinner(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "race@example.com", "password123456789")
	h.mustVerify(t, "race@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	token := startPasswordlessLink(t, h, string(identifier.KindEmail), "race@example.com")

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		wins   int
		losses int
		badErr error
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.svc.RedeemPasswordless(context.Background(), token)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrPasswordlessLogin):
				losses++
			default:
				badErr = err
			}
		}()
	}
	wg.Wait()
	if badErr != nil {
		t.Fatalf("unexpected redeem error: %v", badErr)
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins=%d losses=%d, want exactly one winner", wins, losses)
	}
	if len(h.sess.m) != 1 {
		t.Errorf("minted %d sessions, want exactly 1", len(h.sess.m))
	}
}

// TestRedeemPasswordlessRateLimited proves the §4.4 pre-consume redeem budget refuses
// before the token is consumed with the distinct rate-limit error (not the generic
// 401), leaving the token spendable once the window resets.
func TestRedeemPasswordlessRateLimited(t *testing.T) {
	h := newHarness(t, nil)
	h.mustRegister(t, "limited@example.com", "password123456789")
	h.mustVerify(t, "limited@example.com")
	enablePasswordless(h, string(identifier.KindEmail))
	token := startPasswordlessLink(t, h, string(identifier.KindEmail), "limited@example.com")

	// Swap in a denying limiter for the redeem budget only (the start already ran).
	h.svc.limiter = denyLimiter{}
	if _, err := h.svc.RedeemPasswordless(context.Background(), token); !errors.Is(err, ErrPasswordlessRateLimited) {
		t.Fatalf("rate-limited redeem err = %v, want ErrPasswordlessRateLimited", err)
	}
	if n := h.loginChallengeCount(challenge.PurposeLoginMagicLink); n != 1 {
		t.Errorf("token consumed under rate limit (count=%d, want 1)", n)
	}
}

// TestMagicLinkURLFragmentExactBase proves the URL-safety construction contract
// (design §6.4): the token rides the URL FRAGMENT (so a server GET on the landing
// page never receives it — no query, no path token), and the link targets EXACTLY
// the configured PublicAuthBaseURL. The builder takes no request input, so a hostile
// request Host can never redirect the link elsewhere.
func TestMagicLinkURLFragmentExactBase(t *testing.T) {
	h := newHarness(t, nil)
	enablePasswordless(h, string(identifier.KindEmail))

	const token = "opaque-256-bit-token-value"
	link := h.svc.magicLinkURL(token)

	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse magic link %q: %v", link, err)
	}
	// Exact-match target: scheme://host/path is exactly the configured base, nothing
	// derived from a request Host.
	if u.Scheme+"://"+u.Host+u.Path != "https://auth.example.com" {
		t.Errorf("magic link target = %q, want exactly the configured base https://auth.example.com", u.Scheme+"://"+u.Host+u.Path)
	}
	// The token is in the fragment only — never the query or path, so it is not sent
	// to the server on the landing-page GET.
	if u.RawQuery != "" {
		t.Errorf("magic link carries a query %q; the token must ride the fragment only", u.RawQuery)
	}
	if strings.Contains(u.Path, token) {
		t.Errorf("magic link path %q carries the token; it must ride the fragment only", u.Path)
	}
	if !strings.Contains(u.Fragment, token) {
		t.Errorf("magic link fragment = %q, want it to carry the token", u.Fragment)
	}
}
