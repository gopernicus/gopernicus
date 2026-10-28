package pgx

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// TestExportMigrations verifies the scaffold step copies every canonical
// migration file into the target dir verbatim — the host then owns the copies.
func TestExportMigrations(t *testing.T) {
	dst := t.TempDir()
	if err := ExportMigrations(dst); err != nil {
		t.Fatalf("ExportMigrations: %v", err)
	}

	srcEntries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	if len(srcEntries) == 0 {
		t.Fatal("no embedded migrations found")
	}

	for _, e := range srcEntries {
		if e.IsDir() {
			continue
		}
		want, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+e.Name())
		if err != nil {
			t.Fatalf("read embedded %s: %v", e.Name(), err)
		}
		got, err := os.ReadFile(filepath.Join(dst, e.Name()))
		if err != nil {
			t.Fatalf("read exported %s: %v", e.Name(), err)
		}
		if string(got) != string(want) {
			t.Errorf("exported %s differs from canonical source", e.Name())
		}
	}
}

// TestRepositoriesFillsFencedQueue pins that Repositories fills the fenced port
// with this package's store over the same database, and that WithSchema reaches
// it: a fenced store left unqualified would read and write the host's own
// unqualified fenced_job_queue.
func TestRepositoriesFillsFencedQueue(t *testing.T) {
	db := &pgxdb.DB{}
	schema, err := pgxdb.NewSchema("jobs_tenant")
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	for _, tc := range []struct {
		name string
		opts []Option
		want string
	}{
		{"unqualified", nil, "fenced_job_queue"},
		{"WithSchema", []Option{WithSchema(schema)}, `"jobs_tenant".fenced_job_queue`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repos := Repositories(db, tc.opts...)
			fenced, ok := repos.FencedQueue.(*FencedQueue)
			if !ok || fenced == nil {
				t.Fatalf("Repositories FencedQueue = %T; want non-nil *FencedQueue", repos.FencedQueue)
			}
			if fenced.db != db {
				t.Error("FencedQueue did not retain the supplied database")
			}
			if got := fenced.table("fenced_job_queue"); got != tc.want {
				t.Errorf("FencedQueue table = %q; want %q", got, tc.want)
			}
		})
	}
}
