package oauth2

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

type constructorRepository struct{ Repository }
type constructorUsers struct{ UserReader }
type constructorSessions struct{ SessionReader }
type constructorSigner struct{ cryptids.JWTSigner }
type constructorVerifier struct{ TokenVerifier }

type clientResolverFunc func(context.Context, string) (Client, error)

func (f clientResolverFunc) Resolve(ctx context.Context, id string) (Client, error) {
	return f(ctx, id)
}

func protocolConfig() Config {
	return Config{Issuer: "https://issuer.example", MCPResource: "https://mcp.example", APIResource: "https://api.example",
		ConfidentialClientID: "mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte("a high entropy confidential test secret"))}}
}

func constructorService(t *testing.T, cfg Config, mode environment.Mode, opts ...Option) (*Service, error) {
	t.Helper()
	return New(Repositories{OAuth2: &constructorRepository{}, Users: &constructorUsers{}, Sessions: &constructorSessions{}},
		&constructorSigner{}, clientResolverFunc(func(context.Context, string) (Client, error) { return Client{}, ErrInvalidClient }),
		&constructorVerifier{}, mode, cfg, opts...)
}

func TestConstructorRejectsUnsafeProtocolPostures(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"missing issuer":          func(c *Config) { c.Issuer = "" },
		"same audience":           func(c *Config) { c.APIResource = c.MCPResource },
		"insecure issuer":         func(c *Config) { c.Issuer = "http://issuer.example" },
		"userinfo":                func(c *Config) { c.Issuer = "https://user@issuer.example" },
		"fragment":                func(c *Config) { c.MCPResource += "#fragment" },
		"empty fragment":          func(c *Config) { c.Issuer += "#" },
		"ambiguous host":          func(c *Config) { c.Issuer = "https://ISSUER.example" },
		"missing secret":          func(c *Config) { c.ConfidentialSecretHashes = nil },
		"empty secret":            func(c *Config) { c.ConfidentialSecretHashes = [][32]byte{sha256.Sum256(nil)} },
		"long code":               func(c *Config) { c.CodeTTL = time.Hour },
		"long access":             func(c *Config) { c.AccessTTL = time.Hour },
		"negative lifetime":       func(c *Config) { c.SessionTTL = -time.Second },
		"production local option": func(c *Config) { c.AllowLocalHTTP = true },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := protocolConfig()
			change(&cfg)
			if _, err := constructorService(t, cfg, environment.ModeProduction); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("unsafe config accepted: %v", err)
			}
		})
	}
	cfg := protocolConfig()
	cfg.Issuer, cfg.MCPResource, cfg.APIResource, cfg.AllowLocalHTTP = "http://localhost:8082", "http://127.0.0.1:8083/mcp", "http://[::1]:8084/api", true
	if _, err := constructorService(t, cfg, environment.ModeDevelopment); err != nil {
		t.Fatalf("explicit loopback development: %v", err)
	}
	cfg.MCPResource = "http://private.example/mcp"
	if _, err := constructorService(t, cfg, environment.ModeDevelopment); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nonloopback insecure resource accepted: %v", err)
	}
	if _, err := constructorService(t, protocolConfig(), environment.ModeProduction, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil option: %v", err)
	}
	if _, err := constructorService(t, protocolConfig(), environment.ModeProduction, WithClock(nil)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil clock: %v", err)
	}
	var typedNil *constructorRepository
	if _, err := New(Repositories{OAuth2: typedNil, Users: &constructorUsers{}, Sessions: &constructorSessions{}}, &constructorSigner{}, clientResolverFunc(nil), &constructorVerifier{}, environment.ModeProduction, protocolConfig()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("typed nil dependency: %v", err)
	}
}

func TestConfidentialProofCannotCrossServicesOrMutateConfiguredSecrets(t *testing.T) {
	cfg := protocolConfig()
	a, err := constructorService(t, cfg, environment.ModeProduction)
	if err != nil {
		t.Fatal(err)
	}
	b, err := constructorService(t, cfg, environment.ModeProduction)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConfidentialSecretHashes[0] = sha256.Sum256([]byte("replacement"))
	proof, err := a.AuthenticateClientContext(t.Context(), "mcp", "a high entropy confidential test secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct{ id, secret string }{{"public", "a high entropy confidential test secret"}, {"mcp", "replacement"}, {"mcp", ""}} {
		if _, err := a.AuthenticateClientContext(t.Context(), bad.id, bad.secret); !errors.Is(err, ErrInvalidClient) {
			t.Fatalf("bad confidential credentials accepted: %v", err)
		}
	}
	if _, err := b.Introspect(t.Context(), proof, "token"); !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("cross-service proof accepted: %v", err)
	}
	if _, err := a.Exchange(t.Context(), ClientProof{}, ExchangeRequest{}); !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("zero proof accepted: %v", err)
	}
}

func TestAuthorizationValidationBeforeAnyIdentityLookup(t *testing.T) {
	svc, err := constructorService(t, protocolConfig(), environment.ModeProduction)
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	digest := sha256.Sum256([]byte(verifier))
	good := AuthorizationRequest{ResponseType: "code", ClientID: "https://client.example/metadata", RedirectURI: "https://client.example/callback", Resource: svc.config.MCPResource,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]), CodeChallengeMethod: "S256"}
	calls := 0
	svc.resolver = clientResolverFunc(func(_ context.Context, id string) (Client, error) {
		calls++
		return Client{ID: id, Name: "Client", RedirectURIs: []string{good.RedirectURI}, ExpiresAt: time.Time{}}, nil
	})
	// An authoritative no-cache metadata response may have no reusable lifetime.
	if _, err := svc.validateAuthorization(t.Context(), good); err != nil {
		t.Fatalf("fresh no-cache metadata rejected: %v", err)
	}
	for name, mutate := range map[string]func(*AuthorizationRequest){
		"plain PKCE":        func(r *AuthorizationRequest) { r.CodeChallengeMethod = "plain" },
		"implicit":          func(r *AuthorizationRequest) { r.ResponseType = "token" },
		"missing challenge": func(r *AuthorizationRequest) { r.CodeChallenge = "" },
		"padded challenge":  func(r *AuthorizationRequest) { r.CodeChallenge += "=" },
		"scope":             func(r *AuthorizationRequest) { r.Scope = "admin" },
		"API audience":      func(r *AuthorizationRequest) { r.Resource = svc.config.APIResource },
		"redirect suffix":   func(r *AuthorizationRequest) { r.RedirectURI += "?other=true" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := good
			mutate(&bad)
			if _, err := svc.PrepareAuthorization(t.Context(), bad, "u", "w"); err == nil {
				t.Fatal("bad request reached identity/store boundary")
			}
		})
	}
	if calls < 2 {
		t.Fatal("client metadata was not checked")
	}
	if !matchesPKCE(verifier, good.CodeChallenge) || matchesPKCE(strings.Repeat("b", 43), good.CodeChallenge) || matchesPKCE(strings.Repeat("a", 42), good.CodeChallenge) {
		t.Fatal("PKCE verifier validation")
	}
}

// TestFallbackRecorderMintsTheRecordDefault pins the OAuth2 fallback recorder:
// built with the zero generator when no WithSecurityEventRecorder is supplied,
// it still mints UUIDv7 event ids because NewRecorder resolves the record-class
// default itself.
func TestFallbackRecorderMintsTheRecordDefault(t *testing.T) {
	events := &auditEvents{}
	s := auditService(t, events)
	s.audit(context.Background(), TypeConsentApproved, "u-1", "sess-1", "https://client.example")
	if len(events.created) != 1 {
		t.Fatalf("recorded %d rows, want 1", len(events.created))
	}
	id := events.created[0].ID
	if len(id) != 36 || id[14] != '7' || strings.Trim(id, "0123456789abcdef-") != "" {
		t.Errorf("fallback event id %q is not a UUIDv7", id)
	}
}

// assuranceStore is one in-memory authorization-server store: sessions and
// codes. plainCodes is a store without migration 0030 — its plain methods never
// read or write a code's Authentication — and assuranceCodes adds
// CodeAuthenticationRepository over the same maps.
type assuranceStore struct {
	mu       sync.Mutex
	sessions map[string]session.Session
	codes    map[string]Code
	probeErr error
	// withCalls counts CreateCodeWithAuthentication/GetCodeWithAuthentication.
	withCalls int
}

type plainCodes struct {
	Repository
	st *assuranceStore
}

type assuranceCodes struct{ plainCodes }

var _ CodeAuthenticationRepository = assuranceCodes{}

func (plainCodes) PutClient(context.Context, Client) error { return nil }

func (r plainCodes) CreateCode(_ context.Context, c Code, _ string) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	c.Authentication = session.AuthenticationMetadata{}
	r.st.codes[c.Hash] = c
	return nil
}

func (r plainCodes) GetCode(_ context.Context, hash string) (Code, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	c, ok := r.st.codes[hash]
	if !ok {
		return Code{}, ErrInvalidGrant
	}
	c.Authentication = session.AuthenticationMetadata{}
	return c, nil
}

func (r plainCodes) RedeemCode(_ context.Context, expected Code, proposed session.Session, _ time.Time) (session.Session, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	if _, ok := r.st.codes[expected.Hash]; !ok {
		return session.Session{}, ErrInvalidGrant
	}
	delete(r.st.codes, expected.Hash)
	r.st.sessions[proposed.ID] = proposed
	return proposed, nil
}

func (r plainCodes) RotateRefresh(_ context.Context, in Refresh) (session.Session, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	for id, s := range r.st.sessions {
		if s.RefreshTokenHash == in.Hash && in.NewHash != "" {
			s.RefreshTokenHash = in.NewHash
			r.st.sessions[id] = s
			return s, nil
		}
	}
	return session.Session{}, ErrInvalidGrant
}

func (r assuranceCodes) CreateCodeWithAuthentication(_ context.Context, c Code, approvingSessionID string) error {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	r.st.withCalls++
	c.Authentication = r.st.sessions[approvingSessionID].Authentication
	r.st.codes[c.Hash] = c
	return nil
}

func (r assuranceCodes) GetCodeWithAuthentication(_ context.Context, hash string) (Code, error) {
	r.st.mu.Lock()
	defer r.st.mu.Unlock()
	r.st.withCalls++
	c, ok := r.st.codes[hash]
	if !ok {
		return Code{}, ErrInvalidGrant
	}
	return c, nil
}

func (r assuranceCodes) ProbeSchema(context.Context) error { return r.st.probeErr }

type assuranceSessions struct{ st *assuranceStore }

func (s assuranceSessions) Get(_ context.Context, id string) (session.Session, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	got, ok := s.st.sessions[id]
	if !ok {
		return session.Session{}, sdk.ErrNotFound
	}
	return got, nil
}

func (s assuranceSessions) GetByRefreshHash(_ context.Context, hash string) (session.Session, session.RefreshMatch, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	for _, got := range s.st.sessions {
		if got.RefreshTokenHash == hash {
			return got, session.RefreshMatchCurrent, nil
		}
	}
	return session.Session{}, session.RefreshMatchCurrent, sdk.ErrNotFound
}

type assuranceUsers struct{}

func (assuranceUsers) Get(_ context.Context, id string) (user.User, error) {
	return user.User{ID: id, DisplayName: "Owner", Status: user.StatusActive}, nil
}

// assuranceSigner keeps every signed claim set, with its expiry as exp, so a
// test reads exactly what a token carries; assuranceVerifier is the delegated
// profile read back from it.
type assuranceSigner struct {
	mu     sync.Mutex
	issued map[string]map[string]any
}

func (s *assuranceSigner) Sign(claims map[string]any, expires time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := fmt.Sprintf("signed-%d", len(s.issued))
	c := maps.Clone(claims)
	c["exp"] = expires
	s.issued[token] = c
	return token, nil
}

func (s *assuranceSigner) Verify(token string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claims, ok := s.issued[token]
	if !ok {
		return nil, errors.New("unknown token")
	}
	return maps.Clone(claims), nil
}

type assuranceVerifier struct{ signer *assuranceSigner }

func (v assuranceVerifier) VerifyOAuth2AccessToken(_ context.Context, raw string) (AccessToken, error) {
	c, err := v.signer.Verify(raw)
	if err != nil || c["session_profile"] != string(session.ProfileDelegated) {
		return AccessToken{}, sdk.ErrUnauthorized
	}
	str := func(k string) string { v, _ := c[k].(string); return v }
	token := AccessToken{UserID: str("user_id"), SessionID: str("session_id"), Issuer: str("iss"), ClientID: str("client_id"),
		OriginClientID: str("origin_client_id"), ExpiresAt: c["exp"].(time.Time)}
	if aud, ok := c["aud"].([]string); ok && len(aud) == 1 {
		token.Audience = aud[0]
	}
	if act, ok := c["act"].(map[string]any); ok {
		token.ActorID, _ = act["sub"].(string)
	}
	return token, nil
}

type assuranceHarness struct {
	st     *assuranceStore
	signer *assuranceSigner
	web    session.Session
}

// newAssuranceHarness seeds one first-party approving session recording
// auth (zero for a session that recorded nothing).
func newAssuranceHarness(auth session.AuthenticationMetadata) *assuranceHarness {
	web := session.Session{ID: "web-1", UserID: "u-1", Profile: session.ProfileFirstParty, ExpiresAt: time.Now().Add(time.Hour), Authentication: auth}
	return &assuranceHarness{st: &assuranceStore{sessions: map[string]session.Session{web.ID: web}, codes: map[string]Code{}},
		signer: &assuranceSigner{issued: map[string]map[string]any{}}, web: web}
}

func (h *assuranceHarness) service(t *testing.T, delegated, migrated bool) (*Service, error) {
	t.Helper()
	cfg := protocolConfig()
	cfg.DelegatedAssurance = delegated
	var repo Repository = plainCodes{st: h.st}
	if migrated {
		repo = assuranceCodes{plainCodes{st: h.st}}
	}
	resolver := clientResolverFunc(func(_ context.Context, id string) (Client, error) {
		return Client{ID: id, Name: "Client", RedirectURIs: []string{denialRedirectURI}}, nil
	})
	return New(Repositories{OAuth2: repo, Users: assuranceUsers{}, Sessions: assuranceSessions{h.st}},
		h.signer, resolver, assuranceVerifier{h.signer}, environment.ModeDevelopment, cfg)
}

func (h *assuranceHarness) mustService(t *testing.T, delegated, migrated bool) *Service {
	t.Helper()
	svc, err := h.service(t, delegated, migrated)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// connect approves and redeems one grant through svc.
func (h *assuranceHarness) connect(t *testing.T, svc *Service) TokenResponse {
	t.Helper()
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	req := AuthorizationRequest{ResponseType: "code", ClientID: denialClientID, RedirectURI: denialRedirectURI, Resource: svc.config.MCPResource,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]), CodeChallengeMethod: "S256"}
	prepared, err := svc.PrepareAuthorization(t.Context(), req, h.web.UserID, h.web.ID)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := svc.Approve(t.Context(), prepared.ConsentToken, h.web.UserID, h.web.ID)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := svc.RedeemCode(t.Context(), CodeRequest{Code: approved.Code, ClientID: denialClientID, RedirectURI: denialRedirectURI, Resource: svc.config.MCPResource, CodeVerifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}

func (h *assuranceHarness) claims(t *testing.T, raw string) (any, any) {
	t.Helper()
	c, err := h.signer.Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c["aal"], c["methods"]
}

func (h *assuranceHarness) delegatedSession(t *testing.T, raw string) session.Session {
	t.Helper()
	c, _ := h.signer.Verify(raw)
	h.st.mu.Lock()
	defer h.st.mu.Unlock()
	return h.st.sessions[c["session_id"].(string)]
}

func aal2Approval() session.AuthenticationMetadata {
	password, _ := session.DescribeMethod(session.MethodPassword)
	passkey, _ := session.DescribeMethod(session.MethodPasskey)
	return session.AuthenticationMetadata{AuthenticatedAt: time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond),
		Methods: []session.AuthenticationMethod{password, passkey, password}, Assurance: session.AssuranceAAL2}
}

// TestDelegatedAssuranceStampsEveryMint pins Config.DelegatedAssurance (S4d
// part 4): an AAL2 approval is copied into the code, persisted on the redeemed
// delegated session, and signed as aal and methods on the redeem, refresh and
// exchange tokens alike; Introspection reports the same; the level is fixed
// at approval — a later step-down of the browser session changes nothing.
func TestDelegatedAssuranceStampsEveryMint(t *testing.T) {
	h := newAssuranceHarness(aal2Approval())
	svc := h.mustService(t, true, true)
	redeemed := h.connect(t, svc)
	if got := h.delegatedSession(t, redeemed.AccessToken).Authentication; !reflect.DeepEqual(got, h.web.Authentication) {
		t.Fatalf("delegated session Authentication = %+v, want the approving session's %+v", got, h.web.Authentication)
	}
	h.st.mu.Lock()
	web := h.st.sessions[h.web.ID]
	web.Authentication = session.AuthenticationMetadata{}
	h.st.sessions[h.web.ID] = web
	h.st.mu.Unlock()

	refreshed, err := svc.Refresh(t.Context(), RefreshRequest{RefreshToken: redeemed.RefreshToken, ClientID: denialClientID, Resource: svc.config.MCPResource})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := svc.AuthenticateClientContext(t.Context(), "mcp", denialSecret)
	if err != nil {
		t.Fatal(err)
	}
	exchanged, err := svc.Exchange(t.Context(), proof, ExchangeRequest{SubjectToken: refreshed.AccessToken, SubjectTokenType: AccessTokenType, Resource: svc.config.APIResource})
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{"redeem": redeemed.AccessToken, "refresh": refreshed.AccessToken, "exchange": exchanged.AccessToken} {
		if aal, methods := h.claims(t, raw); aal != "aal2" || !reflect.DeepEqual(methods, []string{"password", "passkey"}) {
			t.Errorf("%s token aal=%v methods=%v, want aal2 [password passkey]", name, aal, methods)
		}
	}
	got, err := svc.Introspect(t.Context(), proof, refreshed.AccessToken)
	if err != nil || !got.Active || got.Assurance != "aal2" || !reflect.DeepEqual(got.Methods, []string{"password", "passkey"}) {
		t.Fatalf("Introspect = %+v %v, want aal2 [password passkey]", got, err)
	}
}

// TestDelegatedAssuranceOffSignsNothing pins the flag's off posture and R-2:
// with it off nothing is copied or signed even over a migrated store, whose
// WithAuthentication methods are never called; an approval recording no level
// or one no verifier admits (aal3) signs no claim; a grant
// approved before the flag carries no level after it is set; and turning the
// flag off stops a flag-on grant's new tokens carrying one (the rollback).
func TestDelegatedAssuranceOffSignsNothing(t *testing.T) {
	none := func(t *testing.T, h *assuranceHarness, name string, raws ...string) {
		t.Helper()
		for _, raw := range raws {
			if aal, methods := h.claims(t, raw); aal != nil || methods != nil {
				t.Errorf("%s: token carries aal=%v methods=%v, want neither", name, aal, methods)
			}
		}
	}
	refresh := func(t *testing.T, svc *Service, raw string) TokenResponse {
		t.Helper()
		out, err := svc.Refresh(t.Context(), RefreshRequest{RefreshToken: raw, ClientID: denialClientID, Resource: svc.config.MCPResource})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	for _, migrated := range []bool{false, true} {
		h := newAssuranceHarness(aal2Approval())
		off := h.mustService(t, false, migrated)
		tokens := h.connect(t, off)
		if got := h.delegatedSession(t, tokens.AccessToken).Authentication; got.Recorded() {
			t.Fatalf("migrated=%v: flag off copied the approval: %+v", migrated, got)
		}
		proof, _ := off.AuthenticateClientContext(t.Context(), "mcp", denialSecret)
		if got, _ := off.Introspect(t.Context(), proof, tokens.AccessToken); got.Assurance != "" || got.Methods != nil {
			t.Fatalf("migrated=%v: flag-off introspection = %+v", migrated, got)
		}
		none(t, h, fmt.Sprintf("off migrated=%v", migrated), tokens.AccessToken, refresh(t, off, tokens.RefreshToken).AccessToken)
		if h.st.withCalls != 0 {
			t.Fatalf("migrated=%v: flag off called the WithAuthentication methods %d times", migrated, h.st.withCalls)
		}
	}

	h := newAssuranceHarness(aal2Approval())
	preFlag := h.connect(t, h.mustService(t, false, true))
	none(t, h, "pre-flag grant", refresh(t, h.mustService(t, true, true), preFlag.RefreshToken).AccessToken)

	h = newAssuranceHarness(aal2Approval())
	flagOn := h.connect(t, h.mustService(t, true, true))
	if aal, _ := h.claims(t, flagOn.AccessToken); aal != "aal2" {
		t.Fatalf("flag-on grant aal = %v", aal)
	}
	none(t, h, "rollback", refresh(t, h.mustService(t, false, true), flagOn.RefreshToken).AccessToken)

	h = newAssuranceHarness(session.AuthenticationMetadata{})
	none(t, h, "approval recording nothing", h.connect(t, h.mustService(t, true, true)).AccessToken)

	unsignable := aal2Approval()
	unsignable.Assurance = session.AssuranceAAL3
	h = newAssuranceHarness(unsignable)
	none(t, h, "approval at a level no verifier admits", h.connect(t, h.mustService(t, true, true)).AccessToken)
}

// TestDelegatedAssuranceBootRefusals pins New under the flag: a store without
// CodeAuthenticationRepository, an unmigrated one, and a probe failure — which
// is sdk.ErrUnavailable naming its kind, never the store's text.
func TestDelegatedAssuranceBootRefusals(t *testing.T) {
	h := newAssuranceHarness(session.AuthenticationMetadata{})
	if _, err := h.service(t, true, false); !errors.Is(err, ErrDelegatedAssuranceRequires) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("unwired store = %v, want ErrDelegatedAssuranceRequires", err)
	}
	h.st.probeErr = fmt.Errorf("relation missing: %w", sdk.ErrNotFound)
	if _, err := h.service(t, true, true); !errors.Is(err, ErrDelegatedAssuranceSchemaMissing) || !strings.Contains(err.Error(), "0030") {
		t.Fatalf("unmigrated store = %v, want ErrDelegatedAssuranceSchemaMissing", err)
	}
	if _, err := h.service(t, false, true); err != nil {
		t.Fatalf("flag off over an unmigrated store = %v, want a boot (never probed)", err)
	}
	h.st.probeErr = errors.New("dial libsql://db.example.test?authToken=sekrit")
	_, err := h.service(t, true, true)
	if !errors.Is(err, sdk.ErrUnavailable) || strings.Contains(err.Error(), "sekrit") || strings.Contains(err.Error(), "libsql://") {
		t.Fatalf("probe failure = %v, want sdk.ErrUnavailable without the store's text", err)
	}
	h.st.probeErr = nil
	if _, err := h.service(t, true, true); err != nil {
		t.Fatalf("migrated store = %v", err)
	}
}
