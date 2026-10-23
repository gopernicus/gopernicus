package turso

import (
	"context"
	"database/sql"
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
	}}, nil
}
