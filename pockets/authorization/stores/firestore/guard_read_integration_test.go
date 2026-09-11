//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"fmt"
	"testing"

	gcfs "cloud.google.com/go/firestore"
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
)

type failedGuardReader struct {
	firestoredb.Reader
	err error
}

func (r failedGuardReader) Get(context.Context, *gcfs.DocumentRef) (*gcfs.DocumentSnapshot, error) {
	return nil, r.err
}

func TestMutationRetriesGuardReadContention(t *testing.T) {
	db, store, _ := newMutations(t)
	calls := 0
	guard := func(ctx context.Context, view mutations.StoreDecisionView) error {
		calls++
		if calls == 1 {
			concrete := view.(*decisionView)
			concrete.r = failedGuardReader{Reader: concrete.r, err: firestoredb.MapError(firestoretest.AbortedError("injected role read contention"))}
		}
		_, err := view.HasRole(ctx, docScope("d1"), "editor", "user", "u1")
		return err
	}
	result, err := store.ApplyGuarded(context.Background(), grantCmd(t, "d1", "owner", user("u1")), guard, nil)
	if err != nil || result == nil || result.Outcome != mutations.OutcomeApplied || calls != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
	}
	if _, exists := storedRow(t, db, "doc", "d1", "owner", user("u1")); !exists {
		t.Fatal("successful retry left no fact")
	}
}

func TestMutationGuardPolicyConflictsPreserveIdentityWithoutRetry(t *testing.T) {
	_, store, _ := newMutations(t)
	for _, refusal := range []error{errors.New("host policy refusal"), sdk.ErrForbidden, fmt.Errorf("policy: %w", sdk.ErrConflict), mutations.ErrSemanticConflict, mutations.ErrInvariantBlocked,
		firestoretest.AbortedError("raw policy-created status"),
		firestoredb.MapError(firestoretest.AbortedError("policy-created status"))} {
		calls := 0
		cmd := grantCmd(t, "d1", "owner", user("u1"))
		result, err := store.ApplyGuarded(context.Background(), cmd, func(context.Context, mutations.StoreDecisionView) error {
			calls++
			return refusal
		}, nil)
		if result != nil || err != refusal || calls != 1 {
			t.Fatalf("policy refusal lost identity or retried: result=%+v error=%v calls=%d", result, err, calls)
		}
	}
}

func TestGuardGlobalRoleReadsQueriedSubject(t *testing.T) {
	db, store, _ := newMutations(t)
	queried := mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "bob"}
	seed := mutations.Command{Target: queried, Operation: mutations.OpRoleAssign,
		Roles: []mutations.RoleRow{{SubjectType: "user", SubjectID: "bob", Role: "editor"}}}
	applyOK(t, store, seed, mutations.OutcomeApplied)
	err := db.Transact(context.Background(), func(ctx context.Context) error {
		view := newDecisionView(db, db.ReaderFrom(ctx))
		held, err := view.HasRole(ctx, mutations.Target{Kind: mutations.TargetSubject, Type: "user", ID: "alice"}, "editor", "user", "bob")
		if err != nil {
			return err
		}
		if !held {
			return errors.New("global role query used the target subject instead of the requested subject")
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDefaultGuardianAllowsReaderFirstGrant(t *testing.T) {
	db, _, _ := newMutations(t)
	repos, err := Repositories(t.Context(), db, WithoutIndexProbe())
	if err != nil {
		t.Fatal(err)
	}
	if len(repos.Mutations.GuardianPolicy().Rules) != 0 {
		t.Fatal("default guardian is not empty")
	}
	cmd := grantCmd(t, "d1", "reader", user("u1"))
	result, err := repos.Mutations.Apply(context.Background(), cmd, nil)
	if err != nil || result == nil || result.Outcome != mutations.OutcomeApplied {
		t.Fatalf("reader-first grant: %+v %v", result, err)
	}
	conflicting := grantCmd(t, "d1", "editor", user("u1"))
	result, err = repos.Mutations.Apply(context.Background(), conflicting, nil)
	if result != nil || !errors.Is(err, mutations.ErrSemanticConflict) {
		t.Fatalf("semantic refusal: %+v %v", result, err)
	}
}
