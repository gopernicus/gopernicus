package authentication

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/credential"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/oauth"
)

const (
	// oauthStateTTL bounds an in-flight authorization round-trip.
	oauthStateTTL = 10 * time.Minute
	// pendingLinkTTL bounds the anti-takeover pending-link secret.
	pendingLinkTTL = time.Hour
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
type providerIdentity = oauth.UserInfo

// flowState is the payload of a PurposeFlow oauthstate row: the PKCE verifier and
// OIDC nonce for the round-trip, the validated redirect target, and the linking
// user id (empty for a login/register start, set for a session-gated link start).
type flowState struct {
	Mode             OAuthMode `json:"mode"`
	RedirectURI      string    `json:"redirect_uri"`
	CodeVerifier     string    `json:"code_verifier"`
	Nonce            string    `json:"nonce"`
	RedirectTo       string    `json:"redirect_to"`
	LinkUserID       string    `json:"link_user_id"`
	LinkAuthRevision *int64    `json:"link_auth_revision,omitempty"`
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
	AuthRevision        *int64                    `json:"auth_revision"`
}

// OAuthEnabled reports whether any provider is wired. The transport registers
// the OAuth routes only when it is true (deny-by-absence, design §3).
func (s *Service) OAuthEnabled() bool { return len(s.providers) > 0 }

// OAuthProviderNames lists the wired provider names in deterministic
// (sorted) order. The HTML login page renders its "continue with" links from it,
// and the account page derives its link affordances from it; empty means OAuth is
// off and the pages render none.
func (s *Service) OAuthProviderNames() []string {
	names := make([]string, 0, len(s.providers))
	for name := range s.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// OAuthMode binds how the initiating client retains its completion proof.
type OAuthMode string

const (
	OAuthBrowser OAuthMode = "browser"
	OAuthNative  OAuthMode = "native"
)

// OAuthStartRequest explicitly selects the client transport. Native redirect
// URIs must be exactly allowlisted by the host; browser callbacks use the host base.
type OAuthStartRequest struct {
	Mode        OAuthMode
	RedirectTo  string
	RedirectURI string
}

// OAuthStart is returned only to the initiating client. FlowSecret must be kept
// separately from State and must never appear in a redirect URL.
type OAuthStart struct {
	AuthorizationURL string    `json:"authorization_url"`
	State            string    `json:"state"`
	FlowSecret       string    `json:"flow_secret"`
	ExpiresAt        time.Time `json:"expires_at"`
}

// OAuthCallbackRequest redeems a transaction with the initiating client's proof.
// The redirect URI is read from stored state, never from the callback.
type OAuthCallbackRequest struct {
	Mode       OAuthMode
	Code       string
	State      string
	FlowSecret string
}

func (s *Service) OAuthNativeEnabled() bool { return s.OAuthEnabled() && len(s.nativeRedirectURIs) > 0 }
func (s *Service) OAuthBrowserSecure() bool {
	base, err := url.Parse(s.callbackBase)
	return err == nil && base.Scheme == "https"
}

// StartOAuth begins a login/register authorization round-trip.
func (s *Service) StartOAuth(ctx context.Context, providerName string, req OAuthStartRequest) (OAuthStart, error) {
	return s.start(ctx, providerName, req, "")
}

// StartLink begins a link for a host-authenticated user. The caller must authorize
// userID; the bundled transport requires the person's session.
func (s *Service) StartLink(ctx context.Context, userID, providerName string, req OAuthStartRequest) (OAuthStart, error) {
	if userID == "" {
		return OAuthStart{}, sdk.ErrUnauthorized
	}
	return s.start(ctx, providerName, req, userID)
}

// ResolveRedirect returns a safe post-flow destination for target: a safe
// same-origin relative path is honored directly (redirect.SafeRelativePath — a
// relative path is never an off-site open-redirect vector, so it needs no
// allowlisting), an exactly allowlisted absolute target is honored, and anything
// else falls back to the same-origin default "/". It is the single browser-lane
// resolver: the OAuth flow start and the HTML form dispatch (design §9.2) both
// route through it, so a browser sign-in cannot be bounced to an attacker origin
// (design §6.4). The invitation lane keeps the exact-match-only rule — its
// destination is embedded in a MAILED link, where a relative path is not a
// meaningful target.
func (s *Service) ResolveRedirect(target string) string {
	if p := redirect.SafeRelativePath(target); p != "" {
		return p
	}
	return s.redirects.Resolve(target)
}

func (s *Service) start(ctx context.Context, providerName string, req OAuthStartRequest, linkUserID string) (OAuthStart, error) {
	p, err := s.provider(providerName)
	if err != nil {
		return OAuthStart{}, err
	}
	var redirectURI string
	switch req.Mode {
	case OAuthBrowser:
		if req.RedirectURI != "" {
			return OAuthStart{}, sdk.ErrInvalidInput
		}
		redirectURI = s.callbackURL(providerName)
	case OAuthNative:
		if req.RedirectURI == "" || !slices.Contains(s.nativeRedirectURIs, req.RedirectURI) {
			return OAuthStart{}, sdk.ErrInvalidInput
		}
		redirectURI = req.RedirectURI
	default:
		return OAuthStart{}, sdk.ErrInvalidInput
	}
	var linkAuthRevision *int64
	if linkUserID != "" {
		u, err := s.users.Get(ctx, linkUserID)
		if err != nil {
			return OAuthStart{}, err
		}
		if !u.Active() {
			return OAuthStart{}, session.ErrUserNotActive
		}
		linkAuthRevision = &u.AuthRevision
	}
	verifier := newFlowSecret()
	var nonce string
	if _, ok := p.(oauth.IDTokenValidator); ok {
		nonce = newFlowSecret()
	}
	flow := OAuthStart{State: newFlowSecret(), FlowSecret: newFlowSecret(), ExpiresAt: s.now().Add(oauthStateTTL)}
	flow.AuthorizationURL, err = p.GetAuthorizationURL(oauth.AuthorizationRequest{
		State: flow.State, CodeVerifier: verifier, Nonce: nonce, RedirectURI: redirectURI,
	})
	if err != nil {
		return OAuthStart{}, err
	}
	payload, err := json.Marshal(flowState{
		Mode: req.Mode, RedirectURI: redirectURI, CodeVerifier: verifier,
		Nonce: nonce, RedirectTo: s.ResolveRedirect(req.RedirectTo), LinkUserID: linkUserID, LinkAuthRevision: linkAuthRevision,
	})
	if err != nil {
		return OAuthStart{}, err
	}
	st := oauthstate.State{Token: flowLookupKey(providerName, req.Mode, flow.State, flow.FlowSecret),
		Provider: providerName, Purpose: oauthstate.PurposeFlow, Payload: payload, ExpiresAt: flow.ExpiresAt}
	if _, err := s.oauthStates.Create(ctx, st); err != nil {
		return OAuthStart{}, err
	}
	return flow, nil
}

// A framed tuple avoids ambiguous concatenations. The independently random proof
// is absent from the provider URL and makes incorrect callbacks miss the lookup
// without consuming the legitimate transaction. Repositories still consume once.
func flowLookupKey(provider string, mode OAuthMode, state, proof string) string {
	encoded, _ := json.Marshal([5]string{"gopernicus.oauth.flow.v1", provider, string(mode), state, proof})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func newFlowSecret() string {
	var value [32]byte
	rand.Read(value[:])
	return base64.RawURLEncoding.EncodeToString(value[:])
}

// ValidOAuthFlowValue bounds untrusted state/proof before cookie or store lookup.
func ValidOAuthFlowValue(value string) bool {
	if len(value) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value
}

// OAuthCallback processes a provider redirect: it consumes the flow state (single
// use), exchanges the code, reads the provider identity, and resolves the
// three-way anti-takeover branch (existing link → login; matching email, no link
// → pending link; no user → register + link) — or, for a session-gated link
// start, attaches the identity to the linking user. A consumed/expired/unknown
// state surfaces sdk.ErrNotFound / sdk.ErrExpired.
func (s *Service) OAuthCallback(ctx context.Context, providerName string, req OAuthCallbackRequest) (OAuthResult, error) {
	p, err := s.provider(providerName)
	if err != nil {
		return OAuthResult{}, err
	}
	if (req.Mode != OAuthBrowser && req.Mode != OAuthNative) || !ValidOAuthFlowValue(req.State) || !ValidOAuthFlowValue(req.FlowSecret) || strings.TrimSpace(req.Code) == "" {
		return OAuthResult{}, ErrInvalidOAuthState
	}
	st, err := s.oauthStates.Consume(ctx, flowLookupKey(providerName, req.Mode, req.State, req.FlowSecret))
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

	if fs.Mode != req.Mode || fs.RedirectURI == "" || (fs.LinkUserID != "" && fs.LinkAuthRevision == nil) {
		return OAuthResult{}, ErrInvalidOAuthState
	}
	tok, err := p.ExchangeCode(ctx, req.Code, fs.CodeVerifier, fs.RedirectURI)
	if err != nil {
		return OAuthResult{}, fmt.Errorf("oauth code exchange: %w", err)
	}
	ident, err := s.readIdentity(ctx, p, tok, fs.Nonce)
	if err != nil {
		return OAuthResult{}, err
	}

	// Session-gated link start: attach the identity to the linking user.
	if fs.LinkUserID != "" {
		if _, err := s.linkAccount(ctx, fs.LinkUserID, *fs.LinkAuthRevision, providerName, ident, tok); err != nil {
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
		pair, u, err := s.oauthSession(ctx, existing)
		if err != nil {
			return OAuthResult{}, err
		}
		s.recordOAuth(ctx, existing.UserID, providerName, securityevent.TypeOAuthLogin)
		return OAuthResult{Action: ActionLogin, Token: pair.AccessToken, RefreshToken: pair.RefreshToken, User: u, RedirectTo: fs.RedirectTo}, nil
	case !errors.Is(err, sdk.ErrNotFound):
		return OAuthResult{}, err
	}

	// Matching, adoption, and registration are permitted only when the provider
	// asserts the email as verified AND host policy trusts that evidence (design
	// §5.7): a provider email string without verified provenance never auto-matches,
	// registers, or adopts. Branch 1 (an existing link) is keyed on the provider user
	// id and already returned above, so it is unaffected by this gate.
	if !ident.EmailVerified || s.trustOAuthEmail == nil || !s.trustOAuthEmail(providerName, ident) {
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
	if pl.AuthRevision == nil {
		return OAuthResult{}, ErrInvalidOAuthState
	}
	u, err := s.users.Get(ctx, pl.Account.UserID)
	if err != nil {
		return OAuthResult{}, err
	}
	matched, err := s.identifiers.Get(ctx, pl.MatchedIdentifierID)
	if err != nil || matched.UserID != u.ID || !matched.Active() || (!matched.LoginEnabled && !matched.RecoveryEnabled) {
		return OAuthResult{}, ErrInvalidOAuthState
	}
	adoptID := ""
	if pl.UnverifiedAtStart {
		adoptID = pl.MatchedIdentifierID
	}
	created, revision, err := s.oauthAccounts.Link(ctx, pl.Account, *pl.AuthRevision, adoptID, s.now())
	if err != nil {
		return OAuthResult{}, err
	}
	u.AuthRevision = revision
	pair, err := s.mintSession(ctx, u.ID, revision, s.primaryAuthentication(session.MethodOAuth))
	if err != nil {
		return OAuthResult{}, genericIfNotActive(err, invalidCredentials())
	}
	if adoptID != "" {
		s.resolvePendingInvitations(ctx, matched.NormalizedValue, u.ID)
	}
	s.recordOAuth(ctx, created.UserID, created.Provider, securityevent.TypeOAuthLinkVerified)
	return OAuthResult{Action: ActionLinked, Token: pair.AccessToken, RefreshToken: pair.RefreshToken, User: u}, nil
}

// ListLinked returns every provider link owned by userID.
func (s *Service) ListLinked(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	return s.oauthAccounts.ListByUser(ctx, userID)
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
	if s.credentialMutations == nil {
		return StepUpReceipt{}, ErrCredentialMutationUnavailable
	}
	if s.challenges == nil || s.protector == nil {
		return StepUpReceipt{}, ErrStepUpUnavailable
	}
	if err := s.sensitiveCodeBudget(ctx, userID); err != nil {
		return StepUpReceipt{}, err
	}
	if err := s.requireLinked(ctx, userID, providerName); err != nil {
		return StepUpReceipt{}, err
	}
	proof, dest, err := s.credentialRemovalProof(ctx, userID, providerName)
	if err != nil {
		return StepUpReceipt{}, err
	}
	key, err := s.issueAndEnqueueCode(ctx, userID, challenge.PurposeUnlinkOAuth, string(dest.Kind), dest.NormalizedValue,
		withStoredContext(proof), withSubjectKey(grantContextDigest(userID+":unlink_oauth:"+providerName)))
	if err != nil {
		return StepUpReceipt{}, err
	}

	s.recordOAuth(ctx, userID, providerName, securityevent.TypeOAuthUnlinkCodeSent)
	return StepUpReceipt{Delivered: true, Receipt: key}, nil
}

// UnlinkOAuth completes a provider-bound OAuth unlink (design §5.4). Consuming the
// provider-bound unlink_oauth code proves the caller controls a verified recovery
// channel AND that the code was issued for THIS provider: a code minted to unlink a
// different provider is rejected as ErrChallengeInvalid without spending the matching code, never
// authorizing the wrong unlink. The credential policy then guards the proposed
// method set (§5.6), and the link is deleted (its encrypted provider tokens with it)
// and the user's auth_revision bumped atomically under revision-CAS. A concurrent
// credential or recovery-channel change requires a new code. An absent link → sdk.ErrNotFound; a removal that
// would leave no acceptable method is the policy's stable rejection
// (credential.ErrNoLoginMethod).
func (s *Service) UnlinkOAuth(ctx context.Context, userID, providerName, code string) error {
	if s.credentialMutations == nil {
		return ErrCredentialMutationUnavailable
	}
	if err := s.requireLinked(ctx, userID, providerName); err != nil {
		return err
	}
	proof, _, err := s.credentialRemovalProof(ctx, userID, providerName)
	if err != nil {
		return err
	}
	// The provider-bound unlink_oauth code is this flow's reauthentication proof; a
	// wrong, expired, locked-out, or wrong-provider code is the stable challenge error
	// (a wrong-provider code leaves the original provider's challenge intact).
	if _, err := s.consumeChallenge(ctx, userID, challenge.PurposeUnlinkOAuth, code,
		withExpectedContext(proof), withSubjectKey(grantContextDigest(userID+":unlink_oauth:"+providerName))); err != nil {
		return err
	}
	if err := s.applyCredentialMutationAtRevision(ctx, userID, proof.AuthRevision, credential.UnlinkOAuth{Provider: providerName}); err != nil {
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
func (s *Service) linkAccount(ctx context.Context, userID string, expectedAuthRevision int64, providerName string, ident providerIdentity, tok *oauth.TokenResponse) (oauthaccount.OAuthAccount, error) {
	acct, err := s.newAccount(userID, providerName, ident, tok)
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	linked, _, err := s.oauthAccounts.Link(ctx, acct, expectedAuthRevision, "", s.now())
	return linked, err
}

// registerAndLink atomically provisions the user, verified primary email and
// provider link before admitting a session. The caller has already established
// provider provenance under host policy. Verified ownership permits invitation
// resolution after the transaction succeeds.
func (s *Service) registerAndLink(ctx context.Context, providerName, normEmail string, ident providerIdentity, tok *oauth.TokenResponse, redirectTo string) (OAuthResult, error) {
	now := s.now()
	primary, err := identifier.New(s.ids, s.normalizer, "", identifier.KindEmail, ident.Email,
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true, now, now)
	if err != nil {
		return OAuthResult{}, err
	}
	u := user.NewUser(s.ids, ident.Name, now)
	account, err := s.newAccount(u.ID, providerName, ident, tok)
	if err != nil {
		return OAuthResult{}, err
	}
	created, createdIdent, err := s.users.Provision(ctx, u, primary, user.InitialCredentials{OAuth: &account})
	if err != nil {
		if errors.Is(err, sdk.ErrAlreadyExists) {
			// A simultaneous callback for the same provider identity may have committed.
			existing, lookupErr := s.oauthAccounts.GetByProvider(ctx, providerName, ident.ProviderUserID)
			if lookupErr == nil {
				pair, owner, loginErr := s.oauthSession(ctx, existing)
				if loginErr != nil {
					return OAuthResult{}, loginErr
				}
				return OAuthResult{Action: ActionLogin, Token: pair.AccessToken, RefreshToken: pair.RefreshToken, User: owner, RedirectTo: redirectTo}, nil
			}
		}
		return OAuthResult{}, err
	}
	// The account and its provider-VERIFIED primary email now exist and are linked, so
	// the invitee's pending auto-accept invitations resolve exactly as they do at
	// Verify — same best-effort port, same normalized stored identifier value, same
	// (user, id) subject. It runs BEFORE mintSession so the grants are effective before
	// the caller ever holds a session token, and it is best-effort by contract: a
	// failed or absent resolver never fails provisioning or the OAuth login.
	primaryEmail := createdIdent.NormalizedValue
	if primaryEmail == "" {
		primaryEmail = normEmail // a store that does not echo the identifier back
	}
	s.resolvePendingInvitations(ctx, primaryEmail, created.ID)
	pair, err := s.mintSession(ctx, created.ID, created.AuthRevision, s.primaryAuthentication(session.MethodOAuth))
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

// oauthSession binds the provider-link read to the user's credential revision.
func (s *Service) oauthSession(ctx context.Context, account oauthaccount.OAuthAccount) (TokenPair, user.User, error) {
	u, err := s.users.Get(ctx, account.UserID)
	if err != nil {
		return TokenPair{}, user.User{}, err
	}
	current, err := s.oauthAccounts.GetByProvider(ctx, account.Provider, account.ProviderUserID)
	if err != nil || current.UserID != u.ID {
		return TokenPair{}, user.User{}, invalidCredentials()
	}
	pair, err := s.mintSession(ctx, u.ID, u.AuthRevision, s.primaryAuthentication(session.MethodOAuth))
	if err != nil {
		return TokenPair{}, user.User{}, genericIfNotActive(err, invalidCredentials())
	}
	return pair, u, nil
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
	u, err := s.users.Get(ctx, matched.UserID)
	if err != nil {
		return err
	}
	if !u.Active() {
		return session.ErrUserNotActive
	}
	current, err := s.matchIdentifier(ctx, matched.NormalizedValue)
	if err != nil || current.ID != matched.ID || current.UserID != u.ID {
		return ErrInvalidOAuthState
	}
	matched = current
	acct, err := s.newAccount(matched.UserID, providerName, ident, tok)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(pendingLink{
		Account:             acct,
		MatchedIdentifierID: matched.ID,
		UnverifiedAtStart:   !matched.Verified(),
		AuthRevision:        &u.AuthRevision,
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
	issuance := sha256.Sum256([]byte(st.Token))
	key := s.idempotencyKey(sdk.AddressKindEmail, dest, delivery.PurposeOAuthPendingLink) + ":" + hex.EncodeToString(issuance[:])
	if err := s.enqueueRendered(ctx, delivery.PurposeOAuthPendingLink, key, delivery.Request{
		Kind:            sdk.AddressKindEmail,
		Purpose:         delivery.PurposeOAuthPendingLink,
		Destination:     dest,
		ResolutionInput: dest,
		Secret:          st.Token,
		Data:            map[string]any{"ProviderName": providerName, "Link": s.oauthLinkURL(st.Token)},
	}); err != nil {
		if _, cerr := s.oauthStates.Consume(ctx, st.Token); cerr != nil {
			s.logger.Warn("pending-link rollback failed", "error_kind", ErrorKind(cerr))
		}
		return err
	}
	return nil
}

// oauthLinkURL builds the anti-takeover pending-link confirmation URL from the
// configured absolute base only (oauth-pending-link plan D2), mirroring
// magicLinkURL: the request Host/forwarded headers never participate, so a hostile
// Host cannot redirect the link elsewhere — the target is exactly the host-
// configured base. The single-use token rides the URL fragment so it is not sent to
// the server on the landing-page GET (verify-link is POST-only) and can be scrubbed
// from browser history by the landing page. Any existing non-secret query on the
// base is preserved because the fragment is simply appended. It returns the empty
// string when either the base or the token is empty (the D5 unconfigured-base
// fallback), which the template reads as "no link" and renders the bare-token line
// instead. A non-empty base is validated as an absolute http(s) URL with no fragment
// (HTTPS in production) at construction (validateOAuthLinkBaseURL).
func (s *Service) oauthLinkURL(token string) string {
	if s.oauthLinkBase == "" || token == "" {
		return ""
	}
	base := strings.TrimRight(s.oauthLinkBase, "/")
	return base + "#token=" + url.QueryEscape(token)
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
	if err := tok.Validate(); err != nil {
		return providerIdentity{}, err
	}
	var info *oauth.UserInfo
	if validator, ok := p.(oauth.IDTokenValidator); ok {
		if nonce == "" || tok.IDToken == "" {
			return providerIdentity{}, fmt.Errorf("oauth: missing OIDC token or flow nonce")
		}
		claims, err := validator.ValidateIDToken(ctx, tok.IDToken, nonce)
		if err != nil {
			return providerIdentity{}, fmt.Errorf("validate id token: %w", err)
		}
		if claims == nil {
			return providerIdentity{}, fmt.Errorf("oauth: missing ID token claims")
		}
		info = &oauth.UserInfo{ProviderUserID: claims.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified,
			EmailAuthoritative: claims.EmailAuthoritative, Name: claims.Name, Picture: claims.Picture}
	} else {
		if nonce != "" || tok.IDToken != "" {
			return providerIdentity{}, fmt.Errorf("oauth: ID token validator required")
		}
		var err error
		info, err = p.GetUserInfo(ctx, tok.AccessToken)
		if err != nil {
			return providerIdentity{}, fmt.Errorf("get user info: %w", err)
		}
	}
	if err := info.Validate(); err != nil {
		return providerIdentity{}, err
	}
	return *info, nil
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
