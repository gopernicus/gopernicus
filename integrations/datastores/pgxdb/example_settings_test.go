package pgxdb_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

var errNoActor = errors.New("audit store called outside an actor-scoped transaction")

// auditStore reads the actor its audit triggers record from app.actor_id.
type auditStore struct{ db *pgxdb.DB }

// querier hands out the transaction only when it is db's own and
// TransactWithSettings applied app.actor_id to it. A plain Transact, or no
// transaction at all, is refused rather than falling back to the pool.
func (s auditStore) querier(ctx context.Context) (pgxdb.Querier, error) {
	tx, ok := s.db.TxFrom(ctx)
	if !ok {
		return nil, errNoActor
	}
	if _, ok := tx.Setting("app.actor_id"); !ok {
		return nil, errNoActor
	}
	return tx, nil
}

func (s auditStore) Record(ctx context.Context, action string) error {
	q, err := s.querier(ctx)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, "INSERT INTO audit_log (actor_id, action) VALUES (current_setting('app.actor_id'), $1)", action)
	return err
}

func ExampleDB_TransactWithSettings() {
	ctx := context.Background()
	db, err := pgxdb.Open(ctx, pgxdb.Config{DSN: "postgres://localhost:5432/app?sslmode=disable"})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer db.Close()
	store := auditStore{db: db}

	err = db.TransactWithSettings(ctx, []pgxdb.Setting{{Name: "app.actor_id", Value: "user-123"}}, func(ctx context.Context) error {
		return store.Record(ctx, "document.published")
	})
	fmt.Println(err)

	err = db.Transact(ctx, func(ctx context.Context) error {
		return store.Record(ctx, "document.published")
	})
	fmt.Println(errors.Is(err, errNoActor))
}
