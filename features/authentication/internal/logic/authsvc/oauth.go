package authsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

const (
	// oauthStateTTL bounds an in-flight authorization round-trip.
	oauthStateTTL = 10 * time.Minute
	// pendingLinkTTL bounds the anti-takeover pending-link secret.
	pendingLinkTTL = time.Hour
	// adoptionRevisionRetries bounds how many times the adoption password-removal
	// re-reads auth_revision and retries after a concurrent credential mutation lost
	// the revision-CAS (design §5.6). A small ceiling: a single-use pending-link
	// completion rarely races another mutation, and an unbounded retry is a spin.
	adoptionRevisionRetries = 3
)

// OAuth flow outcomes, reported by OAuthCallback / VerifyLink so the transport
// can set a cookie and redirect appropriately.
const (
	// ActionLogin — the provider identity was already linked; a session was
	// minted for the existing user.
	ActionLogin = "login"
	// ActionRegister — no user existed; a new (password-less) user was created,
	// linked, and a session minted.
	ActionRegister = "register"
	// ActionPendingLink — a user with the provider's email exists but the
	// identity was not yet linked; a single-use secret was mailed and the link
	// completes only via VerifyLink. No session is minted.
	ActionPendingLink = "pending_link"
	// ActionLinked — a link completed (either an explicit session-gated link or a
	// pending-link confirmation). VerifyLink also mints a session.
	ActionLinked = "linked"
)

// ErrInvalidOAuthState is returned when a consumed state does not match the
// provider/purpose it is being redeemed for (a tampered or misrouted callback).
// It wraps sdk.ErrNotFound so it maps to 404 and leaks nothing. Checked with
// errors.Is.
var ErrInvalidOAuthState = fmt.Errorf("invalid oauth state: %w", sdk.ErrNotFound)

// ErrProviderEmailUnverified is returned by OAuthCallback when a provider hands
// back an email without verified provenance (the integration does not map the
// provider's verified assertion, or the provider did not assert the address as
// verified) and no existing link resolves the identity. A provider email string
// without verified provenance never auto-matches, registers, or adopts an
// existing identifier (design §5.7): the branch would otherwise let an attacker
// steer an unverified address into an account match. It wraps sdk.ErrForbidden so
// the transport maps it to 403. Checked with errors.Is.
var ErrProviderEmailUnverified = fmt.Errorf("oauth provider did not assert a verified email: %w", sdk.ErrForbidden)

// ErrAdoptionRevocationUnavailable is returned by VerifyLink when a pending link
// must adopt an account whose matched identifier was UNVERIFIED at flow start —
// which requires revoking the pre-existing (squatter) password and sessions
// first (design §5.7/V5) — but the revision-serialized credential-mutation rail
// is not wired. Completing the link without the revocation could leave a squatter
// credential alive, so the flow fails closed rather than adopting unsafely. It
// wraps sdk.ErrForbidden. Checked with errors.Is.
var ErrAdoptionRevocationUnavailable = fmt.Errorf("adoption revocation rail not wired: %w", sdk.ErrForbidden)

// OAuthResult is the outcome of a processed OAuth callback or verify-link. Token
// and RefreshToken are the minted access/refresh pair (§1.1) on a login/register/
// linked outcome; both are empty for ActionPendingLink (no session minted).
type OAuthResult struct {
	Action       string    // one of ActionLogin/ActionRegister/ActionPendingLink/ActionLinked
	Token        string    // access JWT (session cookie value); empty for ActionPendingLink
	RefreshToken string    // opaque refresh token; empty for ActionPendingLink
	User         user.User // the resolved user (zero for a bare pending-link start)
	RedirectTo   string    // the validated post-flow destination
}

// providerIdentity is the identity read from a provider after code exchange —
// ID-token claims for OIDC providers, the userinfo endpoint otherwise.
type providerIdentity struct {
	ProviderUserID string
	Email          string
	EmailVerified  bool
}

// flowState is the payload of a PurposeFlow oauthstate row: the PKCE verifier and
// OIDC nonce for the round-trip, the validated redirect target, and the linking
// user id (empty for a login/register start, set for a session-gated link start).
type flowState struct {
	CodeVerifier string `json:"code_verifier"`
	Nonce        string `json:"nonce"`
	RedirectTo   string `json:"redirect_to"`
	LinkUserID   string `json:"link_user_id"`
}

// pendingLink is the payload of a PurposePendingLink oauthstate row (design
// §5.7/V5): the would-be link plus the anti-takeover facts CAPTURED at branch-2
// match time — the matched identifier's id and whether it was UNVERIFIED at flow
// start. VerifyLink reads UnverifiedAtStart back VERBATIM to decide adoption
// revocation; it is never re-derived at completion, because the identifier's
// verification state can change between start and finish (a TOCTOU the captured
// flag closes).
type pendingLink struct {
	Account             oauthaccount.OAuthAccount `json:"account"`
	MatchedIdentifierID string                    `json:"matched_identifier_id"`
	UnverifiedAtStart   bool                      `json:"unverified_at_start"`
}

// OAuthEnabled reports whether any provider is wired. The transport registers
// the OAuth routes only when it is true (deny-by-absence, design §3).
func (s *Service) OAuthEnabled() bool { return len(s.providers) > 0 }

// OAuthProviderNames lists the wired provider names in deterministic
// (sorted) order. The HTML login page renders its "continue with" links from
// it; empty means OAuth is off and the page renders none.
func (s *Service) OAuthProviderNames() []string {
	names := make([]string, 0, len(s.providers))
	for name := range s.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// StartOAuth begins a login/register authorization round-trip for providerName,
// returning the provider authorization URL to redirect the browser to. It
// persists server-side flow state (PKCE verifier, OIDC nonce, validated redirect
// target). An unknown provider → sdk.ErrNotFound.
func (s *Service) StartOAuth(ctx context.Context, providerName, redirectTo string) (string, error) {
	return s.start(ctx, providerName, redirectTo, "")
}

// StartLink begins a session-gated link round-trip: the resulting link attaches
// the provider identity to userID (rather than logging in or registering).
func (s *Service) StartLink(ctx context.Context, userID, providerName, redirectTo string) (string, error) {
	return s.start(ctx, providerName, redirectTo, userID)
}

// ResolveRedirect returns a safe post-flow destination for target: the value
// itself when it is exactly allowlisted (or the same-origin default "/"),
// otherwise the same-origin default. The HTML form dispatch (design §9.2) uses it
// to validate a return-to before a 303 redirect, sharing the OAuth open-redirect
// guard so a browser sign-in cannot be bounced to an attacker origin (design §6.4).
func (s *Service) ResolveRedirect(target string) string {
	return s.redirects.Resolve(target)
}

func (s *Service) start(ctx context.Context, providerName, redirectTo, linkUserID string) (string, error) {
	p, err := s.provider(providerName)
	if err != nil {
		return "", err
	}
	verifier := pkceVerifier()
	_ = oauth.GenerateCodeChallenge(verifier) // challenge is derived by the provider from the verifier at authorize time
	var nonce string
	if p.SupportsOIDC() {
		nonce = newSecret()
	}
	payload, err := json.Marshal(flowState{
		CodeVerifier: verifier,
		Nonce:        nonce,
		RedirectTo:   s.redirects.Resolve(redirectTo),
		LinkUserID:   linkUserID,
	})
	if err != nil {
		return "", err
	}
	st := oauthstate.New(providerName, oauthstate.PurposeFlow, payload, oauthStateTTL, s.now())
	if _, err := s.oauthStates.Create(ctx, st); err != nil {
		return "", err
	}
	return p.GetAuthorizationURL(st.Token, verifier, nonce, s.callbackURL(providerName)), nil
}

// OAuthCallback processes a provider redirect: it consumes the flow state (single
// use), exchanges the code, reads the provider identity, and resolves the
// three-way anti-takeover branch (existing link → login; matching email, no link
// → pending link; no user → register + link) — or, for a session-gated link
// start, attaches the identity to the linking user. A consumed/expired/unknown
// state surfaces sdk.ErrNotFound / sdk.ErrExpired.
func (s *Service) OAuthCallback(ctx context.Context, providerName, code, stateToken string) (OAuthResult, error) {
	p, err := s.provider(providerName)
	if err != nil {
		return OAuthResult{}, err
	}
	st, err := s.oauthStates.Consume(ctx, stateToken)
	if err != nil {
		return OAuthResult{}, err
	}
	if st.Purpose != oauthstate.PurposeFlow || st.Provider != providerName {
		return OAuthResult{}, ErrInvalidOAuthState
	}
	var fs flowState
	if err := json.Unmarshal(st.Payload, &fs); err != nil {
		return OAuthResult{}, fmt.Errorf("decode oauth state: %w", err)
	}

	tok, err := p.ExchangeCode(ctx, code, fs.CodeVerifier, s.callbackURL(providerName))
	if err != nil {
		return OAuthResult{}, fmt.Errorf("oauth code exchange: %w", err)
	}
	ident, err := s.readIdentity(ctx, p, tok, fs.Nonce)
	if err != nil {
		return OAuthResult{}, err
	}

	// Session-gated link start: attach the identity to the linking user.
	if fs.LinkUserID != "" {
		if _, err := s.linkAccount(ctx, fs.LinkUserID, providerName, ident, tok); err != nil {
			return OAuthResult{}, err
		}
		u, err := s.users.Get(ctx, fs.LinkUserID)
		if err != nil {
			return OAuthResult{}, err
		}
		s.recordOAuth(ctx, fs.LinkUserID, providerName, securityevent.TypeOAuthLinked)
		return OAuthResult{Action: ActionLinked, User: u, RedirectTo: fs.RedirectTo}, nil
	}

	// Branch 1: the provider identity is already linked → login.
	existing, err := s.oauthAccounts.GetByProvider(ctx, providerName, ident.ProviderUserID)
	switch {
	case err == nil:
		pair, err := s.mintSession(ctx, existing.UserID, s.primaryAuthentication(session.MethodOAuth))
		if err != nil {
			return OAuthResult{}, err
		}
		u, err := s.users.Get(ctx, existing.UserID)
		if err != nil {
			return OAuthResult{}, err
		}
		s.recordOAuth(ctx, existing.UserID, providerName, securityevent.TypeOAuthLogin)
		return OAuthResult{Action: ActionLogin, Token: pair.AccessToken, RefreshToken: pair.RefreshToken, User: u, RedirectTo: fs.RedirectTo}, nil
	case !errors.Is(err, sdk.ErrNotFound):
		return OAuthResult{}, err
	}

	// Matching, adoption, and registration are permitted only when the provider
	// asserts the email as verified AND the integration maps that assertion (design
	// §5.7): a provider email string without verified provenance never auto-matches,
	// registers, or adopts. Branch 1 (an existing link) is keyed on the provider user
	// id and already returned above, so it is unaffected by this gate.
	if !p.TrustEmailVerification() || !ident.EmailVerified {
		return OAuthResult{}, ErrProviderEmailUnverified
	}
	normEmail, err := s.normalizeEmail(ident.Email)
	if err != nil {
		return OAuthResult{}, ErrProviderEmailUnverified
	}

	// Branch 2: an account already claims the provider's email through a login- or
	// recovery-enabled identifier but is not linked → pending link (single-use secret
	// mailed to the address; completes only via VerifyLink). The matched identifier's
	// id and unverified-at-flow-start fact are captured now (§5.7/V5).
	matched, err := s.matchIdentifier(ctx, normEmail)
	switch {
	case err == nil:
		u, err := s.users.Get(ctx, matched.UserID)
		if err != nil {
			return OAuthResult{}, err
		}
		if err := s.startPendingLink(ctx, matched, providerName, ident, tok); err != nil {
			return OAuthResult{}, err
		}
		return OAuthResult{Action: ActionPendingLink, User: u, RedirectTo: fs.RedirectTo}, nil
	case !errors.Is(err, sdk.ErrNotFound):
		return OAuthResult{}, err
	}

	// Branch 3: no account claims the email → register + link.
	return s.registerAndLink(ctx, providerName, normEmail, ident, tok, fs.RedirectTo)
}

// VerifyLink completes a pending link: it consumes the pending-link secret
// (single use), creates the link stored in its payload, and mints a session for
// the now-linked user. An expired/unknown/already-used token surfaces
// sdk.ErrExpired / sdk.ErrNotFound.
func (s *Service) VerifyLink(ctx context.Context, token string) (OAuthResult, error) {
	st, err := s.oauthStates.Consume(ctx, token)
	if err != nil {
		return OAuthResult{}, err
	}
	if st.Purpose != oauthstate.PurposePendingLink {
		return OAuthResult{}, ErrInvalidOAuthState
	}
	var pl pendingLink
	if err := json.Unmarshal(st.Payload, &pl); err != nil {
		return OAuthResult{}, fmt.Errorf("decode pending link: %w", err)
	}
	// Adoption revocation (design §5.7/V5): the matched identifier was UNVERIFIED at
	// flow start, so any password/sessions predating this address-possession proof
	// belong to a squatter and are revoked BEFORE the adopting link is created. The
	// flag is read verbatim from the captured payload, never re-derived here. Ordering
	// (revoke, then link) is the invariant: a completed adoption cannot leave the
	// squatter credential alive because the link is created only after revocation.
	if pl.UnverifiedAtStart {
		if err := s.revokeForAdoption(ctx, pl.Account.UserID); err != nil {
			return OAuthResult{}, err
		}
	}
	created, err := s.oauthAccounts.Create(ctx, pl.Account)
	if err != nil {
		return OAuthResult{}, err
	}
	pair, err := s.mintSession(ctx, created.UserID, s.primaryAuthentication(session.MethodOAuth))
	if err != nil {
		return OAuthResult{}, err
	}
	u, err := s.users.Get(ctx, created.UserID)
	if err != nil {
		return OAuthResult{}, err
	}
	s.recordOAuth(ctx, created.UserID, created.Provider, securityevent.TypeOAuthLinkVerified)
	return OAuthResult{Action: ActionLinked, Token: pair.AccessToken, RefreshToken: pair.RefreshToken, User: u}, nil
}

// ListLinked returns every provider link owned by userID.
func (s *Service) ListLinked(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	return s.oauthAccounts.ListByUser(ctx, userID)
}

// unlinkBinding is the challenge stored-context that pins an unlink_oauth code to
// one provider (design §5.4): a code issued to unlink Google cannot complete an
// unlink of GitHub because the provider digest will not match at consume.
type unlinkBinding struct {
	Provider string `json:"provider"`
}

// StartUnlinkOAuth issues a provider-bound unlink_oauth code and delivers it to an
// existing active verified recovery identifier (design §5.4). The code's stored
// context binds the exact provider, so a code minted to unlink one provider can
// never complete an unlink of another. Possession of the code is the
// reauthentication proof UnlinkOAuth consumes; it never rides to a proposed new
// address, only to a channel the account already owns and has verified. The code
// rides the durable outbox — a delivery failure surfaces through the receipt. An
// absent link → sdk.ErrNotFound; no verified recovery identifier →
// ErrNoRecoveryIdentifier.
func (s *Service) StartUnlinkOAuth(ctx context.Context, userID, providerName string) (StepUpReceipt, error) {
	if s.challenges == nil || s.protector == nil {
		return StepUpReceipt{}, ErrStepUpUnavailable
	}
	if err := s.requireLinked(ctx, userID, providerName); err != nil {
		return StepUpReceipt{}, err
	}
	dest, err := s.verifiedRecoveryIdentifier(ctx, userID)
	if err != nil {
		return StepUpReceipt{}, err
	}
	code, err := s.IssueChallenge(ctx, userID, challenge.PurposeUnlinkOAuth,
		WithStoredContext(unlinkBinding{Provider: providerName}))
	if err != nil {
		return StepUpReceipt{}, err
	}
	kind := string(dest.Kind)
	key := s.idempotencyKey(kind, dest.NormalizedValue, delivery.PurposeSensitiveCode)
	if err := s.enqueueRendered(ctx, delivery.PurposeSensitiveCode, key, delivery.Request{
		Kind:            kind,
		Purpose:         delivery.PurposeSensitiveCode,
		Destination:     dest.NormalizedValue,
		ResolutionInput: dest.NormalizedValue,
		Secret:          code,
	}); err != nil {
		return StepUpReceipt{}, err
	}
	s.recordOAuth(ctx, userID, providerName, securityevent.TypeOAuthUnlinkCodeSent)
	return StepUpReceipt{Delivered: true, Receipt: key}, nil
}

// UnlinkOAuth completes a provider-bound OAuth unlink (design §5.4). Consuming the
// provider-bound unlink_oauth code proves the caller controls a verified recovery
// channel AND that the code was issued for THIS provider: a code minted to unlink a
// different provider is consumed and rejected as ErrChallengeInvalid, never
// authorizing the wrong unlink. The credential policy then guards the proposed
// method set (§5.6), and the link is deleted (its encrypted provider tokens with it)
// and the user's auth_revision bumped atomically under revision-CAS, re-evaluating
// policy on a concurrent conflict. An absent link → sdk.ErrNotFound; a removal that
// would leave no acceptable method is the policy's stable rejection
// (credential.ErrNoLoginMethod).
func (s *Service) UnlinkOAuth(ctx context.Context, userID, providerName, code string) error {
	if s.credentialMutations == nil {
		return ErrCredentialMutationUnavailable
	}
	if err := s.requireLinked(ctx, userID, providerName); err != nil {
		return err
	}
	// The provider-bound unlink_oauth code is this flow's reauthentication proof; a
	// wrong, expired, locked-out, or wrong-provider code is the stable challenge error
	// (a wrong-provider code is consumed and rejected — design §5.4).
	if _, err := s.ConsumeChallenge(ctx, userID, challenge.PurposeUnlinkOAuth, code,
		WithExpectedContext(unlinkBinding{Provider: providerName})); err != nil {
		return err
	}
	if err := s.applyCredentialMutation(ctx, userID, credential.UnlinkOAuth{Provider: providerName}); err != nil {
		return err
	}
	s.recordOAuth(ctx, userID, providerName, securityevent.TypeOAuthUnlinked)
	return nil
}

// requireLinked verifies userID currently holds a link to providerName, returning
// sdk.ErrNotFound when it does not (design §5.4).
func (s *Service) requireLinked(ctx context.Context, userID, providerName string) error {
	linked, err := s.oauthAccounts.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, a := range linked {
		if a.Provider == providerName {
			return nil
		}
	}
	return fmt.Errorf("no %s link for user: %w", providerName, sdk.ErrNotFound)
}

// linkAccount builds and persists a link, propagating a duplicate-identity
// collision (sdk.ErrAlreadyExists) from the store.
func (s *Service) linkAccount(ctx context.Context, userID, providerName string, ident providerIdentity, tok *oauth.TokenResponse) (oauthaccount.OAuthAccount, error) {
	acct, err := s.newAccount(userID, providerName, ident, tok)
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return s.oauthAccounts.Create(ctx, acct)
}

// registerAndLink creates a password-less user for a first-seen provider identity
// together with its primary email identifier in one atomic operation
// (CreateWithPrimaryIdentifier, design §2.2), links the provider account, and mints
// a session. The caller has already established verified provenance (branch 3 is
// reached only past the §5.7 gate), so the primary identifier is created VERIFIED —
// login-, recovery-, and notification-enabled and primary. normEmail is the
// already-normalized provider email.
func (s *Service) registerAndLink(ctx context.Context, providerName, normEmail string, ident providerIdentity, tok *oauth.TokenResponse, redirectTo string) (OAuthResult, error) {
	now := s.now()
	primary, err := identifier.New(s.ids, s.normalizer, "", identifier.KindEmail, ident.Email,
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, now, now)
	if err != nil {
		return OAuthResult{}, err
	}
	u := user.NewUser(s.ids, "", now)
	created, _, err := s.users.CreateWithPrimaryIdentifier(ctx, u, primary)
	if err != nil {
		return OAuthResult{}, err
	}
	if _, err := s.linkAccount(ctx, created.ID, providerName, ident, tok); err != nil {
		return OAuthResult{}, err
	}
	pair, err := s.mintSession(ctx, created.ID, s.primaryAuthentication(session.MethodOAuth))
	if err != nil {
		return OAuthResult{}, err
	}
	s.recordOAuth(ctx, created.ID, providerName, securityevent.TypeOAuthRegister)
	return OAuthResult{Action: ActionRegister, Token: pair.AccessToken, RefreshToken: pair.RefreshToken, User: created, RedirectTo: redirectTo}, nil
}

// matchIdentifier resolves the account that claims normEmail through an active
// authentication-bearing email identifier (design §5.7): a login-enabled claim
// first, then a recovery-enabled one. sdk.ErrNotFound means no account claims the
// address. Verification is NOT filtered here — the row is returned even when
// unverified so the caller can capture the unverified-at-flow-start fact.
func (s *Service) matchIdentifier(ctx context.Context, normEmail string) (identifier.Identifier, error) {
	kind := string(identifier.KindEmail)
	ident, err := s.identifiers.GetLogin(ctx, kind, normEmail)
	if err == nil {
		return ident, nil
	}
	if !errors.Is(err, sdk.ErrNotFound) {
		return identifier.Identifier{}, err
	}
	return s.identifiers.GetRecovery(ctx, kind, normEmail)
}

// revokeForAdoption revokes a squatter's pre-existing credentials before an
// adopting link is created (design §5.7/V5): it deletes every session for userID,
// then removes any pre-existing password through the revision-serialized
// credential-mutation rail. Sessions are revoked first and the password removal is
// the last step, so a caller that creates the link only on a nil return can never
// leave the squatter password alive. It fails closed
// (ErrAdoptionRevocationUnavailable) when the credential-mutation rail is unwired,
// rather than adopting unsafely.
func (s *Service) revokeForAdoption(ctx context.Context, userID string) error {
	if s.credentialMutations == nil {
		return ErrAdoptionRevocationUnavailable
	}
	if err := s.sessions.DeleteByUser(ctx, userID); err != nil {
		return err
	}
	return s.removePassword(ctx, userID)
}

// removePassword deletes userID's password through the revision-CAS credential
// rail (the password repository exposes no delete — the typed mutation is the only
// removal seam). A user with no password is a no-op success. A revision conflict —
// a concurrent credential mutation moved auth_revision between the read and the
// apply — is retried a bounded number of times against a fresh revision.
func (s *Service) removePassword(ctx context.Context, userID string) error {
	if _, err := s.passwords.Get(ctx, userID); err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return nil
		}
		return err
	}
	var lastErr error
	for attempt := 0; attempt < adoptionRevisionRetries; attempt++ {
		u, err := s.users.Get(ctx, userID)
		if err != nil {
			return err
		}
		err = s.credentialMutations.Apply(ctx, userID, u.AuthRevision, credential.RemovePassword{})
		if err == nil {
			return nil
		}
		if !errors.Is(err, sdk.ErrConflict) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// recordOAuth appends an OAuth-flow audit row for userID. The provider name is an
// identifier (never a secret), so it rides Details.
func (s *Service) recordOAuth(ctx context.Context, userID, providerName, eventType string) {
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID:  userID,
		Type:    eventType,
		Status:  securityevent.StatusSuccess,
		Details: map[string]any{"provider": providerName},
	})
}

// startPendingLink stores the would-be link — plus the captured anti-takeover
// facts (matched identifier id and unverified-at-flow-start flag, design §5.7/V5)
// — as a single-use pending-link state and mails its secret to the matched
// address. The link is created only when VerifyLink redeems it.
func (s *Service) startPendingLink(ctx context.Context, matched identifier.Identifier, providerName string, ident providerIdentity, tok *oauth.TokenResponse) error {
	acct, err := s.newAccount(matched.UserID, providerName, ident, tok)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(pendingLink{
		Account:             acct,
		MatchedIdentifierID: matched.ID,
		UnverifiedAtStart:   !matched.Verified(),
	})
	if err != nil {
		return err
	}
	st := oauthstate.New(providerName, oauthstate.PurposePendingLink, payload, pendingLinkTTL, s.now())
	if _, err := s.oauthStates.Create(ctx, st); err != nil {
		return err
	}
	// The account is resolved (branch 2 matched an existing identifier), so the
	// pending link is not enumeration-sensitive: render the confirmation token on the
	// request path and enqueue the sealed message on the durable outbox. The secret
	// goes to the matched (normalized) address — completion is address-possession
	// proof (§5.7). A failed enqueue rolls back the pending-link state just created so
	// no orphaned secret survives a send that never happened.
	dest := matched.NormalizedValue
	key := s.idempotencyKey(identity.KindEmail, dest, delivery.PurposeOAuthPendingLink)
	if err := s.enqueueRendered(ctx, delivery.PurposeOAuthPendingLink, key, delivery.Request{
		Kind:            identity.KindEmail,
		Purpose:         delivery.PurposeOAuthPendingLink,
		Destination:     dest,
		ResolutionInput: dest,
		Secret:          st.Token,
		Data:            map[string]any{"ProviderName": providerName},
	}); err != nil {
		if _, cerr := s.oauthStates.Consume(ctx, st.Token); cerr != nil {
			s.logger.Warn("pending-link rollback failed", "error_kind", errKind(cerr))
		}
		return err
	}
	return nil
}

// newAccount assembles an OAuthAccount from a provider identity and token,
// encrypting the provider tokens when a TokenEncrypter is wired and dropping them
// (leaving the fields empty) when it is not (design §3).
func (s *Service) newAccount(userID, providerName string, ident providerIdentity, tok *oauth.TokenResponse) (oauthaccount.OAuthAccount, error) {
	acct, err := oauthaccount.New(userID, providerName, ident.ProviderUserID, s.now())
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	acct.ProviderEmail = ident.Email
	acct.ProviderEmailVerified = ident.EmailVerified
	acct.TokenType = tok.TokenType
	acct.Scope = tok.Scopes
	if tok.ExpiresIn > 0 {
		acct.TokenExpiresAt = s.now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	if s.tokenEncrypter != nil {
		if tok.AccessToken != "" {
			enc, err := s.tokenEncrypter.Encrypt(tok.AccessToken)
			if err != nil {
				return oauthaccount.OAuthAccount{}, fmt.Errorf("encrypt access token: %w", err)
			}
			acct.AccessToken = enc
		}
		if tok.RefreshToken != "" {
			enc, err := s.tokenEncrypter.Encrypt(tok.RefreshToken)
			if err != nil {
				return oauthaccount.OAuthAccount{}, fmt.Errorf("encrypt refresh token: %w", err)
			}
			acct.RefreshToken = enc
		}
	}
	return acct, nil
}

// readIdentity reads the provider identity after code exchange: validated
// ID-token claims for OIDC providers (nonce-checked), the userinfo endpoint
// otherwise.
func (s *Service) readIdentity(ctx context.Context, p oauth.Provider, tok *oauth.TokenResponse, nonce string) (providerIdentity, error) {
	if p.SupportsOIDC() {
		claims, err := p.ValidateIDToken(ctx, tok.IDToken, nonce)
		if err != nil {
			return providerIdentity{}, fmt.Errorf("validate id token: %w", err)
		}
		return providerIdentity{ProviderUserID: claims.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified}, nil
	}
	info, err := p.GetUserInfo(ctx, tok.AccessToken)
	if err != nil {
		return providerIdentity{}, fmt.Errorf("get user info: %w", err)
	}
	return providerIdentity{ProviderUserID: info.ProviderUserID, Email: info.Email, EmailVerified: info.EmailVerified}, nil
}

// provider returns the wired provider by name, or sdk.ErrNotFound.
func (s *Service) provider(name string) (oauth.Provider, error) {
	p, ok := s.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown oauth provider %q: %w", name, sdk.ErrNotFound)
	}
	return p, nil
}

// callbackURL builds the absolute redirect URI for providerName from the
// configured base (design §3's OAuthCallbackBase).
func (s *Service) callbackURL(providerName string) string {
	return s.callbackBase + "/auth/oauth/" + providerName + "/callback"
}

// pkceVerifier returns a high-entropy PKCE code verifier from the unreserved
// character set (sdk/foundation/cryptids' dotless alphabet), long enough for RFC 7636.
func pkceVerifier() string { return newSecret() + newSecret() }

// newSecret returns an opaque high-entropy value (sdk/foundation/cryptids) for a nonce or
// PKCE segment.
func newSecret() string { return secrets.MustGenerate() }
