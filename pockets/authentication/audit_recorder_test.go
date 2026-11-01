package authentication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	listing "github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// recorderEvents is a SecurityEventRepository that keeps rows, or fails every
// write with err.
type recorderEvents struct {
	mu      sync.Mutex
	created []securityevent.SecurityEvent
	err     error
}

func (r *recorderEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return securityevent.SecurityEvent{}, r.err
	}
	r.created = append(r.created, evt)
	return evt, nil
}

func (r *recorderEvents) List(context.Context, securityevent.ListFilter, listing.Request) (listing.Page[securityevent.SecurityEvent], error) {
	return listing.Page[securityevent.SecurityEvent]{}, nil
}

// Construction-only collaborators for the OAuth2 server: New never calls them.
type (
	recorderViews             struct{ inbound.Views }
	recorderOAuthViews        struct{ inbound.OAuthViews }
	recorderOAuth2Repository  struct{ oauth2.Repository }
	recorderSessionManagement struct{ session.ManagementRepository }
	recorderClients           struct{ oauth2.ClientResolver }
)

// TestComponentsShareOneSecurityEventRecorder pins the ONE-recorder contract:
// Components.SecurityEvents is the very recorder the service, its external
// engine, the OAuth2 server and the invitation service record through, so
// AuditWriteFailures and the write observer see every lost row.
func TestComponentsShareOneSecurityEventRecorder(t *testing.T) {
	events := &recorderEvents{}
	var mu sync.Mutex
	var observed []securityevent.WriteFailure
	cfg := browserBaseConfig()
	repos := testRepositories(Repositories{
		SecurityEvents: events, Invitations: stubInvitations{}, ExternalSubjects: cookieTestSubjects{},
		OAuth2: recorderOAuth2Repository{}, SessionManagement: recorderSessionManagement{},
	})
	c, err := New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
		WithPassword(PasswordConfig{Hasher: cfg.Hasher}),
		WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}),
		WithBrowser(BrowserConfig{Views: recorderViews{}}),
		WithInvitations(InvitationsConfig{Granter: stubGranter{}, InviteCheck: allowInvite}),
		WithExternal(authlogic.ExternalConfig{Providers: []authlogic.ExternalProvider{{Name: "idp", Provider: cookieTestProvider{}}}}),
		WithOAuth2(OAuth2Config{
			Server: oauth2.Config{Issuer: "https://issuer.example", MCPResource: "https://mcp.example", APIResource: "https://api.example",
				ConfidentialClientID: "mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte("a high entropy confidential test secret"))}},
			Clients: recorderClients{}, Views: recorderOAuthViews{},
		}),
		WithAuditWriteObserver(func(_ context.Context, f securityevent.WriteFailure) {
			mu.Lock()
			defer mu.Unlock()
			observed = append(observed, f)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if c.SecurityEvents == nil || c.SecurityEvents.Repository() != securityevent.SecurityEventRepository(events) {
		t.Fatal("Components.SecurityEvents does not record into Repositories.SecurityEvents")
	}
	want := reflect.ValueOf(c.SecurityEvents).Pointer()
	for _, holder := range []struct {
		name  string
		value any
		field string
	}{
		{"Authentication", c.Authentication, "securityEvents"},
		{"External", c.External, "events"},
		{"OAuth2", c.OAuth2, "events"},
		{"Invitations", c.Invitations, "securityEvents"},
	} {
		if reflect.ValueOf(holder.value).IsNil() {
			t.Fatalf("%s was not built", holder.name)
		}
		if got := reflect.ValueOf(holder.value).Elem().FieldByName(holder.field).Pointer(); got != want {
			t.Errorf("%s records through a recorder other than Components.SecurityEvents", holder.name)
		}
	}

	ctx := sdk.WithRequestID(context.Background(), "req-root")
	c.SecurityEvents.Record(ctx, securityevent.Input{Type: securityevent.TypeLogout, Status: securityevent.StatusSuccess})
	if len(events.created) != 1 || events.created[0].RequestID != "req-root" {
		t.Fatalf("recorded = %+v", events.created)
	}

	events.mu.Lock()
	events.err = errors.New("audit store down")
	events.mu.Unlock()
	c.SecurityEvents.Record(ctx, securityevent.Input{Type: securityevent.TypeLogout, Status: securityevent.StatusSuccess})
	c.SecurityEvents.Record(ctx, securityevent.Input{Type: securityevent.TypeLogin, Status: securityevent.StatusFailure})
	if got := c.AuditWriteFailures(); got != 2 {
		t.Fatalf("AuditWriteFailures = %d, want 2", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 2 || observed[1] != (securityevent.WriteFailure{EventType: securityevent.TypeLogin, EventStatus: securityevent.StatusFailure, ErrorKind: "unknown"}) {
		t.Fatalf("observed = %+v", observed)
	}
}

// TestComponentsRecorderWithoutAuditRail proves the recorder exists — and records
// nothing — when the host wires no SecurityEvents repository.
func TestComponentsRecorderWithoutAuditRail(t *testing.T) {
	cfg := browserBaseConfig()
	c, err := New(testRepositories(Repositories{}), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
		WithPassword(PasswordConfig{Hasher: cfg.Hasher}),
		WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}))
	if err != nil {
		t.Fatal(err)
	}
	if c.SecurityEvents == nil || c.SecurityEvents.Repository() != nil {
		t.Fatal("Components.SecurityEvents must be a no-op recorder without an audit rail")
	}
	c.SecurityEvents.Record(context.Background(), securityevent.Input{Type: securityevent.TypeLogout})
	if c.AuditWriteFailures() != 0 {
		t.Fatalf("AuditWriteFailures = %d, want 0", c.AuditWriteFailures())
	}
}

// keyedLimiter admits through Limiter and remembers every key and limit it was
// asked about.
type keyedLimiter struct {
	ratelimiter.Limiter
	mu     sync.Mutex
	keys   []string
	limits []ratelimiter.Limit
}

func (l *keyedLimiter) Allow(ctx context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	l.mu.Lock()
	l.keys, l.limits = append(l.keys, key), append(l.limits, limit)
	l.mu.Unlock()
	return l.Limiter.Allow(ctx, key, limit)
}

// TestComponentsBudgetFailureRowsOnTheRootLimiter pins the failure-row budget's
// wiring: the root hands the recorder the limiter its routes use, at
// auditFailureRowsPerMinute rows per minute per IP, and AuditRowsSuppressed
// counts the rows it refused.
func TestComponentsBudgetFailureRowsOnTheRootLimiter(t *testing.T) {
	events := &recorderEvents{}
	limiter := &keyedLimiter{Limiter: ratelimiter.NewMemory()}
	cfg := browserBaseConfig()
	repos := testRepositories(Repositories{SecurityEvents: events, OAuth2: recorderOAuth2Repository{}, SessionManagement: recorderSessionManagement{}})
	c, err := New(repos, cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
		WithPassword(PasswordConfig{Hasher: cfg.Hasher}),
		WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}),
		WithBrowser(BrowserConfig{Views: recorderViews{}}),
		WithAbuseProtection(AbuseProtectionConfig{RateLimiter: limiter}),
		WithOAuth2(OAuth2Config{
			Server: oauth2.Config{Issuer: "https://issuer.example", MCPResource: "https://mcp.example", APIResource: "https://api.example",
				ConfidentialClientID: "mcp", ConfidentialSecretHashes: [][32]byte{sha256.Sum256([]byte("a high entropy confidential test secret"))}},
			Clients: recorderClients{}, Views: recorderOAuthViews{},
		}))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= auditFailureRowsPerMinute; i++ {
		if _, err := c.OAuth2.AuthenticateClientContext(context.Background(), "mcp", "wrong-secret-0451"); !errors.Is(err, oauth2.ErrInvalidClient) {
			t.Fatalf("AuthenticateClientContext = %v", err)
		}
	}
	if len(events.created) != auditFailureRowsPerMinute || c.AuditRowsSuppressed() != 1 {
		t.Fatalf("rows = %d, AuditRowsSuppressed = %d; want %d and 1", len(events.created), c.AuditRowsSuppressed(), auditFailureRowsPerMinute)
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	var budgeted int
	for i, key := range limiter.keys {
		if strings.HasPrefix(key, "securityevent:failure-row:ip:") {
			budgeted++
			if limiter.limits[i] != ratelimiter.PerMinute(auditFailureRowsPerMinute) {
				t.Fatalf("budget limit = %+v", limiter.limits[i])
			}
		}
	}
	if budgeted != auditFailureRowsPerMinute+1 {
		t.Fatalf("the root limiter saw %d budget keys, want %d", budgeted, auditFailureRowsPerMinute+1)
	}
}

// TestComponentsWarnUnattributedRowsInProductionOnly pins the root's wiring of
// the missing-attribution WARN: in production a request row recorded without
// client attribution logs it once; development never does, and an attributed
// row never does.
func TestComponentsWarnUnattributedRowsInProductionOnly(t *testing.T) {
	for _, tc := range []struct {
		mode environment.Mode
		want int
	}{
		{environment.ModeProduction, 1},
		{environment.ModeDevelopment, 0},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			var logs bytes.Buffer
			cfg := prodLimiterConfig()
			cfg.RuntimeMode = tc.mode
			cfg.RateLimiter = durableLimiter{}
			cfg.AuditDisabledAcknowledged = false
			cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			c, err := newFixture(testRepositories(Repositories{SecurityEvents: &recorderEvents{}}), cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx := sdk.WithRequestID(context.Background(), "req-unstamped")
			row := securityevent.Input{Type: securityevent.TypeLogout, Status: securityevent.StatusSuccess}
			c.SecurityEvents.Record(authlogic.WithClientInfo(ctx, "203.0.113.7", "agent/1"), row)
			for range 2 {
				c.SecurityEvents.Record(ctx, row)
			}
			if got := strings.Count(logs.String(), "mount ClientInfoMiddleware"); got != tc.want {
				t.Fatalf("missing-attribution WARNs = %d, want %d:\n%s", got, tc.want, logs.String())
			}
		})
	}
}

// TestComponentsAttributeRowsToTheAPIKeyProof drives the via_ attribution
// through the public constructor: the invitation service records through the
// shared recorder, so an invitation_created row written while an act-as-user
// key is the request's proof names the key; the same create without a proof
// carries no via_ key.
func TestComponentsAttributeRowsToTheAPIKeyProof(t *testing.T) {
	events := &recorderEvents{}
	pending := &wiringInvitations{}
	users := &wiringUsers{users: map[string]user.User{"u-owner": {ID: "u-owner"}}}
	cfg := browserBaseConfig()
	c, err := New(testRepositories(Repositories{
		Users:           users,
		ServiceAccounts: &liveServiceAccounts{m: map[string]serviceaccount.ServiceAccount{}},
		APIKeys:         &liveAPIKeys{m: map[string]apikey.APIKey{}},
		SecurityEvents:  events,
		Invitations:     pending,
	}), cfg.TokenSigner, cfg.RuntimeMode, delivery.ModeInProcess,
		WithPassword(PasswordConfig{Hasher: cfg.Hasher, PasswordFlowsDisabled: true}),
		WithDelivery(DeliveryConfig{Mailer: cfg.Mailer, DeliveryEncrypter: stubEncrypter{}}),
		WithInvitations(InvitationsConfig{Granter: stubGranter{}, InviteCheck: allowInvite}),
		WithAdministration(AdministrationConfig{MachineRoutesGate: func(next http.Handler) http.Handler { return next }}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sa, err := c.Authentication.CreateServiceAccount(ctx, "admin", "personal", "", true, "u-owner")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	key, raw, err := c.Authentication.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	keyCtx, err := c.Authentication.Authenticate(ctx, authlogic.CredentialAPIKey, authlogic.TransportHeader, raw)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	created := func(ctx context.Context, invitee string) securityevent.SecurityEvent {
		t.Helper()
		if _, err := c.Invitations.Create(ctx, invitations.CreateInput{ResourceType: "team", ResourceID: "t1", Relation: "member", Identifier: invitee, InvitedBy: "u-owner"}); err != nil {
			t.Fatalf("Invitations.Create: %v", err)
		}
		events.mu.Lock()
		defer events.mu.Unlock()
		for i := len(events.created) - 1; i >= 0; i-- {
			if events.created[i].EventType == securityevent.TypeInvitationCreated {
				return events.created[i]
			}
		}
		t.Fatal("no invitation_created row")
		return securityevent.SecurityEvent{}
	}

	row := created(keyCtx, "bob@example.com")
	want := map[string]any{
		securityevent.DetailViaCredential:       "api_key",
		securityevent.DetailViaAPIKeyID:         key.ID,
		securityevent.DetailViaServiceAccountID: sa.ID,
		securityevent.DetailViaActAsUser:        true,
	}
	for k, v := range want {
		if row.Details[k] != v {
			t.Errorf("invitation_created Details[%q] = %v, want %v (Details %v)", k, row.Details[k], v, row.Details)
		}
	}
	if row.Details["resource_type"] != "team" {
		t.Errorf("the row lost its own Details: %v", row.Details)
	}

	row = created(ctx, "carol@example.com")
	for k := range row.Details {
		if strings.HasPrefix(k, "via_") {
			t.Errorf("an unproven invitation_created row carries %s: %v", k, row.Details)
		}
	}
}
