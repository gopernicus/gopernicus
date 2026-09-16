package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func auditHost(t *testing.T) (Components, *memory.Store) {
	t.Helper()
	store := memory.New(memory.WithAudit())
	components, err := New(Repositories{Tuples: store.Tuples(), Mutations: store.Mutations(), Audit: store.Audit()}, WithModel(lifecycleModel()))
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

func TestTupleAuditUsesSourceAndRecordsOnlyCommittedChanges(t *testing.T) {
	components, store := auditHost(t)
	// A caller cannot use metadata to attribute their guarded write to someone else.
	ctx := audit.WithSource(context.Background(), audit.Source{ActorType: "user", ActorID: "u1", Reason: "support ticket 42"})
	cmd := mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "viewer", Subject: subjU("u2")}
	for _, want := range []mutations.Outcome{mutations.OutcomeApplied, mutations.OutcomeNoChange} {
		got, err := components.Mutations.GrantRelationship(ctx, cmd)
		if err != nil || got == nil || got.Outcome != want {
			t.Fatalf("grant: %+v, %v", got, err)
		}
	}
	cmd.Relation = "editor"
	if got, err := components.Mutations.GrantRelationship(ctx, cmd); err != nil || got == nil || got.Outcome != mutations.OutcomeApplied {
		t.Fatalf("independent fact: %+v, %v", got, err)
	}
	records := auditRecords(t, store)
	if len(records) != 2 {
		t.Fatalf("only applied facts belong in history: %+v", records)
	}
	labels := map[string]bool{}
	for _, got := range records {
		if got.Source != (audit.Source{ActorType: "user", ActorID: "u1", Reason: "support ticket 42"}) {
			t.Fatalf("attribution: %+v", got.Source)
		}
		if got.Change.Action != audit.ActionAdded || got.Change.Tuple.Subject.ID != "u2" {
			t.Fatalf("actual change: %+v", got.Change)
		}
		labels[got.Change.Tuple.Relation] = true
	}
	if !labels["viewer"] || !labels["editor"] {
		t.Fatalf("coexisting facts: %v", labels)
	}
}

func TestTrustedAuditRequiresSourceAndTeardownRecordsReason(t *testing.T) {
	components, store := auditHost(t)
	ctx := context.Background()
	grant := mutations.GrantRelationshipCommand{ResourceType: "doc", ResourceID: "d1", Relation: "owner", Subject: subjU("u1")}
	if got, err := components.Mutations.GrantRelationship(ctx, grant); !errors.Is(err, sdk.ErrInvalidInput) || got != nil {
		t.Fatalf("missing source: %+v, %v", got, err)
	}
	if len(auditRecords(t, store)) != 0 {
		t.Fatal("refused change was recorded")
	}
	ctx = audit.WithSource(ctx, audit.Source{System: "bootstrap"})
	if _, err := components.Mutations.GrantRelationship(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if _, err := components.Mutations.AssignRole(ctx, mutations.AssignRoleCommand{Subject: prinU("u2"), Role: "reader", Scope: tuples.On("doc", "d1")}); err != nil {
		t.Fatal(err)
	}
	ctx = audit.WithSource(ctx, audit.Source{System: "resource-cleanup", Reason: "old reason"})
	got, err := components.Mutations.TeardownResourceAuthorization(ctx, mutations.TeardownResourceAuthorizationCommand{ResourceType: "doc", ResourceID: "d1", Reason: "resource was deleted"})
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
	components, store := auditHost(t)
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
	components, store := auditHost(t)
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
