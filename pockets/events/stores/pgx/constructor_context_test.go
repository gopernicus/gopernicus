package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

func TestConstructorDeadlineWhilePoolIsExhausted(t *testing.T) {
	db, err := pgxdb.Open(context.Background(), pgxdb.Config{DSN: requireDSN(t), MaxConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// The transaction holds the only connection, so a probe must wait for the
	// host's deadline without reaching any table or relying on schema state.
	err = db.InTx(context.Background(), func(_ *pgxdb.Tx) error {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := New(ctx, db)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("construction error = %v, want deadline exceeded", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
