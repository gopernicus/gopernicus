package firestore

import (
	"os"
	"path/filepath"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// TestExportIndexesScaffoldsAnAbsentManifestIdempotently is the scaffold step's
// smoke test — the analogue of the SQL siblings' ExportMigrations check, and
// hermetic for the same reason (no database is involved in writing a file).
//
// It covers the two properties indexes_test.go's merge case does not: the
// CREATE-WHEN-ABSENT path (a host with no manifest yet, which is every
// greenfield Firestore host on its first scaffold), and byte-stability — the
// README tells hosts a re-export of an unchanged fragment is an empty diff, and
// a scaffold step that rewrote the file on every run would put churn in every
// host's diff. WHICH indexes the manifest carries is indexes_test.go's
// assertion, derived from the query matrix; nothing here pins content.
func TestExportIndexesScaffoldsAnAbsentManifestIdempotently(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, IndexesFile)

	if err := ExportIndexes(dst); err != nil {
		t.Fatalf("ExportIndexes into a directory with no manifest: %v", err)
	}
	first, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading the exported manifest: %v", err)
	}

	exported, err := firestoredb.ParseIndexManifest(os.DirFS(dir), IndexesFile)
	if err != nil {
		t.Fatalf("the exported manifest does not parse: %v", err)
	}
	if len(exported.Indexes) == 0 {
		t.Fatal("the exported manifest declares no composite index — this store's queries would run unindexed in production")
	}

	if err := ExportIndexes(dst); err != nil {
		t.Fatalf("re-exporting: %v", err)
	}
	again, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading the re-exported manifest: %v", err)
	}
	if string(again) != string(first) {
		t.Fatal("re-exporting an unchanged fragment rewrote the file — a scaffold re-run must be an empty diff")
	}
}
