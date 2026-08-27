package pgx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// createTableRE lifts the pocket's table names out of the embedded migration
// DDL, so a table added by a future migration is covered here without a
// hand-maintained list.
var createTableRE = regexp.MustCompile(`(?i)CREATE TABLE (?:IF NOT EXISTS )?(\w+)`)

// TestNoBareTableReferences is the regression guard for the WithSchema
// chokepoint: every statement must render its table through a store's table
// method, never as a bare name inside a literal, or a schema-scoped store would
// silently read and write the host's own tables.
//
// It inspects ONLY string literals of the package's non-test files — comments
// and identifiers (the doc comments, the table-name consts, probeTables) are
// excluded, and they would otherwise false-positive. It guards the current
// statement forms; it cannot detect SQL assembled some other way ("FROM " +
// "entries", comma joins, TRUNCATE). The non-default-schema conformance leg is
// the behavioural proof for the paths it executes.
func TestNoBareTableReferences(t *testing.T) {
	tables := migrationTables(t)
	if len(tables) == 0 {
		t.Fatal("no CREATE TABLE statements found in the embedded migrations")
	}

	res := make(map[string]*regexp.Regexp, len(tables))
	for _, tbl := range tables {
		res[tbl] = regexp.MustCompile(`(?i)\b(FROM|INTO|UPDATE|JOIN|TABLE|ON)\s+` + regexp.QuoteMeta(tbl) + `\b`)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var checked int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for tbl, re := range res {
				if m := re.FindString(text); m != "" {
					t.Errorf("%s: bare table reference %q in a string literal — render it through s.table(%q)",
						fset.Position(lit.Pos()), strings.Join(strings.Fields(m), " "), tbl)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no non-test .go files inspected")
	}
}

// TestProbeTablesMatchMigrations pins StatusCheck's inventory to the migrations:
// a table added by a future migration must be probed too, or a schema mismatch
// on it stays silent.
func TestProbeTablesMatchMigrations(t *testing.T) {
	want := migrationTables(t)
	got := append([]string(nil), probeTables...)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("probeTables = %v, migrations declare %v", got, want)
	}
}

// TestWithSchema pins the rendering contract: no option renders bare names
// (byte-identical to the SQL this store has always emitted), and WithSchema
// qualifies every store — including the ones Repositories builds.
func TestWithSchema(t *testing.T) {
	schema, err := pgxdb.NewSchema("cms_x")
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}

	t.Run("zero renders bare", func(t *testing.T) {
		if got := NewEntryStore(nil).table(entriesTable); got != "entries" {
			t.Errorf("EntryStore.table = %q, want %q", got, "entries")
		}
		if got := NewTermStore(nil).table(termsTable); got != "terms" {
			t.Errorf("TermStore.table = %q, want %q", got, "terms")
		}
		if got := NewMenuStore(nil).table(menuItemsTable); got != "menu_items" {
			t.Errorf("MenuStore.table = %q, want %q", got, "menu_items")
		}
		if got := NewAssetStore(nil).table(assetsTable); got != "assets" {
			t.Errorf("AssetStore.table = %q, want %q", got, "assets")
		}
		if got := NewInquiryStore(nil).table(inquiriesTable); got != "inquiries" {
			t.Errorf("InquiryStore.table = %q, want %q", got, "inquiries")
		}
	})

	t.Run("option qualifies", func(t *testing.T) {
		if got := NewEntryStore(nil, WithSchema(schema)).table(entriesTable); got != `"cms_x".entries` {
			t.Errorf("EntryStore.table = %q, want %q", got, `"cms_x".entries`)
		}
		if got := NewTermStore(nil, WithSchema(schema)).table(termsTable); got != `"cms_x".terms` {
			t.Errorf("TermStore.table = %q, want %q", got, `"cms_x".terms`)
		}
		if got := NewMenuStore(nil, WithSchema(schema)).table(menusTable); got != `"cms_x".menus` {
			t.Errorf("MenuStore.table = %q, want %q", got, `"cms_x".menus`)
		}
		if got := NewAssetStore(nil, WithSchema(schema)).table(assetsTable); got != `"cms_x".assets` {
			t.Errorf("AssetStore.table = %q, want %q", got, `"cms_x".assets`)
		}
		if got := NewInquiryStore(nil, WithSchema(schema)).table(inquiriesTable); got != `"cms_x".inquiries` {
			t.Errorf("InquiryStore.table = %q, want %q", got, `"cms_x".inquiries`)
		}
	})

	t.Run("Repositories threads the option", func(t *testing.T) {
		repos := Repositories(nil, WithSchema(schema))
		entries, ok := repos.Entries.(*EntryStore)
		if !ok {
			t.Fatalf("Entries is %T, want *EntryStore", repos.Entries)
		}
		if got := entries.table(entryFieldsTable); got != `"cms_x".entry_fields` {
			t.Errorf("Repositories entry store table = %q, want %q", got, `"cms_x".entry_fields`)
		}
		if got := Repositories(nil).Entries.(*EntryStore).table(entriesTable); got != "entries" {
			t.Errorf("default Repositories entry store table = %q, want %q", got, "entries")
		}
	})
}

// migrationTables returns the sorted table names the embedded migrations create.
func migrationTables(t *testing.T) []string {
	t.Helper()
	var names []string
	err := fs.WalkDir(MigrationsFS, MigrationsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return err
		}
		data, err := fs.ReadFile(MigrationsFS, path)
		if err != nil {
			return err
		}
		for _, m := range createTableRE.FindAllStringSubmatch(string(data), -1) {
			names = append(names, m[1])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk migrations: %v", err)
	}
	sort.Strings(names)
	return names
}
