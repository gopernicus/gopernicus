package oauth2

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const (
	// claimAssurance and claimMethods are the delegated access token's
	// authentication claims — the first-party names, read by the same verifier.
	claimAssurance = "aal"
	claimMethods   = "methods"
	// delegatedAssuranceProbeTimeout bounds New's migration-0030 probe.
	delegatedAssuranceProbeTimeout = 5 * time.Second
)

type Service struct {
	repos    Repositories
	signer   cryptids.JWTSigner
	resolver ClientResolver
	verifier TokenVerifier
	config   Config
	now      func() time.Time
	logger   *slog.Logger
	events   *securityevent.Recorder
	// codeAuth is Repositories.OAuth2 as a CodeAuthenticationRepository, set
	// only under Config.DelegatedAssurance.
	codeAuth CodeAuthenticationRepository
}

func New(repos Repositories, signer cryptids.JWTSigner, resolver ClientResolver, verifier TokenVerifier, mode environment.Mode, cfg Config, opts ...Option) (*Service, error) {
	if err := environment.ValidateMode(mode); err != nil {
		return nil, err
	}
	for _, d := range []struct {
		name  string
		value any
	}{
		{"OAuth2", repos.OAuth2}, {"Users", repos.Users}, {"Sessions", repos.Sessions}, {"Signer", signer}, {"ClientResolver", resolver}, {"TokenVerifier", verifier},
	} {
		if nilDependency(d.value) {
			return nil, fmt.Errorf("oauth2: %s is required: %w", d.name, sdk.ErrInvalidInput)
		}
	}
	o := serviceOptions{now: time.Now, logger: slog.Default()}
	for _, option := range opts {
		if option == nil {
			return nil, fmt.Errorf("oauth2: nil option: %w", sdk.ErrInvalidInput)
		}
		option(&o)
	}
	if o.now == nil {
		return nil, fmt.Errorf("oauth2: clock is required: %w", sdk.ErrInvalidInput)
	}
	if o.logger == nil || (repos.SecurityEvents != nil && nilDependency(repos.SecurityEvents)) {
		return nil, fmt.Errorf("oauth2: nil logger or typed-nil audit repository: %w", sdk.ErrInvalidInput)
	}
	if cfg.AllowLocalHTTP && mode != environment.ModeDevelopment {
		return nil, fmt.Errorf("oauth2: local HTTP requires development mode: %w", sdk.ErrInvalidInput)
	}
	for _, raw := range []string{cfg.Issuer, cfg.MCPResource, cfg.APIResource} {
		if !validEndpoint(raw, cfg.AllowLocalHTTP, false) {
			return nil, fmt.Errorf("oauth2: invalid canonical issuer/resource URL: %w", sdk.ErrInvalidInput)
		}
	}
	if cfg.MCPResource == cfg.APIResource || strings.TrimSpace(cfg.ConfidentialClientID) != cfg.ConfidentialClientID || cfg.ConfidentialClientID == "" || len(cfg.ConfidentialSecretHashes) == 0 {
		return nil, fmt.Errorf("oauth2: distinct resources and confidential client credentials required: %w", sdk.ErrInvalidInput)
	}
	for _, h := range cfg.ConfidentialSecretHashes {
		if h == ([32]byte{}) || h == sha256.Sum256(nil) {
			return nil, fmt.Errorf("oauth2: empty confidential secret: %w", sdk.ErrInvalidInput)
		}
	}
	for _, ttl := range []struct {
		value             *time.Duration
		fallback, maximum time.Duration
	}{
		{&cfg.AccessTTL, 5 * time.Minute, 15 * time.Minute}, {&cfg.SessionTTL, 30 * 24 * time.Hour, 90 * 24 * time.Hour},
		{&cfg.CodeTTL, time.Minute, 5 * time.Minute}, {&cfg.ConsentTTL, 5 * time.Minute, 15 * time.Minute},
	} {
		if *ttl.value == 0 {
			*ttl.value = ttl.fallback
		}
		if *ttl.value < time.Second || *ttl.value > ttl.maximum {
			return nil, fmt.Errorf("oauth2: lifetime outside supported bounds: %w", sdk.ErrInvalidInput)
		}
	}
	if cfg.AccessTTL > cfg.SessionTTL {
		return nil, fmt.Errorf("oauth2: access lifetime exceeds session lifetime: %w", sdk.ErrInvalidInput)
	}
	cfg.ConfidentialSecretHashes = slices.Clone(cfg.ConfidentialSecretHashes)
	codeAuth, err := delegatedAssurance(repos, cfg)
	if err != nil {
		return nil, err
	}
	events := o.recorder
	if events == nil {
		// The zero generator selects the record-class default (UUIDv7) inside
		// NewRecorder; a host wanting its own ids supplies WithSecurityEventRecorder.
		events = securityevent.NewRecorder(repos.SecurityEvents, sdk.IDGenerator{}, o.now, o.logger)
	}
	return &Service{repos: repos, signer: signer, resolver: resolver, verifier: verifier, config: cfg, now: o.now, logger: o.logger, events: events, codeAuth: codeAuth}, nil
}

// delegatedAssurance checks a set Config.DelegatedAssurance against the wiring:
// Repositories.OAuth2 must implement CodeAuthenticationRepository, whose schema
// probe runs under a bounded deadline. Only a probe reporting sdk.ErrNotFound
// means migration 0030 is absent; any other failure is sdk.ErrUnavailable,
// naming the error kind only, never the store's text. Without the flag nothing
// here runs, so an unmigrated store boots.
func delegatedAssurance(repos Repositories, cfg Config) (CodeAuthenticationRepository, error) {
	if !cfg.DelegatedAssurance {
		return nil, nil
	}
	codeAuth, ok := repos.OAuth2.(CodeAuthenticationRepository)
	if !ok {
		return nil, ErrDelegatedAssuranceRequires
	}
	ctx, cancel := context.WithTimeout(context.Background(), delegatedAssuranceProbeTimeout)
	defer cancel()
	if err := codeAuth.ProbeSchema(ctx); err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return nil, ErrDelegatedAssuranceSchemaMissing
		}
		return nil, fmt.Errorf("oauth2: probing the delegated-assurance schema (%s): %w", probeErrorKind(err), sdk.ErrUnavailable)
	}
	return codeAuth, nil
}

// probeErrorKind names a probe failure without its text, which a driver may
// fill with a DSN or database URL.
func probeErrorKind(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, sdk.ErrUnavailable):
		return "unavailable"
	}
	return "store_error"
}

func (s *Service) Metadata() Metadata {
	return Metadata{Issuer: s.config.Issuer, MCPResource: s.config.MCPResource, APIResource: s.config.APIResource, ConfidentialClientID: s.config.ConfidentialClientID}
}

// ClientDisplayName reads an informational snapshot only. It never authorizes
// a request; every credential operation still calls the current trust resolver.
func (s *Service) ClientDisplayName(ctx context.Context, id string) (string, error) {
	client, err := s.repos.OAuth2.GetClient(ctx, id)
	if errors.Is(err, sdk.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return client.Name, nil
}

// AuthenticateClient authenticates the confidential client.
//
// Deprecated: use AuthenticateClientContext. This path records a mismatch as an
// oauth2_grant_failed row with no request id, IP or User-Agent, counted in one
// shared failure-row bucket; it is removed in the core MINOR after
// `stores/turso` repins past v0.21.0.
func (s *Service) AuthenticateClient(clientID, secret string) (ClientProof, error) {
	return s.AuthenticateClientContext(context.Background(), clientID, secret)
}

// AuthenticateClientContext authenticates the confidential client by its id
// and secret. A mismatch records an oauth2_grant_failed row with reason
// invalid_client, attributed to the request in ctx and Budgeted. The row names
// client_id only when the presented id is the configured confidential client:
// any other id is caller-chosen text, not a client this server knows.
func (s *Service) AuthenticateClientContext(ctx context.Context, clientID, secret string) (ClientProof, error) {
	presented := sha256.Sum256([]byte(secret))
	matched := 0
	for _, allowed := range s.config.ConfidentialSecretHashes {
		matched |= subtle.ConstantTimeCompare(presented[:], allowed[:])
	}
	if clientID != s.config.ConfidentialClientID || secret == "" || matched != 1 {
		attributed := ""
		if clientID == s.config.ConfidentialClientID {
			attributed = clientID
		}
		s.auditGrantFailure(ctx, attributed, ErrInvalidClient)
		return ClientProof{}, ErrInvalidClient
	}
	return ClientProof{service: s, clientID: clientID}, nil
}

func (s *Service) validProof(proof ClientProof) bool {
	return proof.service == s && proof.clientID == s.config.ConfidentialClientID
}

func (s *Service) resolveClient(ctx context.Context, id string) (Client, error) {
	if !validEndpoint(id, s.config.AllowLocalHTTP, true) || id == s.config.ConfidentialClientID {
		return Client{}, ErrInvalidClient
	}
	client, err := s.resolver.Resolve(ctx, id)
	if err != nil {
		return Client{}, err
	}
	if client.ID != id || len(client.RedirectURIs) == 0 || len(client.RedirectURIs) > 32 {
		return Client{}, ErrInvalidClient
	}
	for _, redirect := range client.RedirectURIs {
		if !validEndpoint(redirect, s.config.AllowLocalHTTP, true) {
			return Client{}, ErrInvalidClient
		}
		u, _ := url.Parse(redirect)
		values, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return Client{}, ErrInvalidClient
		}
		for _, reserved := range []string{"code", "state", "error", "error_description", "error_uri", "iss"} {
			if values.Has(reserved) {
				return Client{}, ErrInvalidClient
			}
		}
	}
	client.RedirectURIs = slices.Clone(client.RedirectURIs)
	return client, nil
}

func validEndpoint(raw string, localHTTP, query bool) bool {
	u, err := url.Parse(raw)
	if err != nil || raw == "" || len(raw) > 2048 || strings.TrimSpace(raw) != raw || u.Host == "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" || u.RawPath != "" || (!query && (u.RawQuery != "" || u.ForceQuery)) || u.Host != strings.ToLower(u.Host) {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if !localHTTP || u.Scheme != "http" {
		return false
	}
	if u.Hostname() == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err == nil && ip.IsLoopback()
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func hashSecret(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func invalidGrantError(err error) error {
	if errors.Is(err, sdk.ErrNotFound) || errors.Is(err, sdk.ErrExpired) || errors.Is(err, session.ErrUserNotActive) || errors.Is(err, sdk.ErrUnauthorized) {
		return ErrInvalidGrant
	}
	return err
}

func (s *Service) signAccess(sess session.Session, exchanged bool, limit time.Time) (TokenResponse, error) {
	now := s.now().UTC()
	expires := now.Add(s.config.AccessTTL)
	if sess.ExpiresAt.Before(expires) {
		expires = sess.ExpiresAt
	}
	if !limit.IsZero() && limit.Before(expires) {
		expires = limit
	}
	if expires.Sub(now) < time.Second {
		return TokenResponse{}, ErrInvalidGrant
	}
	audience, clientID := sess.Delegation.Resource, sess.Delegation.ClientID
	claims := map[string]any{
		"user_id": sess.UserID, "sub": "user:" + sess.UserID, "session_id": sess.ID,
		"session_profile": string(session.ProfileDelegated), "iss": s.config.Issuer,
		"jti": session.NewRefreshToken(),
	}
	if exchanged {
		audience, clientID = sess.Delegation.ExchangeResource, sess.Delegation.ExchangeClientID
		claims["origin_client_id"] = sess.Delegation.ClientID
		claims["act"] = map[string]any{"sub": clientID}
	}
	claims["aud"] = []string{audience}
	claims["client_id"] = clientID
	if level, kinds, ok := s.recordedAssurance(sess); ok {
		claims[claimAssurance] = level
		if len(kinds) > 0 {
			claims[claimMethods] = kinds
		}
	}
	raw, err := s.signer.Sign(claims, expires)
	if err != nil {
		return TokenResponse{}, err
	}
	response := TokenResponse{AccessToken: raw, TokenType: "Bearer", ExpiresIn: int64(expires.Sub(now) / time.Second)}
	if exchanged {
		response.IssuedTokenType = AccessTokenType
	}
	return response, nil
}

// recordedAssurance returns the delegated session's recorded level (aal1 or
// aal2) and method kinds, each once in order. It reports false with the flag
// off — so turning Config.DelegatedAssurance off stops new tokens carrying a
// level — and for a session that recorded none or any other level.
func (s *Service) recordedAssurance(sess session.Session) (string, []string, bool) {
	auth := sess.Authentication
	if !s.config.DelegatedAssurance || !auth.Recorded() {
		return "", nil, false
	}
	switch auth.Assurance {
	case session.AssuranceAAL1, session.AssuranceAAL2:
	default:
		return "", nil, false
	}
	var kinds []string
	for _, m := range auth.Methods {
		if m.Kind != "" && !slices.Contains(kinds, string(m.Kind)) {
			kinds = append(kinds, string(m.Kind))
		}
	}
	return string(auth.Assurance), kinds, true
}

func (s *Service) validDelegation(d session.Delegation) bool {
	return d.Issuer == s.config.Issuer && d.Resource == s.config.MCPResource && d.ExchangeResource == s.config.APIResource &&
		d.ExchangeClientID == s.config.ConfidentialClientID && d.ClientID != "" && d.ClientID != d.ExchangeClientID && d.AuthRevision >= 0
}

func (s *Service) liveAccess(ctx context.Context, raw string) (AccessToken, session.Session, error) {
	token, err := s.verifier.VerifyOAuth2AccessToken(ctx, raw)
	if err != nil {
		return AccessToken{}, session.Session{}, invalidGrantError(err)
	}
	if token.UserID == "" || token.SessionID == "" || token.Issuer != s.config.Issuer || !s.now().Before(token.ExpiresAt) {
		return AccessToken{}, session.Session{}, ErrInvalidGrant
	}
	sess, err := s.repos.Sessions.Get(ctx, token.SessionID)
	if err != nil {
		return AccessToken{}, session.Session{}, invalidGrantError(err)
	}
	if sess.ID != token.SessionID || sess.UserID != token.UserID || sess.Profile != session.ProfileDelegated || sess.Expired(s.now()) || token.ExpiresAt.After(sess.ExpiresAt) || !s.validDelegation(sess.Delegation) {
		return AccessToken{}, session.Session{}, ErrInvalidGrant
	}
	d := sess.Delegation
	direct := token.Audience == d.Resource && token.ClientID == d.ClientID && token.OriginClientID == "" && token.ActorID == ""
	exchanged := token.Audience == d.ExchangeResource && token.ClientID == d.ExchangeClientID && token.OriginClientID == d.ClientID && token.ActorID == d.ExchangeClientID
	if !direct && !exchanged {
		return AccessToken{}, session.Session{}, ErrInvalidGrant
	}
	owner, err := s.repos.Users.Get(ctx, token.UserID)
	if err != nil {
		return AccessToken{}, session.Session{}, invalidGrantError(err)
	}
	if owner.ID != token.UserID || !owner.Active() {
		return AccessToken{}, session.Session{}, ErrInvalidGrant
	}
	if _, err := s.resolveClient(ctx, d.ClientID); err != nil {
		return AccessToken{}, session.Session{}, err
	}
	return token, sess, nil
}
