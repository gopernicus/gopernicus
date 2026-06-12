package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/workshop/codegen/generators"
)

// The JSON field names are a stable contract — agents and scripts parse
// doctor --json output. These tests pin the shape.
func TestBuildDoctorResult(t *testing.T) {
	checks := []check{
		{name: "go.mod", passed: true},
		{name: "sql: Pred.Raw usage", passed: true, warn: true, detail: "store.go:12 raw predicate"},
		{name: "gopernicus framework", passed: false, detail: "not found in go.mod"},
	}
	result := buildDoctorResult("/proj", "v0.4.0", checks)

	if result.OK {
		t.Error("a hard failure must set ok=false")
	}
	if result.Root != "/proj" || result.Framework != "v0.4.0" {
		t.Errorf("root/framework = %q/%q", result.Root, result.Framework)
	}
	if len(result.Checks) != 3 {
		t.Fatalf("checks = %d, want 3", len(result.Checks))
	}
	if !result.Checks[1].Warn || !result.Checks[1].Passed {
		t.Error("warn check must carry warn=true and passed=true")
	}
}

func TestBuildDoctorResult_WarningsDoNotFail(t *testing.T) {
	result := buildDoctorResult("/proj", "", []check{
		{name: "a", passed: true},
		{name: "b", passed: true, warn: true, detail: "advisory"},
	})
	if !result.OK {
		t.Error("warnings alone must not set ok=false")
	}
}

func TestDoctorResultJSONShape(t *testing.T) {
	result := buildDoctorResult("/proj", "v0.4.0", []check{
		{name: "go.mod", passed: true},
		{name: "broken", passed: false, detail: "why"},
	})
	out, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"root", "framework", "ok", "checks"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("JSON output missing stable key %q", key)
		}
	}
	checks, ok := decoded["checks"].([]any)
	if !ok || len(checks) != 2 {
		t.Fatalf("checks = %v", decoded["checks"])
	}
	first, ok := checks[0].(map[string]any)
	if !ok {
		t.Fatalf("check[0] = %v", checks[0])
	}
	for _, key := range []string{"name", "passed"} {
		if _, ok := first[key]; !ok {
			t.Errorf("check object missing stable key %q", key)
		}
	}
	// Empty-detail and warn=false omit their keys — keep payloads tight.
	if _, present := first["detail"]; present {
		t.Error("empty detail must be omitted")
	}
	if _, present := first["warn"]; present {
		t.Error("warn=false must be omitted")
	}
}

func writeBootstrapFixture(t *testing.T, root, rel, firstLine string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	content := firstLine + "\npackage x\n"
	if firstLine == "" {
		content = "package x\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckBootstrapDrift(t *testing.T) {
	currentHash, ok := generators.BootstrapTemplateHash("repository/repository.go")
	if !ok {
		t.Fatal("repository/repository.go not in registry")
	}
	marker := func(kind, hash string) string {
		return "// gopernicus:bootstrap kind=" + kind + " template=" + hash
	}

	t.Run("current marker passes", func(t *testing.T) {
		root := t.TempDir()
		writeBootstrapFixture(t, root, "core/repositories/d/e/repository.go", marker("repository/repository.go", currentHash))
		c := checkBootstrapDrift(root)
		if !c.passed || c.warn {
			t.Errorf("check = %+v, want pass without warn", c)
		}
		if !strings.Contains(c.detail, "1 tracked") {
			t.Errorf("detail = %q", c.detail)
		}
	})

	t.Run("stale marker warns", func(t *testing.T) {
		root := t.TempDir()
		writeBootstrapFixture(t, root, "core/repositories/d/e/repository.go", marker("repository/repository.go", "000000000000"))
		c := checkBootstrapDrift(root)
		if !c.passed || !c.warn {
			t.Errorf("check = %+v, want pass with warn", c)
		}
		if !strings.Contains(c.detail, "repository.go") || !strings.Contains(c.detail, "older template") {
			t.Errorf("detail = %q", c.detail)
		}
	})

	t.Run("unknown kind warns as drift", func(t *testing.T) {
		root := t.TempDir()
		writeBootstrapFixture(t, root, "bridge/repositories/foo/bridge.go", marker("retired/bridge.go", "abcabcabcabc"))
		c := checkBootstrapDrift(root)
		if !c.warn || !strings.Contains(c.detail, "unknown kind") {
			t.Errorf("check = %+v, want unknown-kind drift warn", c)
		}
	})

	t.Run("pre-marker files counted not warned", func(t *testing.T) {
		root := t.TempDir()
		writeBootstrapFixture(t, root, "core/repositories/d/e/repository.go", "")
		writeBootstrapFixture(t, root, "bridge/repositories/foo/bridge.go", "// plain comment")
		c := checkBootstrapDrift(root)
		if !c.passed || c.warn {
			t.Errorf("check = %+v, want plain pass", c)
		}
		if !strings.Contains(c.detail, "2 pre-marker") {
			t.Errorf("detail = %q", c.detail)
		}
	})

	t.Run("files outside generated trees ignored", func(t *testing.T) {
		root := t.TempDir()
		writeBootstrapFixture(t, root, "app/server/store.go", marker("pgxstore/store.go", "000000000000"))
		c := checkBootstrapDrift(root)
		if c.warn || c.detail != "" {
			t.Errorf("check = %+v, want empty pass", c)
		}
	})
}

func TestHasFlag(t *testing.T) {
	if !hasFlag([]string{"--json"}, "--json") {
		t.Error("expected --json detected")
	}
	if hasFlag([]string{"--jsonx", "tenancy"}, "--json") {
		t.Error("prefix must not match")
	}
}
