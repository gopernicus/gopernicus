package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/examples/production-baseline/integrations/authentication/devissuer"
	"github.com/gopernicus/gopernicus/pockets/authentication"
	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	authorizationmemory "github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// productionKeys are disposable hex fixtures for the two key variables this
// host needs; production refuses to mint either.
var productionKeys = map[authentication.KeyName]string{
	authentication.KeyJWT:        strings.Repeat("a1", 32),
	authentication.KeyIdentifier: strings.Repeat("c3", 32),
}

// The one external user the identity doubles know: the mirror maps the
// provider subject memberSubject to the active local user memberID.
const (
	memberSubject = "u1"
	memberID      = "user-1"
)

// The identity doubles stand in for the authentication pgx store. The pocket
// constructs over them, and on the role routes the external engine reads only
// the subject mirror and the user's status. Every other port method is
// unreachable in these tests and panics through the nil embedded interface.
type users struct{ user.UserRepository }

type identifiers struct {
	identifier.IdentifierRepository
}

type sessions struct{ session.SessionRepository }

type activeSessions struct{ session.ActiveUserRepository }

type externalSubjects struct{ externalsubject.Repository }

type securityEvents struct {
	securityevent.SecurityEventRepository
}

// sharedLimiter stands in for the pgxdb limiter: a limiter that does not
// declare itself in-process is presumed shared across instances.
type sharedLimiter struct{}

func (users) Get(_ context.Context, id string) (user.User, error) {
	if id != memberID {
		return user.User{}, sdk.ErrNotFound
	}
	return user.User{ID: memberID, Status: user.StatusActive}, nil
}

func (externalSubjects) Get(_ context.Context, provider, subject string) (externalsubject.ExternalSubject, error) {
	if provider != providerName || subject != memberSubject {
		return externalsubject.ExternalSubject{}, sdk.ErrNotFound
	}
	return externalsubject.ExternalSubject{Provider: provider, Subject: subject, UserID: memberID}, nil
}

func (securityEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	return evt, nil
}

func (sharedLimiter) Allow(context.Context, string, ratelimiter.Limit) (ratelimiter.Result, error) {
	return ratelimiter.Result{Allowed: true}, nil
}

func (sharedLimiter) Reset(context.Context, string) error { return nil }

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// identityRepositories is the authentication store bundle run wires, over the
// doubles: the security-event audit rail included.
func identityRepositories() authentication.Repositories {
	return authentication.Repositories{
		Users:            users{},
		Identifiers:      identifiers{},
		Sessions:         sessions{},
		ActiveSessions:   activeSessions{},
		ExternalSubjects: externalSubjects{},
		SecurityEvents:   securityEvents{},
	}
}

// authorizationRepositories is the authorization store bundle run wires, over
// the memory store, recording changes when audit is set.
func authorizationRepositories(audit bool) authorization.Repositories {
	options := []authorizationmemory.Option{authorizationmemory.WithIntegrityPolicy(mutations.DefaultIntegrityPolicy())}
	if audit {
		options = append(options, authorizationmemory.WithAudit())
	}
	store := authorizationmemory.New(options...)
	return authorization.Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), Operations: store.Operations(), Audit: store.Audit()}
}

// setKeys sets both key variables for the test.
func setKeys(t *testing.T) {
	t.Helper()
	for name, value := range productionKeys {
		t.Setenv(string(name), value)
	}
}

// productionBaseline is run's composition in production over the doubles, with
// every input production asks for: both keys, an https public origin, an OIDC
// provider that is not development-only, Secure cookies, a shared limiter, the
// security-event rail, and a recording authorization store with its reader.
func productionBaseline(t *testing.T) composition {
	t.Helper()
	setKeys(t)
	t.Setenv("PUBLIC_BASE_URL", "https://baseline.example")
	t.Setenv("IDP_ISSUER", "https://idp.example")
	t.Setenv("IDP_AUDIENCE", "baseline")
	t.Setenv("IDP_JWKS_URL", "https://idp.example/.well-known/jwks.json")
	log := quietLog()
	keys, err := loadKeys(environment.ModeProduction, log)
	if err != nil {
		t.Fatalf("loadKeys: %v", err)
	}
	srv, err := loadServerConfig(environment.ModeProduction)
	if err != nil {
		t.Fatalf("loadServerConfig: %v", err)
	}
	provider, err := newProvider(context.Background(), web.NewWebHandler(), srv.Origin(), environment.ModeProduction, log)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	return composition{
		mode:                environment.ModeProduction,
		log:                 log,
		origin:              srv.Origin(),
		keys:                keys,
		sessionCookie:       authenticationhttp.CookieConfig{Secure: true},
		limiter:             sharedLimiter{},
		authenticationRepos: identityRepositories(),
		authorizationRepos:  authorizationRepositories(true),
		provider:            provider,
	}
}

// TestProductionBaselineConstructs is the positive control: without it the
// matrix below could pass with every case failing for the wrong reason. It also
// proves the decision bridge is wired: the authorization registration line
// reports the observer, and production logs no unrecorded-denials WARN.
func TestProductionBaselineConstructs(t *testing.T) {
	var logs bytes.Buffer
	c := productionBaseline(t)
	c.log = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := mount(web.NewWebHandler(), c); err != nil {
		t.Fatalf("production baseline: %v", err)
	}
	if !strings.Contains(logs.String(), "decision_observer=true") || strings.Contains(logs.String(), "denials are not durably recorded") {
		t.Fatalf("registration does not report the decision observer:\n%s", logs.String())
	}
}

// TestProductionRefusesEachOmission breaks one production input of the
// baseline at a time and asserts the refusal by its stable error.
func TestProductionRefusesEachOmission(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, c *composition)
		want   error
		names  string
	}{
		{
			name:   "security_events",
			mutate: func(_ *testing.T, c *composition) { c.authenticationRepos.SecurityEvents = nil },
			want:   authentication.ErrAuditRequired,
		},
		{
			name:   "authorization_store_without_audit",
			mutate: func(_ *testing.T, c *composition) { c.authorizationRepos = authorizationRepositories(false) },
			want:   authorization.ErrAuditRequired,
			names:  "AuditRecording() false",
		},
		{
			name:   "authorization_audit_reader",
			mutate: func(_ *testing.T, c *composition) { c.authorizationRepos.Audit = nil },
			want:   authorization.ErrAuditRequired,
			names:  "Repositories.Audit is nil",
		},
		{
			name:   "insecure_session_cookie",
			mutate: func(_ *testing.T, c *composition) { c.sessionCookie.Secure = false },
			want:   sdk.ErrInvalidInput,
			names:  "Secure",
		},
		{
			// The dev issuer refuses production itself, so a production pocket
			// only ever meets one built in development; it refuses it by the
			// provider's declared capability.
			name: "development_issuer",
			mutate: func(t *testing.T, c *composition) {
				t.Setenv("IDP_ISSUER", "")
				provider, err := newProvider(context.Background(), web.NewWebHandler(), "http://localhost:8080", environment.ModeDevelopment, quietLog())
				if err != nil {
					t.Fatalf("development issuer: %v", err)
				}
				c.provider = provider
			},
			want: authentication.ErrDevelopmentOnlyProvider,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := productionBaseline(t)
			tc.mutate(t, &c)
			_, err := mount(web.NewWebHandler(), c)
			if !errors.Is(err, tc.want) || !strings.Contains(fmt.Sprint(err), tc.names) {
				t.Fatalf("production %s: got %v, want %v naming %q", tc.name, err, tc.want, tc.names)
			}
		})
	}
}

// TestProductionRefusesUnsetKeys proves production never mints a key: each
// needed key unset is refused by name, and with both unset one error names
// both.
func TestProductionRefusesUnsetKeys(t *testing.T) {
	for name := range productionKeys {
		t.Run(string(name), func(t *testing.T) {
			setKeys(t)
			t.Setenv(string(name), "")
			_, err := loadKeys(environment.ModeProduction, quietLog())
			if !errors.Is(err, authentication.ErrKeyRequired) || !strings.Contains(err.Error(), string(name)) {
				t.Fatalf("err = %v, want ErrKeyRequired naming %s", err, name)
			}
		})
	}
	t.Run("both", func(t *testing.T) {
		for name := range productionKeys {
			t.Setenv(string(name), "")
		}
		_, err := loadKeys(environment.ModeProduction, quietLog())
		if !errors.Is(err, authentication.ErrKeyRequired) {
			t.Fatalf("err = %v, want ErrKeyRequired", err)
		}
		for name := range productionKeys {
			if !strings.Contains(err.Error(), string(name)) {
				t.Errorf("joined error does not name %s: %v", name, err)
			}
		}
	})
}

// TestProductionRequiresPublicBaseURL proves production refuses to serve
// without the https origin cookie mutations are checked against.
func TestProductionRequiresPublicBaseURL(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "")
	if _, err := loadServerConfig(environment.ModeProduction); !errors.Is(err, web.ErrPublicBaseURLRequired) {
		t.Fatalf("err = %v, want web.ErrPublicBaseURLRequired", err)
	}
}

// TestProductionRefusesTheDevIssuer proves that with IDP_ISSUER unset the live
// binary stops at the issuer itself, before any pocket sees it.
func TestProductionRefusesTheDevIssuer(t *testing.T) {
	t.Setenv("IDP_ISSUER", "")
	_, err := newProvider(context.Background(), web.NewWebHandler(), "https://baseline.example", environment.ModeProduction, quietLog())
	if !errors.Is(err, devissuer.ErrNotDevelopment) {
		t.Fatalf("err = %v, want devissuer.ErrNotDevelopment", err)
	}
}

// TestRoleRoutesGateOrder serves run's router and pockets in development with
// the dev issuer on a real loopback listener (the verifier fetches the
// issuer's key set from it) and proves the role routes' gate order: a
// cross-site POST carrying the session cookie is refused by the browser-safe
// mutation gate even with a valid provider token, a request without a
// credential is refused by authentication, and the same token without the
// cookie passes both and is refused by authorization.
func TestRoleRoutesGateOrder(t *testing.T) {
	for name := range productionKeys {
		t.Setenv(string(name), "")
	}
	t.Setenv("IDP_ISSUER", "")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	log := quietLog()
	keys, err := loadKeys(environment.ModeDevelopment, log)
	if err != nil {
		t.Fatalf("loadKeys: %v", err)
	}
	router := newRouter(0, log)
	provider, err := newProvider(context.Background(), router, origin, environment.ModeDevelopment, log)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if _, err := mount(router, composition{
		mode:                environment.ModeDevelopment,
		log:                 log,
		origin:              origin,
		keys:                keys,
		sessionCookie:       authenticationhttp.CookieConfig{Secure: true},
		limiter:             sharedLimiter{},
		authenticationRepos: identityRepositories(),
		authorizationRepos:  authorizationRepositories(true),
		provider:            provider,
	}); err != nil {
		t.Fatalf("mount: %v", err)
	}
	server := httptest.NewUnstartedServer(router)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	status, raw := send(t, http.MethodPost, origin+devissuer.TokenPath, `{"sub":"`+memberSubject+`","email":"a@x.test","email_verified":true}`, nil)
	var minted struct{ Token string }
	if status != http.StatusOK || json.Unmarshal([]byte(raw), &minted) != nil || minted.Token == "" {
		t.Fatalf("mint: %d %s", status, raw)
	}

	assign := `{"subject_type":"user","subject_id":"user-2","role":"admin","scope":{"kind":"global"}}`
	crossSite := http.Header{
		"Authorization":  {"Bearer " + minted.Token},
		"Cookie":         {"session=present"},
		"Origin":         {"https://evil.example"},
		"Sec-Fetch-Site": {"cross-site"},
		"Content-Type":   {"application/json"},
	}
	if status, raw := send(t, http.MethodPost, origin+"/authorization/roles", assign, crossSite); status != http.StatusForbidden || !strings.Contains(raw, "origin_rejected") {
		t.Errorf("cross-site cookie POST: %d %s, want 403 origin_rejected", status, raw)
	}
	if status, raw := send(t, http.MethodPost, origin+"/authorization/roles", assign, http.Header{"Content-Type": {"application/json"}}); status != http.StatusUnauthorized {
		t.Errorf("POST without a credential: %d %s, want 401", status, raw)
	}
	if status, raw := send(t, http.MethodGet, origin+"/authorization/roles/by-subject?subject_type=user&subject_id=x", "", nil); status != http.StatusUnauthorized {
		t.Errorf("GET without a credential: %d %s, want 401", status, raw)
	}
	bearerOnly := http.Header{"Authorization": {"Bearer " + minted.Token}, "Content-Type": {"application/json"}}
	if status, raw := send(t, http.MethodPost, origin+"/authorization/roles", assign, bearerOnly); status != http.StatusForbidden || strings.Contains(raw, "origin_rejected") {
		t.Errorf("bearer POST by a non-admin: %d %s, want 403 from authorization", status, raw)
	}
}

// send makes one request and returns the status and body.
func send(t *testing.T, method, url, body string, header http.Header) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(raw)
}
