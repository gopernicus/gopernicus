package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/workshop/testing/testenv"
)

// TestFeatureSpecSourcesMatchFramework pins the featurespecs_tmpl.go snapshot
// to the framework's own feature queries.sql files byte-for-byte. If this
// fails, a spec changed under core/repositories — regenerate the snapshot in
// featurespecs_tmpl.go from the current sources.
func TestFeatureSpecSourcesMatchFramework(t *testing.T) {
	root, err := testenv.ProjectRoot()
	if err != nil {
		t.Fatalf("resolving framework root: %v", err)
	}

	for key, want := range featureSpecSources {
		path := filepath.Join(root, "core", "repositories", filepath.FromSlash(key), "queries.sql")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("reading framework source %s: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s drifted from the featurespecs_tmpl.go snapshot — regenerate the snapshot", key)
		}
	}
}

// TestFeatureSpecMappingCoversSources pins the snapshot against the on-disk
// feature repositories in both directions: every queries.sql under
// core/repositories is snapshotted, and every snapshot key has an on-disk
// source.
func TestFeatureSpecMappingCoversSources(t *testing.T) {
	root, err := testenv.ProjectRoot()
	if err != nil {
		t.Fatalf("resolving framework root: %v", err)
	}

	repoRoot := filepath.Join(root, "core", "repositories")
	onDisk := make(map[string]bool)
	matches, err := filepath.Glob(filepath.Join(repoRoot, "*", "*", "queries.sql"))
	if err != nil {
		t.Fatalf("globbing feature specs: %v", err)
	}
	for _, m := range matches {
		rel, err := filepath.Rel(repoRoot, filepath.Dir(m))
		if err != nil {
			t.Fatalf("relativizing %s: %v", m, err)
		}
		key := filepath.ToSlash(rel)
		onDisk[key] = true
		if _, ok := featureSpecSources[key]; !ok {
			t.Errorf("feature spec %s on disk is not in the featurespecs_tmpl.go snapshot", key)
		}
	}
	for key := range featureSpecSources {
		if !onDisk[key] {
			t.Errorf("snapshot key %s has no queries.sql under core/repositories", key)
		}
	}
}

// TestShippedSpec pins the exported lookup helper's key shape (domain + "/" +
// package) and its parsability — every shipped spec must parse, since
// generation falls back to ParseString on these sources.
func TestShippedSpec(t *testing.T) {
	spec, ok := ShippedSpec("auth", "users")
	if !ok {
		t.Fatal("ShippedSpec(auth, users) not found")
	}
	if !strings.Contains(spec, "-- @func:") {
		t.Error("auth/users shipped spec has no @func annotations")
	}
	if _, ok := ShippedSpec("auth", "notaspec"); ok {
		t.Error("ShippedSpec returned a spec for an unknown entity")
	}

	for key, src := range featureSpecSources {
		if _, err := ParseString(src); err != nil {
			t.Errorf("shipped spec %s does not parse: %v", key, err)
		}
	}
}
