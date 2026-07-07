package authsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/logic/oauthaccount"
	"github.com/gopernicus/gopernicus/features/auth/logic/oauthstate"
	"github.com/gopernicus/gopernicus/features/auth/logic/user"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/id"
	"github.com/gopernicus/gopernicus/sdk/oauth"
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

// ErrLastAuthMethod is returned by Unlink when the target link is the user's
// only authentication method and no password is set — removing it would lock the
// account out. It wraps errs.ErrConflict (→ 409). Checked with errors.Is.
var ErrLastAuthMethod = fmt.Errorf("cannot unlink the only authentication method: %w", errs.ErrConflict)

// ErrInvalidOAuthState is returned when a consumed state does not match the
// provider/purpose it is being redeemed for (a tampered or misrouted callback).
// It wraps errs.ErrNotFound so it maps to 404 and leaks nothing. Checked with
// errors.Is.
var ErrInvalidOAuthState = fmt.Errorf("invalid oauth state: %w", errs.ErrNotFound)

// OAuthResult is the outcome of a processed OAuth callback or verify-link.
type OAuthResult struct {
	Action     string    // one of ActionLogin/ActionRegister/ActionPendingLink/ActionLinked
	Token      string    // plaintext session cookie value; empty for ActionPendingLink
	User       user.User // the resolved user (zero for a bare pending-link start)
	RedirectTo string    // the validated post-flow destination
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

// OAuthEnabled reports whether any provider is wired. The transport registers
// the OAuth routes only when it is true (deny-by-absence, design §3).
func (s *Service) OAuthEnabled() bool { return len(s.providers) > 0 }

// StartOAuth begins a login/register authorization round-trip for providerName,
// returning the provider authorization URL to redirect the browser to. It
// persists server-side flow state (PKCE verifier, OIDC nonce, validated redirect
// target). An unknown provider → errs.ErrNotFound.
func (s *Service) StartOAuth(ctx context.Context, providerName, redirectTo string) (string, error) {
	return s.start(ctx, providerName, redirectTo, "")
}

// StartLink begins a session-gated link round-trip: the resulting link attaches
// the provider identity to userID (rather than logging in or registering).
func (s *Service) StartLink(ctx context.Context, userID, providerName, redirectTo string) (string, error) {
	return s.start(ctx, providerName, redirectTo, userID)
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
// state surfaces errs.ErrNotFound / errs.ErrExpired.
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
		return OAuthResult{Action: ActionLinked, User: u, RedirectTo: fs.RedirectTo}, nil
	}

	// Branch 1: the provider identity is already linked → login.
	existing, err := s.oauthAccounts.GetByProvider(ctx, providerName, ident.ProviderUserID)
	switch {
	case err == nil:
		token, _, err := s.mintSession(ctx, existing.UserID)
		if err != nil {
			return OAuthResult{}, err
		}
		u, err := s.users.Get(ctx, existing.UserID)
		if err != nil {
			return OAuthResult{}, err
		}
		return OAuthResult{Action: ActionLogin, Token: token, User: u, RedirectTo: fs.RedirectTo}, nil
	case !errors.Is(err, errs.ErrNotFound):
		return OAuthResult{}, err
	}

	// Branch 2: a user with the provider's email exists but is not linked →
	// pending link (single-use secret mailed; completes only via VerifyLink).
	if normEmail, nerr := user.NormalizeEmail(ident.Email); nerr == nil {
		u, err := s.users.GetByEmail(ctx, normEmail)
		switch {
		case err == nil:
			if err := s.startPendingLink(ctx, u, providerName, ident, tok); err != nil {
				return OAuthResult{}, err
			}
			return OAuthResult{Action: ActionPendingLink, User: u, RedirectTo: fs.RedirectTo}, nil
		case !errors.Is(err, errs.ErrNotFound):
			return OAuthResult{}, err
		}
	}

	// Branch 3: no user → register + link.
	return s.registerAndLink(ctx, p, providerName, ident, tok, fs.RedirectTo)
}

// VerifyLink completes a pending link: it consumes the pending-link secret
// (single use), creates the link stored in its payload, and mints a session for
// the now-linked user. An expired/unknown/already-used token surfaces
// errs.ErrExpired / errs.ErrNotFound.
func (s *Service) VerifyLink(ctx context.Context, token string) (OAuthResult, error) {
	st, err := s.oauthStates.Consume(ctx, token)
	if err != nil {
		return OAuthResult{}, err
	}
	if st.Purpose != oauthstate.PurposePendingLink {
		return OAuthResult{}, ErrInvalidOAuthState
	}
	var acct oauthaccount.OAuthAccount
	if err := json.Unmarshal(st.Payload, &acct); err != nil {
		return OAuthResult{}, fmt.Errorf("decode pending link: %w", err)
	}
	created, err := s.oauthAccounts.Create(ctx, acct)
	if err != nil {
		return OAuthResult{}, err
	}
	sessionToken, _, err := s.mintSession(ctx, created.UserID)
	if err != nil {
		return OAuthResult{}, err
	}
	u, err := s.users.Get(ctx, created.UserID)
	if err != nil {
		return OAuthResult{}, err
	}
	return OAuthResult{Action: ActionLinked, Token: sessionToken, User: u}, nil
}

// ListLinked returns every provider link owned by userID.
func (s *Service) ListLinked(ctx context.Context, userID string) ([]oauthaccount.OAuthAccount, error) {
	return s.oauthAccounts.ListByUser(ctx, userID)
}

// Unlink removes userID's link to providerName, enforcing last-authentication-
// method protection: it refuses (ErrLastAuthMethod) when the link is the only
// credential and no password is set. An absent link → errs.ErrNotFound.
func (s *Service) Unlink(ctx context.Context, userID, providerName string) error {
	linked, err := s.oauthAccounts.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	found := false
	for _, a := range linked {
		if a.Provider == providerName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no %s link for user: %w", providerName, errs.ErrNotFound)
	}
	if len(linked) == 1 {
		if _, err := s.passwords.Get(ctx, userID); err != nil {
			if errors.Is(err, errs.ErrNotFound) {
				return ErrLastAuthMethod
			}
			return err
		}
	}
	return s.oauthAccounts.Delete(ctx, userID, providerName)
}

// linkAccount builds and persists a link, propagating a duplicate-identity
// collision (errs.ErrAlreadyExists) from the store.
func (s *Service) linkAccount(ctx context.Context, userID, providerName string, ident providerIdentity, tok *oauth.TokenResponse) (oauthaccount.OAuthAccount, error) {
	acct, err := s.newAccount(userID, providerName, ident, tok)
	if err != nil {
		return oauthaccount.OAuthAccount{}, err
	}
	return s.oauthAccounts.Create(ctx, acct)
}

// registerAndLink creates a password-less user for a first-seen provider
// identity, links it, and mints a session. TrustEmailVerification() gates whether
// the provider's email-verified claim marks the new user verified.
func (s *Service) registerAndLink(ctx context.Context, p oauth.Provider, providerName string, ident providerIdentity, tok *oauth.TokenResponse, redirectTo string) (OAuthResult, error) {
	now := s.now()
	u, err := user.NewUser(ident.Email, "", now)
	if err != nil {
		return OAuthResult{}, err
	}
	if p.TrustEmailVerification() && ident.EmailVerified {
		u.MarkVerified(now)
	}
	created, err := s.users.Create(ctx, u)
	if err != nil {
		return OAuthResult{}, err
	}
	if _, err := s.linkAccount(ctx, created.ID, providerName, ident, tok); err != nil {
		return OAuthResult{}, err
	}
	token, _, err := s.mintSession(ctx, created.ID)
	if err != nil {
		return OAuthResult{}, err
	}
	return OAuthResult{Action: ActionRegister, Token: token, User: created, RedirectTo: redirectTo}, nil
}

// startPendingLink stores the would-be link as a single-use pending-link state
// and mails its secret. The link is created only when VerifyLink redeems it.
func (s *Service) startPendingLink(ctx context.Context, u user.User, providerName string, ident providerIdentity, tok *oauth.TokenResponse) error {
	acct, err := s.newAccount(u.ID, providerName, ident, tok)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(acct)
	if err != nil {
		return err
	}
	st := oauthstate.New(providerName, oauthstate.PurposePendingLink, payload, pendingLinkTTL, s.now())
	if _, err := s.oauthStates.Create(ctx, st); err != nil {
		return err
	}
	return s.sendPendingLinkEmail(ctx, u, providerName, st.Token)
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

// provider returns the wired provider by name, or errs.ErrNotFound.
func (s *Service) provider(name string) (oauth.Provider, error) {
	p, ok := s.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown oauth provider %q: %w", name, errs.ErrNotFound)
	}
	return p, nil
}

// callbackURL builds the absolute redirect URI for providerName from the
// configured base (design §3's OAuthCallbackBase).
func (s *Service) callbackURL(providerName string) string {
	return s.callbackBase + "/auth/oauth/" + providerName + "/callback"
}

func (s *Service) sendPendingLinkEmail(ctx context.Context, u user.User, providerName, token string) error {
	msg := email.Message{
		From:    s.mailFrom,
		To:      []string{u.Email},
		Subject: "Confirm linking your " + providerName + " account",
		Text:    "To finish linking your " + providerName + " account, confirm with this token: " + token,
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("send pending-link email: %w", err)
	}
	return nil
}

// pkceVerifier returns a high-entropy PKCE code verifier from the unreserved
// character set (sdk/id's base32 alphabet), long enough for RFC 7636.
func pkceVerifier() string { return newSecret() + newSecret() }

// newSecret returns an opaque high-entropy value (sdk/id) for a nonce or PKCE
// segment.
func newSecret() string { return id.New() }
