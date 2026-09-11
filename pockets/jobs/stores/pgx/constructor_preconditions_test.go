package pgx

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestRawStoreDatabasePrecondition(t *testing.T) {
	constructors := []struct {
		name     string
		database func(*pgxdb.DB) *pgxdb.DB
	}{
		{"NewFencedQueueStore", func(db *pgxdb.DB) *pgxdb.DB { return NewFencedQueueStore(db).db }},
		{"NewQueueStore", func(db *pgxdb.DB) *pgxdb.DB { return NewQueueStore(db).db }},
		{"NewScheduleStore", func(db *pgxdb.DB) *pgxdb.DB { return NewScheduleStore(db).db }},
		{"Repositories", func(db *pgxdb.DB) *pgxdb.DB { return Repositories(db).Queue.(*Queue).db }},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			// A zero wrapper cannot perform I/O. Construction only borrows it.
			db := &pgxdb.DB{}
			if got := constructor.database(db); got != db {
				t.Fatal("constructor did not retain the supplied database")
			}
			defer func() {
				recovered := recover()
				if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "nil database") {
					t.Fatalf("panic = %v; want nil database diagnostic", recovered)
				}
			}()
			constructor.database(nil)
		})
	}
}

func TestStatusCheckRejectsNilDatabase(t *testing.T) {
	if err := StatusCheck(t.Context(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("StatusCheck nil database: %v", err)
	}
}
