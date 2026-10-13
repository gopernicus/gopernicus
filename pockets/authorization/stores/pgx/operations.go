package pgx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/jackc/pgx/v5"
)

func (m *mutationStore) replayOperation(ctx context.Context, tx *writeTx, cmd mutations.Command) (*mutations.Result, error) {
	var encoding, fingerprint string
	var outcome mutations.Outcome
	err := tx.QueryRow(ctx, "SELECT encoding, fingerprint, outcome FROM "+m.schema.Table("iam_operations")+" WHERE operation_id=$1", cmd.OperationID).Scan(&encoding, &fingerprint, &outcome)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if encoding != mutations.OperationEncoding {
		return nil, fmt.Errorf("authorization operation: unsupported encoding %q", encoding)
	}
	if outcome == mutations.OutcomeFenced {
		return &mutations.Result{Outcome: mutations.OutcomeFenced, Replayed: true, Superseded: true}, nil
	}
	if fingerprint != mutations.Fingerprint(cmd) {
		return nil, mutations.ErrOperationMismatch
	}
	if !outcome.Valid() {
		return nil, fmt.Errorf("authorization operation: invalid outcome %q", outcome)
	}
	facts, err := m.readFacts(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	return &mutations.Result{Outcome: outcome, Replayed: true, Superseded: mutations.Superseded(cmd, facts)}, nil
}

func (m *mutationStore) recordOperation(ctx context.Context, tx *writeTx, cmd mutations.Command, outcome mutations.Outcome) error {
	tag, err := tx.Exec(ctx, "INSERT INTO "+m.schema.Table("iam_operations")+" (operation_id, encoding, fingerprint, outcome, committed_at) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (operation_id) DO NOTHING", cmd.OperationID, mutations.OperationEncoding, mutations.Fingerprint(cmd), outcome, time.Now().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return mutations.ErrConcurrentMutation
	}
	return nil
}

// Fence writes a fenced record under the same lock and isolation as Apply, so a
// fence and an Apply are totally ordered and an in-flight Apply sees the fence at
// its replay lookup. An existing record is returned unchanged.
func (m *mutationStore) Fence(ctx context.Context, operationID string) (*mutations.FenceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := pgxdb.TxFromContext(ctx); ok {
		return nil, mutations.ErrMutationInsideTransaction
	}
	if err := tuples.ValidateRefField("operation id", operationID); err != nil {
		return nil, err
	}
	var result *mutations.FenceResult
	err := retryMutation(ctx, func() (error, bool) {
		result = nil
		err := m.db.InTx(ctx, func(tx *pgxdb.Tx) error {
			if _, err := tx.Exec(ctx, `SET TRANSACTION ISOLATION LEVEL READ COMMITTED`); err != nil {
				return err
			}
			if err := lockAuthorization(ctx, tx, m.schema); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, "INSERT INTO "+m.schema.Table("iam_operations")+" (operation_id, encoding, fingerprint, outcome, committed_at) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (operation_id) DO NOTHING", operationID, mutations.OperationEncoding, mutations.FenceFingerprint, mutations.OutcomeFenced, time.Now().UTC())
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 1 {
				result = &mutations.FenceResult{Outcome: mutations.OutcomeFenced}
				return nil
			}
			var outcome mutations.Outcome
			err = tx.QueryRow(ctx, "SELECT outcome FROM "+m.schema.Table("iam_operations")+" WHERE operation_id=$1", operationID).Scan(&outcome)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("authorization operation: fence conflicted but no record exists for %q", operationID)
			}
			if err != nil {
				return err
			}
			if !outcome.Valid() {
				return fmt.Errorf("authorization operation: invalid outcome %q", outcome)
			}
			result = &mutations.FenceResult{Outcome: outcome, Recorded: true}
			return nil
		})
		return err, false
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Recorded reads the operation record on the pool without the write lock.
func (m *mutationStore) Recorded(ctx context.Context, operationID string) (mutations.Outcome, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if err := tuples.ValidateRefField("operation id", operationID); err != nil {
		return "", false, err
	}
	var outcome mutations.Outcome
	err := m.db.QueryRow(ctx, "SELECT outcome FROM "+m.schema.Table("iam_operations")+" WHERE operation_id=$1", operationID).Scan(&outcome)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, pgxdb.MapError(err)
	}
	if !outcome.Valid() {
		return "", false, fmt.Errorf("authorization operation: invalid outcome %q", outcome)
	}
	return outcome, true, nil
}
