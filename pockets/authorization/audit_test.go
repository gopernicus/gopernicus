package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func auditHost(t *testing.T, guard mutations.MutationGuard) (Components, *memory.Store) {
	t.Helper()
	store := memory.New(memory.WithAudit())
	components, err := New(Repositories{Relationships: store.Relationships(), Roles: store.Roles(), Mutations: store.Mutations(), Audit: store.Audit()}, WithRelationshipModel(lifecycleModel()), WithGuard(guard))
	if err != nil {
		t.Fatal(err)
	}
	return components, store
}

func auditRecords(t *testing.T, store *memory.Store) []audit.Record {
	t.Helper()
	page, err := store.Audit().List(context.Background(), audit.Filter{}, list.Request{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

func TestGuardedAuditUsesActorAndRecordsOnlyCommittedChanges(t *testing.T) {
	guard := &stubGuard{}
	components, store := auditHost(t, guard)
	// A caller cannot use metadata to attribute their guarded write to someone else.
	ctx := audit.WithSource(context.Background(), audit.Source{System: "spoofed-system", Reason: "support ticket 42"})
	cmd := mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2")}
	for _, want := range []mutations.Outcome{mutations.OutcomeApplied, mutations.OutcomeNoChange} {
		got, err := components.Mutations.GrantRelationship(ctx, actorU1(), cmd)
		if err != nil || got == nil || got.Outcome != want {
			t.Fatalf("grant: %+v, %v", got, err)
		}
	}
	guard.err = sdk.ErrForbidden
	if got, err := components.Mutations.GrantRelationship(ctx, actorU1(), cmd); !errors.Is(err, sdk.ErrForbidden) || got != nil {
		t.Fatalf("guard must run on repeated command: %+v, %v", got, err)
	}
	guard.err = nil
	cmd.Relation = "editor"
	if got, err := components.Mutations.GrantRelationship(ctx, actorU1(), cmd); !errors.Is(err, mutations.ErrSemanticConflict) || got != nil {
		t.Fatalf("conflict: %+v, %v", got, err)
	}
	records := auditRecords(t, store)
	if len(records) != 1 {
		t.Fatalf("only applied change belongs in history: %+v", records)
	}
	got := records[0]
	if got.Source != (audit.Source{ActorType: "user", ActorID: "u1", Reason: "support ticket 42"}) {
		t.Fatalf("attribution: %+v", got.Source)
	}
	if got.Change.Action != audit.ActionAdded || got.Change.Relationship == nil || got.Change.Relationship.Relation != "viewer" || got.Change.Relationship.SubjectID != "u2" {
		t.Fatalf("actual change: %+v", got.Change)
	}
}

func TestTrustedAuditRequiresSourceAndTeardownRecordsReason(t *testing.T) {
	components, store := auditHost(t, &stubGuard{})
	ctx := context.Background()
	grant := mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "owner", Subject: subjU("u1")}
	if got, err := components.SystemMutator.GrantRelationship(ctx, grant); !errors.Is(err, sdk.ErrInvalidInput) || got != nil {
		t.Fatalf("missing source: %+v, %v", got, err)
	}
	if len(auditRecords(t, store)) != 0 {
		t.Fatal("refused change was recorded")
	}
	ctx = audit.WithSource(ctx, audit.Source{System: "bootstrap"})
	if _, err := components.SystemMutator.GrantRelationship(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if _, err := components.SystemMutator.AssignRole(ctx, mutations.AssignRoleCommand{Subject: prinU("u2"), Role: "reader", ResourceType: "doc", ResourceID: "d1"}); err != nil {
		t.Fatal(err)
	}
	ctx = audit.WithSource(ctx, audit.Source{System: "resource-cleanup", Reason: "old reason"})
	got, err := components.SystemMutator.TeardownResourceAuthorization(ctx, mutations.TeardownResourceAuthorizationCommand{ResourceType: "doc", ResourceID: "d1", Reason: "resource was deleted"})
	if err != nil || got == nil || got.Outcome != mutations.OutcomeApplied {
		t.Fatalf("teardown: %+v, %v", got, err)
	}
	records := auditRecords(t, store)
	if len(records) != 4 {
		t.Fatalf("two additions and two removals: %+v", records)
	}
	event := ""
	removals := 0
	for _, record := range records {
		if record.Change.Action != audit.ActionRemoved {
			continue
		}
		removals++
		if record.Source != (audit.Source{System: "resource-cleanup", Reason: "resource was deleted"}) {
			t.Fatalf("teardown source: %+v", record.Source)
		}
		if event != "" && event != record.EventID {
			t.Fatal("one teardown split into separate audit events")
		}
		event = record.EventID
	}
	if removals != 2 {
		t.Fatalf("removed facts: %d", removals)
	}
}

func TestBaselineWriterParticipatesInAudit(t *testing.T) {
	components, store := auditHost(t, nil)
	rows := []relationships.CreateRelationship{{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", SubjectType: "user", SubjectID: "u2"}}
	if err := components.RelationshipWriter.CreateRelationships(context.Background(), rows); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("raw source requirement: %v", err)
	}
	ctx := audit.WithSource(context.Background(), audit.Source{System: "import"})
	if err := components.RelationshipWriter.CreateRelationships(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := components.RelationshipWriter.CreateRelationships(ctx, rows); err != nil {
		t.Fatal(err)
	}
	records := auditRecords(t, store)
	if len(records) != 1 || records[0].Source.System != "import" {
		t.Fatalf("raw history: %+v", records)
	}
}

func TestBaselineEmptyCreateEnforcesSourceAndCancellation(t *testing.T) {
	components, store := auditHost(t, nil)
	if err := components.RelationshipWriter.CreateRelationships(context.Background(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty batch without source: %v", err)
	}
	ctx := audit.WithSource(context.Background(), audit.Source{System: "import"})
	if err := components.RelationshipWriter.CreateRelationships(ctx, nil); err != nil {
		t.Fatalf("valid empty batch: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := components.RelationshipWriter.CreateRelationships(canceled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled empty batch: %v", err)
	}
	if len(auditRecords(t, store)) != 0 {
		t.Fatal("empty batch added history")
	}
}
