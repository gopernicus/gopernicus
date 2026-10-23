package pgx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

var createTableRE = regexp.MustCompile(`(?i)CREATE TABLE (?:IF NOT EXISTS )?(\w+)`)

// TestExportMigrations verifies the scaffold step copies every canonical
// migration verbatim.
func TestExportMigrations(t *testing.T) {
	dst := t.TempDir()
	if err := ExportMigrations(dst); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("embedded migrations: %v %v", entries, err)
	}
	for _, e := range entries {
		want, _ := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+e.Name())
		got, err := os.ReadFile(filepath.Join(dst, e.Name()))
		if err != nil || string(got) != string(want) {
			t.Errorf("exported %s differs: %v", e.Name(), err)
		}
	}
}

// TestNoBareTableReferences guards WithSchema: every table reference in the
// runtime SQL renders through table(), never as a bare literal. It inspects
// string literals in non-test files only.
func TestNoBareTableReferences(t *testing.T) {
	tables := migrationTables(t)
	if len(tables) != len(probeTables) {
		t.Fatalf("migration creates %v but StatusCheck probes %v", tables, probeTables)
	}
	patterns := make(map[string]*regexp.Regexp, len(tables))
	for _, table := range tables {
		patterns[table] = regexp.MustCompile(`(?i)\b(FROM|INTO|UPDATE|JOIN|TABLE|ON)\s+` + regexp.QuoteMeta(table) + `\b`)
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil || len(pkgs) == 0 {
		t.Fatalf("parse: %v", err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				for table, re := range patterns {
					if re.MatchString(value) {
						t.Errorf("%s:%d: bare reference to %s", name, fset.Position(lit.Pos()).Line, table)
					}
				}
				return true
			})
		}
	}
}

// TestAuditTriggerBodyMatchesMigration pins the canonical body the posture
// probe verifies to the dollar-quoted body 0003 installs, byte for byte, and
// AuditTables to the table and function 0003 names.
func TestAuditTriggerBodyMatchesMigration(t *testing.T) {
	src, err := fs.ReadFile(MigrationsFS, MigrationsDir+"/0003_secrets_audit_immutable.sql")
	if err != nil {
		t.Fatal(err)
	}
	tag := "$" + auditTriggerFunction + "$"
	parts := strings.Split(string(src), tag)
	if len(parts) != 3 {
		t.Fatalf("0003 holds %d %s quotes, want 2", len(parts)-1, tag)
	}
	if !strings.Contains(parts[0], "CREATE OR REPLACE FUNCTION "+auditTriggerFunction+"() RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS ") {
		t.Fatalf("0003 does not create %s as the probe expects:\n%s", auditTriggerFunction, parts[0])
	}
	if parts[1] != auditTriggerBody {
		t.Fatalf("0003 body differs from auditTriggerBody:\n%q\n%q", parts[1], auditTriggerBody)
	}
	want := pgxdb.AppendOnlyTable{Name: "secrets_audit", Canonical: []pgxdb.TriggerBody{{Function: auditTriggerFunction, Body: auditTriggerBody}}}
	if got := AuditTables(); len(got) != 1 || got[0].Name != want.Name || got[0].AllowDelete || len(got[0].Canonical) != 1 || got[0].Canonical[0] != want.Canonical[0] {
		t.Fatalf("AuditTables() = %+v, want %+v", got, want)
	}
	s, err := pgxdb.NewSchema("vault")
	if err != nil {
		t.Fatal(err)
	}
	if got := AuditTables(WithSchema(s)); got[0].Name != s.Table("secrets_audit") {
		t.Fatalf("AuditTables(WithSchema) names %q, want %q", got[0].Name, s.Table("secrets_audit"))
	}
}

func migrationTables(t *testing.T) []string {
	t.Helper()
	var tables []string
	entries, err := fs.ReadDir(MigrationsFS, MigrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		src, _ := fs.ReadFile(MigrationsFS, MigrationsDir+"/"+e.Name())
		for _, m := range createTableRE.FindAllStringSubmatch(string(src), -1) {
			tables = append(tables, m[1])
		}
	}
	return tables
}
