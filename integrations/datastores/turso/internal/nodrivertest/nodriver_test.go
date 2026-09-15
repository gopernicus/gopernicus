package nodrivertest_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// TestOpenWithoutASQLiteDriverNamesTheImport: this binary registers no
// "sqlite"/"sqlite3" driver, so a local file cannot open, and the error must
// tell the host which package to import.
func TestOpenWithoutASQLiteDriverNamesTheImport(t *testing.T) {
	_, err := tursodb.Open(context.Background(), tursodb.Config{URL: "file:" + filepath.Join(t.TempDir(), "auth.db")})
	if err == nil {
		t.Fatal("Open succeeded without a registered sqlite driver")
	}
	const hint = "github.com/gopernicus/gopernicus/integrations/datastores/turso/localfile"
	if !strings.Contains(err.Error(), hint) {
		t.Fatalf("error = %v, want it to name %s", err, hint)
	}
}
