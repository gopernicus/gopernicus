package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// directoryAdmin is a user.AdminRepository whose reads answer from a fixed
// summary, or fail with err.
type directoryAdmin struct {
	user.AdminRepository
	err error
}

func (d directoryAdmin) List(context.Context, list.Request) (list.Page[user.Summary], error) {
	if d.err != nil {
		return list.Page[user.Summary]{}, d.err
	}
	return list.Page[user.Summary]{Items: []user.Summary{{ID: "u-1", DisplayName: "Ada", PrimaryEmail: "ada@example.com"}}}, nil
}

func (d directoryAdmin) GetSummary(_ context.Context, id string) (user.Summary, error) {
	if d.err != nil {
		return user.Summary{}, d.err
	}
	return user.Summary{ID: id, DisplayName: "Ada", PrimaryEmail: "ada@example.com"}, nil
}

func TestUserDirectoryReadRecordsActor(t *testing.T) {
	h := newHarness(t, nil)
	h.svc.userAdmin = directoryAdmin{}
	ctx := sdk.WithPrincipal(context.Background(), sdk.Principal{Type: "user", ID: "admin-1"})

	if _, err := h.svc.ListUsers(ctx, list.Request{Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.GetUserSummary(ctx, "u-1"); err != nil {
		t.Fatal(err)
	}
	var reads []securityevent.SecurityEvent
	for _, e := range h.events.recorded() {
		if e.EventType == securityevent.TypeUserDirectoryRead {
			reads = append(reads, e)
		}
	}
	if len(reads) != 2 {
		t.Fatalf("recorded %d user_directory_read rows, want one per call (2)", len(reads))
	}
	for _, e := range reads {
		if e.EventStatus != securityevent.StatusSuccess || e.Actor != (securityevent.Principal{Type: "user", ID: "admin-1"}) || e.UserID != "" {
			t.Errorf("directory read = %+v, want success attributed to the admin actor", e)
		}
		for _, v := range e.Details {
			if v == "Ada" || v == "ada@example.com" {
				t.Errorf("directory read Details leaked a directory value: %v", e.Details)
			}
		}
	}
	if reads[0].Details["page"] != true || len(reads[0].Details) != 1 {
		t.Errorf("page read Details = %v, want {page: true}", reads[0].Details)
	}
	if reads[1].Details["page"] != false || reads[1].Details["target_user_id"] != "u-1" {
		t.Errorf("summary read Details = %v, want {page: false, target_user_id: u-1}", reads[1].Details)
	}

	before := h.events.count()
	h.svc.userAdmin = directoryAdmin{err: sdk.ErrNotFound}
	if _, err := h.svc.GetUserSummary(ctx, "missing"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("GetUserSummary = %v, want not found", err)
	}
	if _, err := h.svc.ListUsers(ctx, list.Request{Limit: 10}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("ListUsers = %v, want the store error", err)
	}
	if h.events.count() != before {
		t.Fatal("a failed directory read recorded a row")
	}
}

func TestServiceAndExternalShareOneRecorder(t *testing.T) {
	h := newHarness(t, nil)
	svc, _, _ := hybridService(t, h, nil)
	if svc.securityEvents == nil || svc.external == nil || svc.external.events != svc.securityEvents {
		t.Fatal("the external engine New builds does not record through the service's recorder")
	}

	rec := securityevent.NewRecorder(h.events, sdk.IDGenerator{}, time.Now, nil)
	components, err := New(Repositories{
		Users: h.users, Identifiers: h.idents, Sessions: h.sess, ActiveSessions: fakeActiveSessions{h.users, h.sess},
		ExternalSubjects: extSubjects{newExtStore()},
	}, h.signer, environment.ModeDevelopment, ratelimiter.NewMemory(),
		WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
		WithDelegatedTokens(DelegatedTokensConfig{Issuer: delegatedTestIssuer}),
		WithExternal(ExternalConfig{Providers: []ExternalProvider{{Name: "alpha", Provider: &extProvider{issuer: extIssuerA}}}}),
		WithSecurityEventRecorder(rec))
	if err != nil {
		t.Fatal(err)
	}
	if components.Service.securityEvents != rec || components.Service.ExternalAuthenticator().events != rec {
		t.Fatal("WithSecurityEventRecorder did not reach the service and its external engine")
	}
}

func TestAuditWriteObserverRequiresTheBuiltRecorder(t *testing.T) {
	h := newHarness(t, nil)
	repos := Repositories{Users: h.users, Identifiers: h.idents, Sessions: h.sess, ActiveSessions: fakeActiveSessions{h.users, h.sess}, SecurityEvents: h.events}
	observer := func(context.Context, securityevent.WriteFailure) {}
	rec := securityevent.NewRecorder(h.events, sdk.IDGenerator{}, nil, nil)
	if _, err := New(repos, h.signer, environment.ModeDevelopment, ratelimiter.NewMemory(),
		WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
		WithSecurityEventRecorder(rec), WithAuditWriteObserver(observer)); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("recorder + observer = %v, want invalid input", err)
	}

	var observed []securityevent.WriteFailure
	h.events.createErr = errors.New("audit store down")
	components, err := New(repos, h.signer, environment.ModeDevelopment, ratelimiter.NewMemory(),
		WithPassword(PasswordConfig{PasswordFlowsDisabled: true}),
		WithAuditWriteObserver(func(_ context.Context, f securityevent.WriteFailure) { observed = append(observed, f) }))
	if err != nil {
		t.Fatal(err)
	}
	components.Service.recordSecurityEvent(context.Background(), securityEventInput{Type: securityevent.TypeLogout, Status: securityevent.StatusSuccess})
	if len(observed) != 1 || observed[0].EventType != securityevent.TypeLogout || components.Service.securityEvents.WriteFailures() != 1 {
		t.Fatalf("observer saw %+v, failures=%d", observed, components.Service.securityEvents.WriteFailures())
	}
}

func TestStandaloneExternalTakesTheRecorderOption(t *testing.T) {
	h := newHarness(t, nil)
	repos := ExternalRepositories{Users: h.users, Identifiers: h.idents, ExternalSubjects: extSubjects{newExtStore()}, SecurityEvents: h.events}
	cfg := ExternalConfig{Providers: []ExternalProvider{{Name: "alpha", Provider: &extProvider{issuer: extIssuerA}}}}
	rec := securityevent.NewRecorder(h.events, sdk.IDGenerator{}, nil, nil)
	engine, err := NewExternalAuthenticator(repos, cfg, environment.ModeDevelopment, WithExternalSecurityEventRecorder(rec))
	if err != nil {
		t.Fatal(err)
	}
	if engine.events != rec {
		t.Fatal("WithExternalSecurityEventRecorder did not reach the engine")
	}
	own, err := NewExternalAuthenticator(repos, cfg, environment.ModeDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	if own.events == nil || own.events == rec || own.events.Repository() != h.events {
		t.Fatal("a standalone engine without the option does not record over ExternalRepositories.SecurityEvents")
	}
}
