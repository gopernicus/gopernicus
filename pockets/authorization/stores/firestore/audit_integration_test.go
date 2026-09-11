//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func newAudited(t *testing.T) (*firestoredb.DB, authorization.Repositories, context.Context) {
	t.Helper()
	db, _, _ := newMutations(t)
	repos, err := Repositories(t.Context(), db, WithoutIndexProbe(), WithAudit())
	if err != nil {
		t.Fatal(err)
	}
	return db, repos, audit.WithSource(context.Background(), audit.Source{ActorType: "user", ActorID: "actor", Reason: "maintenance"})
}

func history(t *testing.T, reader audit.Reader) []audit.Record {
	t.Helper()
	page, err := reader.List(context.Background(), audit.Filter{}, list.Request{Limit: 1000, Order: audit.DefaultOrder})
	if err != nil {
		t.Fatal(err)
	}
	if page.HasMore {
		t.Fatal("test history unexpectedly truncated")
	}
	return page.Items
}

func TestAuditRawWriteSurfaceAndNoOps(t *testing.T) {
	_, repos, ctx := newAudited(t)
	r := repos.Relationships
	a := ctfUserset("doc", "d1", "viewer", "group", "g1", "member")
	b := ctf("doc", "d1", "viewer", "user", "u2")
	steps := []struct {
		name  string
		run   func() error
		count int
	}{
		{"create", func() error { return r.CreateRelationships(ctx, []relationships.CreateRelationship{a, b, a}) }, 2},
		{"duplicate", func() error { return r.CreateRelationships(ctx, []relationships.CreateRelationship{a, b}) }, 2},
		{"occupied claim", func() error {
			c := a
			c.Relation = "editor"
			return r.CreateRelationships(ctx, []relationships.CreateRelationship{c})
		}, 2},
		{"replace targets", func() error {
			return r.SetRelationTargets(ctx, "doc", "d1", "viewer", []relationships.CreateRelationship{b, ctf("doc", "d1", "viewer", "user", "u3")})
		}, 4},
		{"exact delete", func() error { return r.DeleteRelationshipTarget(ctx, "doc", "d1", "viewer", user("u3")) }, 5},
		{"exact absent", func() error { return r.DeleteRelationshipTarget(ctx, "doc", "d1", "viewer", user("u3")) }, 5},
		{"subject delete", func() error { return r.DeleteByResourceAndSubject(ctx, "doc", "d1", "user", "u2") }, 6},
		{"seed delete pair", func() error {
			return r.CreateRelationships(ctx, []relationships.CreateRelationship{a, ctf("doc", "d1", "viewer", "group", "g1")})
		}, 8},
		{"pair delete", func() error { return r.DeleteRelationship(ctx, "doc", "d1", "viewer", "group", "g1") }, 10},
		{"seed resource", func() error { return r.CreateRelationships(ctx, []relationships.CreateRelationship{a, b}) }, 12},
		{"resource delete", func() error { return r.DeleteResourceRelationships(ctx, "doc", "d1") }, 14},
		{"assign", func() error {
			return repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "editor"})
		}, 15},
		{"duplicate role", func() error {
			return repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "u1", Role: "editor"})
		}, 15},
		{"unassign", func() error { return repos.Roles.Unassign(ctx, "user", "u1", "editor", "", "") }, 16},
		{"absent unassign", func() error { return repos.Roles.Unassign(ctx, "user", "u1", "editor", "", "") }, 16},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			if err := step.run(); err != nil {
				t.Fatal(err)
			}
			if got := len(history(t, repos.Audit)); got != step.count {
				t.Fatalf("got %d records want %d", got, step.count)
			}
		})
	}
	for _, record := range history(t, repos.Audit) {
		if record.Source.ActorID != "actor" || record.Source.Reason != "maintenance" {
			t.Fatalf("lost source: %+v", record)
		}
	}
}

func TestAuditGuardedDeltasRefusalsAndNoOps(t *testing.T) {
	_, repos, ctx := newAudited(t)
	apply := func(cmd mutations.Command, want mutations.Outcome, count int) {
		t.Helper()
		result, err := repos.Mutations.ApplyGuarded(ctx, cmd, func(context.Context, mutations.StoreDecisionView) error { return nil }, nil)
		if err != nil || result.Outcome != want {
			t.Fatalf("%s result=%+v err=%v", cmd.Operation, result, err)
		}
		if got := len(history(t, repos.Audit)); got != count {
			t.Fatalf("%s audit count %d want %d", cmd.Operation, got, count)
		}
	}
	grant := grantCmd(t, "d1", "viewer", user("u1"))
	apply(grant, mutations.OutcomeApplied, 1)
	apply(grant, mutations.OutcomeNoChange, 1)
	replace := grant
	replace.Operation = mutations.OpReplace
	replace.Relationships = []mutations.RelationshipRow{{Relation: "editor", Subject: user("u1")}}
	apply(replace, mutations.OutcomeApplied, 3)
	roleCmd := mutations.Command{Target: docScope("d1"), Operation: mutations.OpRoleAssign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "u2", Role: "editor"}}}
	apply(roleCmd, mutations.OutcomeApplied, 4)
	apply(mutations.Command{Target: docScope("d1"), Operation: mutations.OpTeardown}, mutations.OutcomeApplied, 6)
	apply(mutations.Command{Target: docScope("d1"), Operation: mutations.OpTeardown}, mutations.OutcomeNoChange, 6)
	apply(grant, mutations.OutcomeApplied, 7)
	apply(mutations.Command{Target: docScope("d1"), Operation: mutations.OpPurge}, mutations.OutcomeApplied, 8)
	refusal := errors.New("guard refuses")
	result, err := repos.Mutations.ApplyGuarded(ctx, grant, func(context.Context, mutations.StoreDecisionView) error { return refusal }, nil)
	if result != nil || err != refusal || len(history(t, repos.Audit)) != 8 {
		t.Fatalf("refusal wrote history: %+v %v", result, err)
	}
}

func TestAuditSourceRequiredEvenForNoOpsAndReaderAvailableWhenOff(t *testing.T) {
	db, repos, ctx := newAudited(t)
	for name, run := range map[string]func() error{
		"empty tuple create": func() error { return repos.Relationships.CreateRelationships(context.Background(), nil) },
		"absent tuple remove": func() error {
			return repos.Relationships.DeleteResourceRelationships(context.Background(), "doc", "missing")
		},
		"absent role remove": func() error { return repos.Roles.Unassign(context.Background(), "user", "u1", "editor", "", "") },
		"mutation no-op": func() error {
			_, err := repos.Mutations.Apply(context.Background(), mutations.Command{Target: docScope("d1"), Operation: mutations.OpPurge}, nil)
			return err
		},
	} {
		if err := run(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("%s accepted missing source: %v", name, err)
		}
	}
	if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{ctf("doc", "d1", "viewer", "user", "u1")}); err != nil {
		t.Fatal(err)
	}
	off, err := Repositories(t.Context(), db, WithoutIndexProbe())
	if err != nil {
		t.Fatal(err)
	}
	if err := off.Relationships.CreateRelationships(context.Background(), []relationships.CreateRelationship{ctf("doc", "d2", "viewer", "user", "u2")}); err != nil {
		t.Fatal(err)
	}
	if got := len(history(t, off.Audit)); got != 1 {
		t.Fatalf("disabled reader saw %d records, want prior 1", got)
	}
}

type failingAuditWriter struct {
	firestoredb.Writer
	failure   error
	calls     *int
	oversized bool
}

func (w failingAuditWriter) Create(ctx context.Context, ref *gcfs.DocumentRef, data any) error {
	if ref.Parent.ID == collectionAudit {
		*w.calls++
		if w.oversized {
			doc := data.(auditDoc)
			doc.Reason = strings.Repeat("x", 11<<20)
			return w.Writer.Create(ctx, ref, doc)
		}
		return w.failure
	}
	return w.Writer.Create(ctx, ref, data)
}

func TestAuditWriteFailureRollsBackFactsAndClaims(t *testing.T) {
	db, repos, ctx := newAudited(t)
	refusal := errors.New("audit write failed")
	calls := 0
	row := newRow(ctf("doc", "d1", "viewer", "user", "u1"))
	err := db.Transact(ctx, func(ctx context.Context) error {
		return (factWrites{creates: []relationshipDoc{row}}).flush(ctx, db, failingAuditWriter{Writer: db.WriterFrom(ctx), failure: refusal, calls: &calls}, true)
	})
	if !errors.Is(err, refusal) || calls != 1 {
		t.Fatalf("audit error=%v calls=%d", err, calls)
	}
	assertTupleDocs(t, db, row, false)
	if got := len(history(t, repos.Audit)); got != 0 {
		t.Fatalf("failed audit left %d records", got)
	}
}

func TestAuditNativeSizeFailureRollsBackFacts(t *testing.T) {
	db, repos, ctx := newAudited(t)
	calls := 0
	row := newRow(ctf("doc", "d1", "viewer", "user", "u1"))
	// Inject an oversized stored payload at the audit writer boundary to exercise
	// the native commit refusal after the fact and claim have been queued.
	err := db.Transact(ctx, func(ctx context.Context) error {
		return (factWrites{creates: []relationshipDoc{row}}).flush(ctx, db, failingAuditWriter{Writer: db.WriterFrom(ctx), calls: &calls, oversized: true}, true)
	})
	if err == nil {
		t.Fatal("native Firestore accepted oversized audit commit")
	}
	assertTupleDocs(t, db, row, false)
	if len(history(t, repos.Audit)) != 0 {
		t.Fatal("native size failure persisted audit")
	}
}

func TestAuditListingsFilterAndPageTiedTimestamps(t *testing.T) {
	db, repos, ctx := newAudited(t)
	same := time.Date(2026, 9, 11, 1, 2, 3, 123456000, time.UTC)
	var changes []audit.Change
	for i := 0; i < 8; i++ {
		tuple := ctf("doc", fmt.Sprintf("d%d", i%2), "viewer", "user", fmt.Sprintf("u%d", i))
		changes = append(changes, audit.Change{Action: audit.ActionAdded, Relationship: &tuple})
	}
	records, err := audit.NewRecords(ctx, changes, same)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Transact(ctx, func(ctx context.Context) error { return appendAudit(ctx, db, db.WriterFrom(ctx), records) }); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{list.ASC, list.DESC} {
		want := append([]audit.Record(nil), records...)
		if direction == list.DESC {
			slices.Reverse(want)
		}
		var got []audit.Record
		cursor := ""
		var second, third list.Page[audit.Record]
		for i := 0; i < 5; i++ {
			page, err := repos.Audit.List(context.Background(), audit.Filter{}, list.Request{Limit: 2, Cursor: cursor, Order: list.NewOrder("occurred_at", direction), WithCount: true})
			if err != nil {
				t.Fatal(err)
			}
			if page.Total == nil || *page.Total != 8 {
				t.Fatalf("bad total: %+v", page.Total)
			}
			got = append(got, page.Items...)
			if i == 1 {
				second = page
			}
			if i == 2 {
				third = page
			}
			if !page.HasMore {
				break
			}
			cursor = page.NextCursor
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s lost tied record order", direction)
		}
		previous, err := repos.Audit.List(ctx, audit.Filter{}, list.Request{Limit: 2, Cursor: third.PreviousCursor, Order: list.NewOrder("occurred_at", direction)})
		if err != nil || !reflect.DeepEqual(previous.Items, second.Items) {
			t.Fatalf("reverse cursor: %v", err)
		}
		offset, err := repos.Audit.List(ctx, audit.Filter{}, list.Request{Limit: 2, Offset: 2, Strategy: list.StrategyOffset, Order: list.NewOrder("occurred_at", direction)})
		if err != nil || !reflect.DeepEqual(offset.Items, second.Items) {
			t.Fatalf("offset: %v", err)
		}
	}
	for mask := 0; mask < 8; mask++ {
		filter := audit.Filter{}
		want := 8
		if mask&1 != 0 {
			filter.ActorType, filter.ActorID = "user", "actor"
		}
		if mask&2 != 0 {
			filter.ResourceType, filter.ResourceID = "doc", "d0"
			want = 4
		}
		if mask&4 != 0 {
			filter.SubjectType, filter.SubjectID = "user", "u0"
			want = 1
		}
		page, err := repos.Audit.List(ctx, filter, list.Request{Limit: 20})
		if err != nil || len(page.Items) != want {
			t.Fatalf("mask %d rows=%d err=%v", mask, len(page.Items), err)
		}
	}
}

func TestRemoveLegacyMutationStoragePreservesFactsAndHistory(t *testing.T) {
	db, repos, ctx := newAudited(t)
	if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{ctf("doc", "d1", "viewer", "user", "u1")}); err != nil {
		t.Fatal(err)
	}
	for _, collection := range []string{"iam_scopes", "iam_mutations"} {
		for i := 0; i < 103; i++ {
			if err := db.WriterFrom(ctx).Set(ctx, db.Doc(collection, fmt.Sprint(i)), map[string]any{"legacy": true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for pass := 0; pass < 2; pass++ {
		if err := RemoveLegacyMutationStorage(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	for _, collection := range []string{"iam_scopes", "iam_mutations"} {
		count, err := db.ReaderFrom(ctx).Count(ctx, db.Collection(collection).Query)
		if err != nil || count != 0 {
			t.Fatalf("legacy %s count=%d err=%v", collection, count, err)
		}
	}
	if len(history(t, repos.Audit)) != 1 {
		t.Fatal("cleanup changed audit history")
	}
	if _, exists := storedRow(t, db, "doc", "d1", "viewer", user("u1")); !exists {
		t.Fatal("cleanup removed live fact")
	}
}
