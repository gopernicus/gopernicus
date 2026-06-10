package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectRoot(t *testing.T) {
	root, err := ProjectRoot()
	if err != nil {
		t.Fatalf("ProjectRoot: %v", err)
	}
	if !filepath.IsAbs(root) {
		t.Errorf("ProjectRoot returned a non-absolute path: %s", root)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Errorf("ProjectRoot %s does not contain go.mod: %v", root, err)
	}
}
