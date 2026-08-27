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

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// createTableRe extracts the table names this package owns from its embedded
// migration files, so a future migration is covered without a hand-maintained
// list (the "enumerate by directory, not by hand" precedent).
var createTableRe = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)`)

// TestNoBareTableReferences is the regression guard for the WithSchema seam:
// every table reference in this package's runtime SQL must render through
// Store.table, never as a bare literal, or a schema-scoped store would silently
// read and write the host's own tables.
//
// It parses the package's non-test .go files and inspects ONLY string basic
// literals — comments and identifiers are excluded, because the package doc and
// the table-name const would otherwise false-positive. It guards the CURRENT
// statement forms: inspecting individual literals cannot detect constructions
// such as "FROM " + "event_outbox", comma joins, or a SQL form outside the
// keyword set. The non-default-schema conformance leg is the behavioral proof
// for the paths it executes.
func TestNoBareTableReferences(t *testing.T) {
	tables := migrationTables(t)
	if len(tables) == 0 {
		t.Fatal("no CREATE TABLE statements found in MigrationsFS — the guard would pass vacuously")
	}

	bare := make(map[string]*regexp.Regexp, len(tables))
	for _, table := range tables {
		bare[table] = regexp.MustCompile(`(?i)\b(FROM|INTO|UPDATE|JOIN|TABLE|ON)\s+` + regexp.QuoteMeta(table) + `\b`)
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
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				for table, re := range bare {
					if m := re.FindString(val); m != "" {
						t.Errorf("%s:%d: bare table reference %q — render it through s.table(%q)",
							name, fset.Position(lit.Pos()).Line, m, table)
					}
				}
				return true
			})
		}
	}
}

// TestWithSchema pins the option's two states through the store's own chokepoint:
// no option renders bare names (today's SQL byte-for-byte), a schema renders
// "<schema>".table.
func TestWithSchema(t *testing.T) {
	zero := &Store{}
	if got := zero.table(outboxTable); got != outboxTable {
		t.Errorf("zero schema table = %q, want %q", got, outboxTable)
	}

	s, err := pgxdb.NewSchema("events_x")
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	var cfg config
	WithSchema(s)(&cfg)
	scoped := &Store{schema: cfg.schema}
	if want := `"events_x".` + outboxTable; scoped.table(outboxTable) != want {
		t.Errorf("scoped table = %q, want %q", scoped.table(outboxTable), want)
	}
}

// migrationTables returns the table names created by the embedded migrations.
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
		body, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range createTableRe.FindAllStringSubmatch(string(body), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				tables = append(tables, m[1])
			}
		}
	}
	return tables
}
