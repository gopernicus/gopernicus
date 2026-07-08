package pgxdb

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"testing/fstest"
)

// TestExportMigrations verifies the scaffold helper copies every regular file at
// dir verbatim, skips subdirectories, and creates a not-yet-existing dst.
func TestExportMigrations(t *testing.T) {
	src := fstest.MapFS{
		"migrations/0001_init.sql":   {Data: []byte("CREATE TABLE a (id TEXT);\n")},
		"migrations/0002_more.sql":   {Data: []byte("ALTER TABLE a ADD COLUMN b TEXT;\n")},
		"migrations/sub/0003_ig.sql": {Data: []byte("-- nested, must be skipped\n")},
	}

	// dst does not exist yet — ExportMigrations must create the full path.
	dst := filepath.Join(t.TempDir(), "nested", "migrations")

	if err := ExportMigrations(src, "migrations", dst); err != nil {
		t.Fatalf("ExportMigrations: %v", err)
	}

	entries, err := os.ReadDir(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(got)

	want := []string{"0001_init.sql", "0002_more.sql"}
	if len(got) != len(want) {
		t.Fatalf("file set = %v, want %v (subdirectory contents must not be copied)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("file set = %v, want %v", got, want)
		}
	}

	for name, f := range src {
		base := filepath.Base(name)
		if base == "0003_ig.sql" {
			if _, err := os.Stat(filepath.Join(dst, base)); !os.IsNotExist(err) {
				t.Fatalf("nested file %s was copied; want skipped", base)
			}
			continue
		}
		out, err := os.ReadFile(filepath.Join(dst, base))
		if err != nil {
			t.Fatalf("read exported %s: %v", base, err)
		}
		if string(out) != string(f.Data) {
			t.Errorf("exported %s = %q, want %q", base, out, f.Data)
		}
	}
}
