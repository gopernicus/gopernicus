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
	"os"
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
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	authorizationmemory "github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
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

type userAdmin struct{ user.AdminRepository }

type userErasure struct{ user.EraseRepository }

type maintenanceStore struct{ maintenance.Repository }

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
// doubles: the security-event audit rail, user administration, erasure and
// maintenance included.
func identityRepositories() authentication.Repositories {
	return authentication.Repositories{
		Users:            users{},
		Identifiers:      identifiers{},
		Sessions:         sessions{},
		ActiveSessions:   activeSessions{},
		ExternalSubjects: externalSubjects{},
		SecurityEvents:   securityEvents{},
		UserAdmin:        userAdmin{},
		UserErasure:      userErasure{},
		Maintenance:      maintenanceStore{},
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
	return authorization.Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), Operations: store.Operations(), OperationExpiry: store.OperationExpiry(), Audit: store.Audit()}
}

// setKeys sets both key variables for the test.
func setKeys(t *testing.T) {
	t.Helper()
	for name, value := range productionKeys {
		t.Setenv(string(name), value)
	}
}

// productionBaseline is run's composition in production over the doubles, with
// every input production asks for: both keys, an https public origin, the
// archive, an OIDC provider that is not development-only, Secure cookies, a
// shared limiter, the security-event rail, user administration, erasure and
// maintenance, and a recording authorization store with its reader.
func productionBaseline(t *testing.T) composition {
	t.Helper()
	setKeys(t)
	t.Setenv("PUBLIC_BASE_URL", "https://baseline.example")
	t.Setenv("IDP_ISSUER", "https://idp.example")
	t.Setenv("IDP_AUDIENCE", "baseline")
	t.Setenv("IDP_JWKS_URL", "https://idp.example/.well-known/jwks.json")
	t.Setenv("AUDIT_ARCHIVE_DIR", t.TempDir())
	log := quietLog()
	keys, err := loadKeys(environment.ModeProduction, log)
	if err != nil {
		t.Fatalf("loadKeys: %v", err)
	}
	srv, err := loadServerConfig(environment.ModeProduction)
	if err != nil {
		t.Fatalf("loadServerConfig: %v", err)
	}
	archive, closeArchive, err := openArchive(environment.ModeProduction)
	if err != nil {
		t.Fatalf("openArchive: %v", err)
	}
	t.Cleanup(closeArchive)
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
		archive:             archive,
		authenticationRepos: identityRepositories(),
		authorizationRepos:  authorizationRepositories(true),
		provider:            provider,
	}
}

// TestProductionBaselineConstructs is the positive control: without it the
// matrix below could pass with every case failing for the wrong reason. It also
// proves the decision bridge is wired: the authorization registration line
// reports the observer, and production logs no unrecorded-denials WARN. And it
// proves retention is: the maintenance registration line reports the
// acknowledged runner and the disk archive, the operation expiry is on, and
// production logs none of the retention WARNs.
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
	for _, want := range []string{"maintenance_runner=true", "archive_sink=disk:" + os.Getenv("AUDIT_ARCHIVE_DIR"), "security_event_retention=9600h0m0s", "operation_expiry", "audit_read_route=true"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("registration lacks %q:\n%s", want, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "mfa_required=admins source=config gates=/auth/admin/users*") {
		t.Errorf("registration lacks the mfa posture line:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("production logs a WARN:\n%s", logs.String())
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
			name:   "maintenance_repository",
			mutate: func(_ *testing.T, c *composition) { c.authenticationRepos.Maintenance = nil },
			want:   authentication.ErrMaintenanceAcknowledgedWithoutRepository,
		},
		{
			name:   "user_erasure_repository",
			mutate: func(_ *testing.T, c *composition) { c.authenticationRepos.UserErasure = nil },
			want:   authentication.ErrUserErasureRouteRequires,
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

// TestArchiveIsRequiredInProduction proves production refuses to boot without
// AUDIT_ARCHIVE_DIR, while development without it purges unarchived (the
// registration line's archive_sink=none, no WARN), and that a set directory
// opens as a disk sink labeled disk:<dir>.
func TestArchiveIsRequiredInProduction(t *testing.T) {
	t.Setenv("AUDIT_ARCHIVE_DIR", "")
	if _, _, err := openArchive(environment.ModeProduction); !errors.Is(err, errArchiveDirRequired) {
		t.Fatalf("production without AUDIT_ARCHIVE_DIR: err = %v, want errArchiveDirRequired", err)
	}
	archive, closeArchive, err := openArchive(environment.ModeDevelopment)
	if err != nil || archive.sink != nil {
		t.Fatalf("development without AUDIT_ARCHIVE_DIR: %+v %v, want no sink", archive, err)
	}
	closeArchive()

	var logs bytes.Buffer
	c := productionBaseline(t)
	c.mode, c.archive = environment.ModeDevelopment, archive
	c.log = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := mount(web.NewWebHandler(), c); err != nil {
		t.Fatalf("development without an archive: %v", err)
	}
	if !strings.Contains(logs.String(), "archive_sink=none") || strings.Contains(logs.String(), "maintenance_runner=unacknowledged") {
		t.Fatalf("registration does not report archive_sink=none alone:\n%s", logs.String())
	}

	dir := t.TempDir()
	t.Setenv("AUDIT_ARCHIVE_DIR", dir)
	archive, closeArchive, err = openArchive(environment.ModeProduction)
	if err != nil {
		t.Fatalf("openArchive: %v", err)
	}
	defer closeArchive()
	if _, ok := archive.sink.(*filestorage.Disk); !ok || archive.location != "disk:"+dir {
		t.Fatalf("archive = %T %q, want a disk sink labeled disk:%s", archive.sink, archive.location, dir)
	}
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
// credential or with a single-factor token is refused by authentication (the
// latter with the RFC 9470 challenge), and an MFA token without the cookie
// passes both and is refused by authorization. The erase route is mounted
// behind the same principal and refuses a non-admin with 403 whatever the
// token's assurance.
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

	status, raw := send(t, http.MethodPost, origin+devissuer.TokenPath, `{"sub":"`+memberSubject+`","email":"a@x.test","email_verified":true,"amr":["pwd","mfa"]}`, nil)
	var minted struct{ Token string }
	if status != http.StatusOK || json.Unmarshal([]byte(raw), &minted) != nil || minted.Token == "" {
		t.Fatalf("mint: %d %s", status, raw)
	}
	status, raw = send(t, http.MethodPost, origin+devissuer.TokenPath, `{"sub":"`+memberSubject+`","email":"a@x.test","email_verified":true,"amr":["pwd"]}`, nil)
	var single struct{ Token string }
	if status != http.StatusOK || json.Unmarshal([]byte(raw), &single) != nil || single.Token == "" {
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
	singleFactor := http.Header{"Authorization": {"Bearer " + single.Token}, "Content-Type": {"application/json"}}
	if status, header, raw := sendWithHeader(t, http.MethodPost, origin+"/authorization/roles", assign, singleFactor); status != http.StatusUnauthorized || !strings.Contains(header.Get("WWW-Authenticate"), `error="insufficient_user_authentication"`) {
		t.Errorf("single-factor bearer POST: %d %q %s, want 401 with the RFC 9470 challenge", status, header.Get("WWW-Authenticate"), raw)
	}
	bearerOnly := http.Header{"Authorization": {"Bearer " + minted.Token}, "Content-Type": {"application/json"}}
	if status, raw := send(t, http.MethodPost, origin+"/authorization/roles", assign, bearerOnly); status != http.StatusForbidden || strings.Contains(raw, "origin_rejected") {
		t.Errorf("bearer POST by a non-admin: %d %s, want 403 from authorization", status, raw)
	}
	if status, raw := send(t, http.MethodPost, origin+"/auth/admin/users/user-2/erase", "{}", http.Header{"Content-Type": {"application/json"}}); status != http.StatusUnauthorized {
		t.Errorf("erase without a credential: %d %s, want 401", status, raw)
	}
	if status, raw := send(t, http.MethodPost, origin+"/auth/admin/users/user-2/erase", "{}", bearerOnly); status != http.StatusForbidden {
		t.Errorf("erase by a non-admin: %d %s, want 403 from the user-administration check", status, raw)
	}
	if status, raw := send(t, http.MethodPost, origin+"/auth/admin/users/user-2/erase", "{}", singleFactor); status != http.StatusForbidden {
		t.Errorf("erase by a single-factor non-admin: %d %s, want 403: the check runs before the MFA gate", status, raw)
	}
}

// send makes one request and returns the status and body.
func send(t *testing.T, method, url, body string, header http.Header) (int, string) {
	t.Helper()
	status, _, raw := sendWithHeader(t, method, url, body, header)
	return status, raw
}

// sendWithHeader makes one request and returns the status, the response
// header and the body.
func sendWithHeader(t *testing.T, method, url, body string, header http.Header) (int, http.Header, string) {
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
	return resp.StatusCode, resp.Header, string(raw)
}

// TestAuditRoutesAndSelfAssignment serves run's router in development over a
// recording audit rail with user-1 seeded as the global admin and proves the
// S7 wiring: an admin assigning a role to itself is refused by
// RefuseSelfTarget while assigning another user succeeds; both audit listings
// answer the admin, each read leaving one audit_read row on its rail; and a
// non-admin is refused both.
func TestAuditRoutesAndSelfAssignment(t *testing.T) {
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
	authorizationRepos := authorizationRepositories(true)
	seed, err := authorization.New(authorizationRepos, authorization.WithRuntimeMode(environment.ModeDevelopment), authorization.WithModel(accessModel()), authorization.WithLogger(log))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Mutations.AssignRole(audit.WithSource(context.Background(), audit.Source{System: "test"}), mutations.AssignRoleCommand{
		Subject: model.PrincipalRef{Type: "user", ID: memberID}, Role: adminRole, Scope: tuples.Global(),
	}); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	events := &recordedEvents{}
	repos := identityRepositories()
	repos.SecurityEvents = events
	if _, err := mount(router, composition{
		mode: environment.ModeDevelopment, log: log, origin: origin, keys: keys,
		sessionCookie: authenticationhttp.CookieConfig{Secure: true}, limiter: sharedLimiter{},
		authenticationRepos: repos, authorizationRepos: authorizationRepos, provider: provider,
	}); err != nil {
		t.Fatalf("mount: %v", err)
	}
	server := httptest.NewUnstartedServer(router)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)

	status, raw := send(t, http.MethodPost, origin+devissuer.TokenPath, `{"sub":"`+memberSubject+`","email":"a@x.test","email_verified":true,"amr":["pwd","mfa"]}`, nil)
	var minted struct{ Token string }
	if status != http.StatusOK || json.Unmarshal([]byte(raw), &minted) != nil || minted.Token == "" {
		t.Fatalf("mint: %d %s", status, raw)
	}
	admin := http.Header{"Authorization": {"Bearer " + minted.Token}, "Content-Type": {"application/json"}}

	if status, raw := send(t, http.MethodPost, origin+"/authorization/roles", `{"subject_type":"user","subject_id":"`+memberID+`","role":"admin","scope":{"kind":"global"}}`, admin); status != http.StatusForbidden {
		t.Errorf("self-assignment: %d %s, want 403 from RefuseSelfTarget", status, raw)
	}
	if status, raw := send(t, http.MethodPost, origin+"/authorization/roles", `{"subject_type":"user","subject_id":"user-2","role":"admin","scope":{"kind":"global"}}`, admin); status != http.StatusOK {
		t.Errorf("assign another user: %d %s, want 200", status, raw)
	}
	status, header, raw := sendWithHeader(t, http.MethodGet, origin+"/authorization/audit?subject_type=user&subject_id=user-2", "", admin)
	if status != http.StatusOK || header.Get("Cache-Control") != "no-store" || !strings.Contains(raw, `"subject":{"type":"user","id":"user-2"`) {
		t.Fatalf("authorization audit: %d %q %s", status, header.Get("Cache-Control"), raw)
	}
	if status, raw := send(t, http.MethodGet, origin+"/auth/admin/security-events?event_type=audit_read", "", admin); status != http.StatusOK || !strings.Contains(raw, `"rail":"authorization"`) {
		t.Fatalf("security events: %d %s", status, raw)
	}
	reads := events.ofType(securityevent.TypeAuditRead)
	if len(reads) != 2 || reads[0].Details["rail"] != "authorization" || reads[1].Details["rail"] != "security_events" || reads[0].Actor.ID != memberID {
		t.Fatalf("audit_read rows = %+v, want one per rail by the admin", reads)
	}
	if keys, _ := reads[0].Details["filters"].([]string); len(keys) != 1 || keys[0] != "subject" {
		t.Errorf("authorization audit_read filters = %v, want [subject]", reads[0].Details["filters"])
	}
	if len(events.ofType(decisions.EventRoleWriteRefused)) != 1 {
		t.Errorf("role_write_refused rows = %d, want 1 (the self-assignment)", len(events.ofType(decisions.EventRoleWriteRefused)))
	}
}
