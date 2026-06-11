package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/workshop/codegen/schema"
)

// fkProbeResolved builds a verification_codes-shaped entity: string PK in the
// create fields, a nullable string FK, and a composite UNIQUE constraint on
// two non-FK columns. The bogus-FK probe copies the fixture row, so without
// deconfliction the UNIQUE constraint fires before the FK check.
func fkProbeResolved() *ResolvedFile {
	return &ResolvedFile{
		Table: &schema.TableInfo{
			TableName: "verification_codes",
			Constraints: []schema.ConstraintInfo{
				{Name: "unique_active_code", Type: "UNIQUE", Columns: []string{"identifier", "purpose"}},
			},
			Indexes: []schema.IndexInfo{
				{Name: "unique_active_code", Columns: []string{"identifier", "purpose"}, Unique: true},
			},
		},
		EntityName:  "VerificationCode",
		EntityLower: "verificationcode",
		TableName:   "verification_codes",
		PKColumn:    "code_id",
		PKGoName:    "CodeID",
		PKGoType:    "string",
		AllColumns: []schema.ColumnInfo{
			{Name: "code_id", DBType: "varchar", GoType: "string", IsPrimaryKey: true},
			{Name: "identifier", DBType: "varchar(255)", GoType: "string"},
			{Name: "purpose", DBType: "varchar(50)", GoType: "string"},
			{Name: "user_id", DBType: "varchar", GoType: "*string", IsForeignKey: true, IsNullable: true},
		},
		Queries: []ResolvedQuery{{
			InsertFields: []FieldInfo{
				{GoName: "CodeID", GoType: "string", DBName: "code_id", IsPrimaryKey: true},
				{GoName: "Identifier", GoType: "string", DBName: "identifier", MaxLength: 255},
				{GoName: "Purpose", GoType: "string", DBName: "purpose", MaxLength: 50},
				{GoName: "UserID", GoType: "*string", DBName: "user_id", IsForeignKey: true, IsNullable: true},
			},
		}},
	}
}

func fkProbeEnrichment(t *testing.T, resolved *ResolvedFile) IntegrationTestData {
	t.Helper()
	data := IntegrationTestData{
		RepoPkg:    "verificationcodes",
		EntityName: resolved.EntityName,
		PKGoName:   resolved.PKGoName,
		HasCreate:  true,
	}
	methods := []MethodSig{{Name: "Create", Category: "create"}}
	buildEnrichmentData(&data, resolved, methods)
	return data
}

// The FK violation probe must freshen copied unique columns so the FK
// constraint — not a UNIQUE constraint — is the one that fires.
func TestFKViolationProbeFreshensUniqueColumns(t *testing.T) {
	data := fkProbeEnrichment(t, fkProbeResolved())

	if !data.HasFKViolationTest {
		t.Fatal("expected FK violation test")
	}
	if data.FKViolationGoName != "UserID" || !data.FKViolationIsPointer {
		t.Errorf("unexpected FK selection: %q pointer=%v", data.FKViolationGoName, data.FKViolationIsPointer)
	}
	if data.PKReplacementExpr == "" {
		t.Error("expected PK replacement (PK rides in create fields)")
	}

	// One assign per unique group, first eligible column, fresh literal.
	if len(data.FKUniqueAssigns) != 1 {
		t.Fatalf("expected 1 unique assign, got %v", data.FKUniqueAssigns)
	}
	got := data.FKUniqueAssigns[0]
	if got.GoName != "Identifier" || got.Expr != `"fk-probe-identifier"` || got.IsPointer {
		t.Errorf("unexpected assign: %+v", got)
	}
}

// The rendered probe must carry the freshened values into the create input.
func TestFKViolationProbeRendersUniqueAssigns(t *testing.T) {
	data := fkProbeEnrichment(t, fkProbeResolved())
	data.StorePkg = "verificationcodespgx"
	data.RepoImport = "example.com/app/core/repositories/auth/verificationcodes"
	data.FixtureImport = "example.com/app/workshop/testing/fixtures"

	out, err := renderIntegrationTestTemplate(integrationTestGeneratedTemplate, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	content := string(out)
	for _, want := range []string{
		"TestGeneratedVerificationCodeStore_CreateInvalidReference",
		`input.Identifier = "fk-probe-identifier"`,
		`bogusFK := "nonexistent-fk-id"`,
		"input.UserID = &bogusFK",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("rendered probe missing %q", want)
		}
	}
}

// A unique group containing the bogus-FK column is already distinct — no
// freshening needed.
func TestFKViolationProbeSkipsGroupsCoveredByBogusFK(t *testing.T) {
	resolved := fkProbeResolved()
	resolved.Table.Constraints = []schema.ConstraintInfo{
		{Name: "uq", Type: "UNIQUE", Columns: []string{"user_id", "purpose"}},
	}
	resolved.Table.Indexes = nil

	data := fkProbeEnrichment(t, resolved)
	if !data.HasFKViolationTest {
		t.Fatal("expected FK violation test")
	}
	if len(data.FKUniqueAssigns) != 0 {
		t.Errorf("expected no unique assigns, got %v", data.FKUniqueAssigns)
	}
}

// Pointer unique columns get a local + address-of; varchar caps truncate the
// fresh value like the fixture generator does.
func TestFKViolationProbeHandlesPointerAndMaxLength(t *testing.T) {
	resolved := fkProbeResolved()
	resolved.Table.Constraints = []schema.ConstraintInfo{
		{Name: "uq", Type: "UNIQUE", Columns: []string{"slug"}},
	}
	resolved.Table.Indexes = nil
	resolved.Queries[0].InsertFields = append(resolved.Queries[0].InsertFields,
		FieldInfo{GoName: "Slug", GoType: "*string", DBName: "slug", IsNullable: true, MaxLength: 12})

	data := fkProbeEnrichment(t, resolved)
	if len(data.FKUniqueAssigns) != 1 {
		t.Fatalf("expected 1 unique assign, got %v", data.FKUniqueAssigns)
	}
	got := data.FKUniqueAssigns[0]
	if got.GoName != "Slug" || !got.IsPointer {
		t.Errorf("unexpected assign: %+v", got)
	}
	if got.Expr != `"fk-probe-slu"` { // "fk-probe-slug"[:12]
		t.Errorf("expected truncated literal, got %s", got.Expr)
	}
}

// ─── update-mutation value selection ─────────────────────────────────────────

// principalsResolved builds a principals-shaped entity: an update-returning
// Update whose only eligible column is CHECK-IN constrained (folded into
// IsEnum/EnumValues at schema load) with two allowed values.
func principalsResolved() *ResolvedFile {
	enumVals := []string{"user", "service_account"}
	return &ResolvedFile{
		Table:       &schema.TableInfo{TableName: "principals"},
		EntityName:  "Principal",
		EntityLower: "principal",
		TableName:   "principals",
		PKColumn:    "principal_id",
		PKGoName:    "PrincipalID",
		PKGoType:    "string",
		AllColumns: []schema.ColumnInfo{
			{Name: "principal_id", DBType: "varchar", GoType: "string", IsPrimaryKey: true},
			{Name: "principal_type", DBType: "varchar(64)", GoType: "string", IsEnum: true, EnumValues: enumVals},
		},
		Queries: []ResolvedQuery{{
			SetFields: []FieldInfo{
				{GoName: "PrincipalType", GoType: "string", DBName: "principal_type",
					IsEnum: true, EnumValues: enumVals, MaxLength: 64},
			},
		}},
	}
}

func updateEnrichment(t *testing.T, resolved *ResolvedFile) IntegrationTestData {
	t.Helper()
	data := IntegrationTestData{
		RepoPkg:    "principals",
		EntityName: resolved.EntityName,
		PKGoName:   resolved.PKGoName,
	}
	methods := []MethodSig{{Name: "Update", Category: "update_returning"}}
	buildEnrichmentData(&data, resolved, methods)
	return data
}

// A constrained column's update value must come from its allowed set, and
// must differ from the fixture row's value (fixtures seed EnumValues[0]).
func TestUpdateMutationPicksSecondAllowedValueForCheckEnum(t *testing.T) {
	data := updateEnrichment(t, principalsResolved())

	if !data.HasUpdateMutation {
		t.Fatal("expected update-mutation test for enum-constrained column")
	}
	if data.UpdateFieldGoName != "PrincipalType" {
		t.Errorf("UpdateFieldGoName = %q, want PrincipalType", data.UpdateFieldGoName)
	}
	if data.UpdateValueExpr != `"service_account"` {
		t.Errorf("UpdateValueExpr = %s, want %q", data.UpdateValueExpr, "service_account")
	}
}

// A single-value constraint cannot differ from the fixture value — the only
// allowed value is still the only safe pick.
func TestUpdateMutationSingleAllowedValue(t *testing.T) {
	resolved := principalsResolved()
	resolved.Queries[0].SetFields[0].EnumValues = []string{"email"}

	data := updateEnrichment(t, resolved)
	if !data.HasUpdateMutation || data.UpdateValueExpr != `"email"` {
		t.Errorf("got HasUpdateMutation=%v UpdateValueExpr=%s, want true %q",
			data.HasUpdateMutation, data.UpdateValueExpr, "email")
	}
}

// Free-form strings stay the preferred pick with the historical literal —
// schemas without CHECK-IN constraints regenerate byte-identically.
func TestUpdateMutationPrefersFreeFormString(t *testing.T) {
	resolved := principalsResolved()
	resolved.Queries[0].SetFields = append(resolved.Queries[0].SetFields,
		FieldInfo{GoName: "DisplayName", GoType: "*string", DBName: "display_name", IsNullable: true})

	data := updateEnrichment(t, resolved)
	if data.UpdateFieldGoName != "DisplayName" || data.UpdateValueExpr != `"updated-value"` {
		t.Errorf("got field=%q value=%s, want DisplayName %q",
			data.UpdateFieldGoName, data.UpdateValueExpr, "updated-value")
	}
}

// The rendered Update test must assign the chosen allowed value.
func TestUpdateMutationRendersAllowedValue(t *testing.T) {
	data := updateEnrichment(t, principalsResolved())
	data.StorePkg = "principalspgx"
	data.RepoImport = "example.com/app/core/repositories/auth/principals"
	data.FixtureImport = "example.com/app/workshop/testing/fixtures"

	out, err := renderIntegrationTestTemplate(integrationTestGeneratedTemplate, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	content := string(out)
	for _, want := range []string{
		"TestGeneratedPrincipalStore_Update",
		`newValue := "service_account"`,
		"PrincipalType: &newValue",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("rendered update test missing %q", want)
		}
	}
}

// The bogus-FK probe must never invent a value for an enum/CHECK-constrained
// unique column — when that leaves a unique group undeconflictable, the test
// is dropped rather than emitted with a constraint-violating literal.
func TestFKViolationProbeNeverFreshensEnumColumns(t *testing.T) {
	resolved := fkProbeResolved()
	resolved.Table.Constraints = []schema.ConstraintInfo{
		{Name: "uq", Type: "UNIQUE", Columns: []string{"purpose"}},
	}
	resolved.Table.Indexes = nil
	for i := range resolved.Queries[0].InsertFields {
		if resolved.Queries[0].InsertFields[i].DBName == "purpose" {
			resolved.Queries[0].InsertFields[i].IsEnum = true
			resolved.Queries[0].InsertFields[i].EnumValues = []string{"signup", "reset"}
		}
	}

	data := fkProbeEnrichment(t, resolved)
	if data.HasFKViolationTest {
		t.Error("expected FK violation test to be dropped — enum unique column is not freshenable")
	}
	for _, a := range data.FKUniqueAssigns {
		if a.GoName == "Purpose" {
			t.Errorf("enum column freshened with invented literal: %+v", a)
		}
	}
}

// When a copied unique group has no freshenable column, the probe would map
// to ErrAlreadyExists — the test must be dropped rather than emitted wrong.
func TestFKViolationProbeDroppedWhenUndeconflictable(t *testing.T) {
	resolved := fkProbeResolved()
	resolved.Table.Constraints = []schema.ConstraintInfo{
		{Name: "uq", Type: "UNIQUE", Columns: []string{"attempt_count"}},
	}
	resolved.Table.Indexes = nil
	resolved.Queries[0].InsertFields = append(resolved.Queries[0].InsertFields,
		FieldInfo{GoName: "AttemptCount", GoType: "int", DBName: "attempt_count"})

	data := fkProbeEnrichment(t, resolved)
	if data.HasFKViolationTest {
		t.Error("expected FK violation test to be dropped")
	}
	if len(data.FKUniqueAssigns) != 0 {
		t.Errorf("expected no unique assigns, got %v", data.FKUniqueAssigns)
	}
}

// ─── standard-probe gating ───────────────────────────────────────────────────

// spacesResolved builds a spaces-shaped entity: a nullable self-referential
// FK (parent_space_id — fixtures seed it NULL) rides in the scope params of
// Get / Update / SoftDelete / Delete, while List is scoped by tenant_id only.
func spacesResolved() *ResolvedFile {
	return &ResolvedFile{
		Table: &schema.TableInfo{
			TableName: "spaces",
			ForeignKeys: []schema.ForeignKeyInfo{
				{Columns: []string{"tenant_id"}, RefTable: "tenants", RefColumns: []string{"tenant_id"}, ColumnName: "tenant_id"},
				{Columns: []string{"parent_space_id"}, RefTable: "spaces", RefColumns: []string{"space_id"}, ColumnName: "parent_space_id"},
			},
		},
		EntityName:  "Space",
		EntityLower: "space",
		EntityPlural: "Spaces",
		TableName:   "spaces",
		PackageName: "spaces",
		DomainName:  "tenancy",
		PKColumn:    "space_id",
		PKGoName:    "SpaceID",
		PKGoType:    "string",
		AllColumns: []schema.ColumnInfo{
			{Name: "space_id", DBType: "varchar", GoType: "string", IsPrimaryKey: true},
			{Name: "tenant_id", DBType: "varchar", GoType: "string", IsForeignKey: true},
			{Name: "parent_space_id", DBType: "varchar", GoType: "*string", IsForeignKey: true, IsNullable: true},
			{Name: "name", DBType: "varchar(255)", GoType: "string"},
		},
		Queries: []ResolvedQuery{
			{QueryBlock: QueryBlock{ReturnsRows: true, Params: []string{"space_id", "tenant_id", "parent_space_id"}},
				FuncName: "Get"},
			{QueryBlock: QueryBlock{HasFilters: true, HasOrder: true, HasLimit: true, Params: []string{"tenant_id"}},
				FuncName: "List"},
			{QueryBlock: QueryBlock{Type: QueryUpdate, HasFields: true, ReturnsRows: true,
				Params: []string{"space_id", "tenant_id", "parent_space_id"}},
				FuncName:  "Update",
				SetFields: []FieldInfo{{GoName: "Name", GoType: "string", DBName: "name", MaxLength: 255}}},
			{QueryBlock: QueryBlock{Type: QueryUpdate, Params: []string{"space_id", "tenant_id", "parent_space_id"}},
				FuncName: "SoftDelete"},
			{QueryBlock: QueryBlock{Type: QueryDelete, Params: []string{"space_id", "tenant_id", "parent_space_id"}},
				FuncName: "Delete"},
		},
	}
}

// A scope param reading a nullable self-referential FK (seeded NULL by the
// fixtures) makes a probe unusable: dereferencing the nil pointer panics,
// and `col = NULL` can never match the fixture row. Every probe carrying
// that param must be suppressed; List (scoped by tenant only) survives.
func TestNilSeededScopeArgSuppressesProbes(t *testing.T) {
	data, err := BuildIntegrationTestData(spacesResolved(), "example.com/app", "primary")
	if err != nil {
		t.Fatalf("BuildIntegrationTestData: %v", err)
	}

	if data.HasGet {
		t.Error("Get probe must be suppressed (nil-seeded parent_space_id arg)")
	}
	if data.HasSoftDelete {
		t.Error("SoftDelete probe must be suppressed")
	}
	if data.HasHardDelete {
		t.Error("Delete probe must be suppressed")
	}
	if data.HasUpdateMutation {
		t.Error("Update mutation must be suppressed")
	}
	if !data.HasList {
		t.Fatal("List probe must survive (no nil-seeded scope args)")
	}
	if len(data.ListExtraCallArgs) != 1 || data.ListExtraCallArgs[0] != "created.TenantID" {
		t.Errorf("unexpected List scope args: %v", data.ListExtraCallArgs)
	}
	if !data.HasAnyTest() {
		t.Error("List probe should keep the test file alive")
	}
}

// Only the method literally named "Get" drives the Get-roundtrip tests —
// a GetByFoo-only entity must not emit store.Get(...) calls, and with no
// other probes the whole test file is skipped (it would not compile).
func TestScanOneVariantDoesNotEmitGetProbe(t *testing.T) {
	resolved := &ResolvedFile{
		Table:       &schema.TableInfo{TableName: "dashboard_basic_auth_credentials"},
		EntityName:  "DashboardBasicAuthCredential",
		EntityLower: "dashboardbasicauthcredential",
		EntityPlural: "DashboardBasicAuthCredentials",
		TableName:   "dashboard_basic_auth_credentials",
		PackageName: "dashboardbasicauthcredentials",
		DomainName:  "dashboards",
		PKColumn:    "credential_id",
		PKGoName:    "CredentialID",
		PKGoType:    "string",
		AllColumns: []schema.ColumnInfo{
			{Name: "credential_id", DBType: "varchar", GoType: "string", IsPrimaryKey: true},
			{Name: "dashboard_id", DBType: "varchar", GoType: "string", IsForeignKey: true},
		},
		Queries: []ResolvedQuery{
			{QueryBlock: QueryBlock{ReturnsRows: true, Params: []string{"dashboard_id"}},
				FuncName: "GetByDashboardID"},
		},
	}

	data, err := BuildIntegrationTestData(resolved, "example.com/app", "primary")
	if err != nil {
		t.Fatalf("BuildIntegrationTestData: %v", err)
	}
	if data.HasGet {
		t.Error("GetByDashboardID must not drive the standard Get probe")
	}
	if data.HasAnyTest() {
		t.Error("no probes expected — the test file must be skipped entirely")
	}
}

// Nullable scope columns that fixtures seed non-NULL (anything but a
// self-referential FK) still dereference the fixture row's pointer field.
func TestScopeArgsDerefNonSelfRefNullablePointers(t *testing.T) {
	resolved := spacesResolved()
	// Re-point the parent FK at another table: now seeded by the fixture.
	resolved.Table.ForeignKeys[1].RefTable = "orgs"

	data, err := BuildIntegrationTestData(resolved, "example.com/app", "primary")
	if err != nil {
		t.Fatalf("BuildIntegrationTestData: %v", err)
	}
	if !data.HasGet {
		t.Fatal("Get probe expected (no nil-seeded args)")
	}
	want := []string{"created.TenantID", "*created.ParentSpaceID"}
	if len(data.GetExtraCallArgs) != len(want) {
		t.Fatalf("unexpected Get scope args: %v", data.GetExtraCallArgs)
	}
	for i, w := range want {
		if data.GetExtraCallArgs[i] != w {
			t.Errorf("arg %d: got %q want %q", i, data.GetExtraCallArgs[i], w)
		}
	}
}

// Without a standard Get, Delete still smoke-tests but must not render the
// Get-based verification (store.Get does not exist on such stores).
func TestDeleteRendersWithoutGetVerification(t *testing.T) {
	data := IntegrationTestData{
		StorePkg:      "thingspgx",
		RepoPkg:       "things",
		EntityName:    "Thing",
		EntityLower:   "thing",
		RepoImport:    "example.com/app/core/repositories/stuff/things",
		FixtureImport: "example.com/app/workshop/testing/fixtures",
		PKGoName:      "ThingID",
		PKGoType:      "string",
		HasHardDelete: true,
	}
	out, err := renderIntegrationTestTemplate(integrationTestGeneratedTemplate, data)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	content := string(out)
	if !strings.Contains(content, "TestGeneratedThingStore_Delete") {
		t.Error("expected Delete test")
	}
	if strings.Contains(content, "store.Get(") {
		t.Error("store.Get must not be referenced without a standard Get")
	}
}

// An entity with no surviving probes must not emit generated_test.go at all
// (its fixed imports would be unused), and a stale copy must be removed.
func TestEmptyGeneratedTestFileSkippedAndStaleRemoved(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "generated_test.go")
	if err := os.WriteFile(stale, []byte("package thingspgx\n"), 0644); err != nil {
		t.Fatal(err)
	}

	data := IntegrationTestData{
		StorePkg:      "thingspgx",
		RepoPkg:       "things",
		EntityName:    "Thing",
		EntityLower:   "thing",
		RepoImport:    "example.com/app/core/repositories/stuff/things",
		FixtureImport: "example.com/app/workshop/testing/fixtures",
		PKGoName:      "ThingID",
		PKGoType:      "string",
		MigrationsDir: "workshop/migrations/primary",
	}
	if err := GenerateIntegrationTest(data, dir, Options{}); err != nil {
		t.Fatalf("GenerateIntegrationTest: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale generated_test.go must be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "store_test.go")); err != nil {
		t.Error("store_test.go bootstrap must still be created")
	}
}
