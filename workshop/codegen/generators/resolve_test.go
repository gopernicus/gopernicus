package generators

import (
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/workshop/codegen/schema"
)

// fixtureDefaultsSchema builds a tenant_secrets-shaped table: the JSON
// payload + CHECK-constrained kind columns that motivated @fixture-default,
// plus PK/FK/time/numeric columns for the rejection cases.
func fixtureDefaultsSchema() *schema.ReflectedSchema {
	cols := []schema.ColumnInfo{
		{Name: "secret_id", DBType: "text", GoType: "string", IsPrimaryKey: true},
		{Name: "tenant_id", DBType: "text", GoType: "string", IsForeignKey: true},
		{Name: "payload", DBType: "jsonb", GoType: "json.RawMessage", GoImport: "encoding/json"},
		{Name: "kind", DBType: "text", GoType: "string"},
		{Name: "note", DBType: "text", GoType: "*string", IsNullable: true},
		{Name: "enabled", DBType: "boolean", GoType: "bool"},
		{Name: "version", DBType: "integer", GoType: "int"},
		{Name: "weight", DBType: "double precision", GoType: "float64"},
		{Name: "rotated_at", DBType: "timestamptz", GoType: "time.Time", GoImport: "time"},
	}
	return &schema.ReflectedSchema{
		SchemaName: "public",
		Tables: map[string]*schema.TableInfo{
			"tenant_secrets": {
				TableName: "tenant_secrets",
				Schema:    "public",
				PrimaryKey: &schema.PrimaryKeyInfo{
					Column: "secret_id",
					DBType: "text",
					GoType: "string",
				},
				Columns: cols,
			},
		},
	}
}

func resolveWithFixtureDefaults(t *testing.T, entries ...string) (*ResolvedFile, error) {
	t.Helper()
	input := ""
	for _, e := range entries {
		input += "-- @fixture-default: " + e + "\n"
	}
	input += `
-- @func: Get
SELECT * FROM tenant_secrets WHERE secret_id = @secret_id;
`
	qf, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	qf.Table = "tenant_secrets"
	return Resolve(qf, fixtureDefaultsSchema(), "tenancy")
}

func TestResolveFixtureDefaults_Valid(t *testing.T) {
	resolved, err := resolveWithFixtureDefaults(t,
		`payload {"backend":"db","ciphertext":"dGVzdA=="}`,
		"kind entity",
		"enabled true",
		"version 3",
		"weight 0.5",
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := map[string]string{
		"payload": `{"backend":"db","ciphertext":"dGVzdA=="}`,
		"kind":    "entity",
		"enabled": "true",
		"version": "3",
		"weight":  "0.5",
	}
	for col, val := range want {
		if resolved.FixtureDefaults[col] != val {
			t.Errorf("FixtureDefaults[%s] = %q, want %q", col, resolved.FixtureDefaults[col], val)
		}
	}
}

func TestResolveFixtureDefaults_Errors(t *testing.T) {
	cases := []struct {
		name    string
		entry   string
		wantErr string
	}{
		{"unknown column", "missing_col x", "not found"},
		{"missing value", "kind", "want `<column> <value>`"},
		{"primary key", "secret_id custom", "primary key"},
		{"foreign key", "tenant_id custom", "foreign key"},
		{"bad json", "payload {not-json", "not valid JSON"},
		{"json backtick", "payload {\"a\":\"`\"}", "backtick"},
		{"bad bool", "enabled yes", "must be `true` or `false`"},
		{"bad int", "version three", "invalid integer"},
		{"bad float", "weight heavy", "invalid float"},
		{"time column", "rotated_at 2026-01-01", "time columns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveWithFixtureDefaults(t, tc.entry)
			if err == nil {
				t.Fatalf("expected error for %q", tc.entry)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestResolveFixtureDefaults_DuplicateColumn(t *testing.T) {
	_, err := resolveWithFixtureDefaults(t, "kind entity", "kind other")
	if err == nil {
		t.Fatal("expected error for duplicate column")
	}
	if !strings.Contains(err.Error(), "more than once") {
		t.Errorf("error %q does not mention duplication", err)
	}
}
