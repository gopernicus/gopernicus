package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/workshop/codegen/manifest"
	"github.com/gopernicus/gopernicus/workshop/codegen/schema"
)

func TestTSTypeForColumn(t *testing.T) {
	cases := []struct {
		goType string
		isEnum bool
		enums  []string
		want   string
	}{
		{"string", false, nil, "string"},
		{"*string", false, nil, "string | null"},
		{"bool", false, nil, "boolean"},
		{"int64", false, nil, "number"},
		{"*float64", false, nil, "number | null"},
		{"time.Time", false, nil, "string"},
		{"*time.Time", false, nil, "string | null"},
		{"json.RawMessage", false, nil, "unknown"},
		{"[]byte", false, nil, "string"},
		{"string", true, []string{"db", "gcp_kms"}, `"db" | "gcp_kms"`},
		{"*string", true, []string{"a"}, `"a" | null`},
	}
	for _, tc := range cases {
		if got := tsTypeForColumn(tc.goType, tc.isEnum, tc.enums); got != tc.want {
			t.Errorf("tsTypeForColumn(%q) = %q, want %q", tc.goType, got, tc.want)
		}
	}
}

// Regression: the first implementation re-scanned its own `${p(...)}`
// insertion and looped forever.
func TestTSPathExpr(t *testing.T) {
	cases := []struct {
		path       string
		wantExpr   string
		wantParams []string
	}{
		{"/tenants", "`/tenants`", nil},
		{"/tenants/{tenant_id}", "`/tenants/${p(tenantID)}`", []string{"tenantID"}},
		{"/users/{parent_user_id}/sessions/{session_id}",
			"`/users/${p(parentUserID)}/sessions/${p(sessionID)}`",
			[]string{"parentUserID", "sessionID"}},
	}
	for _, tc := range cases {
		expr, params := tsPathExpr(tc.path)
		if expr != tc.wantExpr {
			t.Errorf("expr(%q) = %s, want %s", tc.path, expr, tc.wantExpr)
		}
		if strings.Join(params, ",") != strings.Join(tc.wantParams, ",") {
			t.Errorf("params(%q) = %v, want %v", tc.path, params, tc.wantParams)
		}
	}
}

func TestEmitTypeScriptClient(t *testing.T) {
	resolved := &ResolvedFile{
		EntityName:   "Tenant",
		EntityPlural: "Tenants",
		TableName:    "tenants",
		AllColumns: []schema.ColumnInfo{
			{Name: "tenant_id", GoType: "string", IsPrimaryKey: true},
			{Name: "name", GoType: "string"},
			{Name: "backend", GoType: "string", IsEnum: true, EnumValues: []string{"db", "kms"}},
			{Name: "description", GoType: "*string", IsNullable: true},
			{Name: "created_at", GoType: "time.Time", GoImport: "time"},
		},
	}
	data := &BridgeTemplateData{
		EntityName: "Tenant",
		CreateQueries: []BridgeCreateQuery{{FuncName: "Create", Fields: []BridgeField{
			{DBName: "name", GoName: "Name", GoType: "string", IsRequired: true, IsString: true},
			{DBName: "description", GoName: "Description", GoType: "*string"},
		}}},
		ListQueries: []BridgeListQuery{{FuncName: "List", HasSearch: true, FilterFields: []BridgeField{
			{DBName: "name", GoName: "Name", GoType: "string", IsString: true},
		}}},
		Routes: []BridgeRoute{
			{FuncName: "List", Method: "GET", Path: "/tenants", Category: "list", Authenticated: "user"},
			{FuncName: "Get", Method: "GET", Path: "/tenants/{tenant_id}", Category: "scan_one"},
			{FuncName: "Create", Method: "POST", Path: "/tenants", Category: "create"},
			{FuncName: "Delete", Method: "DELETE", Path: "/tenants/{tenant_id}", Category: "exec"},
		},
	}

	dir := t.TempDir()
	cfg := Config{
		ProjectRoot: dir,
		Manifest:    &manifest.Manifest{Clients: &manifest.ClientsConfig{TypeScript: &manifest.TypeScriptClientConfig{}}},
	}
	entity := BuildTSClientEntity(data, resolved)
	if err := emitTypeScriptClient(cfg, []TSClientEntity{entity}, Options{}); err != nil {
		t.Fatalf("emit: %v", err)
	}

	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, "workshop/clients/typescript", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}

	types := read("types.gen.ts")
	for _, want := range []string{
		"export interface Tenant {",
		`backend: "db" | "kms";`,
		"description: string | null;",
		"created_at: string;",
		"export interface CreateTenantRequest {",
		"name: string;",
		"description?: string | null;",
	} {
		if !strings.Contains(types, want) {
			t.Errorf("types.gen.ts missing %q", want)
		}
	}

	client := read("client.gen.ts")
	for _, want := range []string{
		"readonly tenants = {",
		"list: (options: PageOptions & {",
		"s?: string;",
		"get: (tenantID: string, ): Promise<RecordResponse<t.Tenant>>",
		"create: (body: t.CreateTenantRequest): Promise<RecordResponse<t.Tenant>>",
		"delete: (tenantID: string): Promise<void>",
		"`/tenants/${p(tenantID)}`",
	} {
		if !strings.Contains(client, want) {
			t.Errorf("client.gen.ts missing %q", want)
		}
	}

	boot := read("client.ts")
	if kind, _, ok := ParseBootstrapMarker(strings.SplitN(boot, "\n", 2)[0]); !ok || kind != "tsclient/client.ts" {
		t.Errorf("client.ts bootstrap marker missing/wrong: %q", strings.SplitN(boot, "\n", 2)[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "workshop/clients/typescript", "tsconfig.json")); err != nil {
		t.Error("tsconfig.json not emitted")
	}

	// Disabled manifest emits nothing.
	emptyDir := t.TempDir()
	if err := emitTypeScriptClient(Config{ProjectRoot: emptyDir, Manifest: &manifest.Manifest{}}, []TSClientEntity{entity}, Options{}); err != nil {
		t.Fatalf("disabled emit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(emptyDir, "workshop")); !os.IsNotExist(err) {
		t.Error("disabled manifest must emit nothing")
	}
}
