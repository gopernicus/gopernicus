package authentication

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"sync"
	"testing"

	inbound "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
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
