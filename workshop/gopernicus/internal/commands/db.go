package commands

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// defaultLedger is the default ledger subdirectory under workshop/migrations
	// (the --db flag). It matches the charter and the cms example.
	defaultLedger = "primary"

	// runnerPkg is the host-owned migration runner package. `db migrate` and
	// `db status` DELEGATE to it (go run) — the CLI stays stdlib-only, so no
	// datastore driver ever enters this module (review-gate fold item 1).
	runnerPkg = "./workshop/migrations"
)

// runDB is the second level of the command tree; it routes to a `db` subcommand.
// `create` is pure filesystem; `migrate`/`status` delegate to the host-owned
// runner (the CLI never links a datastore driver).
func runDB(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "gopernicus db: subcommand required\n\n")
		dbUsage(os.Stderr)
		return 1
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "migrate":
		return runDBMigrate(rest)
	case "status":
		return runDBStatus(rest)
	case "create":
		return runDBCreate(rest)
	case "-h", "--help", "help":
		dbUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gopernicus db: unknown subcommand %q\n\n", sub)
		dbUsage(os.Stderr)
		return 1
	}
}

// runDBCreate scaffolds NNNN_<slug>.sql into workshop/migrations/<db>/, honoring
// never-renumber (next = max+1 across the ledger). Pure filesystem.
func runDBCreate(args []string) int {
	// The slug is a leading positional (gopernicus db create <slug> [--db …]).
	// stdlib flag stops at the first non-flag arg, so pull it out before parsing;
	// a trailing positional is still accepted as a fallback.
	var slug string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		slug, args = args[0], args[1:]
	}

	fs := flag.NewFlagSet("db create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	db := fs.String("db", defaultLedger, "ledger subdirectory under workshop/migrations")
	fs.Usage = func() { dbCreateUsage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}

	if slug == "" {
		slug = fs.Arg(0)
	}
	if slug == "" {
		fmt.Fprintln(os.Stderr, "gopernicus db create: migration slug is required (the positional argument)")
		dbCreateUsage(os.Stderr)
		return 1
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus db create: %v\n", err)
		return 1
	}

	path, err := createMigration(root, *db, slug)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus db create: %v\n", err)
		return 1
	}

	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = path
	}
	fmt.Printf("created %s\n", filepath.ToSlash(rel))
	return 0
}

// createMigration writes an empty-stub NNNN_<slug>.sql into the host ledger,
// creating the ledger directory when absent. The number is max+1 across the
// ledger's already-present files; it refuses to clobber an existing file.
func createMigration(hostRoot, ledger, slug string) (string, error) {
	name := sanitizeMigrationName(slug)
	if name == "" {
		return "", fmt.Errorf("invalid migration slug %q: name must contain at least one [a-z0-9_] character", slug)
	}

	dir := ledgerDir(hostRoot, ledger)
	next, err := nextMigrationNumber(dir)
	if err != nil {
		return "", err
	}

	filename := fmt.Sprintf("%04d_%s.sql", next, name)
	path := filepath.Join(dir, filename)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("refusing to overwrite existing migration: %s", filename)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	content := fmt.Sprintf("-- migration: %s\n", filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// nextMigrationNumber scans the ledger for *.sql files (skipping "_"-prefixed
// scratch files), parses the leading digits before the first underscore, and
// returns max+1. An absent ledger directory starts at 1.
func nextMigrationNumber(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, err
	}
	max := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, "_") || !strings.HasSuffix(name, ".sql") {
			continue
		}
		if n := leadingMigrationNumber(name); n > max {
			max = n
		}
	}
	return max + 1, nil
}

// leadingMigrationNumber parses the digits before the first underscore, e.g.
// "0012_add_notes.sql" -> 12. A name with no leading digits (or no underscore)
// contributes 0 — it never advances the counter.
func leadingMigrationNumber(name string) int {
	i := strings.IndexByte(name, '_')
	if i <= 0 {
		return 0
	}
	n, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0
	}
	return n
}

// sanitizeMigrationName lower-cases the slug and keeps [a-z0-9_], mapping spaces
// and dashes to underscores and dropping anything else — the original CLI's
// name-sanitize shape.
func sanitizeMigrationName(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			b.WriteRune(c)
		case c == ' ' || c == '-':
			b.WriteByte('_')
		}
	}
	return b.String()
}

// runDBMigrate delegates to the host-owned runner: it verifies the runner exists
// (a loud, actionable error otherwise) then execs `go run ./workshop/migrations`,
// streaming its output and propagating its exit code.
func runDBMigrate(args []string) int {
	fs := flag.NewFlagSet("db migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { dbMigrateUsage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus db migrate: %v\n", err)
		return 1
	}
	return migrateRunner(root, os.Stdout, os.Stderr)
}

func migrateRunner(hostRoot string, stdout, stderr io.Writer) int {
	if err := requireRunner(hostRoot); err != nil {
		fmt.Fprintf(stderr, "gopernicus db migrate: %v\n", err)
		return 1
	}
	cmd := exec.Command("go", "run", runnerPkg)
	cmd.Dir = hostRoot
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return exitCode(err)
	}
	return 0
}

// runDBStatus delegates to the runner's -status mode when present, streaming its
// applied-vs-pending report. When the runner is absent, or it exits non-zero
// (a connection-ish failure), it prints the FILE-ONLY fallback — the ledger
// listing as all-pending, the only status a stdlib CLI can self-produce.
func runDBStatus(args []string) int {
	fs := flag.NewFlagSet("db status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	db := fs.String("db", defaultLedger, "ledger subdirectory for the file-only fallback")
	fs.Usage = func() { dbStatusUsage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus db status: %v\n", err)
		return 1
	}
	return statusRunner(root, *db, os.Stdout, os.Stderr)
}

func statusRunner(hostRoot, ledger string, stdout, stderr io.Writer) int {
	if err := requireRunner(hostRoot); err != nil {
		fmt.Fprintf(stdout, "%v\n", err)
		fmt.Fprintln(stdout, "file view — every migration listed pending:")
		return printFileOnlyStatus(hostRoot, ledger, stdout, stderr)
	}

	cmd := exec.Command("go", "run", runnerPkg, "-status")
	cmd.Dir = hostRoot
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(stdout, "DB unreachable — file view (every migration listed pending):")
		printFileOnlyStatus(hostRoot, ledger, stdout, stderr)
		return exitCode(err)
	}
	return 0
}

// printFileOnlyStatus lists the ledger's migrations as all-pending. It is the
// fallback for `db status` when no database is reachable.
func printFileOnlyStatus(hostRoot, ledger string, stdout, stderr io.Writer) int {
	files, err := ledgerFiles(ledgerDir(hostRoot, ledger))
	if err != nil {
		fmt.Fprintf(stderr, "gopernicus db status: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "pending (%d):\n", len(files))
	for _, f := range files {
		fmt.Fprintf(stdout, "  %s\n", f)
	}
	return 0
}

// ledgerFiles returns the ledger's applicable *.sql migrations in filename order,
// skipping directories and "_"-prefixed scratch files (os.ReadDir sorts by name).
// An absent ledger directory yields an empty list.
func ledgerFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, "_") || !strings.HasSuffix(name, ".sql") {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// requireRunner verifies the host-owned runner exists before the CLI delegates
// to it. The error names `gopernicus init --db=...`, the command that emits one.
func requireRunner(hostRoot string) error {
	main := filepath.Join(hostRoot, "workshop", "migrations", "main.go")
	if _, err := os.Stat(main); err != nil {
		return fmt.Errorf("no migration runner at %s — scaffold one with `gopernicus init --db=turso|pgx` (the runner is host-owned; the CLI delegates to it)", filepath.Join("workshop", "migrations"))
	}
	return nil
}

func ledgerDir(hostRoot, ledger string) string {
	return filepath.Join(hostRoot, "workshop", "migrations", ledger)
}

// exitCode extracts a child process's exit code, defaulting to 1 for a
// non-ExitError failure (e.g. the `go` binary is missing).
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code > 0 {
			return code
		}
	}
	return 1
}

func dbUsage(w io.Writer) {
	fmt.Fprint(w, `gopernicus db — host migration utilities

Usage:
  gopernicus db <subcommand>

Subcommands:
  create    Scaffold a new NNNN_<slug>.sql migration into the host ledger
  migrate   Apply pending migrations via the host-owned runner
  status    Show applied vs pending migrations (file-only when no DB is reachable)

`)
}

func dbCreateUsage(w io.Writer) {
	fmt.Fprint(w, `gopernicus db create — scaffold a migration file

Usage:
  gopernicus db create <slug> [--db <ledger>]

Flags:
  --db   ledger subdirectory under workshop/migrations (default: primary)

Writes workshop/migrations/<db>/NNNN_<slug>.sql, where NNNN is max+1 across the
ledger (never renumber). The slug is lower-cased to [a-z0-9_]. Run from the host
root.
`)
}

func dbMigrateUsage(w io.Writer) {
	fmt.Fprint(w, `gopernicus db migrate — apply pending migrations

Usage:
  gopernicus db migrate

Delegates to the host-owned runner (go run ./workshop/migrations), which applies
the ledger pre-boot. Set the datastore env first (see .env.example). Run from the
host root; scaffold a runner with `+"`gopernicus init --db=turso|pgx`"+`.
`)
}

func dbStatusUsage(w io.Writer) {
	fmt.Fprint(w, `gopernicus db status — show migration status

Usage:
  gopernicus db status [--db <ledger>]

Flags:
  --db   ledger subdirectory for the file-only fallback (default: primary)

Delegates to the host-owned runner (go run ./workshop/migrations -status). When
no runner exists or no database is reachable, prints the ledger listing as
all-pending — the only status a stdlib CLI can self-produce. Run from the host
root.
`)
}
