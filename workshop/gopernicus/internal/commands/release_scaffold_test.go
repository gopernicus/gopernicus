//go:build release

package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Unlike the ordinary scaffold checks, this gate resolves the emitted framework
// pins through GOPROXY, without replacing them with this checkout. Before tagging,
// point GOPROXY at the candidate module proxy; after tagging, use the public proxy.
func TestReleaseScaffoldsResolvePinnedModules(t *testing.T) {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GOWORK", "GOFLAGS", "POSTGRES_TEST_DSN", "TURSO_DATABASE_URL", "TURSO_AUTH_TOKEN":
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "GOWORK=off", "GOFLAGS=-mod=mod")

	for _, db := range []string{"none", "pgx", "turso"} {
		t.Run("host/"+db, func(t *testing.T) {
			target := t.TempDir()
			params, err := buildInitParams("example.com/releasecheck/"+db, db)
			if err != nil {
				t.Fatal(err)
			}
			if err := emitInit(target, params); err != nil {
				t.Fatal(err)
			}
			verifyReleaseScaffold(t, target, env)
		})
	}

	t.Run("pocket", func(t *testing.T) {
		target := t.TempDir()
		if err := emitPocket(target, scaffoldPocketParams(t)); err != nil {
			t.Fatal(err)
		}
		for _, rel := range []string{".", "stores/pgx", "stores/turso"} {
			t.Run(rel, func(t *testing.T) {
				verifyReleaseScaffold(t, filepath.Join(target, rel), env)
			})
		}
	})
}

func verifyReleaseScaffold(t *testing.T, dir string, env []string) {
	t.Helper()
	runGo(t, dir, env, "mod", "tidy")

	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m -json all: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var module struct {
			Path    string
			Version string
			Replace *struct{ Path string }
		}
		if err := decoder.Decode(&module); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if strings.HasPrefix(module.Path, baseModule+"/") {
			if module.Replace != nil {
				t.Fatalf("framework module %s is replaced by %s", module.Path, module.Replace.Path)
			}
			t.Logf("%s %s", module.Path, module.Version)
		}
	}

	runGo(t, dir, env, "build", "./...")
	runGo(t, dir, env, "test", "-count=1", "./...")
	runGo(t, dir, env, "vet", "-tags=integration", "./...")
}
