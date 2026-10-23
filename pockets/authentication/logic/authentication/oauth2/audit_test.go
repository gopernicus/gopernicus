package oauth2

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type auditEvents struct{ created []securityevent.SecurityEvent }

func (a *auditEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	a.created = append(a.created, evt)
	return evt, nil
}

func (a *auditEvents) List(context.Context, securityevent.ListFilter, list.Request) (list.Page[securityevent.SecurityEvent], error) {
	return list.Page[securityevent.SecurityEvent]{}, nil
}

func auditService(t *testing.T, events securityevent.SecurityEventRepository, opts ...Option) *Service {
	t.Helper()
	s, err := New(Repositories{OAuth2: &constructorRepository{}, Users: &constructorUsers{}, Sessions: &constructorSessions{}, SecurityEvents: events},
		&constructorSigner{}, clientResolverFunc(func(context.Context, string) (Client, error) { return Client{}, ErrInvalidClient }),
		&constructorVerifier{}, environment.ModeDevelopment, protocolConfig(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOAuth2TypesAliasTheRegistry(t *testing.T) {
	for _, tc := range []struct{ alias, registered string }{
		{TypeConsentApproved, securityevent.TypeOAuth2ConsentApproved},
		{TypeConnectionCreated, securityevent.TypeOAuth2ConnectionCreated},
		{TypeRefresh, securityevent.TypeOAuth2Refresh},
		{TypeRefreshReuse, securityevent.TypeOAuth2RefreshReuse},
		{TypeTokenExchanged, securityevent.TypeOAuth2TokenExchanged},
		{TypeRevocationRequested, securityevent.TypeOAuth2RevocationRequested},
	} {
		if tc.alias != tc.registered {
			t.Errorf("alias %q != registered %q", tc.alias, tc.registered)
		}
	}
}

func TestOAuth2AuditRecordsThroughTheSuppliedRecorder(t *testing.T) {
	shared := &auditEvents{}
	rec := securityevent.NewRecorder(shared, sdk.NewIDGenerator(func() (string, error) { return "host-id", nil }), nil, nil,
		securityevent.WithClientInfoReader(func(context.Context) (string, string) { return "198.51.100.4", "client/1" }))
	ignored := &auditEvents{}
	s := auditService(t, ignored, WithSecurityEventRecorder(rec))

	ctx := sdk.WithPrincipal(sdk.WithRequestID(context.Background(), "req-oauth"), sdk.Principal{Type: "user", ID: "u-1"})
	s.audit(ctx, TypeConsentApproved, "u-1", "sess-1", "https://client.example")
	if len(ignored.created) != 0 || len(shared.created) != 1 {
		t.Fatalf("recorder rows = %d, repository rows = %d; the recorder must win", len(shared.created), len(ignored.created))
	}
	evt := shared.created[0]
	if evt.ID != "host-id" || evt.IPAddress != "198.51.100.4" || evt.UserAgent != "client/1" || evt.RequestID != "req-oauth" ||
		evt.Actor != (securityevent.Principal{Type: "user", ID: "u-1"}) || evt.Details["client_id"] != "https://client.example" || evt.Details["session_id"] != "sess-1" {
		t.Fatalf("event = %+v", evt)
	}
}

func TestOAuth2AuditOwnRecorderHasNoClientInfo(t *testing.T) {
	events := &auditEvents{}
	s := auditService(t, events)
	s.audit(context.Background(), TypeRefreshReuse, "u-1", "sess-1", "https://client.example")
	if len(events.created) != 1 {
		t.Fatalf("recorded %d rows, want 1", len(events.created))
	}
	evt := events.created[0]
	if evt.EventStatus != securityevent.StatusBlocked || evt.IPAddress != "" || evt.UserAgent != "" || evt.ID == "" {
		t.Fatalf("event = %+v", evt)
	}
	if none := auditService(t, nil); none.events.Repository() != nil {
		t.Fatal("a service without SecurityEvents must record nothing")
	}
}
