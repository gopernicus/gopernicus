package turso

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// receiptSchemaDigestUnset is the placeholder schema digest recorded when a command
// carries no governing digest (a trusted/migration path that supplies no compiled
// schema; the mutation service stamps a real digest onto actor-facing relationship
// commands as of AZ3-3.1). The `iam_mutations` schema_digest column is NOT NULL
// non-empty (ck_iam_mutations_nonempty), so this stable non-empty sentinel satisfies
// the constraint AND the replay-returns-the-same-digest contract (an exact replay
// reads back the recorded value). It is the SAME sentinel the pgx sibling uses so a
// receipt is byte-identical across dialects (a store-local constant, never parsed).
const receiptSchemaDigestUnset = "unset"

// receiptSchemaDigest returns the governing digest to record: the command's stamped
// SchemaDigest, or the non-empty sentinel when the command supplied none.
func receiptSchemaDigest(cmd mutation.Command) string {
	if cmd.SchemaDigest != "" {
		return cmd.SchemaDigest
	}
	return receiptSchemaDigestUnset
}

// mutationStore is the Turso/libSQL mutation.MutationRepository: it applies one
// mutation.Command as a single write-serializing transaction, mirroring the
// reference memstore's single-lock critical section and the pgx sibling's
// FOR UPDATE-locked transaction. libSQL/SQLite has no FOR UPDATE, so the
// concurrency spine is the connector's BEGIN IMMEDIATE transaction (the auth v3
// precedent): it takes the write intent up front, so sqld SERIALIZES contending
// writers — the loser reads the winner's committed state and the store's own
// revision compare-and-set returns the deterministic application outcome
// (stale/replay/invariant_blocked), never a raw SQLITE_BUSY. Under that single
// serialized transaction the store creates the missing scope anchors, re-reads the
// mutation scope plus every guard-observed dependency anchor in canonical order,
// re-validates the observed revisions, de-duplicates by receipt, evaluates
// invariants, applies all requested row changes or none, bumps the scope revision
// exactly once, and mints the receipt — with no service orchestration and no
// check-then-write outside the transaction. Full write serialization is strictly
// stronger than pgx's per-anchor FOR UPDATE: it precludes a mid-transaction
// dependency change by construction, so the canonical-order dependency
// re-validation is a defense-in-depth MIRROR of the pgx contract (the plan's
// "prove parity even though BEGIN IMMEDIATE already serializes writers"), not the
// primary mechanism.
type mutationStore struct {
	db       *tursodb.DB
	guardian mutation.GuardianPolicy
}

func newMutationStore(db *tursodb.DB, guardian mutation.GuardianPolicy) *mutationStore {
	return &mutationStore{db: db, guardian: guardian}
}

var _ mutation.MutationRepository = (*mutationStore)(nil)

// Apply runs the trusted (unguarded) atomic write path. See the port doc comment
// on mutation.MutationRepository for the full ordered contract.
func (m *mutationStore) Apply(ctx context.Context, cmd mutation.Command, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	return m.apply(ctx, cmd, nil, validate)
}

// ApplyGuarded runs the actor-facing atomic write path: it evaluates guard against
// a dependency-tracking DecisionView inside the same transaction, then validates
// every observed dependency revision before commit. A nil guard is the trusted
// path.
func (m *mutationStore) ApplyGuarded(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	return m.apply(ctx, cmd, guard, validate)
}

// apply structurally validates the command, then runs the whole ordered contract
// inside ONE BEGIN IMMEDIATE transaction, wrapped in the connector's bounded
// busy-retry so a rare SQLITE_BUSY (a serialization that timed out under load
// rather than blocking) resurfaces as a re-run to a deterministic terminal
// outcome, never as a raw driver error. Because Apply is idempotent by MutationID,
// re-running is safe: a retry either replays a now-committed receipt or
// re-evaluates against current state. A command error (stale revision, payload
// mismatch, guard denial, semantic-validate failure, cancellation, or
// infrastructure failure) returns (nil, err) and the transaction rolls back — no
// rows, no revision bump, no receipt.
func (m *mutationStore) apply(ctx context.Context, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	var out *mutation.Receipt
	if err := retryBusy(ctx, func() error {
		out = nil // reset per attempt; only a committed transaction leaves a receipt
		return m.db.InTx(ctx, func(tx *tursodb.Tx) error {
			rcpt, err := m.applyTx(ctx, tx, cmd, guard, validate)
			out = rcpt
			return err
		})
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// applyTx is the single transactional critical section. The BEGIN IMMEDIATE
// transaction holds the write intent for the whole body, so the guard's reads and
// the commit are consistent — the honest libSQL mirror of the memstore holding one
// mutex across the whole apply and the pgx sibling holding every anchor FOR UPDATE.
func (m *mutationStore) applyTx(ctx context.Context, tx *tursodb.Tx, cmd mutation.Command, guard mutation.Guard, validate mutation.SemanticValidator) (*mutation.Receipt, error) {
	// 1. Authorize the actor (guarded path) FIRST — before the receipt/digest check
	//    — so an actor-facing replay still runs its guard. The view records every
	//    scope + observed revision the guard read (an absent anchor records 0).
	var view *decisionView
	if guard != nil {
		view = newDecisionView(tx)
		if err := runGuard(ctx, guard, view); err != nil {
			return nil, err
		}
	}

	// 2. Re-read the mutation scope plus every recorded dependency anchor in
	//    canonical order: insert any missing revision-0 anchor, then read its
	//    revision. Sorting by ScopeKey.Canonical() keeps the same
	//    record-and-validate discipline the pgx sibling locks in; BEGIN IMMEDIATE
	//    already serializes writers, so this re-read reflects only this
	//    transaction's own snapshot (no concurrent writer can commit meanwhile),
	//    but materializing an absent anchor at revision 0 keeps the anchor state
	//    identical to pgx.
	locked := map[string]mutation.Revision{}
	for _, sk := range m.lockSet(cmd.Scope, view) {
		rev, err := anchorLock(ctx, tx, sk)
		if err != nil {
			return nil, err
		}
		locked[sk.Canonical()] = rev
	}

	// 3. De-duplicate by MutationID. Under BEGIN IMMEDIATE serialization the receipt
	//    state is stable: the first application commits its receipt, and a
	//    serialized retry sees it.
	existing, found, err := lookupReceipt(ctx, tx, cmd.MutationID)
	if err != nil {
		return nil, err
	}
	if found {
		if existing.PayloadDigest != cmd.PayloadDigest() {
			return nil, mutation.ErrPayloadMismatch
		}
		if err := validateDeps(view, locked); err != nil {
			return nil, err
		}
		replay := existing
		replay.Replayed = true
		return &replay, nil
	}

	// 4. Receipt-absent: validate the guard's observed dependency revisions against
	//    the re-read. Any change is a stale-revision command error. (Serialization
	//    precludes a mid-transaction change, so this is the parity mirror of pgx.)
	if err := validateDeps(view, locked); err != nil {
		return nil, err
	}

	// 5. Receipt-absent semantic validation against the CURRENT schema. Skipped on
	//    replay (step 3), which is why an exact stored replay survives a schema that
	//    would now reject the original relation.
	if validate != nil {
		if err := validate(cmd); err != nil {
			return nil, err
		}
	}

	// 6. Expected-revision precondition against the mutation scope's re-read revision.
	current := locked[cmd.Scope.Canonical()]
	if cmd.ExpectedRevision != nil && *cmd.ExpectedRevision != current {
		return nil, mutation.ErrStaleRevision
	}

	// 7. Evaluate invariants and apply ALL requested row changes or NONE.
	outcome, changed, err := m.evaluate(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if outcome == mutation.OutcomeSemanticConflict || outcome == mutation.OutcomeInvariantBlocked {
		return m.receipt(cmd, outcome, current, time.Now().UTC()), nil
	}

	// 8. Bump the scope revision exactly once on a change; persist the receipt.
	revision := current
	if changed {
		revision, err = bumpRevision(ctx, tx, cmd.Scope)
		if err != nil {
			return nil, err
		}
	}
	rcpt := m.receipt(cmd, outcome, revision, time.Now().UTC())
	// Op-specific, non-persisted annotation: for a scoped role unassign, report
	// whether a global grant still satisfies the exact role. The read runs inside
	// this same BEGIN IMMEDIATE transaction, AFTER evaluate removed the scoped rows,
	// so the answer is consistent with the removal — never a detached post-commit
	// read. It is not a persisted column, so a later replay returns it false.
	remains, err := sameRoleGrantRemains(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	rcpt.SameRoleGrantRemains = remains
	if outcome.Persisted() {
		if err := insertReceipt(ctx, tx, rcpt); err != nil {
			return nil, err
		}
	}
	return rcpt, nil
}

// sameRoleGrantRemains reports, for a SCOPED role unassign only, whether a GLOBAL
// ("","") assignment for one of the command's exact (subject, role) rows still
// exists after the unassign. It reads through the same transaction that removed
// the scoped rows, so the answer is atomic with the removal — the honest
// same_role_grant_remains value, not a claim about generic access.
func sameRoleGrantRemains(ctx context.Context, tx *tursodb.Tx, cmd mutation.Command) (bool, error) {
	if cmd.Operation != mutation.OpRoleUnassign || cmd.Scope.Kind != mutation.ScopeResource {
		return false, nil
	}
	for _, row := range cmd.Roles {
		ok, err := hasExactRoleTx(ctx, tx, row.SubjectType, row.SubjectID, row.Role, "", "")
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// lockSet returns the mutation scope plus every guard dependency scope, deduped
// and sorted by ScopeKey.Canonical() — the canonical anchor materialization order.
func (m *mutationStore) lockSet(mutScope mutation.ScopeKey, view *decisionView) []mutation.ScopeKey {
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

// receipt builds the receipt for a resolved outcome, recording the command's
// governing SchemaDigest (or the non-empty sentinel when it supplied none).
func (m *mutationStore) receipt(cmd mutation.Command, outcome mutation.Outcome, revision mutation.Revision, now time.Time) *mutation.Receipt {
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

// runGuard invokes guard, converting a panic into a coarse infrastructure error so
// the transaction rolls back (never leaking a half-open write). The guard may use
// only the supplied view; it must not call the outer Service or perform
// network/unrelated-store I/O.
func runGuard(ctx context.Context, guard mutation.Guard, view *decisionView) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("authorization turso store: guard panicked: %v: %w", r, sdk.ErrUnavailable)
		}
	}()
	return guard(ctx, view)
}

// =============================================================================
// Scope anchors and receipts
// =============================================================================

// anchorLock inserts a missing revision-0 anchor for sk, then re-reads its
// revision. The INSERT ... ON CONFLICT DO NOTHING materializes the anchor for a
// brand-new scope (an absent anchor observed as 0 by the guard becomes a
// concrete revision-0 row); the SELECT then reflects the transaction's snapshot.
// There is no FOR UPDATE: the enclosing BEGIN IMMEDIATE transaction is the write
// lock, so no concurrent writer can advance this anchor while the transaction is
// open.
func anchorLock(ctx context.Context, tx *tursodb.Tx, sk mutation.ScopeKey) (mutation.Revision, error) {
	if _, err := tx.Exec(ctx,
		`INSERT INTO iam_scopes (scope_kind, scope_type, scope_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		string(sk.Kind), sk.Type, sk.ID); err != nil {
		return 0, err
	}
	var rev int64
	if err := tx.QueryRow(ctx,
		`SELECT revision FROM iam_scopes WHERE scope_kind = ? AND scope_type = ? AND scope_id = ?`,
		string(sk.Kind), sk.Type, sk.ID).Scan(&rev); err != nil {
		return 0, tursodb.MapError(err)
	}
	return mutation.Revision(rev), nil
}

// scopeRevision reads a scope's current revision WITHOUT materializing an anchor
// (an absent anchor reads as 0) — the guard view's read of a dependency revision.
func scopeRevision(ctx context.Context, tx *tursodb.Tx, sk mutation.ScopeKey) (mutation.Revision, error) {
	var rev int64
	err := tx.QueryRow(ctx,
		`SELECT revision FROM iam_scopes WHERE scope_kind = ? AND scope_type = ? AND scope_id = ?`,
		string(sk.Kind), sk.Type, sk.ID).Scan(&rev)
	if errors.Is(tursodb.MapError(err), sdk.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, tursodb.MapError(err)
	}
	return mutation.Revision(rev), nil
}

// bumpRevision increments the scope anchor by exactly one and returns the new
// revision. Under BEGIN IMMEDIATE the anchor cannot change between anchorLock and
// this write, so the increment is atomic against every other writer.
func bumpRevision(ctx context.Context, tx *tursodb.Tx, sk mutation.ScopeKey) (mutation.Revision, error) {
	if _, err := tx.Exec(ctx,
		`UPDATE iam_scopes SET revision = revision + 1 WHERE scope_kind = ? AND scope_type = ? AND scope_id = ?`,
		string(sk.Kind), sk.Type, sk.ID); err != nil {
		return 0, err
	}
	var rev int64
	if err := tx.QueryRow(ctx,
		`SELECT revision FROM iam_scopes WHERE scope_kind = ? AND scope_type = ? AND scope_id = ?`,
		string(sk.Kind), sk.Type, sk.ID).Scan(&rev); err != nil {
		return 0, tursodb.MapError(err)
	}
	return mutation.Revision(rev), nil
}

// receiptRow is the db-tagged projection of an iam_mutations row.
type receiptRow struct {
	MutationID      string       `db:"mutation_id"`
	ScopeKind       string       `db:"scope_kind"`
	ScopeType       string       `db:"scope_type"`
	ScopeID         string       `db:"scope_id"`
	Operation       string       `db:"operation"`
	PayloadEncoding string       `db:"payload_encoding"`
	PayloadDigest   string       `db:"payload_digest"`
	Outcome         string       `db:"outcome"`
	Revision        int64        `db:"revision"`
	SchemaDigest    string       `db:"schema_digest"`
	CreatedAt       tursodb.Time `db:"created_at"`
}

func (r receiptRow) toReceipt() mutation.Receipt {
	return mutation.Receipt{
		MutationID:      mutation.MutationID(r.MutationID),
		Scope:           mutation.ScopeKey{Kind: mutation.ScopeKind(r.ScopeKind), Type: r.ScopeType, ID: r.ScopeID},
		Operation:       mutation.Operation(r.Operation),
		PayloadEncoding: r.PayloadEncoding,
		PayloadDigest:   r.PayloadDigest,
		Outcome:         mutation.Outcome(r.Outcome),
		Revision:        mutation.Revision(r.Revision),
		SchemaDigest:    r.SchemaDigest,
		CreatedAt:       r.CreatedAt.Time,
	}
}

// lookupReceipt returns the stored receipt for id, or (zero, false) when none.
func lookupReceipt(ctx context.Context, tx *tursodb.Tx, id mutation.MutationID) (mutation.Receipt, bool, error) {
	row, err := tursodb.QueryOne[receiptRow](ctx, tx,
		`SELECT mutation_id, scope_kind, scope_type, scope_id, operation, payload_encoding, payload_digest, outcome, revision, schema_digest, created_at
		 FROM iam_mutations WHERE mutation_id = ?`,
		string(id))
	if errors.Is(err, sdk.ErrNotFound) {
		return mutation.Receipt{}, false, nil
	}
	if err != nil {
		return mutation.Receipt{}, false, tursodb.MapError(err)
	}
	return row.toReceipt(), true, nil
}

// insertReceipt persists a committed receipt (permanent retention: expires_at left
// NULL). A cross-scope reuse of the same MutationID surfaces as a UNIQUE violation
// (already mapped to sdk.ErrAlreadyExists by the tx), remapped to the stable
// payload-mismatch command error.
func insertReceipt(ctx context.Context, tx *tursodb.Tx, r *mutation.Receipt) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO iam_mutations (mutation_id, scope_kind, scope_type, scope_id, operation, payload_encoding, payload_digest, outcome, revision, schema_digest, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.MutationID),
		string(r.Scope.Kind),
		r.Scope.Type,
		r.Scope.ID,
		string(r.Operation),
		r.PayloadEncoding,
		r.PayloadDigest,
		string(r.Outcome),
		int64(r.Revision),
		r.SchemaDigest,
		tursodb.FormatTime(r.CreatedAt),
	)
	if err != nil {
		if errors.Is(err, sdk.ErrAlreadyExists) {
			return mutation.ErrPayloadMismatch
		}
		return err
	}
	return nil
}
