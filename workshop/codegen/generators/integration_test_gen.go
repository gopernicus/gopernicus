package generators

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gopernicus/gopernicus/workshop/codegen/schema"
)

// ─── integration test template data types ───────────────────────────────────

// IntegrationTestMethod describes a store method to test.
type IntegrationTestMethod struct {
	Name     string // e.g. "Get", "Create", "List"
	Category string // "scan_one", "create", "list", "update", "update_returning", "exec"

	// For scan_one / get: the PK param name
	PKParam string // e.g. "userID"

	// For create
	HasCreate bool

	// For list
	HasList bool

	// For update
	HasUpdate bool

	// For exec (soft delete, archive, restore, hard delete)
	IsDelete    bool // hard delete
	IsSoftState bool // soft delete / archive / restore
	NewState    string // e.g. "deleted", "archived", "active"

	// For update_returning
	ReturnsEntity bool
}

// IntegrationTestData holds all data for rendering a store integration test.
type IntegrationTestData struct {
	// FrameworkPath is the gopernicus framework module path (for sdk, testing imports).

	// SpecMode selects the test harness: spec → testsqlite + sqlitefixtures +
	// NewStore(q, d, inTx); pgx → testpgx + fixtures + NewStore(log, pool).
	SpecMode bool

	// Package info
	StorePkg   string // e.g. "userspgx"
	RepoPkg    string // e.g. "users"
	EntityName string // e.g. "User"
	EntityLower string // e.g. "user"

	// Import paths
	RepoImport    string // full import path to repo package
	FixtureImport string // full import path to fixtures package

	// PK info
	PKColumn string // e.g. "user_id"
	PKGoName string // e.g. "UserID"
	PKGoType string // e.g. "string"

	// MigrationsDir is the project-root-relative migrations directory for
	// the database hosting this store, e.g. "workshop/migrations/primary".
	MigrationsDir string

	// Methods to test
	Methods []IntegrationTestMethod

	// Feature flags
	HasCreate     bool
	HasGet        bool
	HasList       bool
	HasUpdate     bool
	HasSoftDelete bool
	HasHardDelete bool

	// Enrichment: sentinel-error and round-trip coverage. All computed from
	// the create/update @fields so the tests construct real inputs.
	NeedsRepoImport bool     // any test references the repo package
	CreateAssigns   []string // create-field GoNames copied from the fixture entity
	RoundTripFields []string // create-field GoNames asserted equal after Get (non-time)

	// Duplicate-create → ErrAlreadyExists. Requires the PK among @fields so
	// copying the fixture's PK collides.
	HasDuplicateTest bool

	// Bogus-FK create → ErrInvalidReference.
	HasFKViolationTest   bool
	FKViolationGoName    string // create-field GoName of a non-self FK
	FKViolationExpr      string // string literal for the bogus FK value
	FKViolationIsPointer bool   // create-field is a pointer — take the address
	// PKReplacementExpr, when non-empty, replaces the copied PK so the dup
	// constraint doesn't mask the FK violation.
	PKReplacementExpr string
	// FKUniqueAssigns replace copied values on columns that participate in
	// non-PK UNIQUE constraints/indexes, so the bogus-FK probe can only fail
	// on the FK constraint. Without this, Postgres surfaces the unique
	// violation first and the probe maps to ErrAlreadyExists instead of
	// ErrInvalidReference (e.g. verification_tokens.token_hash, tenants.slug).
	FKUniqueAssigns []FKUniqueAssign

	// Update-mutation test (update_returning only — plain updates return no
	// entity). Preferred pick: a free-form (non-enum) string without a tight
	// MaxLength, mutated to "updated-value". Fallback pick: an enum string —
	// native pg enum or CHECK ... IN constrained (folded into IsEnum at
	// schema load) — mutated to the first allowed value that differs from
	// the fixture row's (fixtures seed EnumValues[0]). Entity-side
	// pointer-ness drives the assertion. UpdateValueExpr is the quoted Go
	// literal assigned to newValue.
	HasUpdateMutation        bool
	UpdateFieldGoName        string
	UpdateFieldEntityPointer bool
	UpdateValueExpr          string

	// Domain info
	DomainName string

	// *ExtraCallArgs are Go expressions to pass for queries whose SQL declares
	// scope parameters beyond the PK / filter (e.g. @parent_world_id on a
	// Get / List / SoftDelete / Delete). Each expression reads from the first
	// created fixture's struct, dereferencing nullable columns. Examples:
	// []string{"*created.ParentWorldID"}.
	//
	// Held per-method because the same entity may have different scope shapes
	// across its standard verbs (though in practice they usually match).
	ListExtraCallArgs       []string
	GetExtraCallArgs        []string
	SoftDeleteExtraCallArgs []string
	HardDeleteExtraCallArgs []string
}

// FKUniqueAssign is one fresh-value assignment in the bogus-FK probe input,
// deconflicting a unique column copied from the fixture row.
type FKUniqueAssign struct {
	GoName    string // create-field GoName
	Expr      string // string literal distinct from any fixture-generated value
	IsPointer bool   // create-field is a pointer — take the address
}

// BuildIntegrationTestData creates test data from a resolved file. dbName is
// the manifest database hosting this store — it locates the migrations dir
// the bootstrapped migrateTestDB applies.
func BuildIntegrationTestData(resolved *ResolvedFile, modulePath, dbName string) (IntegrationTestData, error) {
	wiring := buildStackWiring(resolved, modulePath, dbName, false)
	data := IntegrationTestData{
		StorePkg:      wiring.StorePkg,
		RepoPkg:       wiring.RepoPkg,
		EntityName:    resolved.EntityName,
		EntityLower:   resolved.EntityLower,
		RepoImport:    wiring.RepoImport,
		FixtureImport: modulePath + "/workshop/testing/fixtures",
		PKColumn:      resolved.PKColumn,
		PKGoName:      resolved.PKGoName,
		PKGoType:      resolved.PKGoType,
		MigrationsDir: wiring.MigrationsDir,
		DomainName:    resolved.DomainName,
	}

	methods, err := buildRepoMethods(resolved)
	if err != nil {
		return IntegrationTestData{}, err
	}

	for i, m := range methods {
		rq := resolved.Queries[i]
		tm := IntegrationTestMethod{
			Name:     m.Name,
			Category: m.Category,
		}

		switch m.Category {
		case "scan_one", "scan_one_custom":
			data.HasGet = true
			if pk := FindPKParam(m.PKParams, resolved.PKColumn); pk != "" {
				tm.PKParam = pk
			}
			if m.Name == "Get" {
				data.GetExtraCallArgs = buildScopeCallArgs(rq, resolved.PKColumn, resolved)
			}

		case "create":
			data.HasCreate = true
			tm.HasCreate = true

		case "list":
			// Only generate the standard List test for the method named "List"
			// (not "ListByFoo" variants which have different filter types).
			if m.Name == "List" {
				data.HasList = true
				tm.HasList = true
				data.ListExtraCallArgs = buildScopeCallArgs(rq, "", resolved)
			}

		case "update":
			data.HasUpdate = true
			tm.HasUpdate = true
			// Check for soft-delete state changes.
			nameLower := strings.ToLower(m.Name)
			switch {
			case nameLower == "softdelete":
				tm.IsSoftState = true
				tm.NewState = "deleted"
			case nameLower == "archive":
				tm.IsSoftState = true
				tm.NewState = "archived"
			case nameLower == "restore":
				tm.IsSoftState = true
				tm.NewState = "active"
			}

		case "update_returning":
			data.HasUpdate = true
			tm.HasUpdate = true
			tm.ReturnsEntity = true

		case "exec":
			// Determine if it's a delete or state change. Only the method
			// literally named "Delete" drives the standard hard-delete test —
			// auxiliary delete-by-X variants have different signatures and
			// would clobber the scope-arg list if we let them in.
			if rq.Type == QueryDelete && m.Name == "Delete" {
				data.HasHardDelete = true
				tm.IsDelete = true
				data.HardDeleteExtraCallArgs = buildScopeCallArgs(rq, resolved.PKColumn, resolved)
			} else if rq.Type != QueryDelete {
				nameLower := strings.ToLower(m.Name)
				switch {
				case nameLower == "softdelete":
					data.HasSoftDelete = true
					tm.IsSoftState = true
					tm.NewState = "deleted"
					data.SoftDeleteExtraCallArgs = buildScopeCallArgs(rq, resolved.PKColumn, resolved)
				case nameLower == "archive":
					tm.IsSoftState = true
					tm.NewState = "archived"
				case nameLower == "restore":
					tm.IsSoftState = true
					tm.NewState = "active"
				}
			}
		}

		data.Methods = append(data.Methods, tm)
	}

	buildEnrichmentData(&data, resolved, methods)

	return data, nil
}

// buildEnrichmentData computes the sentinel-error, round-trip, and
// update-mutation test inputs from the create/update @fields.
func buildEnrichmentData(data *IntegrationTestData, resolved *ResolvedFile, methods []MethodSig) {
	// Self-referential FK columns are excluded from the bogus-FK test (the
	// fixtures leave them nil by design).
	selfRefCols := make(map[string]bool)
	if resolved.Table != nil {
		for _, fk := range resolved.Table.ForeignKeys {
			if fk.RefTable == resolved.TableName {
				for _, col := range fk.Columns {
					selfRefCols[col] = true
				}
			}
		}
	}

	var createFields, updateFields []FieldInfo
	hasUpdateReturning := false
	for i, m := range methods {
		rq := resolved.Queries[i]
		switch m.Category {
		case "create":
			if len(createFields) == 0 {
				createFields = rq.InsertFields
			}
		case "update_returning":
			if m.Name == "Update" {
				hasUpdateReturning = true
				if len(updateFields) == 0 {
					updateFields = rq.SetFields
				}
			}
		}
	}

	pkInCreate := false
	fkViolationCol := ""
	for _, f := range createFields {
		data.CreateAssigns = append(data.CreateAssigns, f.GoName)
		if !f.IsTime {
			data.RoundTripFields = append(data.RoundTripFields, f.GoName)
		}
		if f.DBName == resolved.PKColumn {
			pkInCreate = true
		}
		if data.FKViolationGoName == "" && f.IsForeignKey && !selfRefCols[f.DBName] &&
			strings.TrimPrefix(f.GoType, "*") == "string" {
			data.HasFKViolationTest = true
			data.FKViolationGoName = f.GoName
			data.FKViolationIsPointer = strings.HasPrefix(f.GoType, "*")
			data.FKViolationExpr = `"nonexistent-fk-id"`
			fkViolationCol = f.DBName
			if strings.EqualFold(fkColumnDBType(resolved, f.DBName), "uuid") {
				data.FKViolationExpr = `"ffffffff-ffff-4fff-8fff-ffffffffffff"`
			}
		}
	}

	data.HasDuplicateTest = data.HasCreate && pkInCreate && resolved.PKGoType == "string"

	// The FK test must not collide on the copied PK — replace it when the
	// PK rides along in the create fields.
	if data.HasFKViolationTest && pkInCreate && resolved.PKGoType == "string" {
		if pkIsUUID(resolved) {
			data.PKReplacementExpr = `"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"`
		} else {
			data.PKReplacementExpr = `"fk-violation-test-id"`
		}
	}

	// The FK test must not collide on any other UNIQUE constraint either —
	// the database raises the unique violation before the FK check, mapping
	// to ErrAlreadyExists and masking ErrInvalidReference.
	if data.HasFKViolationTest {
		buildFKUniqueAssigns(data, resolved, createFields, fkViolationCol)
	}

	if hasUpdateReturning {
		// Preferred: a free-form string column takes any invented value.
		for _, f := range updateFields {
			tight := f.MaxLength > 0 && f.MaxLength < len("updated-value")
			if strings.TrimPrefix(f.GoType, "*") == "string" && !f.IsEnum && !tight &&
				f.DBName != resolved.PKColumn && !f.IsForeignKey {
				data.HasUpdateMutation = true
				data.UpdateFieldGoName = f.GoName
				data.UpdateFieldEntityPointer = entityFieldIsPointer(resolved, f.DBName)
				data.UpdateValueExpr = `"updated-value"`
				break
			}
		}
		// Fallback: an enum-constrained string column (native enum or
		// CHECK ... IN) still proves the update path — but only a value
		// from its allowed set survives the constraint. Deterministic pick:
		// the second allowed value when one exists (fixtures seed the
		// first), otherwise the only allowed value.
		if !data.HasUpdateMutation {
			for _, f := range updateFields {
				if strings.TrimPrefix(f.GoType, "*") == "string" && f.IsEnum &&
					len(f.EnumValues) > 0 &&
					f.DBName != resolved.PKColumn && !f.IsForeignKey {
					data.HasUpdateMutation = true
					data.UpdateFieldGoName = f.GoName
					data.UpdateFieldEntityPointer = entityFieldIsPointer(resolved, f.DBName)
					data.UpdateValueExpr = fmt.Sprintf("%q", enumUpdateValue(f.EnumValues))
					break
				}
			}
		}
	}

	data.NeedsRepoImport = data.HasList || data.HasDuplicateTest ||
		data.HasFKViolationTest || data.HasUpdateMutation
}

// buildFKUniqueAssigns deconflicts the bogus-FK probe input from the fixture
// row it copies. For every unique column group on the table (UNIQUE
// constraints, unique indexes, and single is_unique columns) the probe must
// differ on at least one column, otherwise the unique violation fires before
// the FK check. Groups containing the PK (replaced/generated) or the bogus-FK
// column are already distinct. For the rest, the first eligible copied column
// — a non-FK, non-enum string create field — gets a fresh literal value. If a
// colliding group has copied columns but none eligible, the probe cannot
// reliably observe the FK violation, so the test is dropped for that entity.
func buildFKUniqueAssigns(data *IntegrationTestData, resolved *ResolvedFile, createFields []FieldInfo, fkViolationCol string) {
	if resolved.Table == nil {
		return
	}

	fieldByCol := make(map[string]FieldInfo, len(createFields))
	for _, f := range createFields {
		fieldByCol[f.DBName] = f
	}

	assigned := make(map[string]bool)
	for _, group := range uniqueColumnGroups(resolved.Table) {
		distinct := false
		copied := false
		var picked *FieldInfo
		for _, col := range group {
			if col == resolved.PKColumn || col == fkViolationCol || assigned[col] {
				// The probe already differs from the fixture row here: the PK
				// is replaced (or store/db-generated), the FK carries the
				// bogus value, or an earlier group freshened this column.
				distinct = true
				break
			}
			f, ok := fieldByCol[col]
			if !ok {
				// Not copied from the fixture (store/db-supplied) — cannot
				// deconflict here, but also not a guaranteed collision.
				continue
			}
			copied = true
			if picked == nil && !f.IsForeignKey && !f.IsEnum &&
				strings.TrimPrefix(f.GoType, "*") == "string" {
				ff := f
				picked = &ff
			}
		}
		if distinct || !copied {
			continue
		}
		if picked == nil {
			// Copied unique group with no freshenable column — the probe
			// would map to ErrAlreadyExists, not ErrInvalidReference.
			data.HasFKViolationTest = false
			data.FKViolationGoName = ""
			data.FKViolationExpr = ""
			data.FKViolationIsPointer = false
			data.PKReplacementExpr = ""
			data.FKUniqueAssigns = nil
			return
		}
		assigned[picked.DBName] = true
		data.FKUniqueAssigns = append(data.FKUniqueAssigns, FKUniqueAssign{
			GoName:    picked.GoName,
			Expr:      fkProbeUniqueValueExpr(*picked),
			IsPointer: strings.HasPrefix(picked.GoType, "*"),
		})
	}
}

// uniqueColumnGroups collects every unique column group on a table: UNIQUE
// constraints, unique indexes, and single columns flagged is_unique —
// deduplicated, PK constraint excluded (callers handle the PK separately).
func uniqueColumnGroups(table *schema.TableInfo) [][]string {
	seen := make(map[string]bool)
	var groups [][]string
	add := func(cols []string) {
		if len(cols) == 0 {
			return
		}
		key := strings.Join(cols, "\x00")
		if seen[key] {
			return
		}
		seen[key] = true
		groups = append(groups, cols)
	}

	for _, c := range table.Constraints {
		if strings.EqualFold(c.Type, "UNIQUE") {
			add(c.Columns)
		}
	}
	for _, idx := range table.Indexes {
		if idx.Unique {
			add(idx.Columns)
		}
	}
	for _, col := range table.Columns {
		if col.IsUnique && !col.IsPrimaryKey {
			add([]string{col.Name})
		}
	}
	return groups
}

// enumUpdateValue picks the post-update value for an enum-constrained
// column: the second allowed value when one exists — fixtures seed the
// first, so the mutation is observable — otherwise the only allowed value.
func enumUpdateValue(values []string) string {
	if len(values) > 1 {
		return values[1]
	}
	return values[0]
}

// fkProbeUniqueValueExpr returns a quoted literal for a freshened unique
// column. The "fk-probe-" prefix cannot collide with fixture values (random
// cryptids or "test_"-prefixed); varchar(N) caps are honored the same way
// the fixture generator honors them.
func fkProbeUniqueValueExpr(f FieldInfo) string {
	v := "fk-probe-" + strings.ToLower(f.DBName)
	if f.MaxLength > 0 && len(v) > f.MaxLength {
		v = v[:f.MaxLength]
	}
	return fmt.Sprintf("%q", v)
}

// fkColumnDBType returns the db type of a column by name.
func fkColumnDBType(resolved *ResolvedFile, dbName string) string {
	for _, col := range resolved.AllColumns {
		if col.Name == dbName {
			return col.DBType
		}
	}
	return ""
}

// entityFieldIsPointer reports whether the entity struct field for a column
// is a pointer (nullable column).
func entityFieldIsPointer(resolved *ResolvedFile, dbName string) bool {
	for _, col := range resolved.AllColumns {
		if col.Name == dbName {
			return strings.HasPrefix(col.GoType, "*")
		}
	}
	return false
}

// GenerateIntegrationTest produces the generated_test.go file for a pgxstore package.
func GenerateIntegrationTest(data IntegrationTestData, storeDir string, opts Options) error {
	data.SpecMode = false
	return generateIntegrationTestWith(data, storeDir, opts)
}

// GenerateSpecIntegrationTest produces the spec-store variant: testsqlite
// harness, sqlitefixtures, NewStore(q, d, inTx).
func GenerateSpecIntegrationTest(data IntegrationTestData, storeDir string, opts Options) error {
	data.SpecMode = true
	return generateIntegrationTestWith(data, storeDir, opts)
}

func generateIntegrationTestWith(data IntegrationTestData, storeDir string, opts Options) error {
	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	genFiles := []genFile{
		{"generated_test.go", integrationTestGeneratedTemplate, false},
		{"store_test.go", integrationTestBootstrapTemplate, true},
	}

	for _, f := range genFiles {
		path := filepath.Join(storeDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderIntegrationTestTemplate(f.tmpl, data)
		if err != nil {
			return fmt.Errorf("render %s for %s: %w", f.name, data.StorePkg, err)
		}

		if err := renderGoFile(f.name, out, path, opts); err != nil {
			return err
		}
	}

	return nil
}

// buildScopeCallArgs returns Go expressions that read a query's scope
// parameters (e.g. @parent_world_id) off the first created fixture's struct.
// Used by the generated standard smoke tests (Get / List / SoftDelete /
// HardDelete) to match the generated Store method's positional signature.
//
// Pass excludePKColumn to skip the PK param (Get / SoftDelete / Delete
// already pass the PK as their first arg). Pass "" for List, which has no
// PK in its params at all.
//
// Returns nil when the query has no extra params beyond the (optional) PK.
func buildScopeCallArgs(rq ResolvedQuery, excludePKColumn string, resolved *ResolvedFile) []string {
	if len(rq.Params) == 0 {
		return nil
	}
	colByName := make(map[string]int, len(resolved.AllColumns))
	for i, col := range resolved.AllColumns {
		colByName[col.Name] = i
	}
	out := make([]string, 0, len(rq.Params))
	for _, p := range rq.Params {
		if p == excludePKColumn {
			continue
		}
		idx, ok := colByName[p]
		if !ok {
			// Param does not map to a column on this entity (e.g. a free-form
			// search term). Fall back to the Go zero value for its declared
			// type so the test at least compiles.
			goType := "string"
			if t, ok := rq.ParamTypes[p]; ok {
				goType = t
			}
			out = append(out, zeroValueExprForGoType(goType))
			continue
		}
		col := resolved.AllColumns[idx]
		expr := "created." + ToPascalCase(p)
		if strings.HasPrefix(col.GoType, "*") {
			expr = "*" + expr
		}
		out = append(out, expr)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func zeroValueExprForGoType(goType string) string {
	switch goType {
	case "string":
		return `""`
	case "bool":
		return "false"
	case "int", "int32", "int64", "float64":
		return "0"
	default:
		return goType + "{}"
	}
}

func renderIntegrationTestTemplate(tmplStr string, data IntegrationTestData) ([]byte, error) {
	funcMap := template.FuncMap{
		"lower": strings.ToLower,
		"camel": ToCamelCase,
	}

	t, err := template.New("integration_test").Funcs(funcMap).Parse(tmplStr)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
