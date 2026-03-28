package sqlitelimiter_test

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter/ratelimitertest"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter/sqlitelimiter"
)

func TestCompliance(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	store, err := sqlitelimiter.New(db)
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer store.Close()

	ratelimitertest.RunSuite(t, store)
}
