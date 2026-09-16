package pgx

import (
	"context"

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
	return storetest.Repositories{Repositories: repos, Relationships: newRelationshipStore(db, cfg)}, nil
}
