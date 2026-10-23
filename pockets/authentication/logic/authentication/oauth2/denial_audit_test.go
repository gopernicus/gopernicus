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

func denialService(t *testing.T, repo Repository) (*Service, *auditEvents) {
	t.Helper()
	events := &auditEvents{}
	rec := securityevent.NewRecorder(events, sdk.NewIDGenerator(func() (string, error) { return "host-id", nil }), nil, nil,
		securityevent.WithClientInfoReader(func(context.Context) (string, string) { return "198.51.100.7", "agent/2" }))
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
// failure for clientID with reason, attributed through the host recorder.
func requireGrantFailure(t *testing.T, events *auditEvents, clientID, reason string, secrets ...string) {
	t.Helper()
	if len(events.created) != 1 {
		t.Fatalf("recorded %d rows, want one oauth2_grant_failed: %+v", len(events.created), events.created)
	}
	evt := events.created[0]
	if evt.EventType != TypeGrantFailed || evt.EventStatus != securityevent.StatusFailure || evt.Details["reason"] != reason || evt.Details["client_id"] != clientID {
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
		proof, err := s.AuthenticateClient("mcp", denialSecret)
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
		proof, err := s.AuthenticateClient("mcp", denialSecret)
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

func TestOAuth2MalformedRequestsRecordNothing(t *testing.T) {
	s, events := denialService(t, denialRepository{})
	if _, err := s.RedeemCode(t.Context(), CodeRequest{Code: "c", ClientID: denialClientID, RedirectURI: denialRedirectURI,
		Resource: s.config.APIResource, CodeVerifier: denialVerifierString()}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("RedeemCode = %v, want ErrInvalidTarget", err)
	}
	if len(events.created) != 0 {
		t.Fatalf("a malformed request recorded %+v", events.created)
	}
}
