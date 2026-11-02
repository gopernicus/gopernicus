package turso

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

// Raw graph conformance keeps its adapter view separate from production wiring.
func testRepositories(ctx context.Context, db *tursodb.DB, opts ...Option) (storetest.Repositories, error) {
	repos, err := Repositories(ctx, db, opts...)
	if err != nil {
		return storetest.Repositories{}, err
	}
	cfg := repos.Tuples.(*tupleStore).cfg
	return storetest.Repositories{Repositories: repos, Relationships: newRelationshipStore(db, cfg), SetOperationCommittedAt: func(t *testing.T, operationID string, committedAt time.Time) {
		t.Helper()
		// The 0006 trigger refuses every UPDATE, so the record is re-inserted.
		err := db.InTx(context.Background(), func(tx *tursodb.Tx) error {
			var encoding, fingerprint, outcome string
			if err := tx.QueryRow(context.Background(), "SELECT encoding, fingerprint, outcome FROM main.iam_operations WHERE operation_id=? AND outcome<>'fenced'", operationID).Scan(&encoding, &fingerprint, &outcome); err != nil {
				return err
			}
			if _, err := tx.Exec(context.Background(), "DELETE FROM main.iam_operations WHERE operation_id=?", operationID); err != nil {
				return err
			}
			_, err := tx.Exec(context.Background(), "INSERT INTO main.iam_operations (operation_id, encoding, fingerprint, outcome, committed_at) VALUES (?, ?, ?, ?, ?)", operationID, encoding, fingerprint, outcome, tursodb.FormatTime(committedAt))
			return err
		})
		if err == sql.ErrNoRows {
			t.Fatalf("no non-fenced operation record %q", operationID)
		}
		if err != nil {
			t.Fatalf("set committed_at on %q: %v", operationID, err)
		}
	}, SetAuditOccurredAt: func(t *testing.T, id string, occurredAt time.Time) {
		t.Helper()
		// The 0006 trigger refuses every UPDATE, so it is dropped and re-created
		// around the rewrite in one transaction.
		err := db.InTx(context.Background(), func(tx *tursodb.Tx) error {
			if _, err := tx.Exec(context.Background(), "DROP TRIGGER IF EXISTS main.iam_audit_immutable_update"); err != nil {
				return err
			}
			result, err := tx.Exec(context.Background(), "UPDATE main.iam_audit SET occurred_at=? WHERE id=?", tursodb.FormatTime(occurredAt), id)
			if err != nil {
				return err
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				return fmt.Errorf("rows %d: %v", n, err)
			}
			_, err = tx.Exec(context.Background(), iamAuditImmutableUpdate)
			return err
		})
		if err != nil {
			t.Fatalf("set occurred_at on %q: %v", id, err)
		}
	}}, nil
}
