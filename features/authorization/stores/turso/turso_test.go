package turso

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestExportMigrations verifies the scaffold step copies every canonical
// migration file into the target dir verbatim — the host then owns the copies.
// This is the module's hermetic test: it runs on plain `go test ./...` (no
// integration tag, no datastore env), so the module is never silently untested.
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
