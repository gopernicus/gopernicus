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
	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

func newMutations(t *testing.T) (*firestoredb.DB, *mutationStore, *relationshipStore) {
	t.Helper()
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	return db, newMutationStore(db, mutation.DefaultGuardianPolicy(), false), newRelationshipStore(db, false)
}

func docScope(id string) mutation.Target {
	return mutation.Target{Kind: mutation.TargetResource, Type: "doc", ID: id}
}

func groupScope(id string) mutation.Target {
	return mutation.Target{Kind: mutation.TargetResource, Type: "group", ID: id}
}

func grantCmd(t *testing.T, resourceID, relation string, subject relationships.SubjectRef) mutation.Command {
	t.Helper()
	return mutation.Command{

		Target:        docScope(resourceID),
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: relation, Subject: subject}},
	}
}

func user(id string) relationships.SubjectRef { return relationships.SubjectRef{Type: "user", ID: id} }

// applyOK applies a command through the trusted path and requires the outcome.
func applyOK(t *testing.T, m *mutationStore, cmd mutation.Command, want mutation.Outcome) *mutation.Result {
	t.Helper()
	rcpt, err := m.Apply(context.Background(), cmd, nil)
	if err != nil {
		t.Fatalf("Apply(%s): %v", cmd.Operation, err)
	}
	if rcpt == nil {
		t.Fatalf("Apply(%s) returned (nil, nil)", cmd.Operation)
	}
	if rcpt.Outcome != want {
		t.Fatalf("Apply(%s) outcome = %q, want %q", cmd.Operation, rcpt.Outcome, want)
	}
	return rcpt
}

func storedRow(t *testing.T, db *firestoredb.DB, resourceType, resourceID, relation string, subject relationships.SubjectRef) (relationshipDoc, bool) {
	t.Helper()
	ctx := context.Background()
	id := relationshipDocID(resourceType, resourceID, relation, subject.Type, subject.ID, subject.Relation)
	snap, err := db.ReaderFrom(ctx).Get(ctx, db.Doc(collectionRelationships, id))
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("reading %s:%s#%s: %v", resourceType, resourceID, relation, err)
	}
	if snap == nil || !snap.Exists() {
		return relationshipDoc{}, false
	}
	var row relationshipDoc
	if err := snap.DataTo(&row); err != nil {
		t.Fatalf("decoding %s:%s#%s: %v", resourceType, resourceID, relation, err)
	}
	return row, true
}

func ownerGuard(resourceID, principal string) mutation.Guard {
	return func(ctx context.Context, view mutation.StoreDecisionView) error {
		ok, err := view.CheckRelation(ctx, docScope(resourceID), "owner", "user", principal)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s is not an owner of doc:%s: %w", principal, resourceID, sdk.ErrForbidden)
		}
		return nil
	}
}

func TestMutationReplacePreservesRowIdentity(t *testing.T) {
	db, m, _ := newMutations(t)
	ctx := context.Background()

	applyOK(t, m, grantCmd(t, "d1", "owner", user("u1")), mutation.OutcomeApplied)
	applyOK(t, m, grantCmd(t, "d1", "viewer", user("u2")), mutation.OutcomeApplied)
	before, ok := storedRow(t, db, "doc", "d1", "viewer", user("u2"))
	if !ok {
		t.Fatalf("the seeded viewer row is missing")
	}

	applyOK(t, m, mutation.Command{

		Target:        docScope("d1"),
		Operation:     mutation.OpReplace,
		Relationships: []mutation.RelationshipRow{{Relation: "editor", Subject: user("u2")}},
	}, mutation.OutcomeApplied)

	after, ok := storedRow(t, db, "doc", "d1", "editor", user("u2"))
	if !ok {
		t.Fatalf("the replaced row is missing at its new relation")
	}
	if after.SubjectKey != before.SubjectKey || after.ResourceKey != before.ResourceKey {
		t.Fatalf("replace must keep the derived equality keys: %+v -> %+v", before, after)
	}
	if _, ok := storedRow(t, db, "doc", "d1", "viewer", user("u2")); ok {
		t.Fatalf("the old relation's document must be gone — replace has no delete/create gap, but it does move")
	}

	// The subject claim now names the new relation and tuple.
	_, subject := claimRefs(db, after)
	snaps, err := db.ReaderFrom(ctx).GetAll(ctx, []*gcfs.DocumentRef{subject})
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	var claim subjectClaimDoc
	if !snaps[0].Exists() {
		t.Fatalf("the subject claim is missing after a replace")
	}
	if err := snaps[0].DataTo(&claim); err != nil {
		t.Fatalf("decoding the subject claim: %v", err)
	}
	if claim.Relation != "editor" || claim.TupleID != tupleID(after) {
		t.Fatalf("the subject claim still describes the old row: %+v", claim)
	}

}

func TestMutationGuardAndValidatorRunOnEveryApplication(t *testing.T) {
	db, store, _ := newMutations(t)
	cmd := grantCmd(t, "d1", "owner", user("u1"))
	calls, validations := 0, 0
	guard := func(context.Context, mutation.StoreDecisionView) error { calls++; return nil }
	validator := func(mutation.Command) error { validations++; return nil }
	for _, want := range []mutation.Outcome{mutation.OutcomeApplied, mutation.OutcomeNoChange} {
		result, err := store.ApplyGuarded(context.Background(), cmd, guard, validator)
		if err != nil || result.Outcome != want {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	refusal := errors.New("current policy refuses")
	result, err := store.ApplyGuarded(context.Background(), cmd, guard, func(mutation.Command) error { return refusal })
	if result != nil || err != refusal || calls != 3 || validations != 2 {
		t.Fatalf("result=%+v err=%v calls=%d validations=%d", result, err, calls, validations)
	}
	if _, exists := storedRow(t, db, "doc", "d1", "owner", user("u1")); !exists {
		t.Fatal("earlier committed fact disappeared")
	}
}

func TestMutationInvariantRefusalLeavesFactUnchanged(t *testing.T) {
	db, store, _ := newMutations(t)
	cmd := grantCmd(t, "d1", "owner", user("u1"))
	applyOK(t, store, cmd, mutation.OutcomeApplied)
	cmd.Operation = mutation.OpRevoke
	result, err := store.Apply(context.Background(), cmd, nil)
	if result != nil || !errors.Is(err, mutation.ErrInvariantBlocked) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	row, exists := storedRow(t, db, "doc", "d1", "owner", user("u1"))
	if !exists {
		t.Fatal("refusal removed owner")
	}
	assertTupleDocs(t, db, row, true)
	applyOK(t, store, grantCmd(t, "d1", "owner", user("u2")), mutation.OutcomeApplied)
	applyOK(t, store, cmd, mutation.OutcomeApplied)
}

func TestMutationValidatorRefusalsPreserveIdentityWithoutRetry(t *testing.T) {
	for name, refusal := range map[string]error{"plain": errors.New("refused"), "conflict": sdk.ErrConflict, "wrapped conflict": fmt.Errorf("policy: %w", sdk.ErrConflict), "raw Aborted": firestoretest.AbortedError("policy"), "mapped Aborted": firestoredb.MapError(firestoretest.AbortedError("policy"))} {
		t.Run(name, func(t *testing.T) {
			db, store, _ := newMutations(t)
			calls := 0
			cmd := grantCmd(t, "d1", "owner", user("u1"))
			result, err := store.Apply(context.Background(), cmd, func(mutation.Command) error { calls++; return refusal })
			if result != nil || err != refusal || calls != 1 {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
			}
			if _, exists := storedRow(t, db, "doc", "d1", "owner", user("u1")); exists {
				t.Fatal("refusal persisted fact")
			}
			applyOK(t, store, cmd, mutation.OutcomeApplied)
		})
	}
}

func TestMutationRetryReturnsCommittedAttemptResult(t *testing.T) {
	db, store, _ := newMutations(t)
	applyOK(t, store, grantCmd(t, "d1", "owner", user("u1")), mutation.OutcomeApplied)
	attempts := 0
	guard := func(ctx context.Context, view mutation.StoreDecisionView) error {
		attempts++
		if attempts == 1 {
			seedTuples(t, db, ctf("doc", "d1", "viewer", "user", "u2"))
			concrete := view.(*decisionView)
			concrete.r = failedGuardReader{Reader: concrete.r, err: firestoredb.MapError(firestoretest.AbortedError("forced contention"))}
			_, err := view.HasRole(ctx, docScope("d1"), "editor", "user", "u1")
			return err
		}
		return nil
	}
	result, err := store.ApplyGuarded(context.Background(), grantCmd(t, "d1", "viewer", user("u2")), guard, nil)
	if err != nil || result == nil || result.Outcome != mutation.OutcomeNoChange || attempts != 2 {
		t.Fatalf("result=%+v err=%v attempts=%d", result, err, attempts)
	}
}

func TestMutationLargeCommandAppliesInOneTransaction(t *testing.T) {
	db, store, _ := newMutations(t)
	rows := []mutation.RelationshipRow{{Relation: "owner", Subject: user("u0")}}
	for i := 1; i < 200; i++ {
		rows = append(rows, mutation.RelationshipRow{Relation: "viewer", Subject: user(docID("u", i))})
	}
	applyOK(t, store, mutation.Command{Target: docScope("big"), Operation: mutation.OpGrant, Relationships: rows}, mutation.OutcomeApplied)
	if got := len(storedResourceRows(t, db, "doc", "big")); got != 200 {
		t.Fatalf("committed %d of 200 facts", got)
	}
}

func TestTransactionRetriesAMappedAbortedFromATransactionalRead(t *testing.T) {
	db, _, _ := newMutations(t)
	attempts := 0
	err := retryTransact(context.Background(), db, func(ctx context.Context) error {
		attempts++
		reader := db.ReaderFrom(ctx)
		if attempts == 1 {
			reader = failedGuardReader{Reader: reader, err: firestoredb.MapError(firestoretest.AbortedError("read aborted"))}
		}
		exists, err := roleExists(ctx, db, reader, "user", "u1", "editor", "", "")
		if err != nil || exists {
			return err
		}
		return putRole(ctx, db, db.WriterFrom(ctx), roleDoc{SubjectType: "user", SubjectID: "u1", Role: "editor"})
	})
	if err != nil || attempts != 2 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}

func TestGuardedBudgetOverflowLeavesNoFact(t *testing.T) {
	db, store, _ := newMutations(t)
	seedTuples(t, db, ctf("group", "g1", "member", "user", "u1"), ctfUserset("group", "g2", "member", "group", "g1", "member"), ctfUserset("group", "g3", "member", "group", "g2", "member"))
	result, err := store.ApplyGuarded(context.Background(), grantCmd(t, "d1", "viewer", user("u2")), func(ctx context.Context, view mutation.StoreDecisionView) error {
		_, err := view.CheckRelationBounded(ctx, docScope("d1"), "viewer", "user", "u1", 3)
		return err
	}, nil)
	if result != nil || !errors.Is(err, relationships.ErrExpansionBudgetExceeded) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, exists := storedRow(t, db, "doc", "d1", "viewer", user("u2")); exists {
		t.Fatal("overflow committed a fact")
	}
}
