//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
)

func raceRawAndGuard(t *testing.T, raw, guarded func() error) error {
	t.Helper()
	start := make(chan struct{})
	var rawErr, guardErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); <-start; rawErr = raw() }()
	go func() { defer wg.Done(); <-start; guardErr = guarded() }()
	close(start)
	wg.Wait()
	if rawErr != nil {
		t.Fatalf("raw writer failed: %v", rawErr)
	}
	if guardErr != nil && !errors.Is(guardErr, sdk.ErrForbidden) && !errors.Is(guardErr, mutations.ErrConcurrentMutation) {
		t.Fatalf("guard failed unexpectedly: %v", guardErr)
	}
	return guardErr
}

func TestGuardEmptyQuerySerializesWithRawReconciliation(t *testing.T) {
	_, repos, ctx := newAudited(t)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("d%d", i)
		desired := ctf("doc", id, "viewer", "user", "raw")
		guard := func(ctx context.Context, view mutations.StoreDecisionView) error {
			targets, err := view.RelationTargets(ctx, docScope(id), "viewer")
			if err != nil {
				return err
			}
			if len(targets) != 0 {
				return sdk.ErrForbidden
			}
			return nil
		}
		raceRawAndGuard(t, func() error {
			return repos.Relationships.SetRelationTargets(ctx, "doc", id, "viewer", []relationships.CreateRelationship{desired})
		}, func() error {
			_, err := repos.Mutations.ApplyGuarded(ctx, grantCmd(t, id, "viewer", user("guarded")), guard, nil)
			return err
		})
		targets, err := repos.Relationships.GetRelationTargets(ctx, "doc", id, "viewer")
		// Guard first: the reconciliation removes its grant. Reconciliation first:
		// the negative guard refuses. No serial order can leave their union behind.
		if err != nil || len(targets) != 1 || targets[0].ID != "raw" {
			t.Fatalf("empty-query write skew: %+v %v", targets, err)
		}
	}
}

func TestGuardAbsentGlobalRoleUsesNativeCommitOrder(t *testing.T) {
	db, repos, ctx := newAudited(t)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("d%d", i)
		subject := fmt.Sprintf("u%d", i)
		guard := func(ctx context.Context, view mutations.StoreDecisionView) error {
			present, err := view.HasRole(ctx, docScope(id), "blocked", "user", subject)
			if err != nil {
				return err
			}
			if present {
				return sdk.ErrForbidden
			}
			return nil
		}
		err := raceRawAndGuard(t, func() error {
			return repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: subject, Role: "blocked"})
		}, func() error {
			_, err := repos.Mutations.ApplyGuarded(ctx, grantCmd(t, id, "viewer", user(subject)), guard, nil)
			return err
		})
		tupleRef := db.Doc(collectionRelationships, relationshipDocID("doc", id, "viewer", "user", subject, ""))
		tuple, readErr := db.ReaderFrom(ctx).Get(ctx, tupleRef)
		if err != nil {
			if tuple != nil && tuple.Exists() {
				t.Fatal("refused mutation persisted fact")
			}
			continue
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		global, readErr := db.ReaderFrom(ctx).Get(ctx, roleRef(db, "user", subject, "blocked", "", ""))
		if readErr != nil {
			t.Fatal(readErr)
		}
		// Snapshot UpdateTime is the server commit timestamp, unlike callback time
		// or goroutine completion order. A successful negative decision must commit
		// before the global role that falsifies it.
		if !tuple.UpdateTime.Before(global.UpdateTime) {
			t.Fatalf("stale absent-role allow: guarded=%s raw=%s", tuple.UpdateTime, global.UpdateTime)
		}
	}
}

func TestGuardInheritedAllowSerializesWithRawRevoke(t *testing.T) {
	db, repos, ctx := newAudited(t)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("d%d", i)
		group := fmt.Sprintf("g%d", i)
		if err := repos.Relationships.CreateRelationships(ctx, []relationships.CreateRelationship{ctf("group", group, "member", "user", "alice"), ctfUserset("doc", id, "editor", "group", group, "member")}); err != nil {
			t.Fatal(err)
		}
		guard := func(ctx context.Context, view mutations.StoreDecisionView) error {
			allowed, err := view.CheckRelation(ctx, docScope(id), "editor", "user", "alice")
			if err != nil {
				return err
			}
			if !allowed {
				return sdk.ErrForbidden
			}
			return nil
		}
		err := raceRawAndGuard(t, func() error {
			return repos.Relationships.DeleteRelationshipTarget(ctx, "group", group, "member", user("alice"))
		}, func() error {
			_, err := repos.Mutations.ApplyGuarded(ctx, grantCmd(t, id, "viewer", user("reader")), guard, nil)
			return err
		})
		tuple, readErr := db.ReaderFrom(ctx).Get(ctx, db.Doc(collectionRelationships, relationshipDocID("doc", id, "viewer", "user", "reader", "")))
		if err != nil {
			if tuple != nil && tuple.Exists() {
				t.Fatal("refused inherited allow persisted fact")
			}
			continue
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		var removalID string
		for _, record := range history(t, repos.Audit) {
			if r := record.Change.Relationship; r != nil && r.ResourceType == "group" && r.ResourceID == group && record.Change.Action == audit.ActionRemoved {
				removalID = record.ID
			}
		}
		if removalID == "" {
			t.Fatal("raw revoke has no audit record")
		}
		removal, readErr := db.ReaderFrom(ctx).Get(ctx, db.Doc(collectionAudit, removalID))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !tuple.UpdateTime.Before(removal.UpdateTime) {
			t.Fatalf("stale inherited allow: guarded=%s revoke=%s", tuple.UpdateTime, removal.UpdateTime)
		}
	}
}
