//go:build warmcache

package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWarmScaffoldModuleCache makes the hermetic scaffold-compile tests'
// warm-cache assumption true by construction. Those tests (scaffold_test.go,
// feature_scaffold_test.go, db_test.go) tidy emitted modules with GOPROXY=off
// against "the GOMODCACHE `make check` already warmed" — but an emitted
// module's ISOLATED MVS can select transitive versions LOWER than any repo
// module ever downloads (golang.org/x/sys, ncruces/go-strftime via the libsql
// graph), so a minimal cache — CI's — fails them loud even though every repo
// module builds. This test re-runs the SAME template emissions with the module
// proxy ON and tidies them, downloading exactly the module set the hermetic
// tidies will then resolve offline. `make check` runs it (build tag warmcache,
// -count=1) before the module test loop; plain `go test` excludes it, so the
// compile tests themselves stay hermetic.
func TestWarmScaffoldModuleCache(t *testing.T) {
	root := repoRoot(t)

	// Shape 1 — the --db=turso init host (TestScaffoldInitTursoCompiles and
	// TestDBFileFallbackEndToEnd tidy this exact graph).
	initDir := t.TempDir()
	params, err := buildInitParams("example.com/scaffoldwarm/turso", "turso")
	if err != nil {
		t.Fatal(err)
	}
	if err := emitInit(initDir, params); err != nil {
		t.Fatalf("emitInit: %v", err)
	}
	replaceModule(t, initDir, baseModule+"/sdk", filepath.Join(root, "sdk"))
	replaceModule(t, initDir, params.ConnectorPath, filepath.Join(root, filepath.FromSlash(params.ConnectorRel)))
	runGo(t, initDir, warmEnv(), "mod", "tidy")

	// Shape 2 — the feature scaffold's two store modules
	// (TestScaffoldFeatureStoresCompile tidies both). The sdk-only core and
	// --db=memory init shapes need no network (sdk is third-party-free), so
	// they are not warmed.
	featDir := t.TempDir()
	fparams := scaffoldFeatureParams(t)
	if err := emitFeature(featDir, fparams); err != nil {
		t.Fatalf("emitFeature: %v", err)
	}
	stores := []struct {
		dir     string
		connMod string
		connRel string
	}{
		{"stores/turso", baseModule + "/integrations/datastores/turso", "integrations/datastores/turso"},
		{"stores/pgx", baseModule + "/integrations/datastores/pgxdb", "integrations/datastores/pgxdb"},
	}
	for _, st := range stores {
		dir := filepath.Join(featDir, filepath.FromSlash(st.dir))
		replaceModule(t, dir, baseModule+"/sdk", filepath.Join(root, "sdk"))
		replaceModule(t, dir, st.connMod, filepath.Join(root, filepath.FromSlash(st.connRel)))
		runGo(t, dir, warmEnv(), "mod", "tidy")
	}
}

// warmEnv is hermeticEnv minus GOPROXY=off: workspace off so each emitted
// module resolves standalone, -mod=mod so tidy may write go.sum, and the
// module proxy left ON so tidy can download what the cache lacks.
func warmEnv() []string {
	return append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
}
