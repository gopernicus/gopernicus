package firestore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	gcfs "cloud.google.com/go/firestore"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ mutation.MutationRepository = (*mutationStore)(nil)

// receiptSchemaDigestUnset is the placeholder schema digest recorded when a
// command carries no governing digest (a trusted/migration path that supplies no
// compiled schema). The SQL siblings' schema_digest column is NOT NULL non-empty
// and they record this exact sentinel, so a receipt stays byte-identical across
// the three families and an exact replay reads back what it recorded. It is a
// store-local constant, never parsed.
const receiptSchemaDigestUnset = "unset"

// errReceiptRaced reports a receipt document that appeared between this
// transaction's read phase (which found none) and its Create. It is a
// STORE-INTEGRITY signal about the transaction, not a statement about the
// command: the payload was never compared, so calling it
// mutation.ErrPayloadMismatch would report a permanent domain refusal for what
// is a lost race, and a caller replaying its own MutationID would be told its
// payload changed.
//
// It wraps sdk.ErrConflict and is therefore RETRYABLE (retryableConflict): the
// re-run reads the receipt that is now present and replays it — which, if the
// payload really does differ, is where ErrPayloadMismatch is raised, on an
// actual comparison. It should be unreachable, since the read is in the
// transaction's read set and a concurrent Create must abort this commit first.
var errReceiptRaced = fmt.Errorf("authorization firestore store: the receipt for this mutation id was created concurrently: %w", sdk.ErrConflict)

// mutationStore is the Firestore mutation.MutationRepository: ONE
// db.Transact per Apply, with A-D5's six phases strictly ordered — refuse an
// ambient transaction, authorize, replay/digest, validate and read, evaluate and
// guard the invariant, then the single write phase.
//
// The phase order is not stylistic. A Firestore transaction refuses any read
// issued after its first write and never observes its own pending writes, so the
// whole decision — the guard's reads, the receipt, the anchor, every affected row
// and claim — is READ first, evaluated in Go over a staged copy, and only then
// written. That is the same critical section the memstore takes under one mutex
// and the SQL siblings take under BEGIN IMMEDIATE / FOR UPDATE, expressed in the
// one shape Firestore allows.
//
// Commit-time dependency validation is the vendor's: every anchor the guard read
// is in the transaction's read set, so a concurrent bump aborts the commit and
// the callback RE-RUNS from the top — guard, replay check, and evaluation
// included. Every piece of attempt-local state (the view, its dependencies, the
// staged writes, the result) is therefore built inside the callback, never
// outside it, so a losing attempt's answer can never be returned as the
// committed one.
type mutationStore struct {
	db       *firestoredb.DB
	guardian mutation.GuardianPolicy
}

func newMutationStore(db *firestoredb.DB, guardian mutation.GuardianPolicy) *mutationStore {
	return &mutationStore{db: db, guardian: guardian}
}

// Apply runs the trusted (unguarded) atomic write path. See the port doc comment
// on mutation.MutationRepository for the full ordered contract.
func (s *mutationStore) Apply(ctx context.Context, cmd mutation.Command, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	return s.apply(ctx, cmd, nil, validate)
}

// ApplyGuarded runs the actor-facing atomic write path: the guard is evaluated
// against a dependency-tracking view INSIDE the same transaction, before the
// receipt/revision/apply steps, so possession of a MutationID is never authority
// and a stale allow cannot commit. A nil guard is the trusted path.
func (s *mutationStore) ApplyGuarded(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	return s.apply(ctx, cmd, guard, validate)
}

// apply refuses an ambient transaction, structurally validates the command, and
// runs the whole ordered contract inside ONE db.Transact.
//
// A command error (guard denial, payload mismatch, stale revision, a validator
// failure, cancellation, a store failure) returns (nil, err) from the callback,
// which discards every queued write — no row, no anchor bump, no receipt. A
// domain OUTCOME returns (receipt, nil): a blocked or conflicting outcome queues
// nothing, so its transaction commits nothing and its receipt is returned
// without being persisted, exactly as Outcome.Persisted() demands.
//
// Retries exhausted by contention surface as the connector's sdk.ErrConflict —
// an infrastructure conflict the caller may retry, never a committed outcome and
// never a minted receipt. Because Apply is idempotent by MutationID, that retry
// is safe: it either replays a now-committed receipt or re-evaluates against
// current state.
func (s *mutationStore) apply(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	if err := refuseAmbientMutation(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}

	var out *mutation.Receipt
	if err := retryContention(ctx, func() (error, bool) {
		// Reset at the TOP of the retry closure, not only inside the vendor
		// callback: a Transact that fails before it ever calls the callback
		// (a begin failure) must not leave a previous attempt's receipt behind.
		out = nil

		// guardReturned is the one classification the error's identity cannot
		// carry. A guard's refusal is an authorization ANSWER even when it
		// happens to wrap sdk.ErrConflict, so it is terminal for this loop
		// however it is spelled. Everything else — including a mapped Aborted
		// raised by a transactional READ — is judged by retryableConflict.
		guardReturned := false
		err := s.db.Transact(ctx, func(ctx context.Context) error {
			// Reset per attempt too: the vendor re-runs this callback when a
			// commit loses a race, and a receipt built by a losing attempt is
			// not the receipt of the transaction that committed.
			out, guardReturned = nil, false
			rcpt, fromGuard, err := s.applyTx(ctx, cmd, guard, validate)
			guardReturned = fromGuard
			if err != nil {
				// A conflict this callback can SEE is re-run by the loop
				// OUTSIDE, with jitter — never by the vendor's un-jittered one
				// as well. See detachVendorRetry.
				return detachVendorRetry(err)
			}
			out = rcpt
			return nil
		})
		return err, guardReturned
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// applyTx is the transactional critical section: every read, then every write.
//
// The second return value reports that the error came from the GUARD, which the
// retry loop treats as terminal — a denial is an answer, not contention, even
// when it wraps a conflict sentinel.
func (s *mutationStore) applyTx(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Receipt, bool, error) {
	r := s.db.ReaderFrom(ctx)

	// 1. Authorize the actor (guarded path) FIRST — before the MutationID/digest
	//    check — so an actor-facing replay still runs its guard. The view records
	//    every scope it read and the revision it observed there.
	var view *decisionView
	if guard != nil {
		view = newDecisionView(s.db, r)
		if err := runGuard(ctx, guard, view); err != nil {
			return nil, true, err
		}
	}

	// 2. Read the mutation scope's anchor, re-read every dependency anchor in
	//    canonical order, and read the receipt — ONE GetAll, because every one of
	//    them is a document address and a transaction holds its locks until it
	//    commits, so a round trip saved is contention avoided. Under one
	//    Firestore snapshot the anchor re-read can only agree with what the view
	//    observed, so it is the parity mirror of the SQL siblings' locked
	//    re-read; what actually catches a concurrent bump is the read set the
	//    guard's own reads built.
	locked, existing, found, err := readAnchorsAndReceipt(ctx, s.db, r, lockSet(cmd.Scope, view), cmd.MutationID)
	if err != nil {
		return nil, false, err
	}
	current := locked[cmd.Scope.Canonical()]

	// 3. De-duplicate by MutationID. A matching digest replays the stored receipt
	//    verbatim; a different one is the stable payload-mismatch command error.
	if found {
		if existing.PayloadDigest != cmd.PayloadDigest() {
			return nil, false, mutation.ErrPayloadMismatch
		}
		if err := validateDeps(view, locked); err != nil {
			return nil, false, err
		}
		replay := existing
		replay.Replayed = true
		return &replay, false, nil
	}

	// 4. Receipt-absent: validate the guard's observed dependency revisions,
	//    then run the CURRENT-schema semantic validator (skipped on replay above,
	//    which is why an exact stored replay survives a schema that would now
	//    reject the original relation), then the expected-revision precondition.
	if err := validateDeps(view, locked); err != nil {
		return nil, false, err
	}
	if validate != nil {
		if err := validate(cmd); err != nil {
			return nil, false, err
		}
	}
	if cmd.ExpectedRevision != nil && *cmd.ExpectedRevision != current {
		return nil, false, mutation.ErrStaleRevision
	}

	// 5. Read every affected row, claim, and role fact, then evaluate invariants
	//    over a staged copy. Nothing is written yet: the plan the evaluator
	//    returns is the complete write set.
	result, err := s.evaluate(ctx, r, cmd)
	if err != nil {
		return nil, false, err
	}
	now := firestoredb.TruncateTime(time.Now().UTC())
	if result.outcome == mutation.OutcomeSemanticConflict || result.outcome == mutation.OutcomeInvariantBlocked {
		// A domain outcome that commits nothing: no row, no anchor bump, no
		// receipt. The transaction commits an EMPTY write set, so the reads it
		// took are still validated and the caller gets a receipt with a nil
		// error, as the port requires.
		return s.receipt(cmd, result.outcome, current, now), false, nil
	}

	// 6. The write phase. No read may follow it. There is no client-side write
	//    budget to check: Firestore publishes no per-transaction write COUNT
	//    limit, only the 10 MiB request size, and an oversized commit fails at
	//    the server ATOMICALLY — nothing written, mapped error, no receipt
	//    (SCHEMA.md §8.3).
	w := s.db.WriterFrom(ctx)
	if err := result.writes.flush(ctx, s.db, w); err != nil {
		return nil, false, err
	}
	revision := current
	if result.changed {
		revision = current + 1
		if err := writeAnchor(ctx, s.db, w, cmd.Scope, revision); err != nil {
			return nil, false, err
		}
	}
	rcpt := s.receipt(cmd, result.outcome, revision, now)
	// Op-specific, NON-persisted annotation: for a scoped role unassign, whether
	// a global grant still satisfies the exact role. It was read inside this
	// transaction (the global rows the scoped removal cannot touch), so the
	// answer is consistent with the removal rather than a detached post-commit
	// read. It is not a stored field, so a later replay returns it false.
	rcpt.SameRoleGrantRemains = result.sameRoleGrantRemains
	if result.outcome.Persisted() {
		if err := insertReceipt(ctx, s.db, w, rcpt); err != nil {
			return nil, false, err
		}
	}
	return rcpt, false, nil
}

// receipt builds the receipt for a resolved outcome, recording the command's
// governing SchemaDigest (or the non-empty sentinel when it supplied none).
func (s *mutationStore) receipt(cmd mutation.Command, outcome mutation.Outcome, revision mutation.Revision, now time.Time) *mutation.Receipt {
	return &mutation.Receipt{
		MutationID:      cmd.MutationID,
		Scope:           cmd.Scope,
		Operation:       cmd.Operation,
		PayloadEncoding: cmd.PayloadEncoding(),
		PayloadDigest:   cmd.PayloadDigest(),
		Outcome:         outcome,
		Revision:        revision,
		SchemaDigest:    receiptSchemaDigest(cmd),
		Replayed:        false,
		CreatedAt:       now,
	}
}

// receiptSchemaDigest returns the governing digest to record: the command's
// stamped SchemaDigest, or the sentinel when it supplied none.
func receiptSchemaDigest(cmd mutation.Command) string {
	if cmd.SchemaDigest != "" {
		return cmd.SchemaDigest
	}
	return receiptSchemaDigestUnset
}

// runGuard invokes guard, converting a panic into a coarse infrastructure error
// so the transaction rolls back rather than leaving a half-open write. The error
// is NOT a gRPC Aborted status, so the vendor ends the transaction instead of
// re-running the callback (connector README, the retry-gate sharp edge).
func runGuard(ctx context.Context, guard mutation.Guard, view *decisionView) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("authorization firestore store: guard panicked: %v: %w", r, sdk.ErrUnavailable)
		}
	}()
	return guard(ctx, view)
}

// lockSet returns the mutation scope plus every guard dependency scope, deduped
// and sorted by ScopeKey.Canonical() — the canonical order the port names, kept
// so the anchor reads happen in one deterministic sequence across the three
// families.
func lockSet(mutScope mutation.ScopeKey, view *decisionView) []mutation.ScopeKey {
	byKey := map[string]mutation.ScopeKey{mutScope.Canonical(): mutScope}
	if view != nil {
		for _, dep := range view.Dependencies() {
			byKey[dep.Scope.Canonical()] = dep.Scope
		}
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]mutation.ScopeKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, byKey[k])
	}
	return out
}

// validateDeps re-checks each guard-observed dependency revision against the
// re-read; any mismatch is the stale-revision command error. A nil view (the
// trusted path) has no dependencies.
func validateDeps(view *decisionView, locked map[string]mutation.Revision) error {
	if view == nil {
		return nil
	}
	for _, dep := range view.Dependencies() {
		if locked[dep.Scope.Canonical()] != dep.Revision {
			return mutation.ErrStaleRevision
		}
	}
	return nil
}

// =============================================================================
// Scope anchors and receipts
// =============================================================================

// readAnchor reads a scope's current revision. An ABSENT anchor document is
// revision 0 and is NOT materialized: a bare revision-0 row is never written
// just to exist, so a concurrent first writer is a detectable 0→1 change rather
// than a phantom. Get hands back the snapshot alongside its sdk.ErrNotFound, so
// absence is a value here and not a second read.
func readAnchor(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, sk mutation.ScopeKey) (mutation.Revision, error) {
	snap, err := r.Get(ctx, anchorRef(db, sk))
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return 0, err
	}
	return anchorRevisionOf(snap)
}

// anchorRef is the scope anchor's document reference — the iam_scopes primary
// key, hashed.
func anchorRef(db *firestoredb.DB, sk mutation.ScopeKey) *gcfs.DocumentRef {
	return db.Doc(collectionScopes, scopeDocID(string(sk.Kind), sk.Type, sk.ID))
}

// anchorRevisionOf decodes one anchor snapshot: absent (or nil) is revision 0.
func anchorRevisionOf(snap *gcfs.DocumentSnapshot) (mutation.Revision, error) {
	if snap == nil || !snap.Exists() {
		return 0, nil
	}
	var doc scopeDoc
	if err := snap.DataTo(&doc); err != nil {
		return 0, fmt.Errorf("authorization firestore store: decoding %s: %s: %w", collectionScopes, err, sdk.ErrInvalidInput)
	}
	return mutation.Revision(doc.Revision), nil
}

// writeAnchor stores a scope's new revision. The verb is Set, not Create or
// Update: the anchor may be absent (revision 0 is the absent state, so the first
// bump CREATES it) or present (every later bump UPDATES it), and the caller read
// it in this transaction's read phase, so the read set — not a precondition — is
// what makes the increment exactly once.
func writeAnchor(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, sk mutation.ScopeKey, revision mutation.Revision) error {
	return w.Set(ctx, anchorRef(db, sk), scopeDoc{
		ScopeKind: string(sk.Kind),
		ScopeType: sk.Type,
		ScopeID:   sk.ID,
		Revision:  int64(revision),
	})
}

// readAnchorsAndReceipt reads every scope anchor in scopes plus the receipt for
// id in ONE GetAll, returning the revisions by ScopeKey.Canonical() (an absent
// anchor is revision 0) and the stored receipt when there is one.
//
// One call rather than one per document is not only a saved round trip: a
// Firestore transaction holds every lock it takes until it commits, so a longer
// read phase is a wider contention window for every other writer on the same
// scope. A missing document is a snapshot whose Exists() is false, never an
// error, which is exactly the "absent anchor reads as 0" contract.
func readAnchorsAndReceipt(ctx context.Context, db *firestoredb.DB, r firestoredb.Reader, scopes []mutation.ScopeKey, id mutation.MutationID) (map[string]mutation.Revision, mutation.Receipt, bool, error) {
	refs := make([]*gcfs.DocumentRef, 0, len(scopes)+1)
	for _, sk := range scopes {
		refs = append(refs, anchorRef(db, sk))
	}
	refs = append(refs, db.Doc(collectionMutations, mutationDocID(string(id))))

	snaps, err := r.GetAll(ctx, refs)
	if err != nil {
		return nil, mutation.Receipt{}, false, err
	}
	if len(snaps) != len(refs) {
		return nil, mutation.Receipt{}, false, fmt.Errorf("authorization firestore store: read %d of %d mutation documents: %w", len(snaps), len(refs), sdk.ErrUnavailable)
	}

	locked := make(map[string]mutation.Revision, len(scopes))
	for i, sk := range scopes {
		rev, err := anchorRevisionOf(snaps[i])
		if err != nil {
			return nil, mutation.Receipt{}, false, err
		}
		locked[sk.Canonical()] = rev
	}

	receiptSnap := snaps[len(snaps)-1]
	if receiptSnap == nil || !receiptSnap.Exists() {
		return locked, mutation.Receipt{}, false, nil
	}
	var doc mutationDoc
	if err := receiptSnap.DataTo(&doc); err != nil {
		return nil, mutation.Receipt{}, false, fmt.Errorf("authorization firestore store: decoding %s: %s: %w", collectionMutations, err, sdk.ErrInvalidInput)
	}
	return locked, doc.toReceipt(), true, nil
}

// toReceipt projects a stored receipt document back to the domain type. Replayed
// and SameRoleGrantRemains are computed annotations in every dialect and are not
// stored, so a decoded receipt carries them false.
func (d mutationDoc) toReceipt() mutation.Receipt {
	return mutation.Receipt{
		MutationID:      mutation.MutationID(d.MutationID),
		Scope:           mutation.ScopeKey{Kind: mutation.ScopeKind(d.ScopeKind), Type: d.ScopeType, ID: d.ScopeID},
		Operation:       mutation.Operation(d.Operation),
		PayloadEncoding: d.PayloadEncoding,
		PayloadDigest:   d.PayloadDigest,
		Outcome:         mutation.Outcome(d.Outcome),
		Revision:        mutation.Revision(d.Revision),
		SchemaDigest:    d.SchemaDigest,
		CreatedAt:       d.CreatedAt,
	}
}

// insertReceipt persists a committed receipt (permanent retention: expires_at is
// an explicit null). The verb is Create, never Set: the receipt document id IS
// the MutationID, so a concurrent double-apply loses at the server instead of
// overwriting a receipt someone already replayed. The caller read the same
// document in this transaction's read phase, so reaching AlreadyExists means a
// writer the read set missed — errReceiptRaced, which the retry loop re-runs.
func insertReceipt(ctx context.Context, db *firestoredb.DB, w firestoredb.Writer, r *mutation.Receipt) error {
	err := w.Create(ctx, db.Doc(collectionMutations, mutationDocID(string(r.MutationID))), mutationDoc{
		MutationID:      string(r.MutationID),
		ScopeKind:       string(r.Scope.Kind),
		ScopeType:       r.Scope.Type,
		ScopeID:         r.Scope.ID,
		Operation:       string(r.Operation),
		PayloadEncoding: r.PayloadEncoding,
		PayloadDigest:   r.PayloadDigest,
		Outcome:         string(r.Outcome),
		Revision:        int64(r.Revision),
		SchemaDigest:    r.SchemaDigest,
		CreatedAt:       firestoredb.TruncateTime(r.CreatedAt),
		ExpiresAt:       firestoredb.NullTime(time.Time{}),
	})
	if errors.Is(err, sdk.ErrAlreadyExists) {
		return errReceiptRaced
	}
	return err
}
