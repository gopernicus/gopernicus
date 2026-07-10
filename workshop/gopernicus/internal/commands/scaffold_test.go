package commands

// scaffold_test.go is LOAD-BEARING GUARD INFRASTRUCTURE, not an ordinary unit
// test (review-gate fold items 4 + 8). `gopernicus init` emits Go sources that
// live in no module until a user runs the CLI, so no per-module `make` target
// ever compiles them — they can rot silently. This test is the drift answer for
// those scaffold-once surfaces: it emits hosts into t.TempDir(), rewires their
// pre-tag replace directives to ABSOLUTE paths in THIS repo, and runs
// `go mod tidy && go build ./...` as a child process — proving the templates
// still produce a compiling host on every `make check`. It then runs the emitted
// host's guard SHAPES (the app one-rule grep + the G9/G10 hygiene patterns,
// reimplemented as Go string matching) so a template can never smuggle a
// boundary violation into emitted output. A silent skip is a rotting scaffold:
// the legs FAIL LOUD, never skip on a cold cache.

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The forbidden literals the emitted-host guard shapes reject. Assembled from
// fragments so this test file itself stays clean under the repo's own G9/G10
// guards, which grep the whole tree for the verbatim tokens.
var (
	underlyingCall = "." + "Underlying()"
	laxScanSymbol  = "RowToStructByName" + "Lax"
)

// TestScaffoldInitNoneCompiles is the hermetic leg: an sdk-only host builds fully
// offline (sdk is third-party-free, so an empty go.sum suffices). No network, no
// DB, no env.
func TestScaffoldInitNoneCompiles(t *testing.T) {
	root := repoRoot(t)
	target := t.TempDir()

	params, err := buildInitParams("example.com/scaffoldtest/none", "none")
	if err != nil {
		t.Fatal(err)
	}
	if err := emitInit(target, params); err != nil {
		t.Fatalf("emitInit: %v", err)
	}

	// Cheap template sanity: the healthz route is present.
	mainSrc := readFile(t, filepath.Join(target, "cmd", "server", "main.go"))
	if !strings.Contains(mainSrc, "/healthz") {
		t.Fatalf("emitted main.go missing /healthz route:\n%s", mainSrc)
	}

	replaceModule(t, target, baseModule+"/sdk", filepath.Join(root, "sdk"))

	env := hermeticEnv()
	runGo(t, target, env, "mod", "tidy")
	runGo(t, target, env, "build", "./...")

	assertGuardShapes(t, target)
}

// TestScaffoldInitTursoCompiles is the warm-cache leg: a --db=turso host tidies
// and builds with GOPROXY=off against the GOMODCACHE `make check` already warmed
// (the emitted go.mod pins the exact libsql version this repo's turso connector
// requires). A cold cache FAILS LOUD — it never skips.
func TestScaffoldInitTursoCompiles(t *testing.T) {
	root := repoRoot(t)
	target := t.TempDir()

	params, err := buildInitParams("example.com/scaffoldtest/turso", "turso")
	if err != nil {
		t.Fatal(err)
	}
	if err := emitInit(target, params); err != nil {
		t.Fatalf("emitInit: %v", err)
	}

	replaceModule(t, target, baseModule+"/sdk", filepath.Join(root, "sdk"))
	replaceModule(t, target, params.ConnectorPath, filepath.Join(root, filepath.FromSlash(params.ConnectorRel)))

	// GOFLAGS=-mod=mod lets tidy/build populate go.sum from the warm cache
	// (GOPROXY=off, from hermeticEnv) rather than demanding a pre-committed one.
	env := append(hermeticEnv(), "GOFLAGS=-mod=mod")
	runGo(t, target, env, "mod", "tidy")
	runGo(t, target, env, "build", "./...")

	assertGuardShapes(t, target)
}

// repoRoot resolves this repo's root from the test file location and sanity-checks
// it (go.work must live there).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../workshop/gopernicus/internal/commands/scaffold_test.go -> repo root
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		t.Fatalf("repo root sanity check failed (%s): %v", root, err)
	}
	return root
}

// hermeticEnv is the ambient environment with the workspace disabled and the
// module proxy off: the emitted host is a standalone module that must resolve
// with NO network — sdk is third-party-free (empty go.sum), and any driver deps
// come from the GOMODCACHE `make check` already warmed. A cold cache fails loud.
func hermeticEnv() []string {
	return append(os.Environ(), "GOWORK=off", "GOPROXY=off")
}

// replaceModule rewrites an emitted (commented) pre-tag replace into a functional
// absolute one via `go mod edit`. This is the pre-tag wiring the emitted README
// documents, injected here so the child build resolves against this repo.
func replaceModule(t *testing.T, dir, module, path string) {
	t.Helper()
	cmd := exec.Command("go", "mod", "edit", "-replace", module+"="+path)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod edit -replace %s: %v\n%s", module, err, out)
	}
}

func runGo(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// assertGuardShapes reimplements the emitted host's guard SHAPES as Go string
// matching over the emitted tree: the G9/G10 hygiene patterns everywhere, and
// the app one-rule over internal/logic (absent in an sdk-only host, so vacuously
// clean — the assertion proves the templates never emit a violation).
func assertGuardShapes(t *testing.T, target string) {
	t.Helper()
	err := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		s := string(b)
		rel, _ := filepath.Rel(target, path)
		if strings.Contains(s, underlyingCall) {
			t.Errorf("G9 shape violated: emitted %s reaches the raw pool escape hatch", rel)
		}
		if strings.Contains(s, laxScanSymbol) {
			t.Errorf("G10 shape violated: emitted %s uses lax struct scanning", rel)
		}
		if strings.Contains(filepath.ToSlash(path), "/internal/logic/") {
			if strings.Contains(s, "/internal/inbound") ||
				strings.Contains(s, "/internal/outbound") ||
				strings.Contains(s, baseModule+"/integrations") {
				t.Errorf("one-rule shape violated: emitted logic file %s imports an outward layer", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
