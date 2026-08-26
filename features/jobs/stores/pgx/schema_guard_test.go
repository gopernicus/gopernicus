package pgx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// createTableRE lifts the table names this package owns out of the embedded
// canonical migrations, so a table added by a future migration is guarded
// without a hand-maintained list.
var createTableRE = regexp.MustCompile(`(?i)CREATE TABLE (?:IF NOT EXISTS )?(\w+)`)

// TestNoBareTableReferences is the regression guard for WithSchema: every table
// reference in this package's runtime SQL must render through the store's
// table() chokepoint, never as a bare literal, or a host that configures a
// schema would silently read and write the default namespace instead.
//
// It parses the package's non-test .go files and inspects ONLY string literals —
// comments and identifiers are excluded, since the doc comments and the
// probeTables inventory legitimately name the tables. It guards the CURRENT
// statement forms; inspecting individual literals cannot detect a table name
// assembled from two literals, a comma join, or a SQL keyword outside the set
// below. The non-default-schema conformance leg is the behavioral proof for the
// paths it executes.
func TestNoBareTableReferences(t *testing.T) {
	tables := migrationTables(t)
	if len(tables) == 0 {
		t.Fatal("no CREATE TABLE statements found in the embedded migrations")
	}

	patterns := make(map[string]*regexp.Regexp, len(tables))
	for _, table := range tables {
		patterns[table] = regexp.MustCompile(`(?i)\b(FROM|INTO|UPDATE|JOIN|TABLE|ON)\s+` + regexp.QuoteMeta(table) + `\b`)
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no non-test Go files parsed")
	}

	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				for table, re := range patterns {
					if m := re.FindString(text); m != "" {
						t.Errorf("%s:%d: bare table reference %q — qualify it through the store's table(%q) method",
							name, fset.Position(lit.Pos()).Line, m, table)
					}
				}
				return true
			})
		}
	}
}

// TestWithSchema pins the two rendering modes: no option (the default) renders
// bare names byte-for-byte as this store always has, and WithSchema qualifies
// every store's table() with the quoted schema.
func TestWithSchema(t *testing.T) {
	db := (*pgxdb.DB)(nil)

	t.Run("zero schema renders bare", func(t *testing.T) {
		if got := NewQueueStore(db).table("job_queue"); got != "job_queue" {
			t.Errorf("queue table = %q, want %q", got, "job_queue")
		}
		if got := NewScheduleStore(db).table("job_schedules"); got != "job_schedules" {
			t.Errorf("schedules table = %q, want %q", got, "job_schedules")
		}
		if got := NewFencedQueueStore(db).table("fenced_job_queue"); got != "fenced_job_queue" {
			t.Errorf("fenced table = %q, want %q", got, "fenced_job_queue")
		}
	})

	t.Run("schema qualifies", func(t *testing.T) {
		s, err := pgxdb.NewSchema("jobs")
		if err != nil {
			t.Fatalf("NewSchema: %v", err)
		}
		if got, want := NewQueueStore(db, WithSchema(s)).table("job_queue"), `"jobs".job_queue`; got != want {
			t.Errorf("queue table = %q, want %q", got, want)
		}
		if got, want := NewScheduleStore(db, WithSchema(s)).table("job_schedules"), `"jobs".job_schedules`; got != want {
			t.Errorf("schedules table = %q, want %q", got, want)
		}
		if got, want := NewFencedQueueStore(db, WithSchema(s)).table("fenced_job_queue"), `"jobs".fenced_job_queue`; got != want {
			t.Errorf("fenced table = %q, want %q", got, want)
		}
	})
}

// TestWithLease pins WithLease's rule after the move from func(*Queue) to
// func(*config): a positive duration is taken, a non-positive one is ignored and
// DefaultLease is kept.
func TestWithLease(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want time.Duration
	}{
		{name: "no option keeps the default", want: DefaultLease},
		{name: "positive is taken", opts: []Option{WithLease(90 * time.Second)}, want: 90 * time.Second},
		{name: "zero is ignored", opts: []Option{WithLease(0)}, want: DefaultLease},
		{name: "negative is ignored", opts: []Option{WithLease(-time.Minute)}, want: DefaultLease},
		{name: "schema does not disturb the lease", opts: []Option{WithSchema(pgxdb.Schema{})}, want: DefaultLease},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewQueueStore(nil, tc.opts...).lease; got != tc.want {
				t.Errorf("lease = %v, want %v", got, tc.want)
			}
		})
	}
}

// migrationTables returns every table name the embedded canonical migrations
// create — the guard's inventory, enumerated from the files rather than by hand.
func migrationTables(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	seen := map[string]bool{}
	var tables []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range createTableRE.FindAllStringSubmatch(string(b), -1) {
			if name := m[1]; !seen[name] {
				seen[name] = true
				tables = append(tables, name)
			}
		}
	}
	return tables
}
