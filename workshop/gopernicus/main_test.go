package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var binPath string

// TestMain builds the gopernicus tool binary once so individual tests can
// exec it directly (including from working directories outside the module).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "gopernicus-smoke")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating temp dir:", err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "gopernicus")

	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building gopernicus binary: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestVersionCommand(t *testing.T) {
	out, err := exec.Command(binPath, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version exited with error: %v\n%s", err, out)
	}
	if !strings.HasPrefix(string(out), "gopernicus ") {
		t.Fatalf("expected output to start with %q, got: %s", "gopernicus ", out)
	}
}

func TestGenerateWithoutManifest(t *testing.T) {
	tmp := t.TempDir()
	// A go.mod marks the directory as a project root; the missing
	// gopernicus.yml is then the reported failure.
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module smoketest\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "generate")
	cmd.Dir = tmp
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected generate to fail without gopernicus.yml, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "gopernicus.yml") {
		t.Fatalf("expected output to mention gopernicus.yml, got: %s", out)
	}
}
