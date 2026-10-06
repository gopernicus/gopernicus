package turso

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
)

func (m *mutationStore) replayOperation(ctx context.Context, tx *writeTx, cmd mutations.Command) (*mutations.Result, error) {
	var encoding, fingerprint string
	var outcome mutations.Outcome
	err := tx.QueryRow(ctx, "SELECT encoding, fingerprint, outcome FROM "+"main.iam_operations"+" WHERE operation_id=?", cmd.OperationID).Scan(&encoding, &fingerprint, &outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if encoding != mutations.OperationEncoding {
		return nil, fmt.Errorf("authorization operation: unsupported encoding %q", encoding)
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
	tag, err := tx.Exec(ctx, "INSERT INTO "+"main.iam_operations"+" (operation_id, encoding, fingerprint, outcome, committed_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT (operation_id) DO NOTHING", cmd.OperationID, mutations.OperationEncoding, mutations.Fingerprint(cmd), outcome, tursodb.FormatTime(time.Now().UTC()))
	if err != nil {
		return err
	}
	affected, err := tag.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return mutations.ErrConcurrentMutation
	}
	return nil
}
