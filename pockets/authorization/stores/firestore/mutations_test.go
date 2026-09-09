//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	gcfs "cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
)

// A4a–A4c: the guarded mutation path. The shared conformance suite
// (storetest.Run's Mutations family) is the reference specification for the
// OUTCOMES, and it runs green here; what these leaves add is what only this
// family can see — that a refused command left no DOCUMENT behind (rows, both
// claims, the anchor, the receipt), that the guard really precedes the replay
// branch, that a vendor retry re-decides from scratch, and that the decision
// view records the scopes a cross-scope decision rested on.

// newMutations opens this train's emulator database, clears it, and returns the
// full repository set plus the concrete stores, so a test can assert at the
// document level and through the port in the same pass.
func newMutations(t *testing.T) (*firestoredb.DB, *mutationStore, *relationshipStore) {
	t.Helper()
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	return db, newMutationStore(db, mutation.DefaultGuardianPolicy()), newRelationshipStore(db)
}

// mutID mints a conformance-grade MutationID.
func mutID(t *testing.T) mutation.MutationID {
	t.Helper()
	id, err := mutation.NewMutationID()
	if err != nil {
		t.Fatalf("NewMutationID: %v", err)
	}
	return id
}

func docScope(id string) mutation.ScopeKey {
	return mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "doc", ID: id}
}

func groupScope(id string) mutation.ScopeKey {
	return mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "group", ID: id}
}

// grantCmd is one relationship grant on doc:<resourceID>.
func grantCmd(t *testing.T, resourceID, relation string, subject relationship.SubjectRef) mutation.Command {
	t.Helper()
	return mutation.Command{
		MutationID:    mutID(t),
		Scope:         docScope(resourceID),
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: relation, Subject: subject}},
	}
}

func user(id string) relationship.SubjectRef { return relationship.SubjectRef{Type: "user", ID: id} }

// applyOK applies a command through the trusted path and requires the outcome.
func applyOK(t *testing.T, m *mutationStore, cmd mutation.Command, want mutation.Outcome) *mutation.Receipt {
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

// anchorAt reads a scope's stored revision straight from its document — the
// forensic the port cannot make, because every port-level probe is itself a
// command.
func anchorAt(t *testing.T, db *firestoredb.DB, scope mutation.ScopeKey) mutation.Revision {
	t.Helper()
	ctx := context.Background()
	rev, err := readAnchor(ctx, db, db.ReaderFrom(ctx), scope)
	if err != nil {
		t.Fatalf("readAnchor(%s): %v", scope, err)
	}
	return rev
}

// storedReceipt reads the receipt document for id, reporting whether one exists.
func storedReceipt(t *testing.T, db *firestoredb.DB, id mutation.MutationID) (mutationDoc, bool) {
	t.Helper()
	ctx := context.Background()
	snap, err := db.ReaderFrom(ctx).Get(ctx, db.Doc(collectionMutations, mutationDocID(string(id))))
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("reading the receipt for %s: %v", id, err)
	}
	if snap == nil || !snap.Exists() {
		return mutationDoc{}, false
	}
	var doc mutationDoc
	if err := snap.DataTo(&doc); err != nil {
		t.Fatalf("decoding the receipt for %s: %v", id, err)
	}
	return doc, true
}

// storedRow reads the one stored row for an exact tuple, reporting whether it
// exists. It is how the replace case proves the row's IDENTITY survived.
func storedRow(t *testing.T, db *firestoredb.DB, resourceType, resourceID, relation string, subject relationship.SubjectRef) (relationshipDoc, bool) {
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

// ownerGuard allows only while principal holds `owner` on doc:resourceID.
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

// TestMutationGuardPrecedesReplay is the actor-facing half of A-D5 phase 2:
// possession of a MutationID is NOT authority. An actor who WAS authorized when
// a command first applied, and has since lost that authority, is denied on
// replay — the guard runs BEFORE the receipt lookup, so the stored receipt is
// never handed back to someone who could not issue the command today.
//
// The shared suite proves a guard denial on a FIRST application; only this
// ordering case distinguishes "guard first" from "replay first", and getting it
// backwards would be an authorization hole no outcome assertion would notice.
func TestMutationGuardPrecedesReplay(t *testing.T) {
	ctx := context.Background()
	db, m, rel := newMutations(t)

	applyOK(t, m, grantCmd(t, "d1", "owner", user("u1")), mutation.OutcomeApplied)
	applyOK(t, m, grantCmd(t, "d1", "owner", user("u2")), mutation.OutcomeApplied)

	cmd := grantCmd(t, "d1", "viewer", user("u3"))
	first, err := m.ApplyGuarded(ctx, cmd, ownerGuard("d1", "u1"), nil)
	if err != nil {
		t.Fatalf("guarded grant by an owner: %v", err)
	}
	if first.Outcome != mutation.OutcomeApplied || first.Replayed {
		t.Fatalf("first guarded application = %+v, want a fresh applied receipt", first)
	}

	// u1 loses ownership (u2 keeps the guardian minimum).
	applyOK(t, m, mutation.Command{
		MutationID:    mutID(t),
		Scope:         docScope("d1"),
		Operation:     mutation.OpRevoke,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: user("u1")}},
	}, mutation.OutcomeApplied)

	replay, err := m.ApplyGuarded(ctx, cmd, ownerGuard("d1", "u1"), nil)
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Fatalf("a revoked actor replaying its own MutationID must be denied, got rcpt=%+v err=%v", replay, err)
	}
	if replay != nil {
		t.Fatalf("a denied replay must return no receipt, got %+v", replay)
	}

	// The denial changed nothing: the stored receipt is still there, unchanged,
	// for an actor who IS authorized.
	doc, ok := storedReceipt(t, db, cmd.MutationID)
	if !ok || doc.PayloadDigest != cmd.PayloadDigest() || mutation.Revision(doc.Revision) != first.Revision {
		t.Fatalf("the original receipt must survive a denied replay verbatim, got %+v (exists=%v)", doc, ok)
	}
	again, err := m.ApplyGuarded(ctx, cmd, ownerGuard("d1", "u2"), nil)
	if err != nil {
		t.Fatalf("an authorized replay must still return the stored receipt: %v", err)
	}
	if !again.Replayed || again.Revision != first.Revision {
		t.Fatalf("authorized replay = %+v, want the original receipt with Replayed=true", again)
	}
	if ok, _ := rel.CheckRelationExists(ctx, "doc", "d1", "viewer", "user", "u3"); !ok {
		t.Fatalf("the originally applied row must still be present")
	}
}

// TestMutationPayloadMismatchWritesNothing pins the mismatch branch at the
// document level: the stable sentinel (not merely its sdk class), the ORIGINAL
// receipt document untouched, no row or claim for the reused payload, and an
// anchor that never moved.
func TestMutationPayloadMismatchWritesNothing(t *testing.T) {
	ctx := context.Background()
	db, m, _ := newMutations(t)

	original := grantCmd(t, "d1", "owner", user("u1"))
	applied := applyOK(t, m, original, mutation.OutcomeApplied)

	reuse := mutation.Command{
		MutationID:    original.MutationID, // same id
		Scope:         docScope("d1"),
		Operation:     mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: user("u2")}}, // different payload
	}
	rcpt, err := m.Apply(ctx, reuse, nil)
	if !errors.Is(err, mutation.ErrPayloadMismatch) {
		t.Fatalf("payload reuse must be mutation.ErrPayloadMismatch, got rcpt=%+v err=%v", rcpt, err)
	}
	if rcpt != nil {
		t.Fatalf("a payload mismatch must return no receipt, got %+v", rcpt)
	}

	doc, ok := storedReceipt(t, db, original.MutationID)
	if !ok {
		t.Fatalf("the original receipt must still exist")
	}
	if doc.PayloadDigest != original.PayloadDigest() || doc.Operation != string(mutation.OpGrant) {
		t.Fatalf("the stored receipt was rewritten by the mismatched command: %+v", doc)
	}
	if got := anchorAt(t, db, docScope("d1")); got != applied.Revision {
		t.Fatalf("a mismatched command moved the anchor: got %d want %d", got, applied.Revision)
	}
	assertTupleDocs(t, db, relationshipDoc{
		ResourceType: "doc", ResourceID: "d1", Relation: "member",
		SubjectType: "user", SubjectID: "u2", RelationshipID: "never-minted",
	}, false)
}

// TestMutationStaleRevisionIsTheDomainSentinel pins the expected-revision branch
// on the stable sentinel and on a document-level "nothing moved": the shared
// suite asserts the sdk class only, which a store could satisfy with any
// conflict.
func TestMutationStaleRevisionIsTheDomainSentinel(t *testing.T) {
	ctx := context.Background()
	db, m, _ := newMutations(t)

	seed := applyOK(t, m, grantCmd(t, "d1", "owner", user("u1")), mutation.OutcomeApplied)
	applyOK(t, m, grantCmd(t, "d1", "editor", user("u2")), mutation.OutcomeApplied) // the anchor moves on

	stale := seed.Revision
	cmd := grantCmd(t, "d1", "viewer", user("u3"))
	cmd.ExpectedRevision = &stale

	before := anchorAt(t, db, docScope("d1"))
	rcpt, err := m.Apply(ctx, cmd, nil)
	if !errors.Is(err, mutation.ErrStaleRevision) {
		t.Fatalf("a stale expected revision must be mutation.ErrStaleRevision, got rcpt=%+v err=%v", rcpt, err)
	}
	if rcpt != nil {
		t.Fatalf("a stale revision must return no receipt, got %+v", rcpt)
	}
	if got := anchorAt(t, db, docScope("d1")); got != before {
		t.Fatalf("a stale command moved the anchor: got %d want %d", got, before)
	}
	if _, ok := storedReceipt(t, db, cmd.MutationID); ok {
		t.Fatalf("a stale command must not consume its MutationID with a receipt")
	}
}

// TestMutationBlockedOutcomeLeavesNoDocuments is the persistence contract at the
// document level. An invariant-blocked command IS a domain outcome — a receipt
// with a nil error — but it commits NOTHING: no row, no claim, no anchor bump,
// and above all no receipt document, because its MutationID is not consumed and
// a later retry must re-evaluate against current state.
func TestMutationBlockedOutcomeLeavesNoDocuments(t *testing.T) {
	db, m, _ := newMutations(t)

	owner := grantCmd(t, "d1", "owner", user("u1"))
	applied := applyOK(t, m, owner, mutation.OutcomeApplied)
	row, ok := storedRow(t, db, "doc", "d1", "owner", user("u1"))
	if !ok {
		t.Fatalf("the seeded owner row is missing")
	}

	blocked := mutation.Command{
		MutationID:    mutID(t),
		Scope:         docScope("d1"),
		Operation:     mutation.OpRevoke,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: user("u1")}},
	}
	rcpt := applyOK(t, m, blocked, mutation.OutcomeInvariantBlocked)
	if rcpt.Outcome.Persisted() {
		t.Fatalf("invariant_blocked must not be a persisted outcome")
	}
	if rcpt.Revision != applied.Revision {
		t.Fatalf("a blocked receipt must report the CURRENT revision: got %d want %d", rcpt.Revision, applied.Revision)
	}

	assertTupleDocs(t, db, row, true) // the row AND both claims survived
	if got := anchorAt(t, db, docScope("d1")); got != applied.Revision {
		t.Fatalf("a blocked command moved the anchor: got %d want %d", got, applied.Revision)
	}
	if _, ok := storedReceipt(t, db, blocked.MutationID); ok {
		t.Fatalf("a blocked command must persist no receipt")
	}

	// The MutationID was not consumed: the same id applies fresh once the state
	// admits it.
	applyOK(t, m, grantCmd(t, "d1", "owner", user("u2")), mutation.OutcomeApplied)
	if got := applyOK(t, m, blocked, mutation.OutcomeApplied); got.Replayed {
		t.Fatalf("a blocked command must not have consumed its MutationID")
	}
}

// TestMutationRollbackLeavesNoTrace drives the OTHER no-write path: a command
// error raised AFTER the whole read phase (a semantic validator refusal) rolls
// the transaction back, so the row it would have created, both of its claims,
// the anchor, and the receipt are all absent.
func TestMutationRollbackLeavesNoTrace(t *testing.T) {
	ctx := context.Background()
	db, m, _ := newMutations(t)

	seed := applyOK(t, m, grantCmd(t, "d1", "owner", user("u1")), mutation.OutcomeApplied)

	refuse := func(mutation.Command) error {
		return fmt.Errorf("the current schema rejects this: %w", sdk.ErrInvalidInput)
	}
	cmd := grantCmd(t, "d1", "viewer", user("u2"))
	rcpt, err := m.Apply(ctx, cmd, refuse)
	if !errors.Is(err, sdk.ErrInvalidInput) || rcpt != nil {
		t.Fatalf("a validator refusal must be a command error with no receipt, got rcpt=%+v err=%v", rcpt, err)
	}

	assertTupleDocs(t, db, relationshipDoc{
		ResourceType: "doc", ResourceID: "d1", Relation: "viewer",
		SubjectType: "user", SubjectID: "u2", RelationshipID: "never-minted",
	}, false)
	if got := anchorAt(t, db, docScope("d1")); got != seed.Revision {
		t.Fatalf("a rolled-back command moved the anchor: got %d want %d", got, seed.Revision)
	}
	if _, ok := storedReceipt(t, db, cmd.MutationID); ok {
		t.Fatalf("a rolled-back command must persist no receipt")
	}
}

// TestMutationReplacePreservesRowIdentity pins what OpReplace means here. The
// SQL siblings say `UPDATE ... SET relation = ?`, which keeps the row's
// primary key and created_at; Firestore has to MOVE the document, because the
// relation is part of its id. So the assertion is that the move is invisible
// from every other angle: the new row carries the ORIGINAL relationship_id and
// created_at, the old document is gone, the subject claim (whose id does not
// carry the relation) now names the new relation and the new tuple, and the id
// claim still resolves.
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
		MutationID:    mutID(t),
		Scope:         docScope("d1"),
		Operation:     mutation.OpReplace,
		Relationships: []mutation.RelationshipRow{{Relation: "editor", Subject: user("u2")}},
	}, mutation.OutcomeApplied)

	after, ok := storedRow(t, db, "doc", "d1", "editor", user("u2"))
	if !ok {
		t.Fatalf("the replaced row is missing at its new relation")
	}
	if after.RelationshipID != before.RelationshipID {
		t.Fatalf("replace must preserve the relationship_id: %q -> %q", before.RelationshipID, after.RelationshipID)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("replace must preserve created_at: %v -> %v", before.CreatedAt, after.CreatedAt)
	}
	if after.SubjectKey != before.SubjectKey || after.ResourceKey != before.ResourceKey {
		t.Fatalf("replace must keep the derived equality keys: %+v -> %+v", before, after)
	}
	if _, ok := storedRow(t, db, "doc", "d1", "viewer", user("u2")); ok {
		t.Fatalf("the old relation's document must be gone — replace has no delete/create gap, but it does move")
	}

	// The two claims moved WITH the row: the subject claim names the new
	// relation and tuple, and the id claim resolves to the new tuple.
	_, subject, id := claimRefs(db, after)
	snaps, err := db.ReaderFrom(ctx).GetAll(ctx, []*gcfs.DocumentRef{subject, id})
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
	var idClaim idClaimDoc
	if !snaps[1].Exists() {
		t.Fatalf("the id claim is missing after a replace")
	}
	if err := snaps[1].DataTo(&idClaim); err != nil {
		t.Fatalf("decoding the id claim: %v", err)
	}
	if idClaim.TupleID != tupleID(after) || idClaim.RelationshipID != before.RelationshipID {
		t.Fatalf("the id claim still points at the old tuple: %+v", idClaim)
	}
}

// TestMutationRetryReturnsTheCommittedAttemptsAnswer is the C-D4 discipline
// applied to this path, and it is deterministic rather than raced: the guard
// changes the world on attempt ONE and then hands back a raw gRPC Aborted
// status, which the vendor treats as contention and re-runs the callback with
// (connector finding N4). The attempt that COMMITS therefore sees a different
// state than the attempt that did not, and the receipt the caller gets must be
// the committed one — no revision bump, no duplicate row, no stale outcome.
func TestMutationRetryReturnsTheCommittedAttemptsAnswer(t *testing.T) {
	ctx := context.Background()
	db, m, _ := newMutations(t)

	seed := applyOK(t, m, grantCmd(t, "d1", "owner", user("u1")), mutation.OutcomeApplied)

	attempts := 0
	guard := func(context.Context, mutation.StoreDecisionView) error {
		attempts++
		if attempts == 1 {
			// Nothing is locked yet — the guard runs before this transaction's
			// first read — so an outside writer can still land the very row the
			// command was going to create. Attempt one would have concluded
			// "applied"; attempt two must conclude "no_change".
			seedTuples(t, db, ctf("doc", "d1", "viewer", "user", "u2"))
			return status.Error(codes.Aborted, "forced contention")
		}
		return nil
	}

	rcpt, err := m.ApplyGuarded(ctx, grantCmd(t, "d1", "viewer", user("u2")), guard, nil)
	if err != nil {
		t.Fatalf("ApplyGuarded: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("the callback ran %d times, want 2 (a raw Aborted from the guard must re-run it)", attempts)
	}
	if rcpt.Outcome != mutation.OutcomeNoChange {
		t.Fatalf("the committed attempt saw the row already present: outcome = %q, want no_change", rcpt.Outcome)
	}
	if rcpt.Revision != seed.Revision {
		t.Fatalf("a no-change outcome must not bump the anchor: got %d want %d", rcpt.Revision, seed.Revision)
	}
	if got := anchorAt(t, db, docScope("d1")); got != seed.Revision {
		t.Fatalf("the stored anchor moved: got %d want %d", got, seed.Revision)
	}
}

// TestGuardedViewRecordsEveryTraversedScope is the deterministic
// dependency-completeness proof the plan calls for (the F2 rule): a decision
// that is only satisfied THROUGH another resource must record THAT resource as a
// dependency, or a concurrent revoke there would produce a stale allow nothing
// could catch. It also pins the revisions recorded — an anchor observed before
// its rows, and an absent anchor recorded as revision 0 rather than skipped.
func TestGuardedViewRecordsEveryTraversedScope(t *testing.T) {
	ctx := context.Background()
	db, m, _ := newMutations(t)

	// group:g — owner (guardian minimum) + alice as member.
	applyOK(t, m, mutation.Command{
		MutationID: mutID(t), Scope: groupScope("g"), Operation: mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: user("gowner")}},
	}, mutation.OutcomeApplied)
	applyOK(t, m, mutation.Command{
		MutationID: mutID(t), Scope: groupScope("g"), Operation: mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: user("alice")}},
	}, mutation.OutcomeApplied)
	// doc:d1 — owner, plus an editor USERSET for group:g#member, so alice is an
	// editor of doc:d1 only through her membership.
	applyOK(t, m, grantCmd(t, "d1", "owner", user("u1")), mutation.OutcomeApplied)
	applyOK(t, m, grantCmd(t, "d1", "editor", relationship.SubjectRef{Type: "group", ID: "g", Relation: "member"}), mutation.OutcomeApplied)

	beforeDoc, beforeGroup := anchorAt(t, db, docScope("d1")), anchorAt(t, db, groupScope("g"))

	var deps []mutation.Dependency
	guard := func(gctx context.Context, view mutation.StoreDecisionView) error {
		ok, err := view.CheckRelation(gctx, docScope("d1"), "editor", "user", "alice")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("alice is not an editor: %w", sdk.ErrForbidden)
		}
		deps = view.Dependencies()
		return nil
	}
	if _, err := m.ApplyGuarded(ctx, grantCmd(t, "d1", "viewer", user("u9")), guard, nil); err != nil {
		t.Fatalf("ApplyGuarded: %v", err)
	}

	byScope := map[string]mutation.Revision{}
	for _, dep := range deps {
		byScope[dep.Scope.Canonical()] = dep.Revision
	}
	// The mutation scope AND the traversed group scope, each at the revision the
	// transaction observed — which is the PRE-mutation value, because the guard
	// reads before the write phase.
	for _, want := range []struct {
		scope    mutation.ScopeKey
		revision mutation.Revision
	}{{docScope("d1"), beforeDoc}, {groupScope("g"), beforeGroup}} {
		rev, ok := byScope[want.scope.Canonical()]
		if !ok {
			t.Fatalf("the guard traversed %s but did not record it: deps=%+v", want.scope, deps)
		}
		if rev != want.revision {
			t.Fatalf("%s recorded revision %d, want the observed %d", want.scope, rev, want.revision)
		}
	}
	// The seed subject is recorded as a resource scope too (the harmless
	// over-record every family makes), at revision 0 because no anchor exists.
	seed := mutation.ScopeKey{Kind: mutation.ScopeResource, Type: "user", ID: "alice"}
	rev, ok := byScope[seed.Canonical()]
	if !ok || rev != 0 {
		t.Fatalf("the expansion seed must be recorded at revision 0: got %d (present=%v) deps=%+v", rev, ok, deps)
	}
	// Sorted by ScopeKey.Canonical(), as the port requires.
	for i := 1; i < len(deps); i++ {
		if deps[i-1].Scope.Canonical() > deps[i].Scope.Canonical() {
			t.Fatalf("Dependencies() must be sorted by ScopeKey.Canonical(): %+v", deps)
		}
	}
}

// TestGuardedMutationConcurrentDependencyBump drives the cross-scope dependency
// race with a handshake rather than with luck: the guard finishes its reads,
// signals, and a concurrent trusted revoke of the exact membership the decision
// rests on starts while the guarded transaction is still open.
//
// The invariant asserted holds on every family and is the one that matters: a
// receipt is NEVER built on a stale dependency. Either the guarded write
// committed — and then the dependency revision it recorded is the PRE-revoke one,
// so the decision was still true at commit — or it aborted cleanly as
// stale/denied with no receipt and no row. The revoke always commits.
//
// The emulator holds transaction locks for up to thirty seconds and does not
// implement all transaction behavior, so WHICH of the two arms is taken is a
// timing fact here, not a serializability proof; the strict form is the live
// case below it.
func TestGuardedMutationConcurrentDependencyBump(t *testing.T) {
	ctx := context.Background()
	db, m, rel := newMutations(t)

	applyOK(t, m, mutation.Command{
		MutationID: mutID(t), Scope: groupScope("g"), Operation: mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "owner", Subject: user("gowner")}},
	}, mutation.OutcomeApplied)
	applyOK(t, m, mutation.Command{
		MutationID: mutID(t), Scope: groupScope("g"), Operation: mutation.OpGrant,
		Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: user("alice")}},
	}, mutation.OutcomeApplied)
	applyOK(t, m, grantCmd(t, "d1", "owner", user("u1")), mutation.OutcomeApplied)
	applyOK(t, m, grantCmd(t, "d1", "editor", relationship.SubjectRef{Type: "group", ID: "g", Relation: "member"}), mutation.OutcomeApplied)

	preRevoke := anchorAt(t, db, groupScope("g"))

	var (
		mu       sync.Mutex
		observed mutation.Revision
		started  = make(chan struct{})
		once     sync.Once
	)
	guard := func(gctx context.Context, view mutation.StoreDecisionView) error {
		ok, err := view.CheckRelation(gctx, docScope("d1"), "editor", "user", "alice")
		if err != nil {
			return err
		}
		mu.Lock()
		for _, dep := range view.Dependencies() {
			if dep.Scope.Canonical() == groupScope("g").Canonical() {
				observed = dep.Revision
			}
		}
		mu.Unlock()
		// The reads are done and their locks are held: release the racer, then
		// give it time to reach the database before this attempt commits.
		once.Do(func() { close(started) })
		time.Sleep(50 * time.Millisecond)
		if !ok {
			return fmt.Errorf("alice is not an editor: %w", sdk.ErrForbidden)
		}
		return nil
	}

	guarded := grantCmd(t, "d1", "viewer", user("u3"))
	revokeCmd := mutation.Command{
		MutationID: mutID(t), Scope: groupScope("g"), Operation: mutation.OpRevoke,
		Relationships: []mutation.RelationshipRow{{Relation: "member", Subject: user("alice")}},
	}

	var (
		wg                      sync.WaitGroup
		guardedRcpt, revokeRcpt *mutation.Receipt
		guardedErr, revokeErr   error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		guardedRcpt, guardedErr = m.ApplyGuarded(ctx, guarded, guard, nil)
	}()
	go func() {
		defer wg.Done()
		<-started
		revokeRcpt, revokeErr = m.Apply(ctx, revokeCmd, nil)
	}()
	wg.Wait()

	if revokeErr != nil || revokeRcpt.Outcome != mutation.OutcomeApplied {
		t.Fatalf("the membership revoke must commit: rcpt=%+v err=%v", revokeRcpt, revokeErr)
	}
	viewerPresent, err := rel.CheckRelationExists(ctx, "doc", "d1", "viewer", "user", "u3")
	if err != nil {
		t.Fatalf("CheckRelationExists: %v", err)
	}

	switch {
	case guardedErr == nil:
		if guardedRcpt == nil || guardedRcpt.Outcome != mutation.OutcomeApplied || !viewerPresent {
			t.Fatalf("a nil-error guarded write must be applied with its row: rcpt=%+v row=%v", guardedRcpt, viewerPresent)
		}
		mu.Lock()
		got := observed
		mu.Unlock()
		if got != preRevoke {
			t.Fatalf("a committed guarded write rested on group:g at revision %d, but the revoke had already moved it past %d — a receipt on a stale dependency", got, preRevoke)
		}
		if _, ok := storedReceipt(t, db, guarded.MutationID); !ok {
			t.Fatalf("an applied guarded write must persist its receipt")
		}
	case errors.Is(guardedErr, sdk.ErrConflict) || errors.Is(guardedErr, sdk.ErrForbidden):
		if guardedRcpt != nil || viewerPresent {
			t.Fatalf("a stale/denied guarded write must leave no receipt and no row: rcpt=%+v row=%v", guardedRcpt, viewerPresent)
		}
		if _, ok := storedReceipt(t, db, guarded.MutationID); ok {
			t.Fatalf("a stale/denied guarded write must persist no receipt")
		}
	default:
		t.Fatalf("the guarded write must be applied, stale, or denied; got rcpt=%+v err=%v", guardedRcpt, guardedErr)
	}
}

// TestMutationWriteCeiling pins the family difference the SQL siblings do not
// have: Firestore commits at most 500 documents in one transaction, and a
// relationship tuple owns three of them, so an oversized command is REFUSED
// before its first write rather than split across transactions — a split is a
// partially applied command, which is exactly the atomicity this port promises.
func TestMutationWriteCeiling(t *testing.T) {
	ctx := context.Background()
	db, m, _ := newMutations(t)

	// 166 tuples * 3 documents + the anchor + the receipt = 500, the exact
	// ceiling; one more row is 503 and must be refused.
	rows := make([]mutation.RelationshipRow, 0, 167)
	rows = append(rows, mutation.RelationshipRow{Relation: "owner", Subject: user("u0")}) // satisfies the guardian
	for i := 1; i < 167; i++ {
		rows = append(rows, mutation.RelationshipRow{Relation: "viewer", Subject: user(docID("u", i))})
	}
	cmd := mutation.Command{
		MutationID: mutID(t), Scope: docScope("big"), Operation: mutation.OpGrant,
		Relationships: rows,
	}

	rcpt, err := m.Apply(ctx, cmd, nil)
	if !errors.Is(err, ErrMutationWriteLimit) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("an oversized command must be ErrMutationWriteLimit (invalid input), got rcpt=%+v err=%v", rcpt, err)
	}
	if rcpt != nil {
		t.Fatalf("a refused command must return no receipt")
	}
	if got := anchorAt(t, db, docScope("big")); got != 0 {
		t.Fatalf("a refused command must write nothing, anchor = %d", got)
	}
	if _, ok := storedRow(t, db, "doc", "big", "owner", user("u0")); ok {
		t.Fatalf("a refused command must write no row")
	}

	// One row fewer fits exactly and applies.
	fits := cmd
	fits.MutationID = mutID(t)
	fits.Relationships = rows[:166]
	applyOK(t, m, fits, mutation.OutcomeApplied)
}
