package commands

// scaffold_test.go is LOAD-BEARING GUARD INFRASTRUCTURE, not an ordinary unit
// test (review-gate fold items 4 + 8). `gopernicus init` emits Go sources that
// live in no module until a user runs the CLI, so no per-module `make` target
// ever compiles them — they can rot silently. This test is the drift answer for
// those scaffold-once surfaces: it emits hosts into t.TempDir(), adds framework
// replace directives to ABSOLUTE paths in THIS repo, and runs
// `go mod tidy && go build ./...` as a child process — proving the templates
// still produce a compiling host on every `make check`. Temporary host binaries
// also exercise configuration failures before serving or opening a database.
// It then runs the emitted
// host's guard SHAPES (the app one-rule grep + the G9/G10 hygiene patterns,
// reimplemented as Go string matching) so a template can never smuggle a
// boundary violation into emitted output. A silent skip is a rotting scaffold:
// the legs FAIL LOUD, never skip on a cold cache.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The forbidden literals the emitted-host guard shapes reject. Assembled from
// fragments so this test file itself stays clean under the repo's own G9/G10
// guards, which grep the whole tree for the verbatim tokens.
var (
	underlyingCall = "." + "Underlying()"
	laxScanSymbol  = "RowToStructByName" + "Lax"
)

// The .env.example tripwire's expectations: the shape no emitted line may take
// (a value followed by whitespace then a `#` hint) and the pins the emitted
// go.mod must carry. Bump them with every tag the templates move to.
var (
	inlineHint = regexp.MustCompile(`^[A-Z0-9_]+=\S*\s+#`)
	sdkPin     = baseModule + "/sdk v0.16.0"
	dbPins     = map[string]string{
		"pgx":   baseModule + "/integrations/datastores/pgxdb v0.11.0",
		"turso": baseModule + "/integrations/datastores/turso v0.8.0",
	}
)

// scaffoldModeKey is the runtime-mode key the emitted binaries read from the
// process environment.
const scaffoldModeKey = "APP_RUNTIME_MODE"

// TestScaffoldInitNoneCompiles is the hermetic leg: an sdk-only host builds fully
// offline (sdk is third-party-free, so an empty go.sum suffices). Startup checks
// use disposable dotenv files and no network or database.
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
	assertPostureClean(t, target)

	binary := filepath.Join(t.TempDir(), "server")
	runGo(t, target, env, "build", "-o", binary, "./cmd/server")
	t.Run("malformed dotenv", func(t *testing.T) {
		assertMalformedEnvRejected(t, binary)
	})
	t.Run("mode from the process environment only", func(t *testing.T) {
		_, stderr := runStartupFailure(t, binary, t.TempDir(), []string{"LOG_FORMAT=json", "LOG_OUTPUT=STDOUT"})
		if !strings.Contains(stderr, scaffoldModeKey+" is unset") {
			t.Fatalf("an unset mode must stop startup naming %s, got: %s", scaffoldModeKey, stderr)
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(scaffoldModeKey+"=development\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, stderr = runStartupFailure(t, binary, dir, []string{scaffoldModeKey + "=development", "LOG_FORMAT=json", "LOG_OUTPUT=STDOUT"})
		if !strings.Contains(stderr, ".env:1 names "+scaffoldModeKey) {
			t.Fatalf("a .env naming the mode must be refused, got: %s", stderr)
		}
	})
	t.Run("production requires PUBLIC_BASE_URL", func(t *testing.T) {
		stdout, _ := runStartupFailure(t, binary, t.TempDir(), []string{scaffoldModeKey + "=production", "LOG_FORMAT=json", "LOG_OUTPUT=STDOUT"})
		if !strings.Contains(stdout, "PUBLIC_BASE_URL is required in production") {
			t.Fatalf("production without PUBLIC_BASE_URL must stop startup, got: %s", stdout)
		}
	})
	t.Run("boots in development with HSTS", func(t *testing.T) {
		assertBootsInDevelopment(t, binary)
	})
	t.Run("configured startup error", func(t *testing.T) {
		dir := t.TempDir()
		stdout, stderr := runStartupFailure(t, binary, dir, []string{
			scaffoldModeKey + "=development", "LOG_FORMAT=json", "LOG_OUTPUT=STDOUT", "READ_TIMEOUT=invalid-duration",
		})
		if stderr != "" {
			t.Fatalf("configured startup error used stderr: %s", stderr)
		}
		var record struct {
			Level   string `json:"level"`
			Message string `json:"msg"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal([]byte(stdout), &record); err != nil {
			t.Fatalf("startup output is not one JSON log record: %v\n%s", err, stdout)
		}
		if record.Level != "ERROR" || record.Message != "server exited with error" || !strings.Contains(record.Error, "READ_TIMEOUT") {
			t.Fatalf("unexpected startup error record: %+v", record)
		}
	})
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
	assertPostureClean(t, target)

	for _, pkg := range []string{"cmd/server", "workshop/migrations"} {
		t.Run(pkg+" rejects malformed dotenv", func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "startup")
			runGo(t, target, env, "build", "-o", binary, "./"+pkg)
			assertMalformedEnvRejected(t, binary)
		})
	}
}

// TestScaffoldInitPgxCompiles is the pin leg: a --db=pgx host keeps its emitted
// pins — no framework replace — and tidies and builds with GOPROXY=off against
// the GOMODCACHE warm-scaffold-cache filled (TestWarmScaffoldModuleCache resolves
// this same host through the proxy). guard --posture is clean with P9 out of
// scope (the scaffold mounts no store), and the runner's -posture-role answers
// without a database while postureTables is empty. A cold cache FAILS LOUD.
func TestScaffoldInitPgxCompiles(t *testing.T) {
	target := t.TempDir()
	params, err := buildInitParams("example.com/scaffoldtest/pgx", "pgx")
	if err != nil {
		t.Fatal(err)
	}
	if err := emitInit(target, params); err != nil {
		t.Fatalf("emitInit: %v", err)
	}

	env := append(hermeticEnv(), "GOFLAGS=-mod=mod")
	runGo(t, target, env, "mod", "tidy")
	runGo(t, target, env, "build", "./...")
	gomod := readFile(t, filepath.Join(target, "go.mod"))
	for _, pin := range []string{sdkPin, dbPins["pgx"]} {
		if !strings.Contains(gomod, pin) {
			t.Errorf("tidied go.mod lost %q:\n%s", pin, gomod)
		}
	}

	assertGuardShapes(t, target)
	var code int
	out := captureStdout(t, func() int { code = Run([]string{"guard", "--posture", target}); return code })
	if code != 0 || !strings.Contains(out, "\nP9   no scope  (no framework store module mounted)\n") {
		t.Errorf("guard --posture over the pgx scaffold exited %d, want 0 with P9 out of scope:\n%s", code, out)
	}

	binary := filepath.Join(t.TempDir(), "migrations")
	runGo(t, target, env, "build", "-o", binary, "./workshop/migrations")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-posture-role", "app_runtime")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{scaffoldModeKey + "=development", "LOG_OUTPUT=STDOUT"}
	got, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(got)) != "no append-only tables mounted; add each mounted pgx store's AuditTables() to postureTables" {
		t.Fatalf("-posture-role with no tables = %v:\n%s", err, got)
	}
}

func assertMalformedEnvRejected(t *testing.T, binary string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("GOPERNICUS_STARTUP_TEST=\"dummy-private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := runStartupFailure(t, binary, dir, []string{scaffoldModeKey + "=development", "LOG_FORMAT=json", "LOG_OUTPUT=STDOUT"})
	if stdout != "" {
		t.Fatalf("malformed dotenv reached configured startup: %s", stdout)
	}
	if !strings.Contains(stderr, "load environment") || !strings.Contains(stderr, ".env") || strings.Contains(stderr, "dummy-private-value") {
		t.Fatalf("expected a value-free dotenv bootstrap error, got: %s", stderr)
	}
}

// assertBootsInDevelopment serves the emitted binary in development with no
// .env and proves GET /healthz answers 200 under the SecurityHeaders preset.
func assertBootsInDevelopment(t *testing.T, binary string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{scaffoldModeKey + "=development", "HOST=127.0.0.1", "PORT=" + port, "LOG_OUTPUT=STDOUT"}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var status int
	var hsts string
	for ctx.Err() == nil {
		resp, err := http.Get("http://127.0.0.1:" + port + "/healthz")
		if err == nil {
			resp.Body.Close()
			status, hsts = resp.StatusCode, resp.Header.Get("Strict-Transport-Security")
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if status != http.StatusOK || !strings.HasPrefix(hsts, "max-age=") {
		t.Fatalf("GET /healthz = %d with Strict-Transport-Security %q, want 200 with HSTS\n%s", status, hsts, out.String())
	}
}

// assertPostureClean runs `guard --posture` over an emitted host.
func assertPostureClean(t *testing.T, target string) {
	t.Helper()
	var code int
	out := captureStdout(t, func() int { code = Run([]string{"guard", "--posture", target}); return code })
	if code != 0 {
		t.Fatalf("guard --posture over the emitted host exited %d, want 0:\n%s", code, out)
	}
}

func runStartupFailure(t *testing.T, binary, dir string, env []string) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = dir
	cmd.Env = append([]string{}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("startup did not reject configuration before running: %v\nstdout: %s\nstderr: %s", ctx.Err(), stdout.String(), stderr.String())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("startup exit = %v, want status 1\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

// TestScaffoldInitEnvTemplate is the render-only tripwire for the emitted
// .env.example and go.mod (plan decision 5): the convention header is present,
// no key carries an inline hint after its value (the `KEY=   # hint` shape is
// #34 verbatim on any host whose sdk predates the LoadPath comment rule), and
// the sdk pin is the version this train tags. It renders only — no tidy, no
// build — so it never pulls a connector into the hermetic module cache.
func TestScaffoldInitEnvTemplate(t *testing.T) {
	for _, db := range []string{"pgx", "turso"} {
		t.Run(db, func(t *testing.T) {
			target := t.TempDir()
			params, err := buildInitParams("example.com/scaffoldtest/env", db)
			if err != nil {
				t.Fatal(err)
			}
			if err := emitInit(target, params); err != nil {
				t.Fatalf("emitInit: %v", err)
			}

			env := readFile(t, filepath.Join(target, ".env.example"))
			for _, header := range []string{
				"# An empty value (KEY=) or a commented-out key keeps the default.",
				`# Quote a value that contains " #" — an unquoted " #" starts a comment.`,
			} {
				if !strings.Contains(env, header) {
					t.Errorf("emitted .env.example missing header line %q:\n%s", header, env)
				}
			}
			for _, line := range strings.Split(env, "\n") {
				if inlineHint.MatchString(line) {
					t.Errorf("emitted .env.example keeps an inline hint after a value: %q", line)
				}
			}

			for _, line := range strings.Split(env, "\n") {
				if strings.Contains(line, scaffoldModeKey) && !strings.HasPrefix(strings.TrimSpace(line), "#") {
					t.Errorf("emitted .env.example assigns %s outside a comment: %q", scaffoldModeKey, line)
				}
			}
			if !strings.Contains(env, "# "+scaffoldModeKey+"=") {
				t.Errorf("emitted .env.example never mentions %s in a comment:\n%s", scaffoldModeKey, env)
			}
			if db == "pgx" && !strings.Contains(env, "\nDB_SSLMODE=verify-full\n") {
				t.Errorf("emitted pgx .env.example lacks DB_SSLMODE=verify-full:\n%s", env)
			}

			gomod := readFile(t, filepath.Join(target, "go.mod"))
			for _, pin := range []string{sdkPin, dbPins[db]} {
				if !strings.Contains(gomod, pin) {
					t.Errorf("emitted go.mod missing %q:\n%s", pin, gomod)
				}
			}

			assertPostureClean(t, target)
		})
	}
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

// replaceModule adds an absolute replacement via `go mod edit` so the child
// build exercises this checkout. Released hosts need no framework replacements.
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
