package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapRegistry(t *testing.T) {
	if len(bootstrapTemplates) == 0 {
		t.Fatal("registry is empty")
	}
	hashes := make(map[string]string)
	for kind, tmpl := range bootstrapTemplates {
		if tmpl == "" {
			t.Errorf("kind %q has an empty template", kind)
		}
		if !strings.Contains(kind, "/") {
			t.Errorf("kind %q must be <generator>/<file>", kind)
		}
		h, ok := BootstrapTemplateHash(kind)
		if !ok || len(h) != 12 {
			t.Errorf("BootstrapTemplateHash(%q) = %q, %v", kind, h, ok)
		}
		hashes[kind] = h
	}
	if _, ok := BootstrapTemplateHash("nope/nope.go"); ok {
		t.Error("unknown kind must not resolve")
	}
}

func TestBootstrapMarkerRoundTrip(t *testing.T) {
	out := prependBootstrapMarker("repository/repository.go", []byte("package x\n"))
	firstLine, _, _ := strings.Cut(string(out), "\n")

	kind, hash, ok := ParseBootstrapMarker(firstLine)
	if !ok {
		t.Fatalf("marker line did not parse: %q", firstLine)
	}
	if kind != "repository/repository.go" {
		t.Errorf("kind = %q", kind)
	}
	want, _ := BootstrapTemplateHash(kind)
	if hash != want {
		t.Errorf("hash = %q, want %q", hash, want)
	}

	if _, _, ok := ParseBootstrapMarker("// just a comment"); ok {
		t.Error("non-marker line must not parse")
	}
}

func TestPrependBootstrapMarkerPanicsOnUnknownKind(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic for unregistered kind — unmarked bootstraps must fail loud")
		}
	}()
	prependBootstrapMarker("unregistered/file.go", []byte("package x\n"))
}

// Generated bootstrap files must carry the marker as their first line and
// still survive gofmt — verified through a real generator (fixtures).
func TestGeneratedBootstrapCarriesMarker(t *testing.T) {
	dir := t.TempDir()
	data := FixtureTemplateData{
		ModulePath: "example.com/app",
		Entities:   []FixtureEntity{BuildFixtureEntity(principalsFixtureResolved(), "example.com/app")},
	}
	if err := GenerateFixtures(data, dir, Options{}); err != nil {
		t.Fatalf("GenerateFixtures: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "fixtures.go"))
	if err != nil {
		t.Fatal(err)
	}
	firstLine, _, _ := strings.Cut(string(out), "\n")
	kind, hash, ok := ParseBootstrapMarker(firstLine)
	if !ok {
		t.Fatalf("fixtures.go first line is not a marker: %q", firstLine)
	}
	if kind != "fixtures/fixtures.go" {
		t.Errorf("kind = %q", kind)
	}
	if want, _ := BootstrapTemplateHash(kind); hash != want {
		t.Errorf("hash = %q, want %q", hash, want)
	}

	// The generated (non-bootstrap) file must NOT carry a marker.
	gen, err := os.ReadFile(filepath.Join(dir, "generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	genFirst, _, _ := strings.Cut(string(gen), "\n")
	if _, _, ok := ParseBootstrapMarker(genFirst); ok {
		t.Error("generated.go must not carry a bootstrap marker")
	}
}
