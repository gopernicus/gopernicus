package pgx

import (
	"context"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/storetest"
)

// Raw graph conformance keeps its adapter view separate from production wiring.
func testRepositories(ctx context.Context, db *pgxdb.DB, opts ...Option) (storetest.Repositories, error) {
	repos, err := Repositories(ctx, db, opts...)
	if err != nil {
		return storetest.Repositories{}, err
	}
	cfg := repos.Tuples.(*tupleStore).cfg
	return storetest.Repositories{Repositories: repos, Relationships: newRelationshipStore(db, cfg), SetOperationCommittedAt: func(t *testing.T, operationID string, committedAt time.Time) {
		t.Helper()
		// The fixtures connect as the table owner, which the 0006 trigger exempts.
		tag, err := db.Exec(context.Background(), "UPDATE "+cfg.schema.Table("iam_operations")+" SET committed_at=$2 WHERE operation_id=$1 AND outcome<>'fenced'", operationID, committedAt)
		if err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("set committed_at on %q: %v %v", operationID, tag, err)
		}
	}}, nil
}
