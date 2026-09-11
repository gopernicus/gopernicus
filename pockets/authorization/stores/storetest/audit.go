package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// RunAudit exercises optional change recording on all three write surfaces.
// factory returns a fresh bundle with an empty guardian policy and recording
// enabled exactly when requested. Audit must always be supplied for reads.
func RunAudit(t *testing.T, factory func(*testing.T, bool) authorization.Repositories) {
	t.Run("Disabled", func(t *testing.T) {
		r := factory(t, false)
		requireAudit(t, r)
		if err := r.Relationships.CreateRelationships(context.Background(), []relationships.CreateRelationship{ct("doc", "d", "viewer", "user", "u")}); err != nil {
			t.Fatal(err)
		}
		mustApply(t, r.Mutations, grant("doc2", "viewer", "u"))
		if got := readAudit(t, r.Audit); len(got) != 0 {
			t.Fatalf("disabled store recorded: %+v", got)
		}
	})
	t.Run("SourceRequired", func(t *testing.T) {
		r := factory(t, true)
		requireAudit(t, r)
		a := ct("doc", "d", "viewer", "user", "u")
		if err := r.Relationships.CreateRelationships(context.Background(), []relationships.CreateRelationship{a}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("unattributed raw write: %v", err)
		}
		if err := r.Roles.Assign(context.Background(), roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "editor"}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("unattributed role: %v", err)
		}
		if got, err := r.Mutations.Apply(context.Background(), grant("d", "viewer", "u"), nil); got != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("unattributed command: %+v %v", got, err)
		}
		if relationExists(t, r, "d", "viewer", "u") || len(readAudit(t, r.Audit)) != 0 {
			t.Fatal("unattributed write published state")
		}
		ctx := audit.WithSource(context.Background(), audit.Source{System: "sync", Reason: "projection"})
		expectAuditChanges(t, r, func() error { return r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{a}) }, []audit.Change{addedRelationship(a)})
		if err := r.Relationships.CreateRelationships(context.Background(), []relationships.CreateRelationship{a}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("unattributed no-op accepted: %v", err)
		}
	})
	t.Run("RawChanges", func(t *testing.T) { specRawAudit(t, factory(t, true)) })
	t.Run("MutationChanges", func(t *testing.T) { specMutationAudit(t, factory(t, true)) })
	t.Run("PurgeBound", func(t *testing.T) { specAuditPurgeBound(t, factory(t, true)) })
	t.Run("PagingAndOwnership", func(t *testing.T) { specAuditPaging(t, factory(t, true)) })
}
func requireAudit(t *testing.T, r authorization.Repositories) {
	t.Helper()
	if r.Audit == nil || r.Relationships == nil || r.Roles == nil || r.Mutations == nil {
		t.Fatal("audit factory must wire reader and all writers")
	}
}
func addedRelationship(r relationships.CreateRelationship) audit.Change {
	return audit.Change{Action: audit.ActionAdded, Relationship: &r}
}
func removedRelationship(r relationships.CreateRelationship) audit.Change {
	return audit.Change{Action: audit.ActionRemoved, Relationship: &r}
}
func addedRole(r roles.Assignment) audit.Change {
	return audit.Change{Action: audit.ActionAdded, Role: &r}
}
func removedRole(r roles.Assignment) audit.Change {
	return audit.Change{Action: audit.ActionRemoved, Role: &r}
}
func readAudit(t *testing.T, r audit.Reader) []audit.Record {
	t.Helper()
	page, err := r.List(context.Background(), audit.Filter{}, list.Request{Limit: 100})
	if err != nil || page.HasMore {
		t.Fatalf("read audit fixture: %+v %v", page, err)
	}
	return page.Items
}
func changeStrings(t *testing.T, changes []audit.Change) []string {
	t.Helper()
	out := make([]string, len(changes))
	for i, c := range changes {
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = string(data)
	}
	slices.Sort(out)
	return out
}
func expectAuditChanges(t *testing.T, r authorization.Repositories, write func() error, want []audit.Change) {
	t.Helper()
	before := map[string]bool{}
	for _, row := range readAudit(t, r.Audit) {
		before[row.ID] = true
	}
	if err := write(); err != nil {
		t.Fatal(err)
	}
	var changes []audit.Change
	eventID := ""
	for _, row := range readAudit(t, r.Audit) {
		if before[row.ID] {
			continue
		}
		if row.EventID == "" || row.ID == "" || row.OccurredAt.IsZero() {
			t.Fatalf("incomplete record: %+v", row)
		}
		if eventID != "" && eventID != row.EventID {
			t.Fatal("one write split into unrelated events")
		}
		eventID = row.EventID
		changes = append(changes, row.Change)
	}
	if !slices.Equal(changeStrings(t, changes), changeStrings(t, want)) {
		t.Fatalf("actual deltas=%+v want=%+v", changeStrings(t, changes), changeStrings(t, want))
	}
}
func specRawAudit(t *testing.T, r authorization.Repositories) {
	requireAudit(t, r)
	ctx := audit.WithSource(context.Background(), audit.Source{System: "sync", Reason: "projection"})
	a := ct("doc", "d", "viewer", "user", "u")
	b := ctUserset("doc", "d", "viewer", "group", "g", "member")
	c := b
	c.SubjectRelation = "admin"
	expectAuditChanges(t, r, func() error {
		return r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{a, a, b})
	}, []audit.Change{addedRelationship(a), addedRelationship(b)})
	expectAuditChanges(t, r, func() error {
		return r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{a, b})
	}, nil)
	conflict := a
	conflict.Relation = "editor"
	expectAuditChanges(t, r, func() error {
		return r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{conflict})
	}, nil)
	expectAuditChanges(t, r, func() error {
		return r.Relationships.SetRelationTargets(ctx, "doc", "d", "viewer", []relationships.CreateRelationship{c})
	}, []audit.Change{removedRelationship(a), removedRelationship(b), addedRelationship(c)})
	expectAuditChanges(t, r, func() error { return r.Relationships.DeleteRelationshipTarget(ctx, "doc", "d", "viewer", c.Subject()) }, []audit.Change{removedRelationship(c)})
	expectAuditChanges(t, r, func() error { return r.Relationships.DeleteRelationshipTarget(ctx, "doc", "d", "viewer", c.Subject()) }, nil)
	for _, method := range []string{"exact", "subject", "resource"} {
		a.ResourceID = method
		expectAuditChanges(t, r, func() error { return r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{a}) }, []audit.Change{addedRelationship(a)})
		expectAuditChanges(t, r, func() error {
			switch method {
			case "exact":
				return r.Relationships.DeleteRelationship(ctx, "doc", method, "viewer", "user", "u")
			case "subject":
				return r.Relationships.DeleteByResourceAndSubject(ctx, "doc", method, "user", "u")
			default:
				return r.Relationships.DeleteResourceRelationships(ctx, "doc", method)
			}
		}, []audit.Change{removedRelationship(a)})
	}
	global := roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "editor"}
	expectAuditChanges(t, r, func() error { return r.Roles.Assign(ctx, global) }, []audit.Change{addedRole(global)})
	expectAuditChanges(t, r, func() error { return r.Roles.Assign(ctx, global) }, nil)
	expectAuditChanges(t, r, func() error { return r.Roles.Unassign(ctx, "user", "u", "editor", "", "") }, []audit.Change{removedRole(global)})
	for _, row := range readAudit(t, r.Audit) {
		if row.Source != (audit.Source{System: "sync", Reason: "projection"}) {
			t.Fatalf("source lost: %+v", row.Source)
		}
	}
}
func specMutationAudit(t *testing.T, r authorization.Repositories) {
	requireAudit(t, r)
	ctx := audit.WithSource(context.Background(), audit.Source{ActorType: "user", ActorID: "actor"})
	apply := func(cmd mutations.Command) func() error {
		return func() error { _, err := r.Mutations.Apply(ctx, cmd, nil); return err }
	}
	a := ct("doc", "d", "viewer", "user", "u")
	b := a
	b.Relation = "editor"
	expectAuditChanges(t, r, apply(grant("d", "viewer", "u")), []audit.Change{addedRelationship(a)})
	expectAuditChanges(t, r, apply(grant("d", "viewer", "u")), nil)
	expectAuditChanges(t, r, apply(replace("d", "editor", "u")), []audit.Change{removedRelationship(a), addedRelationship(b)})
	expectAuditChanges(t, r, apply(revoke("d", "editor", "u")), []audit.Change{removedRelationship(b)})
	expectAuditChanges(t, r, apply(revoke("d", "editor", "u")), nil)
	expectAuditChanges(t, r, apply(grant("d", "viewer", "u")), []audit.Change{addedRelationship(a)})
	expectAuditChanges(t, r, apply(mutations.Command{Target: resTarget("d"), Operation: mutations.OpPurge}), []audit.Change{removedRelationship(a)})
	expectAuditChanges(t, r, apply(grant("d", "viewer", "u")), []audit.Change{addedRelationship(a)})
	assignment := roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "editor", ResourceType: "doc", ResourceID: "d"}
	roleCmd := mutations.Command{Target: resTarget("d"), Operation: mutations.OpRoleAssign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "u", Role: "editor"}}}
	expectAuditChanges(t, r, apply(roleCmd), []audit.Change{addedRole(assignment)})
	roleCmd.Operation = mutations.OpRoleUnassign
	expectAuditChanges(t, r, apply(roleCmd), []audit.Change{removedRole(assignment)})
	roleCmd.Operation = mutations.OpRoleAssign
	expectAuditChanges(t, r, apply(roleCmd), []audit.Change{addedRole(assignment)})
	before := readAudit(t, r.Audit)
	if result, err := r.Mutations.ApplyGuarded(ctx, grant("d", "viewer", "other"), func(context.Context, mutations.StoreDecisionView) error { return sdk.ErrForbidden }, nil); result != nil || !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("denial: %+v %v", result, err)
	}
	if !reflect.DeepEqual(before, readAudit(t, r.Audit)) {
		t.Fatal("denied command recorded history")
	}
	expectAuditChanges(t, r, apply(mutations.Command{Target: resTarget("d"), Operation: mutations.OpTeardown}), []audit.Change{removedRelationship(a), removedRole(assignment)})
}
func specAuditPaging(t *testing.T, r authorization.Repositories) {
	requireAudit(t, r)
	ctx := audit.WithSource(context.Background(), audit.Source{ActorType: "user", ActorID: "actor"})
	rows := []relationships.CreateRelationship{ct("doc", "a", "viewer", "user", "u"), ct("doc", "b", "viewer", "user", "u"), ctUserset("doc", "a", "viewer", "group", "g", "member"), ct("doc", "c", "viewer", "user", "u")}
	if err := r.Relationships.CreateRelationships(ctx, rows); err != nil {
		t.Fatal(err)
	}
	all := readAudit(t, r.Audit)
	for _, direction := range []string{list.ASC, list.DESC} {
		order := list.NewOrder("occurred_at", direction)
		want := slices.Clone(all)
		if direction == list.ASC {
			slices.Reverse(want)
		}
		cursor := ""
		var previous []audit.Record
		for i := range want {
			page, err := r.Audit.List(ctx, audit.Filter{}, list.Request{Limit: 1, Cursor: cursor, Order: order, WithCount: true})
			if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0], want[i]) || page.Total == nil || *page.Total != int64(len(want)) || page.HasPrev != (i > 0) || page.HasMore != (i < len(want)-1) {
				t.Fatalf("page %d: %+v %v", i, page, err)
			}
			if i > 0 {
				back, err := r.Audit.List(ctx, audit.Filter{}, list.Request{Limit: 1, Cursor: page.PreviousCursor, Order: order})
				if err != nil || !reflect.DeepEqual(back.Items, previous) {
					t.Fatalf("reverse page %d: %+v %v", i, back, err)
				}
			}
			offset, err := r.Audit.List(ctx, audit.Filter{}, list.Request{Strategy: list.StrategyOffset, Offset: i, Limit: 1, Order: order})
			if err != nil || !reflect.DeepEqual(offset.Items, page.Items) {
				t.Fatalf("offset page %d: %+v %v", i, offset, err)
			}
			cursor, previous = page.NextCursor, page.Items
		}
	}
	for _, tc := range []struct {
		filter audit.Filter
		count  int
	}{{audit.Filter{ResourceType: "doc", ResourceID: "a"}, 2}, {audit.Filter{SubjectType: "user", SubjectID: "u"}, 3}, {audit.Filter{ActorType: "user", ActorID: "actor"}, 4}, {audit.Filter{ActorType: "user", ActorID: "absent"}, 0}} {
		page, err := r.Audit.List(ctx, tc.filter, list.Request{WithCount: true})
		if err != nil || len(page.Items) != tc.count || page.Total == nil || *page.Total != int64(tc.count) {
			t.Fatalf("filter %+v: %+v %v", tc.filter, page, err)
		}
	}
	first := readAudit(t, r.Audit)
	first[0].Change.Relationship.SubjectID = "changed"
	if reflect.DeepEqual(first, readAudit(t, r.Audit)) {
		t.Fatal("reader returned retained pointer storage")
	}
	if _, err := r.Audit.List(ctx, audit.Filter{ActorType: "user"}, list.Request{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("half actor filter: %v", err)
	}
	wrongType, err := list.EncodeCursor("occurred_at", "not-a-time", "row")
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []list.Request{
		{Cursor: "bad cursor"}, {Cursor: wrongType}, {Search: "viewer"},
		{Order: list.NewOrder("actor_id", list.ASC)}, {Strategy: "invalid"},
		{Strategy: list.StrategyOffset, Offset: -1}, {Offset: 1},
	} {
		if _, err := r.Audit.List(ctx, audit.Filter{}, req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid audit request %+v: %v", req, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Audit.List(canceled, audit.Filter{}, list.Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled audit read: %v", err)
	}
}

func specAuditPurgeBound(t *testing.T, r authorization.Repositories) {
	requireAudit(t, r)
	ctx := audit.WithSource(context.Background(), audit.Source{System: "test"})
	rows := []relationships.CreateRelationship{ct("doc", "bounded", "viewer", "user", "a"), ct("doc", "bounded", "viewer", "user", "b")}
	if err := r.Relationships.CreateRelationships(ctx, rows); err != nil {
		t.Fatal(err)
	}
	before := readAudit(t, r.Audit)
	cmd := mutations.Command{Target: resTarget("bounded"), Operation: mutations.OpPurge, MaxAffectedRows: 1}
	if result, err := r.Mutations.Apply(ctx, cmd, nil); result != nil || !errors.Is(err, mutations.ErrInvariantBlocked) {
		t.Fatalf("overbound purge: %+v %v", result, err)
	}
	if !reflect.DeepEqual(before, readAudit(t, r.Audit)) || !relationExists(t, r, "bounded", "viewer", "a") || !relationExists(t, r, "bounded", "viewer", "b") {
		t.Fatal("refused purge changed facts/history")
	}
	cmd.MaxAffectedRows = 2
	expectAuditChanges(t, r, func() error {
		result, err := r.Mutations.Apply(ctx, cmd, nil)
		if err == nil && result.Outcome != mutations.OutcomeApplied {
			t.Fatalf("at-bound purge: %+v", result)
		}
		return err
	}, []audit.Change{removedRelationship(rows[0]), removedRelationship(rows[1])})
}
