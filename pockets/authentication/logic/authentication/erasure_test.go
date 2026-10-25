package authentication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// eraseIdentifiers serves ListByUser; the rest of the port is unused.
type eraseIdentifiers struct {
	identifier.IdentifierRepository
	byUser map[string][]identifier.Identifier
}

func (f eraseIdentifiers) ListByUser(_ context.Context, userID string) ([]identifier.Identifier, error) {
	return f.byUser[userID], nil
}

// eraseAdmin serves GetSummary and counts SetStatus calls.
type eraseAdmin struct {
	user.AdminRepository
	setStatusCalls int
}

func (f *eraseAdmin) GetSummary(_ context.Context, id string) (user.Summary, error) {
	return user.Summary{ID: id, Status: user.StatusErased}, nil
}

func (f *eraseAdmin) SetStatus(context.Context, string, user.Status, time.Time) (user.StatusChange, error) {
	f.setStatusCalls++
	return user.StatusChange{}, nil
}

// eraseStore records each Erase, the context it ran under and that context's
// state at the call.
type eraseStore struct {
	mu     sync.Mutex
	inputs []user.EraseInput
	ctxs   []context.Context
	errs   []error
	result user.Erasure
	err    error
}

func (f *eraseStore) Erase(ctx context.Context, in user.EraseInput) (user.Erasure, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, in)
	f.ctxs = append(f.ctxs, ctx)
	f.errs = append(f.errs, ctx.Err())
	return f.result, f.err
}

// eraseEvents records rail writes.
type eraseEvents struct {
	mu   sync.Mutex
	rows []securityevent.SecurityEvent
}

func (f *eraseEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, evt)
	return evt, nil
}

func (f *eraseEvents) List(context.Context, securityevent.ListFilter, list.Request) (list.Page[securityevent.SecurityEvent], error) {
	return list.Page[securityevent.SecurityEvent]{}, nil
}

func (f *eraseEvents) recorded() []securityevent.SecurityEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]securityevent.SecurityEvent(nil), f.rows...)
}

var eraseAdminActor = Principal{Type: PrincipalUser, ID: "admin-1"}

func newEraseService(store user.EraseRepository, admin user.AdminRepository, events *eraseEvents) *Service {
	recorder := securityevent.NewRecorder(events, sdk.IDGenerator{}, nil, nil, securityevent.WithClientInfoReader(ClientInfoFromContext))
	return newService(constructorConfig{
		Identifiers: eraseIdentifiers{byUser: map[string][]identifier.Identifier{
			"target": {
				{ID: "i1", UserID: "target", Kind: identifier.KindEmail, NormalizedValue: "target@example.com"},
				{ID: "i2", UserID: "target", Kind: identifier.KindPhone, NormalizedValue: "+15551230000"},
			},
		}},
		UserAdmin:             admin,
		UserErasure:           store,
		SecurityEventRecorder: recorder,
	})
}

func TestEraseUserRefusals(t *testing.T) {
	events := &eraseEvents{}
	unwired := newEraseService(nil, &eraseAdmin{}, events)
	if unwired.UserErasureEnabled() {
		t.Error("UserErasureEnabled without the repository")
	}
	if _, _, err := unwired.EraseUser(context.Background(), eraseAdminActor, "target"); !errors.Is(err, ErrUserErasureUnavailable) || !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("unwired: err = %v, want ErrUserErasureUnavailable", err)
	}
	store := &eraseStore{}
	svc := newEraseService(store, &eraseAdmin{}, events)
	if !svc.UserErasureEnabled() {
		t.Error("UserErasureEnabled false with the repository")
	}
	if _, _, err := svc.EraseUser(context.Background(), Principal{Type: PrincipalUser, ID: "target"}, "target"); !errors.Is(err, ErrEraseSelf) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("self: err = %v, want ErrEraseSelf", err)
	}
	if _, _, err := svc.EraseUser(context.Background(), eraseAdminActor, " "); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("empty id: err = %v, want ErrInvalidInput", err)
	}
	if len(store.inputs) != 0 || len(events.recorded()) != 0 {
		t.Fatalf("a refused erase reached the store (%d) or the rail (%d)", len(store.inputs), len(events.recorded()))
	}
}

func TestEraseUserPreparesTheRowForTheTransaction(t *testing.T) {
	events := &eraseEvents{}
	store := &eraseStore{result: user.Erasure{Changed: true, ErasedAt: time.Now().UTC(), Counts: user.EraseCounts{Sessions: 2, IdentifiersBlanked: 2}}}
	svc := newEraseService(store, &eraseAdmin{}, events)

	parent, cancel := context.WithCancel(sdk.WithRequestID(WithClientInfo(context.Background(), "198.51.100.4", "admin-console/2"), "req-erase"))
	cancel()
	summary, erasure, err := svc.EraseUser(parent, eraseAdminActor, "target")
	if err != nil {
		t.Fatalf("EraseUser: %v", err)
	}
	if summary.ID != "target" || summary.Status != user.StatusErased || !erasure.Changed || erasure.Counts.Sessions != 2 {
		t.Errorf("summary=%+v erasure=%+v", summary, erasure)
	}
	if len(store.inputs) != 1 {
		t.Fatalf("Erase called %d times", len(store.inputs))
	}
	in, ctx := store.inputs[0], store.ctxs[0]
	if store.errs[0] != nil {
		t.Error("the store call inherited the request's cancellation")
	}
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > EraseTimeout {
		t.Errorf("the store call deadline = %v (set %v), want within EraseTimeout", dl, ok)
	}
	if in.ID != "target" || in.Now.IsZero() {
		t.Errorf("input = %+v", in)
	}
	want := []string{
		svc.magicLinkSubjectKey("email", "target@example.com"),
		svc.magicLinkSubjectKey("phone", "+15551230000"),
	}
	if len(in.SubjectKeys) != 2 || in.SubjectKeys[0] != want[0] || in.SubjectKeys[1] != want[1] {
		t.Errorf("SubjectKeys = %v, want %v", in.SubjectKeys, want)
	}
	evt := in.Event
	if evt.EventType != securityevent.TypeUserErased || evt.EventStatus != securityevent.StatusSuccess || evt.UserID != "target" ||
		evt.Actor != (securityevent.Principal{Type: PrincipalUser, ID: "admin-1"}) || evt.ID == "" || evt.CreatedAt.IsZero() {
		t.Errorf("prepared row = %+v", evt)
	}
	if evt.IPAddress != "198.51.100.4" || evt.UserAgent != "admin-console/2" || evt.RequestID != "req-erase" {
		t.Errorf("prepared row lacks the admin's attribution: %+v", evt)
	}
	if len(evt.Details) != 0 {
		t.Errorf("the service put Details on the row (counts are the store's; an address never): %v", evt.Details)
	}
	if rows := events.recorded(); len(rows) != 0 {
		t.Fatalf("the service wrote the row itself (%d rows); the store appends it in its transaction", len(rows))
	}
	merged := erasure.Counts.Merge(evt.Details)
	if merged["sessions"] != 2 || merged["identifiers_blanked"] != 2 || merged["api_keys"] != 0 || len(merged) != 17 {
		t.Errorf("merged Details = %v", merged)
	}
	for key := range merged {
		if key == "email" || key == "identifier" || key == "display_name" {
			t.Errorf("merged Details carry %q", key)
		}
	}
}

func TestEraseUserReportsTheBlockingClaim(t *testing.T) {
	store := &eraseStore{err: user.ErrEraseBlockedByClaim}
	svc := newEraseService(store, &eraseAdmin{}, &eraseEvents{})
	if _, _, err := svc.EraseUser(context.Background(), eraseAdminActor, "target"); !errors.Is(err, user.ErrEraseBlockedByClaim) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("err = %v, want ErrEraseBlockedByClaim (409)", err)
	}
}

func TestSetUserStatusRefusesErasedAsATransition(t *testing.T) {
	admin := &eraseAdmin{}
	svc := newEraseService(&eraseStore{}, admin, &eraseEvents{})
	_, _, err := svc.SetUserStatus(context.Background(), eraseAdminActor, "target", user.StatusErased)
	if !errors.Is(err, user.ErrErased) || !errors.Is(err, sdk.ErrConflict) || errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrErased as a refused transition", err)
	}
	if admin.setStatusCalls != 0 {
		t.Error("the refused transition reached the store")
	}
}

// cascadingAccounts adds the cascade capability to the machine fake.
type cascadingAccounts struct {
	*fakeServiceAccounts
	deletion serviceaccount.Deletion
}

func (f cascadingAccounts) DeleteWithKeys(ctx context.Context, id string) (serviceaccount.Deletion, error) {
	if err := f.Delete(ctx, id); err != nil {
		return serviceaccount.Deletion{}, err
	}
	return f.deletion, nil
}

func TestDeleteServiceAccountRecordsCountsWithAndWithoutCascade(t *testing.T) {
	for _, cascade := range []bool{true, false} {
		accounts := newFakeServiceAccounts()
		accounts.m["sa1"] = serviceaccount.ServiceAccount{ID: "sa1", ActAsUser: true, OwnerUserID: "owner-1"}
		var repo serviceaccount.ServiceAccountRepository = accounts
		if cascade {
			repo = cascadingAccounts{accounts, serviceaccount.Deletion{KeyCount: 3, UnrevokedKeyCount: 2}}
		}
		events := &eraseEvents{}
		svc := newService(constructorConfig{
			ServiceAccounts:       repo,
			APIKeys:               newFakeAPIKeys(),
			SecurityEventRecorder: securityevent.NewRecorder(events, sdk.IDGenerator{}, nil, nil),
		})
		deletion, err := svc.DeleteServiceAccount(context.Background(), eraseAdminActor, "sa1")
		if err != nil {
			t.Fatalf("cascade=%v: %v", cascade, err)
		}
		if _, ok := accounts.m["sa1"]; ok {
			t.Errorf("cascade=%v: the account survived", cascade)
		}
		wantKeys := 0
		if cascade {
			wantKeys = 3
		}
		if deletion.KeyCount != wantKeys {
			t.Errorf("cascade=%v: deletion = %+v", cascade, deletion)
		}
		rows := events.recorded()
		if len(rows) != 1 {
			t.Fatalf("cascade=%v: %d rows", cascade, len(rows))
		}
		row := rows[0]
		if row.EventType != securityevent.TypeServiceAccountDeleted || row.UserID != "owner-1" || row.Actor.ID != "admin-1" ||
			row.Details["service_account_id"] != "sa1" || row.Details["key_count"] != wantKeys || row.Details["cascade"] != cascade || len(row.Details) != 4 {
			t.Errorf("cascade=%v: row = %+v", cascade, row)
		}
		if _, err := svc.DeleteServiceAccount(context.Background(), eraseAdminActor, "sa1"); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("cascade=%v: a repeat delete = %v, want ErrNotFound", cascade, err)
		}
	}
	if _, err := newService(constructorConfig{}).DeleteServiceAccount(context.Background(), eraseAdminActor, "sa1"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("unwired machine identity: err = %v", err)
	}
}
