package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeMigrationName(t *testing.T) {
	cases := map[string]string{
		"Add Notes":       "add_notes",
		"second-thing":    "second_thing",
		"MixedCASE_123":   "mixedcase_123",
		"weird!@#chars":   "weirdchars",
		"  spaced  out  ": "__spaced__out__",
		"!!!":             "",
	}
	for in, want := range cases {
		if got := sanitizeMigrationName(in); got != want {
			t.Errorf("sanitizeMigrationName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNextMigrationNumber(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  int
	}{
		{"empty ledger", nil, 1},
		{"single", []string{"0001_a.sql"}, 2},
		{"gap keeps max+1", []string{"0001_a.sql", "0003_c.sql"}, 4},
		{"skips underscore-prefixed", []string{"0002_b.sql", "_0009_scratch.sql"}, 3},
		{"parse before first underscore", []string{"0009_a_b_c.sql"}, 10},
		{"non-numeric contributes zero", []string{"0007_a.sql", "notanumber.sql"}, 8},
		{"non-sql ignored", []string{"0004_a.sql", "README.md"}, 5},
		{"unpadded leading digits", []string{"12_x.sql"}, 13},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range c.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := nextMigrationNumber(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("nextMigrationNumber(%v) = %d, want %d", c.files, got, c.want)
			}
		})
	}
}

func TestNextMigrationNumberAbsentDir(t *testing.T) {
	got, err := nextMigrationNumber(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("absent ledger = %d, want 1", got)
	}
}

func TestCreateMigrationNumbersAndSanitizes(t *testing.T) {
	root := t.TempDir()

	p1, err := createMigration(root, defaultLedger, "Add Notes!")
	if err != nil {
		t.Fatal(err)
	}
	if base := filepath.Base(p1); base != "0001_add_notes.sql" {
		t.Fatalf("first migration = %q, want 0001_add_notes.sql", base)
	}
	if got := readFile(t, p1); !strings.Contains(got, "-- migration: 0001_add_notes.sql") {
		t.Fatalf("missing header comment: %q", got)
	}
	// It landed in workshop/migrations/<ledger>/, not the host root.
	wantDir := ledgerDir(root, defaultLedger)
	if filepath.Dir(p1) != wantDir {
		t.Fatalf("migration dir = %q, want %q", filepath.Dir(p1), wantDir)
	}

	p2, err := createMigration(root, defaultLedger, "second-thing")
	if err != nil {
		t.Fatal(err)
	}
	if base := filepath.Base(p2); base != "0002_second_thing.sql" {
		t.Fatalf("second migration = %q, want 0002_second_thing.sql", base)
	}
}

func TestCreateMigrationRejectsEmptySlug(t *testing.T) {
	if _, err := createMigration(t.TempDir(), defaultLedger, "!!!"); err == nil {
		t.Fatal("expected an error for a slug that sanitizes to empty")
	}
}

func TestCreateMigrationRefusesClobber(t *testing.T) {
	root := t.TempDir()
	dir := ledgerDir(root, defaultLedger)
	// A directory named like the next migration is skipped by the numbering scan
	// (directories are skipped) yet occupies the computed path — standing in for
	// the concurrent same-slug collision the max+1 rule can hit. createMigration
	// must refuse rather than clobber.
	if err := os.MkdirAll(filepath.Join(dir, "0001_dup.sql"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := createMigration(root, defaultLedger, "dup")
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("createMigration should refuse to clobber, got err=%v", err)
	}
}

func TestCreateMigrationCustomLedger(t *testing.T) {
	root := t.TempDir()
	p, err := createMigration(root, "secondary", "thing")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "workshop", "migrations", "secondary", "0001_thing.sql")
	if p != want {
		t.Fatalf("custom-ledger path = %q, want %q", p, want)
	}
}

func TestMigrateRunnerMissing(t *testing.T) {
	root := t.TempDir() // no workshop/migrations/main.go
	var out, errBuf bytes.Buffer
	if code := migrateRunner(root, &out, &errBuf); code != 1 {
		t.Fatalf("migrateRunner (no runner) exit = %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "gopernicus init") {
		t.Fatalf("missing actionable init hint: %q", errBuf.String())
	}
}

func TestStatusRunnerMissingFallsBack(t *testing.T) {
	root := t.TempDir()
	seedLedger(t, ledgerDir(root, defaultLedger), "0001_a.sql", "0002_b.sql")

	var out, errBuf bytes.Buffer
	if code := statusRunner(root, defaultLedger, &out, &errBuf); code != 0 {
		t.Fatalf("statusRunner (no runner) exit = %d, want 0; stderr=%q", code, errBuf.String())
	}
	s := out.String()
	if !strings.Contains(s, "file view") {
		t.Fatalf("missing file-view note: %q", s)
	}
	if !strings.Contains(s, "pending (2)") ||
		!strings.Contains(s, "0001_a.sql") ||
		!strings.Contains(s, "0002_b.sql") {
		t.Fatalf("file-only listing wrong: %q", s)
	}
}

func TestStatusFileOnlyListingFilters(t *testing.T) {
	root := t.TempDir()
	seedLedger(t, ledgerDir(root, defaultLedger),
		"0001_a.sql", "0002_b.sql", "_scratch.sql", "README.md")

	var out, errBuf bytes.Buffer
	if code := statusRunner(root, defaultLedger, &out, &errBuf); code != 0 {
		t.Fatalf("statusRunner exit = %d; stderr=%q", code, errBuf.String())
	}
	s := out.String()
	if !strings.Contains(s, "pending (2)") {
		t.Fatalf("want 2 pending: %q", s)
	}
	if strings.Contains(s, "_scratch.sql") || strings.Contains(s, "README.md") {
		t.Fatalf("fallback listed a skipped file: %q", s)
	}
}

// TestDBFileFallbackEndToEnd is the emit-then-exec leg (reusing the W2 harness):
// emit a --db=turso host, `db create` a migration into it, then `db status` with
// no database — the delegated runner fails to connect (empty TURSO_DATABASE_URL)
// and its own file-only fallback lists the created migration as pending. Offline
// against the warm GOMODCACHE; a cold cache FAILS LOUD.
func TestDBFileFallbackEndToEnd(t *testing.T) {
	root := repoRoot(t)
	target := t.TempDir()

	params, err := buildInitParams("example.com/scaffoldtest/dbverbs", "turso")
	if err != nil {
		t.Fatal(err)
	}
	if err := emitInit(target, params); err != nil {
		t.Fatalf("emitInit: %v", err)
	}

	replaceModule(t, target, baseModule+"/sdk", filepath.Join(root, "sdk"))
	replaceModule(t, target, params.ConnectorPath, filepath.Join(root, filepath.FromSlash(params.ConnectorRel)))

	// Tidy against the warm cache (GOPROXY=off) so the runner compiles offline.
	// -mod=mod lets tidy populate go.sum.
	tidyEnv := append(hermeticEnv(), "GOFLAGS=-mod=mod")
	runGo(t, target, tidyEnv, "mod", "tidy")

	// db create → 0001_add_notes.sql in workshop/migrations/primary.
	created, err := createMigration(target, defaultLedger, "add_notes")
	if err != nil {
		t.Fatalf("createMigration: %v", err)
	}
	if base := filepath.Base(created); base != "0001_add_notes.sql" {
		t.Fatalf("created %q, want 0001_add_notes.sql", base)
	}

	// db status → the runner runs offline (empty URL → open() fails immediately),
	// prints its file-only fallback listing the created migration pending, exit 0.
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("TURSO_DATABASE_URL", "")
	t.Setenv("TURSO_AUTH_TOKEN", "")

	var out bytes.Buffer
	if code := statusRunner(target, defaultLedger, &out, &out); code != 0 {
		t.Fatalf("statusRunner exit = %d (want 0, the runner's file-only fallback):\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "0001_add_notes.sql") {
		t.Fatalf("status output missing the created migration as pending:\n%s", out.String())
	}
}

func seedLedger(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("-- x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
