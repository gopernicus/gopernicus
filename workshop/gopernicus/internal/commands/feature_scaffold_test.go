package commands

// feature_scaffold_test.go is LOAD-BEARING GUARD INFRASTRUCTURE (review-gate fold
// items 4 + 8), the W3 sibling of scaffold_test.go. `gopernicus new feature` emits
// a standalone feature tree that lives in no module until a user runs the CLI, so
// no per-module `make` target ever compiles it — it can rot silently. These two
// legs are the drift answer:
//
//   - Hermetic leg: emit a feature into t.TempDir(), absolute-replace sdk, then
//     tidy + build + test the CORE (sdk-only, empty go.sum, fully offline). The
//     `go test ./...` exercises the storetest suite against the in-core memstore —
//     the six-case pagination family + DBGeneratedIDOnEmpty hermetically. Then the
//     FS1 + G2/G6/G10 guard SHAPES run over the emitted core.
//   - Warm-cache leg: absolute-replace sdk + the connector into each store module
//     and GOPROXY=off tidy + build + vet -tags=integration both. The emitted
//     go.mods PIN the exact driver versions this repo's stores use, so GOMODCACHE
//     is already warm from `make check`'s own builds (workshop is last in MODULES).
//     A cold cache FAILS LOUD — it never skips.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaffoldFeatureParams is the fixed identity the scaffold legs emit. The module
// ROOT is arbitrary (no tag exists); the feature/aggregate names differ from the
// aggregate default so the title-casing + plural-field derivation are exercised.
func scaffoldFeatureParams(t *testing.T) featureParams {
	t.Helper()
	p, err := buildFeatureParams("example.com/scaffoldtest", "widgets", "widget")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestScaffoldFeatureCoreCompiles is the hermetic leg: an sdk-only feature core
// builds and its storetest passes fully offline against the in-core memstore.
func TestScaffoldFeatureCoreCompiles(t *testing.T) {
	root := repoRoot(t)
	target := t.TempDir()
	params := scaffoldFeatureParams(t)

	if err := emitFeature(target, params); err != nil {
		t.Fatalf("emitFeature: %v", err)
	}

	// Cheap template sanity: the socket + both migrations landed.
	socket := readFile(t, filepath.Join(target, params.Feature+".go"))
	if !strings.Contains(socket, "func NewService(") || !strings.Contains(socket, ") Register(") {
		t.Fatalf("emitted socket missing the FS2 trio:\n%s", socket)
	}
	for _, m := range []string{
		filepath.Join("stores", "turso", "migrations", "0001_"+params.Agg+".sql"),
		filepath.Join("stores", "pgx", "migrations", "0001_"+params.Agg+".sql"),
	} {
		if _, err := os.Stat(filepath.Join(target, m)); err != nil {
			t.Fatalf("emitted migration missing: %s", m)
		}
	}

	replaceModule(t, target, baseModule+"/sdk", filepath.Join(root, "sdk"))

	env := hermeticEnv()
	runGo(t, target, env, "mod", "tidy")
	runGo(t, target, env, "build", "./...")
	runGo(t, target, env, "test", "./...")

	assertFeatureGuardShapes(t, target, params)
}

// TestScaffoldFeatureStoresCompile is the warm-cache leg: both emitted store
// modules tidy, build, and vet -tags=integration against the warm GOMODCACHE (no
// DB run — their conformance is env-gated by construction).
func TestScaffoldFeatureStoresCompile(t *testing.T) {
	root := repoRoot(t)
	target := t.TempDir()
	params := scaffoldFeatureParams(t)

	if err := emitFeature(target, params); err != nil {
		t.Fatalf("emitFeature: %v", err)
	}

	// GOFLAGS=-mod=mod lets tidy/build populate go.sum from the warm cache
	// (GOPROXY=off) rather than demanding a pre-committed one.
	env := append(hermeticEnv(), "GOFLAGS=-mod=mod")

	stores := []struct {
		dir     string
		connMod string
		connRel string
	}{
		{"stores/turso", baseModule + "/integrations/datastores/turso", "integrations/datastores/turso"},
		{"stores/pgx", baseModule + "/integrations/datastores/pgxdb", "integrations/datastores/pgxdb"},
	}
	for _, st := range stores {
		dir := filepath.Join(target, filepath.FromSlash(st.dir))
		// The core resolves via the emitted `replace => ../..`; sdk + the connector
		// are the pre-tag replaces a human adopter (and this test) injects.
		replaceModule(t, dir, baseModule+"/sdk", filepath.Join(root, "sdk"))
		replaceModule(t, dir, st.connMod, filepath.Join(root, filepath.FromSlash(st.connRel)))
		runGo(t, dir, env, "mod", "tidy")
		runGo(t, dir, env, "build", "./...")
		runGo(t, dir, env, "vet", "-tags=integration", "./...")
	}
}

// assertFeatureGuardShapes reimplements the emitted core's guard SHAPES as Go
// string matching (review-gate fold item 8): FS1 (go.mod requires sdk only),
// G2 (core imports no integrations/examples/own stores/views), G6/FS9 (no
// hand-rolled HTTP response writing in internal/), and G9/G10 hygiene.
func assertFeatureGuardShapes(t *testing.T, target string, p featureParams) {
	t.Helper()

	// FS1: the core go.mod requires exactly sdk.
	gomod := readFile(t, filepath.Join(target, "go.mod"))
	for _, mod := range moduleRequires(gomod) {
		if mod != baseModule+"/sdk" {
			t.Errorf("FS1 shape violated: emitted core go.mod requires %q (want sdk only)", mod)
		}
	}

	coreForbidden := []string{
		baseModule + "/integrations",
		baseModule + "/examples",
		p.ModulePath + "/stores",
		p.ModulePath + "/views",
	}

	err := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		slashed := filepath.ToSlash(path)
		inStores := strings.Contains(slashed, "/stores/")
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		s := string(b)
		rel, _ := filepath.Rel(target, path)

		// G9/G10 hygiene apply to the WHOLE emitted tree (stores included).
		if strings.Contains(s, underlyingCall) {
			t.Errorf("G9 shape violated: emitted %s reaches the raw pool escape hatch", rel)
		}
		if strings.Contains(s, laxScanSymbol) {
			t.Errorf("G10 shape violated: emitted %s uses lax struct scanning", rel)
		}

		// FS1/G2 (feature core isolation) and G6/FS9 apply to the CORE only — the
		// store modules legitimately import their connector.
		if inStores {
			return nil
		}
		for _, f := range coreForbidden {
			if strings.Contains(s, `"`+f) {
				t.Errorf("G2 shape violated: emitted core file %s imports an adapter layer (%s)", rel, f)
			}
		}
		if strings.Contains(slashed, "/internal/") && !strings.HasSuffix(path, "_test.go") {
			if strings.Contains(s, "json.NewEncoder(") || strings.Contains(s, "http.Error(") {
				t.Errorf("G6/FS9 shape violated: emitted core file %s hand-rolls an HTTP response", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// moduleRequires returns the module paths a go.mod requires, from both the
// single-line and block forms, skipping `// indirect` lines, comments, and the
// replace directives. It mirrors G5's awk parse.
func moduleRequires(gomod string) []string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(gomod, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "//"):
			continue
		case inBlock && trimmed == ")":
			inBlock = false
		case inBlock:
			if strings.Contains(trimmed, "// indirect") || trimmed == "" {
				continue
			}
			out = append(out, strings.Fields(trimmed)[0])
		case strings.HasPrefix(trimmed, "require ("):
			inBlock = true
		case strings.HasPrefix(trimmed, "require "):
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 && !strings.Contains(trimmed, "// indirect") {
				out = append(out, fields[1])
			}
		}
	}
	return out
}
