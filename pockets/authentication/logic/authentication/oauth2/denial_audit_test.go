package oauth2

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

const (
	denialClientID    = "https://client.example/metadata"
	denialRedirectURI = "https://client.example/callback"
	denialSecret      = "a high entropy confidential test secret"
)

// denialSigner keeps signed claims in memory; Verify accepts only what it signed.
type denialSigner struct {
	mu     sync.Mutex
	issued map[string]map[string]any
}

func (s *denialSigner) Sign(claims map[string]any, _ time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.issued == nil {
		s.issued = map[string]map[string]any{}
	}
	token := fmt.Sprintf("signed-%d", len(s.issued))
	s.issued[token] = maps.Clone(claims)
	return token, nil
}

func (s *denialSigner) Verify(token string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claims, ok := s.issued[token]
	if !ok {
		return nil, errors.New("unknown token")
	}
	return maps.Clone(claims), nil
}

type denialUsers struct{}

func (denialUsers) Get(_ context.Context, id string) (user.User, error) {
	return user.User{ID: id, DisplayName: "Owner"}, nil
}

type denialSessions struct{}

func (denialSessions) Get(_ context.Context, id string) (session.Session, error) {
	return session.Session{ID: id, UserID: "u-1", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (denialSessions) GetByRefreshHash(context.Context, string) (session.Session, session.RefreshMatch, error) {
	return session.Session{}, session.RefreshMatchCurrent, sdk.ErrNotFound
}

// denialRepository refuses every code and rotates nothing: rotateErr is what a
// spent or unknown refresh credential reports.
type denialRepository struct {
	Repository
	rotateErr error
}

func (denialRepository) PutClient(context.Context, Client) error { return nil }

func (denialRepository) GetCode(context.Context, string) (Code, error) {
	return Code{}, ErrInvalidGrant
}

func (r denialRepository) RotateRefresh(context.Context, Refresh) (session.Session, error) {
	return session.Session{}, r.rotateErr
}

type denialVerifier struct{}

func (denialVerifier) VerifyOAuth2AccessToken(context.Context, string) (AccessToken, error) {
	return AccessToken{}, sdk.ErrUnauthorized
}

// denialService builds a Service over repo whose recorder attributes every row
// to 198.51.100.7 / agent/2; opts follow that reader, so a later one wins.
func denialService(t *testing.T, repo Repository, opts ...securityevent.RecorderOption) (*Service, *auditEvents) {
	t.Helper()
	events := &auditEvents{}
	opts = append([]securityevent.RecorderOption{
		securityevent.WithClientInfoReader(func(context.Context) (string, string) { return "198.51.100.7", "agent/2" }),
	}, opts...)
	rec := securityevent.NewRecorder(events, sdk.NewIDGenerator(func() (string, error) { return "host-id", nil }), nil, nil, opts...)
	resolver := clientResolverFunc(func(_ context.Context, id string) (Client, error) {
		if id != denialClientID {
			return Client{}, ErrInvalidClient
		}
		return Client{ID: id, Name: "Client", RedirectURIs: []string{denialRedirectURI}}, nil
	})
	cfg := protocolConfig()
	cfg.ConfidentialSecretHashes = [][32]byte{sha256.Sum256([]byte(denialSecret))}
	s, err := New(Repositories{OAuth2: repo, Users: denialUsers{}, Sessions: denialSessions{}},
		&denialSigner{}, resolver, denialVerifier{}, environment.ModeDevelopment, cfg, WithSecurityEventRecorder(rec))
	if err != nil {
		t.Fatal(err)
	}
	return s, events
}

// requireGrantFailure asserts the only recorded row is an oauth2_grant_failed
// failure for clientID with reason, attributed through the host recorder. An
// empty clientID asserts the row has no client_id key.
func requireGrantFailure(t *testing.T, events *auditEvents, clientID, reason string, secrets ...string) {
	t.Helper()
	if len(events.created) != 1 {
		t.Fatalf("recorded %d rows, want one oauth2_grant_failed: %+v", len(events.created), events.created)
	}
	evt := events.created[0]
	got, present := evt.Details["client_id"]
	if evt.EventType != TypeGrantFailed || evt.EventStatus != securityevent.StatusFailure || evt.Details["reason"] != reason ||
		present != (clientID != "") || (present && got != clientID) {
		t.Fatalf("row = %+v, want oauth2_grant_failed failure client_id=%q reason=%s", evt, clientID, reason)
	}
	if evt.ID != "host-id" || evt.IPAddress != "198.51.100.7" || evt.UserAgent != "agent/2" {
		t.Fatalf("row id/ip/ua = %q/%q/%q, want the host recorder's", evt.ID, evt.IPAddress, evt.UserAgent)
	}
	for key, value := range evt.Details {
		for _, secret := range secrets {
			if secret != "" && strings.Contains(fmt.Sprint(value), secret) {
				t.Fatalf("Details[%q] carries an attempted secret", key)
			}
		}
	}
}

func denialVerifierString() string { return strings.Repeat("v", 43) }

func TestOAuth2DenyRecordsConsentDenied(t *testing.T) {
	s, events := denialService(t, denialRepository{})
	digest := sha256.Sum256([]byte(denialVerifierString()))
	req := AuthorizationRequest{ResponseType: "code", ClientID: denialClientID, RedirectURI: denialRedirectURI, Resource: s.config.MCPResource,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(digest[:]), CodeChallengeMethod: "S256", State: "client-state-0451"}
	auth, err := s.PrepareAuthorization(t.Context(), req, "u-1", "web-1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Deny(t.Context(), auth.ConsentToken, "u-1", "web-1")
	if err != nil || result.Error != "access_denied" {
		t.Fatalf("Deny = %+v, %v", result, err)
	}
	if len(events.created) != 1 {
		t.Fatalf("recorded %d rows, want one oauth2_consent_denied", len(events.created))
	}
	evt := events.created[0]
	if evt.EventType != TypeConsentDenied || evt.EventStatus != securityevent.StatusSuccess || evt.UserID != "u-1" ||
		evt.Details["client_id"] != denialClientID || evt.Details["session_id"] != "web-1" || len(evt.Details) != 2 {
		t.Fatalf("row = %+v", evt)
	}
	if evt.ID != "host-id" || evt.IPAddress != "198.51.100.7" || evt.UserAgent != "agent/2" {
		t.Fatalf("row id/ip/ua = %q/%q/%q, want the host recorder's", evt.ID, evt.IPAddress, evt.UserAgent)
	}
	for key, value := range evt.Details {
		if text := fmt.Sprint(value); strings.Contains(text, auth.ConsentToken) || strings.Contains(text, "client-state-0451") {
			t.Fatalf("Details[%q] carries the consent token or state", key)
		}
	}
}

func TestOAuth2GrantFailuresAreRecorded(t *testing.T) {
	const code = "oauth2_code.attempted-0451"
	t.Run("redeem unknown code", func(t *testing.T) {
		s, events := denialService(t, denialRepository{})
		_, err := s.RedeemCode(t.Context(), CodeRequest{Code: code, ClientID: denialClientID, RedirectURI: denialRedirectURI,
			Resource: s.config.MCPResource, CodeVerifier: denialVerifierString()})
		if !errors.Is(err, ErrInvalidGrant) {
			t.Fatalf("RedeemCode = %v, want ErrInvalidGrant", err)
		}
		requireGrantFailure(t, events, denialClientID, securityevent.ReasonInvalidGrant, code, denialVerifierString())
	})
	t.Run("redeem unknown client", func(t *testing.T) {
		s, events := denialService(t, denialRepository{})
		const client = "https://unknown.example/metadata"
		_, err := s.RedeemCode(t.Context(), CodeRequest{Code: code, ClientID: client, RedirectURI: denialRedirectURI,
			Resource: s.config.MCPResource, CodeVerifier: denialVerifierString()})
		if !errors.Is(err, ErrInvalidClient) {
			t.Fatalf("RedeemCode = %v, want ErrInvalidClient", err)
		}
		requireGrantFailure(t, events, client, securityevent.ReasonInvalidClient, code)
	})
	t.Run("refresh unknown token", func(t *testing.T) {
		s, events := denialService(t, denialRepository{rotateErr: ErrInvalidGrant})
		refresh := session.NewDelegatedRefreshToken()
		if _, err := s.Refresh(t.Context(), RefreshRequest{RefreshToken: refresh, ClientID: denialClientID, Resource: s.config.MCPResource}); !errors.Is(err, ErrInvalidGrant) {
			t.Fatalf("Refresh = %v, want ErrInvalidGrant", err)
		}
		requireGrantFailure(t, events, denialClientID, securityevent.ReasonInvalidGrant, refresh)
	})
	t.Run("exchange without client proof", func(t *testing.T) {
		s, events := denialService(t, denialRepository{})
		const subject = "subject-token-0451"
		if _, err := s.Exchange(t.Context(), ClientProof{}, ExchangeRequest{SubjectToken: subject}); !errors.Is(err, ErrInvalidClient) {
			t.Fatalf("Exchange = %v, want ErrInvalidClient", err)
		}
		requireGrantFailure(t, events, "", securityevent.ReasonInvalidClient, subject)
	})
	t.Run("exchange of a rejected subject token", func(t *testing.T) {
		s, events := denialService(t, denialRepository{})
		proof, err := s.AuthenticateClientContext(t.Context(), "mcp", denialSecret)
		if err != nil {
			t.Fatal(err)
		}
		const subject = "subject-token-0452"
		if _, err := s.Exchange(t.Context(), proof, ExchangeRequest{SubjectToken: subject, SubjectTokenType: AccessTokenType, Resource: s.config.APIResource}); !errors.Is(err, ErrInvalidGrant) {
			t.Fatalf("Exchange = %v, want ErrInvalidGrant", err)
		}
		requireGrantFailure(t, events, "mcp", securityevent.ReasonInvalidGrant, subject, denialSecret)
	})
	t.Run("introspect a rejected token", func(t *testing.T) {
		s, events := denialService(t, denialRepository{})
		proof, err := s.AuthenticateClientContext(t.Context(), "mcp", denialSecret)
		if err != nil {
			t.Fatal(err)
		}
		const raw = "introspected-token-0451"
		result, err := s.Introspect(t.Context(), proof, raw)
		if err != nil || result.Active {
			t.Fatalf("Introspect = %+v, %v; want inactive", result, err)
		}
		requireGrantFailure(t, events, "mcp", securityevent.ReasonInvalidGrant, raw, denialSecret)
	})
	t.Run("revoke for an unknown client", func(t *testing.T) {
		s, events := denialService(t, denialRepository{})
		const client = "https://unknown.example/metadata"
		if err := s.Revoke(t.Context(), RevokeRequest{Token: "revoked-token-0451", ClientID: client}); !errors.Is(err, ErrInvalidClient) {
			t.Fatalf("Revoke = %v, want ErrInvalidClient", err)
		}
		requireGrantFailure(t, events, client, securityevent.ReasonInvalidClient, "revoked-token-0451")
	})
	t.Run("revoke a rejected access token", func(t *testing.T) {
		s, events := denialService(t, denialRepository{})
		if err := s.Revoke(t.Context(), RevokeRequest{Token: "revoked-token-0452", ClientID: denialClientID}); err != nil {
			t.Fatalf("Revoke = %v, want the RFC 7009 success", err)
		}
		requireGrantFailure(t, events, denialClientID, securityevent.ReasonInvalidGrant, "revoked-token-0452")
	})
}

func TestOAuth2RefreshReuseIsNotAGrantFailure(t *testing.T) {
	s, events := denialService(t, denialRepository{rotateErr: ErrRefreshReuse})
	if _, err := s.Refresh(t.Context(), RefreshRequest{RefreshToken: session.NewDelegatedRefreshToken(), ClientID: denialClientID, Resource: s.config.MCPResource}); !errors.Is(err, ErrRefreshReuse) {
		t.Fatalf("Refresh = %v, want ErrRefreshReuse", err)
	}
	if len(events.created) != 1 || events.created[0].EventType != TypeRefreshReuse || events.created[0].EventStatus != securityevent.StatusBlocked {
		t.Fatalf("rows = %+v, want only oauth2_refresh_reuse blocked", events.created)
	}
}

// TestOAuth2MalformedRequestsRecordInvalidRequest pins the evaluated-value half
// of the boundary: a parameter value the service evaluated and refused records
// its wire code as the reason (the shape half — no row — is the handler's).
func TestOAuth2MalformedRequestsRecordInvalidRequest(t *testing.T) {
	exchange := func(t *testing.T, s *Service, req ExchangeRequest) error {
		t.Helper()
		proof, err := s.AuthenticateClientContext(t.Context(), "mcp", denialSecret)
		if err != nil {
			t.Fatal(err)
		}
		req.SubjectToken = "subject-token-0453"
		_, err = s.Exchange(t.Context(), proof, req)
		return err
	}
	for _, tc := range []struct {
		name     string
		call     func(t *testing.T, s *Service) error
		want     error
		clientID string
		reason   string
	}{
		{"redeem for the API resource", func(t *testing.T, s *Service) error {
			_, err := s.RedeemCode(t.Context(), CodeRequest{Code: "c", ClientID: denialClientID, RedirectURI: denialRedirectURI,
				Resource: s.config.APIResource, CodeVerifier: denialVerifierString()})
			return err
		}, ErrInvalidTarget, denialClientID, securityevent.ReasonInvalidTarget},
		{"refresh for the API resource", func(t *testing.T, s *Service) error {
			_, err := s.Refresh(t.Context(), RefreshRequest{RefreshToken: session.NewDelegatedRefreshToken(), ClientID: denialClientID, Resource: s.config.APIResource})
			return err
		}, ErrInvalidTarget, denialClientID, securityevent.ReasonInvalidTarget},
		{"exchange with a scope", func(t *testing.T, s *Service) error {
			return exchange(t, s, ExchangeRequest{SubjectTokenType: AccessTokenType, Resource: s.config.APIResource, Scope: "openid"})
		}, ErrInvalidScope, "mcp", securityevent.ReasonInvalidScope},
		{"exchange with an audience", func(t *testing.T, s *Service) error {
			return exchange(t, s, ExchangeRequest{SubjectTokenType: AccessTokenType, Resource: s.config.APIResource, Audience: "https://other.example"})
		}, ErrInvalidTarget, "mcp", securityevent.ReasonInvalidTarget},
		{"exchange of another token type", func(t *testing.T, s *Service) error {
			return exchange(t, s, ExchangeRequest{SubjectTokenType: "urn:ietf:params:oauth:token-type:id_token", Resource: s.config.APIResource})
		}, ErrInvalidRequest, "mcp", securityevent.ReasonInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, events := denialService(t, denialRepository{})
			if err := tc.call(t, s); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			requireGrantFailure(t, events, tc.clientID, tc.reason, denialSecret, "subject-token-0453", "openid")
		})
	}
}

// TestOAuth2AuthenticateClientContextRecordsMismatch pins the confidential
// client's mismatch row: client_id rides it only when the presented id is the
// configured client, and a success records nothing.
func TestOAuth2AuthenticateClientContextRecordsMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, id, secret, attributed string
	}{
		{"foreign id", "someone-else", "wrong-secret-0451", ""},
		{"configured id, wrong secret", "mcp", "wrong-secret-0451", "mcp"},
		{"configured id, empty secret", "mcp", "", "mcp"},
		{"configured id with a NUL", "mcp\x00", denialSecret, ""},
		{"4 KB id", strings.Repeat("m", 4096), "wrong-secret-0451", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, events := denialService(t, denialRepository{})
			if _, err := s.AuthenticateClientContext(t.Context(), tc.id, tc.secret); !errors.Is(err, ErrInvalidClient) {
				t.Fatalf("AuthenticateClientContext = %v, want ErrInvalidClient", err)
			}
			requireGrantFailure(t, events, tc.attributed, securityevent.ReasonInvalidClient, tc.secret, denialSecret)
			if tc.attributed == "" {
				for key, value := range events.created[0].Details {
					if text := fmt.Sprint(value); tc.id != "" && strings.Contains(text, tc.id) {
						t.Fatalf("Details[%q] carries the presented id", key)
					}
				}
			}
		})
	}
	t.Run("success records nothing", func(t *testing.T) {
		s, events := denialService(t, denialRepository{})
		if _, err := s.AuthenticateClientContext(t.Context(), "mcp", denialSecret); err != nil {
			t.Fatal(err)
		}
		if len(events.created) != 0 {
			t.Fatalf("a successful client authentication recorded %+v", events.created)
		}
	})
}

// TestOAuth2DeprecatedAuthenticateClientRecordsUnattributed pins the deprecated
// shell: it shares the comparison and records the mismatch, with no request
// to attribute it to.
func TestOAuth2DeprecatedAuthenticateClientRecordsUnattributed(t *testing.T) {
	fromRequest := securityevent.WithClientInfoReader(func(ctx context.Context) (string, string) {
		if _, ok := sdk.RequestIDFromContext(ctx); ok {
			return "198.51.100.8", "agent/3"
		}
		return "", ""
	})
	s, events := denialService(t, denialRepository{}, fromRequest)
	ctx := sdk.WithRequestID(t.Context(), "req-mismatch")
	if _, err := s.AuthenticateClientContext(ctx, "mcp", "wrong-secret-0451"); !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("AuthenticateClientContext = %v", err)
	}
	if _, err := s.AuthenticateClient("mcp", "wrong-secret-0451"); !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("AuthenticateClient = %v", err)
	}
	if _, err := s.AuthenticateClient("mcp", denialSecret); err != nil {
		t.Fatalf("AuthenticateClient with the secret = %v", err)
	}
	if len(events.created) != 2 {
		t.Fatalf("recorded %d rows, want the two mismatches", len(events.created))
	}
	attributed, shell := events.created[0], events.created[1]
	if attributed.RequestID != "req-mismatch" || attributed.IPAddress != "198.51.100.8" || attributed.UserAgent != "agent/3" {
		t.Fatalf("context row = %+v, want the request's attribution", attributed)
	}
	if shell.EventType != TypeGrantFailed || shell.Details["reason"] != securityevent.ReasonInvalidClient || shell.Details["client_id"] != "mcp" ||
		shell.RequestID != "" || shell.IPAddress != "" || shell.UserAgent != "" {
		t.Fatalf("deprecated row = %+v, want an unattributed invalid_client row", shell)
	}
}

// TestOAuth2PresentedClientIDIsBounded pins the public-client paths: a
// presented id the attribution rule refuses rides the row blank, never
// truncated, and the row is not lost.
func TestOAuth2PresentedClientIDIsBounded(t *testing.T) {
	for _, tc := range []struct{ name, id string }{
		{"NUL", denialClientID + "\x00"},
		{"4 KB", denialClientID + "/" + strings.Repeat("a", 4096)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, events := denialService(t, denialRepository{})
			_, err := s.RedeemCode(t.Context(), CodeRequest{Code: "oauth2_code.attempted-0452", ClientID: tc.id, RedirectURI: denialRedirectURI,
				Resource: s.config.MCPResource, CodeVerifier: denialVerifierString()})
			if !errors.Is(err, ErrInvalidClient) {
				t.Fatalf("RedeemCode = %v, want ErrInvalidClient", err)
			}
			if len(events.created) != 1 {
				t.Fatalf("recorded %d rows, want one", len(events.created))
			}
			if got, ok := events.created[0].Details["client_id"]; !ok || got != "" {
				t.Fatalf("client_id = %q (present %v), want present and blank", got, ok)
			}
		})
	}
}

// TestOAuth2GrantFailuresAreBudgeted pins that every oauth2_grant_failed row
// asks the failure-row budget and other OAuth2 rows do not.
func TestOAuth2GrantFailuresAreBudgeted(t *testing.T) {
	s, events := denialService(t, denialRepository{}, securityevent.WithFailureRowBudget(denyAllower{}, ratelimiter.PerMinute(1)))
	if _, err := s.AuthenticateClientContext(t.Context(), "mcp", "wrong-secret-0451"); !errors.Is(err, ErrInvalidClient) {
		t.Fatal(err)
	}
	if _, err := s.RedeemCode(t.Context(), CodeRequest{Code: "oauth2_code.attempted-0453", ClientID: denialClientID, RedirectURI: denialRedirectURI,
		Resource: s.config.MCPResource, CodeVerifier: denialVerifierString()}); !errors.Is(err, ErrInvalidGrant) {
		t.Fatal(err)
	}
	if len(events.created) != 0 || s.events.SuppressedRows() != 2 {
		t.Fatalf("rows = %+v, suppressed = %d; want none written and two suppressed", events.created, s.events.SuppressedRows())
	}
	s.audit(t.Context(), TypeRefreshReuse, "u-1", "sess-1", denialClientID)
	if len(events.created) != 1 || s.events.SuppressedRows() != 2 {
		t.Fatalf("a refresh-reuse row was budgeted: rows = %+v", events.created)
	}
}

// denyAllower refuses every admission.
type denyAllower struct{}

func (denyAllower) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: false}, nil
}
