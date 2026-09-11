package turso

import (
	"fmt"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

func TestRawStoreDatabasePrecondition(t *testing.T) {
	constructors := []struct {
		name     string
		database func(*tursodb.DB) *tursodb.DB
	}{
		{"NewFencedQueueStore", func(db *tursodb.DB) *tursodb.DB { return NewFencedQueueStore(db).db }},
		{"NewQueueStore", func(db *tursodb.DB) *tursodb.DB { return NewQueueStore(db).db }},
		{"NewScheduleStore", func(db *tursodb.DB) *tursodb.DB { return NewScheduleStore(db).db }},
		{"Repositories", func(db *tursodb.DB) *tursodb.DB { return Repositories(db).Queue.(*Queue).db }},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			// A zero wrapper cannot perform I/O. Construction only borrows it.
			db := &tursodb.DB{}
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
