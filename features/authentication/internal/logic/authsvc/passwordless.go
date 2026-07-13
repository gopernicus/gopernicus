package authsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

// Passwordless start methods (design §4.3). A start selects the secret rail: a
// magic LINK (256-bit token, redeemed by URL) or a one-time CODE (OTP, verified
// by short code). The default per kind is link for email and code for phone, but
// both are caller-selectable when the transport can carry them (a magic link in an
// SMS body is legal — the directive's "magic links deliverable to sms or email").
const (
	// MethodLink is the passwordless magic-link method.
	MethodLink = "link"
	// MethodCode is the passwordless one-time-code (OTP) method.
	MethodCode = "code"
)

const (
	// passwordlessStartsPerIdentifierPerMinute bounds how many passwordless starts one
	// identifier may trigger per minute (design §4.4: the SMS/email flood arm that
	// protects the identifier's owner). Keyed by the PII-free identifier digest.
	passwordlessStartsPerIdentifierPerMinute = 3
	// passwordlessStartsPerIPPerMinute bounds how many passwordless starts one client
	// IP may trigger per minute (design §4.4: the farming arm). Keyed by the trusted
	// client IP (never a spoofable X-Forwarded-For).
	passwordlessStartsPerIPPerMinute = 10
	// passwordlessVerifiesPerMinute bounds how many OTP verify attempts one
	// keyed-identifier + trusted-IP pair may make per minute (design §4.4 verify key).
	// It is the coarse pre-lookup throttle that layers over the per-code challenge
	// lockout, so a farmer cannot cycle codes for one identifier faster than this.
	passwordlessVerifiesPerMinute = 10
	// passwordlessRedeemsPerIPPerMinute bounds how many magic-link redemptions one
	// trusted client IP may attempt per minute (design §4.4, the redeem twin of the
	// verify budget). Redeem carries no identifier, so the trusted IP is the only
	// stable key; the 256-bit token is unguessable, so this is a coarse pre-consume
	// farming bound on a distinct key prefix, not the primary defense.
	passwordlessRedeemsPerIPPerMinute = 10
)

// Stable passwordless-start errors (design §4.2/§4.3/§5.8). Each wraps an sdk kind
// so the transport maps it; callers detect them with errors.Is. None names an
// account: kind/method rejections are deterministic configuration/request-shape
// outcomes, identical whether or not the identifier exists.
var (
	// ErrPasswordlessKindDisabled is returned when a start names a kind the host has
	// not enabled for passwordless (design §4.2). It is a request-shape rejection,
	// not an account signal. Wraps sdk.ErrInvalidInput (→ 400).
	ErrPasswordlessKindDisabled = fmt.Errorf("passwordless is not enabled for this identifier kind: %w", sdk.ErrInvalidInput)
	// ErrPasswordlessMethodInvalid is returned when a start names a method that is not
	// link or code (design §4.3). Wraps sdk.ErrInvalidInput (→ 400).
	ErrPasswordlessMethodInvalid = fmt.Errorf("passwordless method must be link or code: %w", sdk.ErrInvalidInput)
	// ErrPasswordlessRateLimited is returned when the per-identifier or per-IP start
	// budget — or the verify budget — is exhausted (design §4.4). Distinct so the
	// transport maps it to 429; it applies equally to known and unknown identifiers,
	// so it leaks no existence signal.
	ErrPasswordlessRateLimited = errors.New("too many passwordless requests")
	// ErrPasswordlessLogin is the SINGLE generic passwordless-login failure (design
	// §5.8): a disabled kind, malformed identifier, unknown/replaced/login-disabled/
	// unverified identifier, wrong/expired code, stale binding, and lockout all
	// collapse to it, so a verify/redeem response never distinguishes the reason nor
	// enumerates accounts. Wraps sdk.ErrUnauthorized (→ 401), exactly like a failed
	// password login. Rate limiting is the only non-401 verify outcome.
	ErrPasswordlessLogin = fmt.Errorf("passwordless login failed: %w", sdk.ErrUnauthorized)
)

// loginBinding pins a passwordless challenge to the identifier it was issued for
// (design §4.1/§4.3): the worker stores the resolved identifier's ID, kind, and
// normalized value as the challenge context, and the verify/redeem step validates
// the CURRENT identifier against it before minting, so a link/code cannot log in
// after its identifier was removed, replaced, or had login disabled.
type loginBinding struct {
	IdentifierID    string `json:"identifier_id"`
	Kind            string `json:"kind"`
	NormalizedValue string `json:"normalized_value"`
}

// PasswordlessEnabled reports whether login-only passwordless authentication is
// enabled for any kind (design §4.2). The transport registers the passwordless
// routes (POST /auth/passwordless/{start,verify,redeem}) only when it is true
// (deny-by-absence), mirroring OAuthEnabled / MachineEnabled / TokenEnabled. The
// enabled kind set and every enablement gate (delivery channel, challenge rail,
// durable outbox, public base URL) are validated by package auth at construction.
func (s *Service) PasswordlessEnabled() bool {
	return len(s.passwordless) > 0
}

// PasswordlessKindEnabled reports whether passwordless is enabled for kind (design
// §4.2). A passwordless start/verify/redeem consults it to reject a request for a
// kind the host has not enabled, before resolving any account.
func (s *Service) PasswordlessKindEnabled(kind string) bool {
	return s.passwordless[kind]
}

// StartPasswordless is the enumeration-safe unauthenticated passwordless start
// (design §4.1/§4.3): it validates the enabled kind and requested method,
// normalizes the identifier, applies the per-identifier AND per-IP start budgets,
// then enqueues an OPAQUE delivery command carrying only the normalized identifier
// — it never resolves the account, issues a challenge, or calls a provider on the
// request path. The worker (Service.Initialize) later resolves the active VERIFIED
// login identifier, issues the purpose-bound challenge, renders (a link built from
// PublicAuthBaseURL for the magic-link rail), and delivers; an unknown, unverified,
// or login-disabled identifier resolves nothing there. Known and unknown addresses
// therefore share one bounded request path with identical repository/limiter calls.
// A malformed identifier returns nil (uniform accepted), exactly like ForgotPassword,
// so validity is never revealed. method defaults to link for email, code for phone.
func (s *Service) StartPasswordless(ctx context.Context, kind, identifierValue, method string) error {
	if !s.PasswordlessKindEnabled(kind) {
		return ErrPasswordlessKindDisabled
	}
	deliveryPurpose, err := resolvePasswordlessMethod(kind, method)
	if err != nil {
		return err
	}
	if s.queue == nil {
		return ErrDeliveryDisabled
	}
	// Normalize through the single injected policy so the worker's later GetLogin
	// speaks the stored value. A rejected (malformed) value returns nil — the same
	// accepted outcome a valid-but-unknown identifier gets, so the response never
	// distinguishes validity or existence (design §4.1).
	normalized, err := s.normalizer.Normalize(kind, identifierValue)
	if err != nil {
		return nil
	}
	// The per-identifier and per-IP start budgets run BEFORE any account resolution
	// (which happens off-path in the worker) and key on PII-free digests, so they
	// apply identically to known and unknown identifiers (design §4.4).
	challengePurpose := passwordlessChallengePurpose(deliveryPurpose)
	if err := s.passwordlessStartBudget(ctx, kind, normalized); err != nil {
		if errors.Is(err, ErrPasswordlessRateLimited) {
			// A throttled start is a `passwordless_start` blocked audit row (design
			// §4.3): kind + challenge purpose only, no identifier value, no UserID (the
			// start never resolves the account on the request path — §4.1).
			s.recordPasswordlessStart(ctx, kind, challengePurpose, securityevent.StatusBlocked)
		}
		return err
	}
	key := s.idempotencyKey(kind, normalized, deliveryPurpose)
	if _, err := s.queue.Enqueue(ctx, delivery.Command{
		Kind:           kind,
		Purpose:        deliveryPurpose,
		IdempotencyKey: key,
		Envelope:       delivery.Envelope{ResolutionInput: normalized},
	}); err != nil {
		return err
	}
	// The opaque job was accepted onto the durable outbox: record the accepted start
	// (design §4.3). Account resolution and the actual send happen off-path in the
	// worker, so this row — like the response — is identical for known and unknown.
	s.recordPasswordlessStart(ctx, kind, challengePurpose, securityevent.StatusSuccess)
	return nil
}

// passwordlessChallengePurpose maps a resolved delivery purpose to the challenge
// purpose the audit row records (design §4.3): a magic-link start issues a
// login_magic_link, a code start a login_otp. Only these two delivery purposes
// reach it (resolvePasswordlessMethod is the only source), so link is the default.
func passwordlessChallengePurpose(deliveryPurpose string) string {
	if deliveryPurpose == delivery.PurposeLoginCode {
		return challenge.PurposeLoginOTP
	}
	return challenge.PurposeLoginMagicLink
}

// recordPasswordlessStart appends a `passwordless_start` audit row (design §4.3).
// Details carries the identifier kind and the challenge purpose ONLY — never the
// identifier value or a secret — and there is no UserID, because the start never
// resolves the account on the request path (enumeration safety, §4.1).
func (s *Service) recordPasswordlessStart(ctx context.Context, kind, purpose, status string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		Type:    securityevent.TypePasswordlessStart,
		Status:  status,
		Details: map[string]any{"kind": kind, "purpose": purpose},
	})
}

// recordPasswordlessLogin appends a `passwordless_login` audit row (design §4.3):
// StatusSuccess on a mint, StatusFailure on the generic 401, StatusBlocked on a
// throttle. Details carries the identifier kind and the challenge purpose ONLY —
// never the identifier value, code, or token. userID is attributed when known (a
// success or a post-resolution failure) and empty otherwise.
func (s *Service) recordPasswordlessLogin(ctx context.Context, userID, kind, purpose, status string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  userID,
		Type:    securityevent.TypePasswordlessLogin,
		Status:  status,
		Details: map[string]any{"kind": kind, "purpose": purpose},
	})
}

// VerifyPasswordless completes the OTP passwordless-login rail (design §4.3): it
// re-resolves the CURRENT active verified login identifier, atomically consumes the
// login_otp challenge the start issued for it, and — only on success — mints a
// session through the single mintSession path. It never resolves the account before
// the rate limit, never trusts the request for the login identity (the user comes
// from the re-resolved current identifier and the consumed row), and returns exactly
// one generic ErrPasswordlessLogin (401) for every failure reason: a disabled kind, a
// malformed identifier, an unknown/replaced/login-disabled/unverified identifier, a
// wrong or expired code, a stale identifier binding, or a lockout. An exhausted
// verify budget is the only distinct outcome (ErrPasswordlessRateLimited → 429), and
// a genuine infrastructure error propagates (→ 500) rather than masquerading as a
// credential failure.
func (s *Service) VerifyPasswordless(ctx context.Context, kind, identifierValue, code string) (TokenPair, error) {
	// Every OTP verify is a login_otp completion, so the audit purpose is fixed; the
	// userID is only known once the current identifier resolves (a success or a
	// post-resolution consume failure).
	const purpose = challenge.PurposeLoginOTP
	if !s.PasswordlessKindEnabled(kind) {
		s.recordPasswordlessLogin(ctx, "", kind, purpose, securityevent.StatusFailure)
		return TokenPair{}, ErrPasswordlessLogin
	}
	// Normalize + rate-limit BEFORE any lookup (design §4.4). A malformed identifier
	// is the same generic failure a valid-but-wrong one gets, so validity is never
	// revealed. The verify budget keys on the §4.4 verify key (kind-scoped identifier
	// digest + trusted IP), distinct from the start prefix.
	normalized, err := s.normalizer.Normalize(kind, identifierValue)
	if err != nil {
		s.recordPasswordlessLogin(ctx, "", kind, purpose, securityevent.StatusFailure)
		return TokenPair{}, ErrPasswordlessLogin
	}
	if err := s.passwordlessVerifyBudget(ctx, kind, normalized); err != nil {
		if errors.Is(err, ErrPasswordlessRateLimited) {
			s.recordPasswordlessLogin(ctx, "", kind, purpose, securityevent.StatusBlocked)
		}
		return TokenPair{}, err
	}
	// Re-resolve the CURRENT active verified login identifier: a removed, replaced,
	// login-disabled, or unverified identifier resolves nothing → generic 401. The
	// login identity the session mints for comes from this current row, so a code can
	// never log in after its identifier changed underneath it (design §4.1).
	ident, ok, err := s.resolvePasswordlessLogin(ctx, kind, normalized)
	if err != nil {
		return TokenPair{}, err
	}
	if !ok {
		s.recordPasswordlessLogin(ctx, "", kind, purpose, securityevent.StatusFailure)
		return TokenPair{}, ErrPasswordlessLogin
	}
	// Atomically consume the login_otp bound to the current identifier (context =
	// identifier ID + kind + normalized value). A code whose bound identifier changed
	// since issue fails the binding and is spent; a wrong/expired code counts an
	// attempt and eventually locks out. Every stable challenge disposition collapses
	// to the one generic 401; only an infrastructure error propagates.
	if _, err := s.ConsumeChallenge(ctx, ident.UserID, purpose, code,
		WithExpectedContext(loginBinding{IdentifierID: ident.ID, Kind: kind, NormalizedValue: ident.NormalizedValue})); err != nil {
		if errors.Is(err, ErrChallengeInvalid) || errors.Is(err, ErrChallengeExpired) || errors.Is(err, ErrTooManyAttempts) {
			s.recordPasswordlessLogin(ctx, ident.UserID, kind, purpose, securityevent.StatusFailure)
			return TokenPair{}, ErrPasswordlessLogin
		}
		return TokenPair{}, err
	}
	// Success mints through the SINGLE session path (design §4.1), stamping the
	// passwordless code method (email_code / sms_code) so the session records how the
	// primary authentication happened.
	pair, err := s.mintSession(ctx, ident.UserID, s.primaryAuthentication(passwordlessCodeMethod(kind)))
	if err != nil {
		return TokenPair{}, err
	}
	s.recordPasswordlessLogin(ctx, ident.UserID, kind, purpose, securityevent.StatusSuccess)
	return pair, nil
}

// passwordlessVerifyBudget applies the §4.4 verify budget on the kind-scoped verify
// key (identifier digest + trusted client IP). An exhausted budget is the generic
// ErrPasswordlessRateLimited; a limiter transport error propagates. The key derives
// from a PII-free digest, never a raw address, and applies identically to known and
// unknown identifiers so it leaks no existence signal before resolution.
func (s *Service) passwordlessVerifyBudget(ctx context.Context, kind, normalizedValue string) error {
	clientIP := clientInfoFromContext(ctx).ip
	res, err := s.limiter.Allow(ctx, s.passwordlessVerifyKey(kind, normalizedValue, clientIP), ratelimiter.PerMinute(passwordlessVerifiesPerMinute))
	if err != nil {
		return err
	}
	if !res.Allowed {
		return ErrPasswordlessRateLimited
	}
	return nil
}

// passwordlessVerifyKey builds the §4.4 verify limiter key
// (`passwordless:<kind>:<identifier-digest>|<trusted-ip>`), the kind-scoped twin of
// loginKey. It is deliberately distinct from the start prefix (`passwordless_start:`)
// so a verify throttle never shares a bucket with a start throttle.
func (s *Service) passwordlessVerifyKey(kind, normalizedValue, clientIP string) string {
	return "passwordless:" + kind + ":" + s.identifierDigest(kind, normalizedValue) + "|" + clientIP
}

// RedeemPasswordless completes the magic-link passwordless-login rail (design
// §4.3/§6.4). Its ONLY input is the opaque 256-bit token (never an identifier), so
// the pre-consume throttle keys on the trusted client IP under a redeem-distinct
// prefix. It atomically consumes the login_magic_link token by digest — the token
// is spent whether or not the binding still validates, so a redeemed link can never
// be replayed — then resolves the login identity FROM the consumed row's stored
// binding, reloads the CURRENT active verified login identifier that binding named,
// and requires it to still be the SAME row before minting through the single
// mintSession path with the email-link method. Every failure — an unknown, expired,
// or replayed token; a removed, replaced, login-disabled, or unverified bound
// identifier; or a malformed stored binding — collapses to the one generic
// ErrPasswordlessLogin (401), so a redeem never distinguishes the reason nor
// enumerates accounts. An exhausted redeem budget is the only distinct outcome
// (ErrPasswordlessRateLimited → 429); a genuine infrastructure error propagates (→
// 500) rather than masquerading as a credential failure.
func (s *Service) RedeemPasswordless(ctx context.Context, token string) (TokenPair, error) {
	// Every redeem is a login_magic_link completion, so the audit purpose is fixed;
	// the kind is unknown until the consumed token's stored binding decodes, so a
	// pre-binding failure records the purpose alone (design §4.3).
	const purpose = challenge.PurposeLoginMagicLink
	// Pre-consume throttle (design §4.4). Redeem carries no identifier, so it keys on
	// the trusted client IP under a prefix distinct from start (passwordless_start:)
	// and verify (passwordless:); an exhausted budget is the distinct 429.
	if err := s.passwordlessRedeemBudget(ctx); err != nil {
		if errors.Is(err, ErrPasswordlessRateLimited) {
			s.recordPasswordlessLogin(ctx, "", "", purpose, securityevent.StatusBlocked)
		}
		return TokenPair{}, err
	}
	// Atomically delete-and-return the magic-link row by digest (design §3.2). Every
	// stable disposition — missing/malformed/replayed or expired — is the one generic
	// 401; only an infrastructure error propagates.
	consumed, err := s.RedeemToken(ctx, purpose, token)
	if err != nil {
		if errors.Is(err, ErrChallengeInvalid) || errors.Is(err, ErrChallengeExpired) {
			s.recordPasswordlessLogin(ctx, "", "", purpose, securityevent.StatusFailure)
			return TokenPair{}, ErrPasswordlessLogin
		}
		return TokenPair{}, err
	}
	// The login identity is the identifier the token was BOUND to at issue, not a
	// request input (design §4.3). Decode the stored binding, then reload the CURRENT
	// active verified login identifier for its (kind, value) and require it to still be
	// the SAME row: a removed, replaced, login-disabled, or unverified identifier
	// resolves nothing (or a different ID) → generic 401, so a link cannot log in after
	// its identifier changed underneath it (design §4.1). This post-consume reload is
	// the token twin of the OTP path's WithExpectedContext binding check — the redeem
	// request supplies no identifier to pass as the expected context.
	var binding loginBinding
	if err := json.Unmarshal(consumed.Context, &binding); err != nil {
		s.recordPasswordlessLogin(ctx, consumed.UserID, "", purpose, securityevent.StatusFailure)
		return TokenPair{}, ErrPasswordlessLogin
	}
	ident, ok, err := s.resolvePasswordlessLogin(ctx, binding.Kind, binding.NormalizedValue)
	if err != nil {
		return TokenPair{}, err
	}
	if !ok || ident.ID != binding.IdentifierID || ident.UserID != consumed.UserID {
		s.recordPasswordlessLogin(ctx, consumed.UserID, binding.Kind, purpose, securityevent.StatusFailure)
		return TokenPair{}, ErrPasswordlessLogin
	}
	// Success mints through the SINGLE session path (design §4.1), stamping the
	// email-link method so the session records how the primary authentication happened.
	pair, err := s.mintSession(ctx, ident.UserID, s.primaryAuthentication(session.MethodEmailLink))
	if err != nil {
		return TokenPair{}, err
	}
	s.recordPasswordlessLogin(ctx, ident.UserID, binding.Kind, purpose, securityevent.StatusSuccess)
	return pair, nil
}

// passwordlessRedeemBudget applies the §4.4 pre-consume redeem throttle on the
// trusted client IP under a prefix distinct from start (passwordless_start:) and
// verify (passwordless:). Redeem carries no identifier, so the IP is the only stable
// key; an exhausted budget is the generic ErrPasswordlessRateLimited and a limiter
// transport error propagates.
func (s *Service) passwordlessRedeemBudget(ctx context.Context) error {
	ip := clientInfoFromContext(ctx).ip
	res, err := s.limiter.Allow(ctx, "passwordless_redeem:ip:"+ip, ratelimiter.PerMinute(passwordlessRedeemsPerIPPerMinute))
	if err != nil {
		return err
	}
	if !res.Allowed {
		return ErrPasswordlessRateLimited
	}
	return nil
}

// passwordlessCodeMethod maps a passwordless kind to the session method its OTP login
// stamps (design §5.0): a phone code is SMS-delivered (sms_code), every other code
// is email-delivered (email_code). The magic-link twin (email_link) is stamped by
// RedeemPasswordless.
func passwordlessCodeMethod(kind string) session.MethodKind {
	if kind == string(identifier.KindPhone) {
		return session.MethodSMSCode
	}
	return session.MethodEmailCode
}

// resolvePasswordlessMethod resolves the requested method to its delivery purpose
// (design §4.3). An empty method defaults per kind (email → link, phone → code);
// an explicit link or code is honored for any kind whose transport can carry it
// (the router's per-kind template availability is the carry check, enforced when
// the worker renders). An unrecognized method is ErrPasswordlessMethodInvalid.
func resolvePasswordlessMethod(kind, method string) (string, error) {
	if method == "" {
		if kind == string(identifier.KindPhone) {
			method = MethodCode
		} else {
			method = MethodLink
		}
	}
	switch method {
	case MethodLink:
		return delivery.PurposeMagicLink, nil
	case MethodCode:
		return delivery.PurposeLoginCode, nil
	default:
		return "", ErrPasswordlessMethodInvalid
	}
}

// passwordlessStartBudget applies the per-identifier and per-IP start budgets
// (design §4.4). Either budget's exhaustion is ErrPasswordlessRateLimited; a
// limiter transport error propagates. The keys derive from PII-free digests (the
// identifier digest and the trusted client IP), never raw addresses.
func (s *Service) passwordlessStartBudget(ctx context.Context, kind, normalizedValue string) error {
	perIdent, err := s.limiter.Allow(ctx, "passwordless_start:"+kind+":"+s.identifierDigest(kind, normalizedValue), ratelimiter.PerMinute(passwordlessStartsPerIdentifierPerMinute))
	if err != nil {
		return err
	}
	if !perIdent.Allowed {
		return ErrPasswordlessRateLimited
	}
	ip := clientInfoFromContext(ctx).ip
	perIP, err := s.limiter.Allow(ctx, "passwordless_start:ip:"+ip, ratelimiter.PerMinute(passwordlessStartsPerIPPerMinute))
	if err != nil {
		return err
	}
	if !perIP.Allowed {
		return ErrPasswordlessRateLimited
	}
	return nil
}

// initPasswordlessLink is the worker initializer for a magic-link start (design
// §4.3/§6.1.1). Off the request path it resolves the active verified login
// identifier for the enqueued normalized value, issues the login_magic_link token
// bound to that identifier, builds the sign-in link from PublicAuthBaseURL, and
// renders the magic-link message. An unknown/unverified/login-disabled identifier
// resolves nothing (deliver=false): the worker terminates the job successfully with
// no send, so a start reveals no account existence.
func (s *Service) initPasswordlessLink(ctx context.Context, kind string, cmd delivery.Envelope) (delivery.Envelope, bool, error) {
	ident, ok, err := s.resolvePasswordlessLogin(ctx, kind, cmd.ResolutionInput)
	if err != nil || !ok {
		return delivery.Envelope{}, false, err
	}
	token, err := s.IssueChallenge(ctx, ident.UserID, challenge.PurposeLoginMagicLink,
		WithStoredContext(loginBinding{IdentifierID: ident.ID, Kind: kind, NormalizedValue: ident.NormalizedValue}))
	if err != nil {
		return delivery.Envelope{}, false, err
	}
	env, err := s.deliver.Render(ctx, delivery.Request{
		Kind:            kind,
		Purpose:         delivery.PurposeMagicLink,
		Destination:     ident.NormalizedValue,
		ResolutionInput: cmd.ResolutionInput,
		Secret:          token,
		Data:            map[string]any{"Link": s.magicLinkURL(token)},
	})
	if err != nil {
		return delivery.Envelope{}, false, err
	}
	return env, true, nil
}

// initPasswordlessCode is the worker initializer for an OTP start (design
// §4.3/§6.1.1): the code twin of initPasswordlessLink. It resolves the active
// verified login identifier, issues the login_otp code bound to that identifier,
// and renders the sign-in-code message. An unknown/unverified/login-disabled
// identifier resolves nothing (deliver=false).
func (s *Service) initPasswordlessCode(ctx context.Context, kind string, cmd delivery.Envelope) (delivery.Envelope, bool, error) {
	ident, ok, err := s.resolvePasswordlessLogin(ctx, kind, cmd.ResolutionInput)
	if err != nil || !ok {
		return delivery.Envelope{}, false, err
	}
	code, err := s.IssueChallenge(ctx, ident.UserID, challenge.PurposeLoginOTP,
		WithStoredContext(loginBinding{IdentifierID: ident.ID, Kind: kind, NormalizedValue: ident.NormalizedValue}))
	if err != nil {
		return delivery.Envelope{}, false, err
	}
	env, err := s.deliver.Render(ctx, delivery.Request{
		Kind:            kind,
		Purpose:         delivery.PurposeLoginCode,
		Destination:     ident.NormalizedValue,
		ResolutionInput: cmd.ResolutionInput,
		Secret:          code,
	})
	if err != nil {
		return delivery.Envelope{}, false, err
	}
	return env, true, nil
}

// resolvePasswordlessLogin resolves the active verified login identifier for a
// passwordless start (design §4.1). GetLogin returns only the active,
// login-enabled claim, so an unknown value or one whose login use is disabled is
// sdk.ErrNotFound → (false, nil): a silent non-resolution. A resolved-but-unverified
// identifier is also (false, nil) — passwordless always requires a proven address
// (§2.3/V3). Only a real infrastructure failure returns a non-nil error (retryable).
func (s *Service) resolvePasswordlessLogin(ctx context.Context, kind, normalizedValue string) (identifier.Identifier, bool, error) {
	ident, err := s.identifiers.GetLogin(ctx, kind, normalizedValue)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return identifier.Identifier{}, false, nil
		}
		return identifier.Identifier{}, false, err
	}
	if !ident.Verified() {
		return identifier.Identifier{}, false, nil
	}
	return ident, true, nil
}

// magicLinkURL builds a passwordless sign-in link from the configured absolute
// public base URL only (design §6.4): the request Host/forwarded headers never
// participate, so a hostile Host cannot redirect the link elsewhere — the target is
// exactly the allowlisted configured base. The 256-bit token rides the URL fragment
// so it is not sent to the server on the landing-page GET (no GET consumes a token —
// RedeemPasswordless is POST-only) and can be scrubbed from browser history by the
// landing page. The base is validated as an absolute http(s) URL (HTTPS in
// production) at construction (validatePublicAuthBaseURL, §6.4).
func (s *Service) magicLinkURL(token string) string {
	base := strings.TrimRight(s.publicBaseURL, "/")
	return base + "#token=" + url.QueryEscape(token)
}
