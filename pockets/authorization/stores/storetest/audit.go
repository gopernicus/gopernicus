package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

var uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// RunAudit exercises optional change recording on all three write surfaces.
// factory returns a fresh bundle with an empty integrity policy and recording
// enabled exactly when requested. Audit must always be supplied for reads.
// Tuples and Mutations must report the request through audit.RecordingReporter.
func RunAudit(t *testing.T, factory func(*testing.T, bool) Repositories) {
	t.Run("RecordingCapability", func(t *testing.T) {
		for _, enabled := range []bool{false, true} {
			t.Run(strconv.FormatBool(enabled), func(t *testing.T) { requireRecording(t, factory(t, enabled), enabled) })
		}
	})
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
		if err := assignRole(context.Background(), r.Tuples, roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "editor", Scope: fixtureScope("", "")}); !errors.Is(err, sdk.ErrInvalidInput) {
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
	t.Run("FenceWritesNoAudit", func(t *testing.T) {
		r := factory(t, true)
		requireAudit(t, r)
		if r.Mutations == nil {
			t.Skip("mutation repository not wired")
		}
		if r.Operations == nil {
			t.Fatal("Repositories.Mutations is wired but Repositories.Operations is nil")
		}
		ctx := audit.WithSource(context.Background(), audit.Source{System: "fence-audit"})
		if _, err := r.Mutations.Apply(ctx, grant("d", "owner", "o"), nil); err != nil {
			t.Fatal(err)
		}
		committed := grant("d", "viewer", "u")
		committed.OperationID = "invitation:committed"
		if _, err := r.Mutations.Apply(ctx, committed, nil); err != nil {
			t.Fatal(err)
		}
		before := readAudit(t, r.Audit)
		got, err := r.Operations.Fence(context.Background(), "invitation:unattributed")
		if err != nil || got == nil || got.Outcome != mutations.OutcomeFenced || got.Recorded {
			t.Fatalf("unattributed fence: %+v %v", got, err)
		}
		got, err = r.Operations.Fence(context.Background(), committed.OperationID)
		if err != nil || got == nil || got.Outcome != mutations.OutcomeApplied || !got.Recorded {
			t.Fatalf("fence of committed ID: %+v %v", got, err)
		}
		if after := readAudit(t, r.Audit); len(after) != len(before) {
			t.Fatalf("fence recorded audit: before=%d after=%d", len(before), len(after))
		}
	})
	t.Run("RawChanges", func(t *testing.T) { specRawAudit(t, factory(t, true)) })
	t.Run("DefaultEventID", func(t *testing.T) {
		r := factory(t, true)
		requireAudit(t, r)
		ctx := audit.WithSource(t.Context(), audit.Source{System: "default-ids"})
		rows := []relationships.CreateRelationship{ct("doc", "d", "viewer", "user", "a"), ct("doc", "d", "viewer", "user", "b")}
		if err := r.Relationships.CreateRelationships(ctx, rows); err != nil {
			t.Fatal(err)
		}
		got := readAudit(t, r.Audit)
		if len(got) != 2 || !uuidV7.MatchString(got[0].EventID) {
			t.Fatalf("default event id is not a version-7 UUID: %+v", got)
		}
		requireEventRows(t, got, got[0].EventID)
	})
	t.Run("OneDeltaAcrossFacades", func(t *testing.T) {
		r := factory(t, true)
		ctx := audit.WithSource(t.Context(), audit.Source{System: "canonical-fixture"})
		row := ct("doc", "d", "owner", "user", "u")
		a := roles.Assignment{Scope: fixtureScope("doc", "d"), Role: "owner", SubjectType: "user", SubjectID: "u"}
		expectAuditChanges(t, r, func() error { return r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{row}) }, []audit.Change{addedRelationship(row)})
		expectAuditChanges(t, r, func() error { return assignRole(ctx, r.Tuples, a) }, nil)
		expectAuditChanges(t, r, func() error { return unassignRole(ctx, r.Tuples, "user", "u", "owner", "doc", "d") }, []audit.Change{removedRelationship(row)})
		if held, err := r.Tuples.Contains(ctx, row.Tuple()); err != nil || held {
			t.Fatalf("facade revoke retained duplicate fact: %v/%v", held, err)
		}
	})
	t.Run("OperationLedger", func(t *testing.T) {
		r := factory(t, true)
		ctx := audit.WithSource(t.Context(), audit.Source{System: "ledger"})
		cmd := grant("d", "viewer", "u")
		cmd.OperationID = "one"
		const n = 8
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for range n {
			wg.Go(func() { _, err := r.Mutations.Apply(ctx, cmd, nil); errs <- err })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := readAudit(t, r.Audit); len(got) != 1 {
			t.Fatalf("concurrent ledger audit: %+v", got)
		}
		if result, err := r.Mutations.Apply(ctx, cmd, nil); err != nil || !result.Replayed {
			t.Fatalf("replay: %+v %v", result, err)
		}
		if got := readAudit(t, r.Audit); len(got) != 1 {
			t.Fatalf("replay appended audit: %+v", got)
		}
		if result, err := r.Mutations.Apply(t.Context(), cmd, nil); result != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("unattributed replay: %+v %v", result, err)
		}
	})
	t.Run("MutationChanges", func(t *testing.T) { specMutationAudit(t, factory(t, true)) })
	t.Run("PurgeBound", func(t *testing.T) { specAuditPurgeBound(t, factory(t, true)) })
	t.Run("PagingAndOwnership", func(t *testing.T) { specAuditPaging(t, factory(t, true)) })
	t.Run("CorrelationIDs", func(t *testing.T) { specAuditCorrelation(t, factory(t, true)) })
}

// RunAuditIDs proves a host generator reaches every recorded event ID.
// factory returns a fresh recording bundle (as RunAudit's factory does with
// recording enabled) whose store was built with ids. A COUNTING generator is
// supplied, never a constant one: a constant id collides on the row key.
func RunAuditIDs(t *testing.T, factory func(*testing.T, sdk.IDGenerator) Repositories) {
	t.Run("HostGenerator", func(t *testing.T) {
		var (
			mu sync.Mutex
			n  int
		)
		r := factory(t, sdk.NewIDGenerator(func() (string, error) {
			mu.Lock()
			defer mu.Unlock()
			n++
			return "fixed-" + strconv.Itoa(n), nil
		}))
		requireAudit(t, r)
		ctx := audit.WithSource(t.Context(), audit.Source{System: "host-ids"})
		writes := []func() error{
			func() error {
				return r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{ct("doc", "raw", "viewer", "user", "a"), ct("doc", "raw", "viewer", "user", "b")})
			},
			func() error {
				return assignRole(ctx, r.Tuples, roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "editor", Scope: fixtureScope("doc", "role")})
			},
			func() error {
				_, err := r.Mutations.Apply(ctx, grant("command", "viewer", "u"), nil)
				return err
			},
		}
		seen := map[string]bool{}
		for i, write := range writes {
			before := map[string]bool{}
			for _, row := range readAudit(t, r.Audit) {
				before[row.ID] = true
			}
			if err := write(); err != nil {
				t.Fatalf("write %d: %v", i, err)
			}
			var added []audit.Record
			for _, row := range readAudit(t, r.Audit) {
				if !before[row.ID] {
					added = append(added, row)
				}
			}
			if len(added) == 0 || !strings.HasPrefix(added[0].EventID, "fixed-") || seen[added[0].EventID] {
				t.Fatalf("write %d did not record a fresh host event id: %+v", i, added)
			}
			seen[added[0].EventID] = true
			requireEventRows(t, added, added[0].EventID)
		}
	})
	t.Run("EmptyMintRefused", func(t *testing.T) {
		r := factory(t, sdk.NewIDGenerator(sdk.DatabaseID))
		requireAudit(t, r)
		ctx := audit.WithSource(t.Context(), audit.Source{System: "empty-ids"})
		row := ct("doc", "empty", "viewer", "user", "u")
		if err := r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{row}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("raw write with an empty event id: %v", err)
		}
		if result, err := r.Mutations.Apply(ctx, grant("empty", "viewer", "u"), nil); result != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("command with an empty event id: %+v %v", result, err)
		}
		if relationExists(t, r, "empty", "viewer", "u") || len(readAudit(t, r.Audit)) != 0 {
			t.Fatal("refused write published state")
		}
	})
}

// requireEventRows asserts rows are one event's whole record set: every row
// carries eventID and the IDs are eventID:<ordinal> for ordinals 1..len(rows).
func requireEventRows(t *testing.T, rows []audit.Record, eventID string) {
	t.Helper()
	var ids, want []string
	for i, row := range rows {
		if row.EventID != eventID {
			t.Fatalf("one write split into events %q and %q", eventID, row.EventID)
		}
		ids = append(ids, row.ID)
		want = append(want, fmt.Sprintf("%s:%020d", eventID, i+1))
	}
	slices.Sort(ids)
	if !slices.Equal(ids, want) {
		t.Fatalf("row ids = %q, want %q", ids, want)
	}
}
func requireAudit(t *testing.T, r Repositories) {
	t.Helper()
	if r.Audit == nil || r.Relationships == nil || r.Tuples == nil || r.Mutations == nil {
		t.Fatal("audit factory must wire reader and all writers")
	}
}
func requireRecording(t *testing.T, r Repositories, enabled bool) {
	t.Helper()
	requireAudit(t, r)
	for name, port := range map[string]any{"Tuples": r.Tuples, "Mutations": r.Mutations} {
		reporter, ok := port.(audit.RecordingReporter)
		if !ok {
			t.Fatalf("%s does not implement audit.RecordingReporter", name)
		}
		if got := reporter.AuditRecording(); got != enabled {
			t.Fatalf("%s AuditRecording() = %v; store constructed with recording %v", name, got, enabled)
		}
	}
}
func addedRelationship(r relationships.CreateRelationship) audit.Change {
	return audit.Change{Action: audit.ActionAdded, Tuple: r.Tuple()}
}
func removedRelationship(r relationships.CreateRelationship) audit.Change {
	return audit.Change{Action: audit.ActionRemoved, Tuple: r.Tuple()}
}
func addedRole(r roles.Assignment) audit.Change {
	return audit.Change{Action: audit.ActionAdded, Tuple: r.Tuple()}
}
func removedRole(r roles.Assignment) audit.Change {
	return audit.Change{Action: audit.ActionRemoved, Tuple: r.Tuple()}
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
func expectAuditChanges(t *testing.T, r Repositories, write func() error, want []audit.Change) {
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
func specRawAudit(t *testing.T, r Repositories) {
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
	independent := a
	independent.Relation = "editor"
	expectAuditChanges(t, r, func() error {
		return r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{independent})
	}, []audit.Change{addedRelationship(independent)})
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
	global := roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "editor", Scope: fixtureScope("", "")}
	expectAuditChanges(t, r, func() error { return assignRole(ctx, r.Tuples, global) }, []audit.Change{addedRole(global)})
	expectAuditChanges(t, r, func() error { return assignRole(ctx, r.Tuples, global) }, nil)
	expectAuditChanges(t, r, func() error { return unassignRole(ctx, r.Tuples, "user", "u", "editor", "", "") }, []audit.Change{removedRole(global)})
	for _, row := range readAudit(t, r.Audit) {
		if row.Source != (audit.Source{System: "sync", Reason: "projection"}) {
			t.Fatalf("source lost: %+v", row.Source)
		}
	}
}
func specMutationAudit(t *testing.T, r Repositories) {
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
	expectAuditChanges(t, r, apply(swap("d", "viewer", "editor", "u")), []audit.Change{removedRelationship(a), addedRelationship(b)})
	expectAuditChanges(t, r, apply(revoke("d", "editor", "u")), []audit.Change{removedRelationship(b)})
	expectAuditChanges(t, r, apply(revoke("d", "editor", "u")), nil)
	expectAuditChanges(t, r, apply(grant("d", "viewer", "u")), []audit.Change{addedRelationship(a)})
	expectAuditChanges(t, r, apply(mutations.Command{Target: resTarget("d"), Operation: mutations.OpPurge}), []audit.Change{removedRelationship(a)})
	expectAuditChanges(t, r, apply(grant("d", "viewer", "u")), []audit.Change{addedRelationship(a)})
	assignment := roles.Assignment{SubjectType: "user", SubjectID: "u", Role: "editor", Scope: fixtureScope("doc", "d")}
	roleCmd := mutations.Command{Target: resTarget("d"), Operation: mutations.OpRoleAssign, Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "u", Role: "editor"}}}
	expectAuditChanges(t, r, apply(roleCmd), []audit.Change{addedRole(assignment)})
	roleCmd.Operation = mutations.OpRoleUnassign
	expectAuditChanges(t, r, apply(roleCmd), []audit.Change{removedRole(assignment)})
	roleCmd.Operation = mutations.OpRoleAssign
	expectAuditChanges(t, r, apply(roleCmd), []audit.Change{addedRole(assignment)})
	before := readAudit(t, r.Audit)
	if result, err := r.Mutations.Apply(ctx, grant("d", "viewer", "other"), func(mutations.Command) error { return sdk.ErrInvalidInput }); result != nil || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("invalid command: %+v %v", result, err)
	}
	if !reflect.DeepEqual(before, readAudit(t, r.Audit)) {
		t.Fatal("invalid command recorded history")
	}
	expectAuditChanges(t, r, apply(mutations.Command{Target: resTarget("d"), Operation: mutations.OpTeardown}), []audit.Change{removedRelationship(a), removedRole(assignment)})
}
func specAuditPaging(t *testing.T, r Repositories) {
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
	first[0].Change.Tuple.Subject.ID = "changed"
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

func specAuditPurgeBound(t *testing.T, r Repositories) {
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

// specAuditCorrelation round-trips Source.RequestID and Source.TraceID through
// every write surface and both list strategies, admits blank and whole ids,
// and refuses an id a host stamps by hand beyond audit.MaxCorrelationIDLen.
func specAuditCorrelation(t *testing.T, r Repositories) {
	requireAudit(t, r)
	whole := strings.Repeat("r", audit.MaxCorrelationIDLen)
	for i, source := range []audit.Source{
		{ActorType: "user", ActorID: "actor", RequestID: "req-1", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"},
		{System: "bootstrap"},
		{ActorType: "user", ActorID: "actor", Reason: "rotation", RequestID: whole, TraceID: whole},
	} {
		ctx := audit.WithSource(context.Background(), source)
		id := "correlated-" + strconv.Itoa(i)
		writes := []func() error{
			func() error {
				return r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{ct("doc", id, "viewer", "user", "raw")})
			},
			func() error { _, err := r.Mutations.Apply(ctx, grant(id, "viewer", "command"), nil); return err },
			func() error {
				return assignRole(ctx, r.Tuples, roles.Assignment{SubjectType: "user", SubjectID: "role", Role: "editor", Scope: fixtureScope("doc", id)})
			},
		}
		for _, write := range writes {
			if err := write(); err != nil {
				t.Fatalf("source %+v: %v", source, err)
			}
		}
		for _, strategy := range []list.Strategy{list.StrategyCursor, list.StrategyOffset} {
			page, err := r.Audit.List(context.Background(), audit.Filter{ResourceType: "doc", ResourceID: id}, list.Request{Strategy: strategy, Limit: 10})
			if err != nil || len(page.Items) != len(writes) {
				t.Fatalf("%s listing of %s: %+v %v", strategy, id, page, err)
			}
			for _, row := range page.Items {
				if row.Source != source {
					t.Fatalf("%s listing changed source: got %+v, want %+v", strategy, row.Source, source)
				}
			}
		}
	}
	before := readAudit(t, r.Audit)
	for _, source := range []audit.Source{{System: "host", RequestID: whole + "r"}, {ActorType: "user", ActorID: "actor", TraceID: "trace\x00"}} {
		ctx := audit.WithSource(context.Background(), source)
		if err := r.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{ct("doc", "refused", "viewer", "user", "raw")}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("raw write accepted invalid correlation %+v: %v", source, err)
		}
		if result, err := r.Mutations.Apply(ctx, grant("refused", "viewer", "command"), nil); result != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("command accepted invalid correlation %+v: %+v %v", source, result, err)
		}
	}
	if after := readAudit(t, r.Audit); len(after) != len(before) || relationExists(t, r, "refused", "viewer", "raw") {
		t.Fatalf("invalid correlation wrote state: before=%d after=%d", len(before), len(after))
	}
}
