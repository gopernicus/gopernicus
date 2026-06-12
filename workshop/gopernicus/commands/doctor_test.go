package commands

import (
	"encoding/json"
	"testing"
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

func TestHasFlag(t *testing.T) {
	if !hasFlag([]string{"--json"}, "--json") {
		t.Error("expected --json detected")
	}
	if hasFlag([]string{"--jsonx", "tenancy"}, "--json") {
		t.Error("prefix must not match")
	}
}
