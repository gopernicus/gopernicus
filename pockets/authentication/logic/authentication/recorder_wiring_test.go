package authentication

import (
	"context"
	"errors"
	"strings"
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

// attributionAdmin reports every SetStatus as an applied deactivation, so the
// status-change row is written.
type attributionAdmin struct{ eraseAdmin }

func (*attributionAdmin) SetStatus(context.Context, string, user.Status, time.Time) (user.StatusChange, error) {
	return user.StatusChange{Changed: true, Status: user.StatusDeactivated}, nil
}

// newAttributionService builds a Service over its own fallback recorder — the
// one the via_ reader is wired into — with machine identity, user
// administration and erasure, and one seeded owner u-owner.
func newAttributionService(t *testing.T) (*Service, *eraseEvents, *eraseStore) {
	t.Helper()
	users := newFakeUsers()
	users.byID["u-owner"] = user.User{ID: "u-owner"}
	events, store := &eraseEvents{}, &eraseStore{result: user.Erasure{Changed: true}}
	svc := newService(constructorConfig{
		Users:           users,
		Identifiers:     eraseIdentifiers{},
		ServiceAccounts: newFakeServiceAccounts(),
		APIKeys:         newFakeAPIKeys(),
		UserAdmin:       &attributionAdmin{},
		UserErasure:     store,
		SecurityEvents:  events,
	})
	return svc, events, store
}

// keyContext authenticates a fresh key of svc as a header bearer and returns
// the request context carrying its proof.
func keyContext(t *testing.T, svc *Service, actAsUser bool) (context.Context, Credential) {
	t.Helper()
	ctx := context.Background()
	owner := ""
	if actAsUser {
		owner = "u-owner"
	}
	sa, err := svc.CreateServiceAccount(ctx, "admin", "bot", "", actAsUser, owner)
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	_, raw, err := svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	if err != nil {
		t.Fatalf("MintAPIKey: %v", err)
	}
	keyCtx, err := svc.Authenticate(ctx, CredentialAPIKey, TransportHeader, raw)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	cred, _ := svc.CurrentCredential(keyCtx)
	return keyCtx, cred
}

// requireVia asserts details carry the via_ attribution of cred.
func requireVia(t *testing.T, details map[string]any, cred Credential) {
	t.Helper()
	want := map[string]any{
		securityevent.DetailViaCredential:       "api_key",
		securityevent.DetailViaAPIKeyID:         cred.APIKeyID,
		securityevent.DetailViaServiceAccountID: cred.ServiceAccountID,
		securityevent.DetailViaActAsUser:        cred.ActAsUser,
	}
	for k, v := range want {
		if details[k] != v {
			t.Errorf("Details[%q] = %v, want %v (Details %v)", k, details[k], v, details)
		}
	}
}

// requireNoVia asserts details carry no via_ key.
func requireNoVia(t *testing.T, details map[string]any) {
	t.Helper()
	for k := range details {
		if strings.HasPrefix(k, "via_") {
			t.Errorf("Details carry %s: %v", k, details)
		}
	}
}

// TestRowsUnderAnAPIKeyProofCarryCredentialAttribution pins the via_ details
// on the status-change row (Record) and the erase row (Prepare, handed to the
// store's transaction): an act-as-user key carries via_act_as_user=true beside
// its owner Actor, a self-acting key false; a session row and a headless row
// carry none.
func TestRowsUnderAnAPIKeyProofCarryCredentialAttribution(t *testing.T) {
	for _, actAsUser := range []bool{true, false} {
		svc, events, store := newAttributionService(t)
		ctx, cred := keyContext(t, svc, actAsUser)
		if cred.Kind != CredentialAPIKey || cred.ActAsUser != actAsUser {
			t.Fatalf("credential = %+v", cred)
		}
		actor, _ := sdk.PrincipalFromContext(ctx)

		before := len(events.recorded())
		if _, _, err := svc.DeactivateUser(ctx, actor, "target"); err != nil {
			t.Fatalf("DeactivateUser: %v", err)
		}
		rows := events.recorded()[before:]
		if len(rows) != 1 || rows[0].EventType != securityevent.TypeUserDeactivated {
			t.Fatalf("act_as_user=%v: rows = %+v", actAsUser, rows)
		}
		requireVia(t, rows[0].Details, cred)
		if rows[0].Details["status"] != string(user.StatusDeactivated) {
			t.Errorf("the row lost its own Details: %v", rows[0].Details)
		}
		if rows[0].Actor.ID != actor.ID {
			t.Errorf("Actor = %+v, want the effective principal %+v", rows[0].Actor, actor)
		}

		if _, _, err := svc.EraseUser(ctx, actor, "target"); err != nil {
			t.Fatalf("EraseUser: %v", err)
		}
		requireVia(t, store.inputs[len(store.inputs)-1].Event.Details, cred)
	}

	svc, events, store := newAttributionService(t)
	session := svc.withAuthenticatedPrincipal(context.Background(), Principal{Type: PrincipalUser, ID: "admin-1"}, Credential{Kind: CredentialAccessToken, Transport: TransportCookie, SessionID: "s1"})
	for _, ctx := range []context.Context{session, context.Background()} {
		if _, _, err := svc.DeactivateUser(ctx, eraseAdminActor, "target"); err != nil {
			t.Fatalf("DeactivateUser: %v", err)
		}
		if _, _, err := svc.EraseUser(ctx, eraseAdminActor, "target"); err != nil {
			t.Fatalf("EraseUser: %v", err)
		}
	}
	for _, row := range events.recorded() {
		requireNoVia(t, row.Details)
	}
	for _, in := range store.inputs {
		requireNoVia(t, in.Event.Details)
	}
}

// TestCredentialAttributionIsOwnerBound proves the reader answers only for a
// proof this exact Service stamped: another Service's API-key proof attributes
// nothing on its rows, and a nil Service — a late-bound reader before its
// assignment — reports nothing without panicking.
func TestCredentialAttributionIsOwnerBound(t *testing.T) {
	owner, _, _ := newAttributionService(t)
	ctx, cred := keyContext(t, owner, true)
	if got, ok := owner.CredentialAttribution(ctx); !ok || got != (securityevent.CredentialAttribution{Kind: "api_key", APIKeyID: cred.APIKeyID, ServiceAccountID: cred.ServiceAccountID, ActAsUser: true}) {
		t.Fatalf("owner attribution = %+v, %v", got, ok)
	}

	foreign, events, _ := newAttributionService(t)
	if _, ok := foreign.CredentialAttribution(ctx); ok {
		t.Fatal("a foreign Service attributed another Service's proof")
	}
	actor, _ := sdk.PrincipalFromContext(ctx)
	if _, _, err := foreign.DeactivateUser(ctx, actor, "target"); err != nil {
		t.Fatalf("DeactivateUser: %v", err)
	}
	rows := events.recorded()
	if len(rows) == 0 {
		t.Fatal("no row recorded")
	}
	for _, row := range rows {
		requireNoVia(t, row.Details)
	}

	var late *Service
	rec := securityevent.NewRecorder(events, sdk.IDGenerator{}, nil, nil,
		securityevent.WithCredentialAttributionReader(func(ctx context.Context) (securityevent.CredentialAttribution, bool) {
			return late.CredentialAttribution(ctx)
		}))
	evt, _ := rec.Prepare(ctx, securityevent.Input{Type: securityevent.TypeLogout, Status: securityevent.StatusSuccess})
	requireNoVia(t, evt.Details)
	late = owner
	evt, _ = rec.Prepare(ctx, securityevent.Input{Type: securityevent.TypeLogout, Status: securityevent.StatusSuccess})
	requireVia(t, evt.Details, cred)
}
