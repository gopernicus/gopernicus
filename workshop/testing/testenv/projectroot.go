package testenv

import (
	"fmt"
	"os"
	"path/filepath"
)

// ProjectRoot walks up from the current working directory to the nearest
// directory containing go.mod and returns it. Under 'go test' the working
// directory is the directory of the package under test, so the walk resolves
// the enclosing module root no matter which package the tests run from.
// Generated integration/e2e/security bootstraps use it to locate the
// project's migrations directory.
func ProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("testenv: resolve working directory: %w", err)
	}
	start := dir
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("testenv: no go.mod found in or above %s", start)
		}
		dir = parent
	}
}
