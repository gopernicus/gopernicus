// Hermetic guards for the WithSchema seam: every table reference in this package
// renders through a store's table method (never a bare literal), and the option
// itself qualifies what it promises to qualify. They run on plain `go test ./...`
// with no datastore env — the schema conformance leg is the behavioral proof for
// the paths it executes, this is the regression guard for the statement forms.
package pgx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// createTableRE extracts the table names the canonical migrations define, so a
// future migration is covered without a hand-maintained list (the
// enumerate-by-directory precedent). CTE names like `reachable` and `capped` are
// not tables and therefore never enter the guarded set.
var createTableRE = regexp.MustCompile(`CREATE TABLE (?:IF NOT EXISTS )?(\w+)`)

// bareRefTemplate is the SQL context a bare table name must never appear in.
const bareRefTemplate = `(?i)\b(FROM|INTO|UPDATE|JOIN|TABLE|ON)\s+%s\b`

// TestNoBareTableReferences parses every non-test .go file in the package and
// fails on any STRING LITERAL that names a migration-declared table directly
// after a SQL keyword. Only *ast.BasicLit values are inspected: comments and
// identifiers (the doc comments, the storeTables inventory) would otherwise
// false-positive. It cannot detect SQL assembled across literals or forms outside
// the keyword set — it guards the current statement forms, nothing wider.
func TestNoBareTableReferences(t *testing.T) {
	tables := migrationTables(t)
	if len(tables) == 0 {
		t.Fatal("no CREATE TABLE statements found in the embedded migrations")
	}

	patterns := make(map[string]*regexp.Regexp, len(tables))
	for _, tbl := range tables {
		patterns[tbl] = regexp.MustCompile(strings.Replace(bareRefTemplate, "%s", regexp.QuoteMeta(tbl), 1))
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
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
				for _, tbl := range tables {
					if m := patterns[tbl].FindString(val); m != "" {
						t.Errorf("%s:%d: bare table reference %q — render it through the store's table method",
							name, fset.Position(lit.Pos()).Line, strings.TrimSpace(m))
					}
				}
				return true
			})
		}
	}
}

// TestWithSchema pins both directions of the option: the zero Schema renders the
// SQL this store has always emitted, and a set Schema qualifies every table the
// three stores and the two shared CTE fragments name.
func TestWithSchema(t *testing.T) {
	var zero config
	if !zero.schema.IsZero() {
		t.Fatal("the default config must carry the zero Schema")
	}

	schema, err := pgxdb.NewSchema("auth")
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	var set config
	WithSchema(schema)(&set)
	if set.schema != schema {
		t.Fatalf("WithSchema config schema = %q, want %q", set.schema, schema)
	}

	for _, tc := range []struct {
		name  string
		table func(cfg config) string
	}{
		{"relationshipStore", func(cfg config) string { return newRelationshipStore(nil, cfg).table("iam_relationships") }},
		{"roleStore", func(cfg config) string { return newRoleStore(nil, cfg).table("iam_roles") }},
		{"mutationStore", func(cfg config) string { return newMutationStore(nil, cfg).table("iam_scopes") }},
	} {
		bare := tc.table(zero)
		if strings.ContainsAny(bare, `".`) {
			t.Errorf("%s: the zero Schema must render a bare name, got %q", tc.name, bare)
		}
		qualified := tc.table(set)
		if want := `"auth".` + bare; qualified != want {
			t.Errorf("%s: WithSchema rendered %q, want %q", tc.name, qualified, want)
		}
	}

	for _, tc := range []struct {
		name string
		cte  func(pgxdb.Schema) string
	}{
		{"reachableCTE", reachableCTE},
		{"boundedReachableCTE", boundedReachableCTE},
	} {
		if got := tc.cte(pgxdb.Schema{}); !strings.Contains(got, "FROM iam_relationships r") {
			t.Errorf("%s: the zero Schema must render the bare table, got %q", tc.name, got)
		}
		if got := tc.cte(schema); !strings.Contains(got, `FROM "auth".iam_relationships r`) {
			t.Errorf("%s: WithSchema must qualify the table, got %q", tc.name, got)
		}
	}
}

// migrationTables returns the sorted table names the embedded migrations create.
func migrationTables(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range createTableRE.FindAllStringSubmatch(migrationsSQL(t), -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for tbl := range seen {
		out = append(out, tbl)
	}
	sort.Strings(out)
	return out
}
