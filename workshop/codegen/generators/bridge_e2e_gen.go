package generators

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// BridgeE2EData renders generated_e2e_test.go for one bridged entity hosted
// by a spec-mode database: a self-contained HTTP stack (testsqlite → spec
// store → repository → bridge → web handler → httptest.Server) driven by the
// entity's resolved bridge.yml routes.
type BridgeE2EData struct {
	BridgePackage string
	EntityName    string

	// SpecMode selects the test stack: spec → testsqlite + sqlitefixtures +
	// NewStore(q,d,inTx); pgx → testpgx + fixtures + NewStore(log, pool).
	// The HTTP-level test bodies are identical either way.
	SpecMode bool

	RepoPkg     string
	RepoImport  string
	StorePkg    string
	StoreImport string

	MigrationsDir string // e.g. "workshop/migrations/litedb"

	// FixturePkg / FixtureImport / FKSeeds drive parent seeding for FK-child
	// entities: the create body's FK fields are populated from rows the
	// fixtures package inserts before the POST.
	FixturePkg    string
	FixtureImport string
	FKSeeds       []FKSeed

	PKJSON string // JSON field name of the PK, e.g. "id"

	// NotFoundID is a syntactically-valid but absent id for the not-found
	// probe — a real UUID for uuid PKs (a non-UUID string would 500 on the
	// pgx driver, not 404), else an arbitrary string.
	NotFoundID string

	CreatePath string // POST path, no params

	// CreateMaxBodySize is the create route's max_body_size in bytes (0 =
	// none) — gates the oversized-payload probe (P6).
	CreateMaxBodySize int64

	HasGet      bool
	GetPathExpr string // Go expr building the GET path from `id`

	HasList  bool
	ListPath string

	HasDelete      bool
	DeletePathExpr string // Go expr building the DELETE path from `id`

	// HasRecordState gates the mass-assignment probe: a POST smuggling a
	// record_state value must not control the stored state (SEC1/P5).
	HasRecordState bool

	// Update-path mass-assignment probe (P5): a PUT setting a legit field
	// plus a smuggled record_state must leave the stored state untouched.
	// Requires record_state, an Update route, and a settable update field.
	HasUpdate        bool
	UpdatePathExpr   string // Go expr building the PUT path from `id`
	UpdateLegitJSON  string // a non-server-set update field (json name)
	UpdateLegitValue string // a valid value literal for that field

	// String filter params (plus "search") get the strict probe: a payload
	// is a parameterized match value, so a 200 must return zero rows.
	// Non-string filter params join order/limit/cursor in the never-500
	// probe only — their parsers ignore unparseable values, legitimately
	// returning unfiltered results (P2).
	StringFilterParams []string
	OtherProbeParams   []string

	// AuthMode selects the credential the suite drives routes with:
	// "" (anonymous), "jwt" (user/any fast path — a minted token, no DB
	// rows), or "session" (user_session — a session row seeded with a real
	// token hash; pgx only). service_account and authorize-gated routes
	// still skip the suite, each with its own printed reason.
	AuthMode string

	// BridgeAuthEnabled mirrors the bridge's AuthEnabled flag: NewBridge
	// takes authenticator+authorizer parameters when set, regardless of
	// which routes authenticate.
	BridgeAuthEnabled bool

	// CreateAuthed / GetAuthed gate the anonymous-401 probe — when the
	// suite runs authenticated, one unauthenticated request must still be
	// rejected, proving enforcement is on in this exact stack.
	CreateAuthed bool
	GetAuthed    bool

	// ModulePath derives the session-mode auth imports (project repos,
	// satisfiers) in the generated file.
	ModulePath string

	// HasAuthorize wires a real schema over the seedable in-memory store
	// (phase C): the bridge's own @auth.create relationship writes at POST
	// time grant the suite's subject the relations the authorize-gated
	// routes check — production semantics, no artificial seeding. Only set
	// when generation-time analysis proves every authorize-gated suite
	// route's permission is satisfiable by a created-on-POST relation.
	HasAuthorize bool

	// AuthSchemaEntity renders the entity's resource schema locally in the
	// generated file — the domain composite package exports AuthSchema()
	// but importing it from an entity bridge package is an import cycle.
	AuthSchemaEntity *AuthSchemaEntity

	// NotFoundStatus is what a probe of an absent id receives: 404, or 403
	// when the Get route is authorize-checked (no relation exists on a
	// nonexistent resource, and denial renders before lookup).
	NotFoundStatus int

	// UniqueRequestFields are create-request string fields backed by UNIQUE
	// columns — probes POSTing the valid request more than once must vary
	// them or the second insert 409s.
	UniqueRequestFields []string
}

// GenerateBridgeE2E emits light e2e tests for a bridged entity hosted by a
// spec-mode database. Entities whose create model requires foreign keys are
// skipped for now — their POST bodies need seeded parents. Routes with
// params other than the PK are likewise skipped (scope params need fixture
// context the plain HTTP round-trip doesn't have). Every skip prints its
// reason — a silently absent e2e suite reads as a generation failure.
func GenerateBridgeE2E(data BridgeTemplateData, resolved *ResolvedFile, bridgeDir, modulePath, hostDB string, specMode bool, opts Options) error {
	path := filepath.Join(bridgeDir, "generated_e2e_test.go")

	e2e, skipReason := buildBridgeE2EData(data, resolved, modulePath, hostDB, specMode)
	if skipReason != "" {
		fmt.Printf("      skip generated_e2e_test.go (%s: %s)\n", data.BridgePackage, skipReason)
		if fileExists(path) && !opts.DryRun {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove stale generated_e2e_test.go: %w", err)
			}
		}
		return nil
	}

	if err := renderE2EFile(path, bridgeE2EGeneratedTemplate, "", e2e, opts); err != nil {
		return err
	}
	fmt.Printf("      write %s\n", path)

	bootstrapPath := filepath.Join(bridgeDir, "e2e_test.go")
	if !fileExists(bootstrapPath) || opts.ForceBootstrap {
		if err := renderE2EFile(bootstrapPath, bridgeE2EBootstrapTemplate, "bridge-e2e/e2e_test.go", e2e, opts); err != nil {
			return err
		}
		fmt.Printf("      create %s\n", bootstrapPath)
	}
	return nil
}

func renderE2EFile(path, tmplText, bootstrapKind string, e2e BridgeE2EData, opts Options) error {
	tmpl, err := template.New("bridge_e2e").Parse(tmplText)
	if err != nil {
		return fmt.Errorf("parse e2e template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, e2e); err != nil {
		return fmt.Errorf("render %s: %w", path, err)
	}
	out := buf.Bytes()
	if bootstrapKind != "" {
		out = prependBootstrapMarker(bootstrapKind, out)
	}
	return renderGoFile(path, out, path, opts)
}

// FKSeed is one foreign-key create field whose value comes from a parent row
// seeded by the fixtures package before the POST.
type FKSeed struct {
	RequestField string // create-request Go field, e.g. "AccountID"
	ParentEntity string // parent entity name, e.g. "Account"
	ParentPKExpr string // expr off the seeded parent, e.g. "parent0.ID"
}

// buildBridgeE2EData assembles the template data for one bridged entity.
// A non-empty skipReason means no e2e suite can be generated and says why.
func buildBridgeE2EData(data BridgeTemplateData, resolved *ResolvedFile, modulePath, hostDB string, specMode bool) (BridgeE2EData, string) {
	if len(data.CreateQueries) == 0 {
		return BridgeE2EData{}, "no create query — nothing to round-trip"
	}

	// Required FK create fields are populated from seeded parents. Each FK
	// column must map to a field in the create request (one that wasn't
	// stripped as a URL path param) and a parent table we can seed.
	seeds, seedSkip := buildFKSeeds(data, resolved)
	if seedSkip != "" {
		return BridgeE2EData{}, seedSkip
	}

	// Store package + fixtures differ by mode; the test bodies do not.
	wiring := buildStackWiring(resolved, modulePath, hostDB, specMode)
	fixturePkg, fixtureDir := "fixtures", "fixtures"
	if specMode {
		fixturePkg, fixtureDir = "sqlitefixtures", "sqlitefixtures"
	}

	e2e := BridgeE2EData{
		BridgePackage: data.BridgePackage,
		EntityName:    data.EntityName,
		SpecMode:      specMode,
		RepoPkg:       wiring.RepoPkg,
		RepoImport:    wiring.RepoImport,
		StorePkg:      wiring.StorePkg,
		StoreImport:   wiring.StoreImport,
		FixturePkg:    fixturePkg,
		FixtureImport: modulePath + "/workshop/testing/" + fixtureDir,
		FKSeeds:       seeds,
		MigrationsDir: wiring.MigrationsDir,
		PKJSON:        resolved.PKColumn,
		NotFoundID:    "nonexistent-e2e-id",
	}
	if pkIsUUID(resolved) {
		e2e.NotFoundID = "00000000-0000-4000-8000-000000000000"
	}
	for _, col := range resolved.AllColumns {
		if col.Name == "record_state" {
			e2e.HasRecordState = true
			break
		}
	}
	for _, lq := range data.ListQueries {
		if lq.FuncName != "List" {
			continue
		}
		for _, f := range lq.FilterFields {
			// Enum filters have a closed, type-checked domain — an
			// out-of-domain value can't inject (it's bound and rejected by
			// the column type), so they aren't a probe surface. (Postgres
			// 500s on an invalid enum cast; tracked as a robustness item.)
			if f.IsEnum {
				continue
			}
			if f.IsString {
				e2e.StringFilterParams = append(e2e.StringFilterParams, f.DBName)
			} else {
				e2e.OtherProbeParams = append(e2e.OtherProbeParams, f.DBName)
			}
		}
		if lq.HasSearch {
			// The generated bridge reads the search term from the "s" query
			// param (parseQueryParams), not "search".
			e2e.StringFilterParams = append(e2e.StringFilterParams, "s")
		}
		break
	}

	uniqueCols := map[string]bool{}
	for _, col := range resolved.AllColumns {
		if col.IsUnique && !col.IsPrimaryKey {
			uniqueCols[col.Name] = true
		}
	}
	// Composite (and partial) unique indexes aren't single-column IsUnique;
	// every member column must vary too or repeated valid inserts 409.
	if resolved.Table != nil {
		for _, idx := range resolved.Table.Indexes {
			if !idx.Unique {
				continue
			}
			for _, col := range idx.Columns {
				if col != resolved.PKColumn {
					uniqueCols[col] = true
				}
			}
		}
	}
	for _, f := range data.CreateQueries[0].Fields {
		if f.IsString && uniqueCols[f.DBName] {
			e2e.UniqueRequestFields = append(e2e.UniqueRequestFields, f.DBName)
		}
	}

	// Route auth requirements are evaluated per SUITE route below — only
	// the routes the probes actually exercise decide the credential.
	type routeAuth struct {
		mode      string // "" | "user" | "service_account" | "user_session" | "any"
		authorize *AuthorizeSpec
		method    string
		path      string
	}
	suiteAuth := map[string]routeAuth{}
	var createRels []BridgeCreateRel
	recordAuth := func(funcName string, r BridgeRoute) {
		ra := routeAuth{method: r.Method, path: r.Path, authorize: r.Authorize}
		for _, m := range r.MiddlewareChain {
			if m.Authenticate != "" {
				ra.mode = m.Authenticate
			}
		}
		suiteAuth[funcName] = ra
		if funcName == "Create" {
			createRels = r.CreateRels
		}
	}

	pkParam := "{" + resolved.PKColumn + "}"
	for _, r := range data.Routes {
		switch r.FuncName {
		case "Create":
			if r.Method == "POST" && !strings.Contains(r.Path, "{") {
				e2e.CreatePath = r.Path
				recordAuth("Create", r)
				for _, m := range r.MiddlewareChain {
					if m.MaxBodySize > 0 {
						e2e.CreateMaxBodySize = m.MaxBodySize
						break
					}
				}
			}
		case "Get":
			if expr, ok := pkOnlyPathExpr(r.Path, pkParam); ok {
				e2e.HasGet = true
				e2e.GetPathExpr = expr
				recordAuth("Get", r)
			}
		case "List":
			if r.Method == "GET" && !strings.Contains(r.Path, "{") {
				e2e.HasList = true
				e2e.ListPath = r.Path
				recordAuth("List", r)
			}
		case "Delete":
			if expr, ok := pkOnlyPathExpr(r.Path, pkParam); ok {
				e2e.HasDelete = true
				e2e.DeletePathExpr = expr
				recordAuth("Delete", r)
			}
		case "Update":
			if expr, ok := pkOnlyPathExpr(r.Path, pkParam); ok {
				e2e.HasUpdate = true
				e2e.UpdatePathExpr = expr
				recordAuth("Update", r)
			}
		}
	}

	// Authorize-gated suite routes (phase C): the stack wires the entity's
	// real schema over a seedable in-memory store, and the bridge's own
	// @auth.create relationship writes at POST time grant the suite's
	// subject its relations — production semantics. Generation-time
	// analysis proves each route's permission is satisfiable by a
	// created-on-POST direct relation; anything it can't prove skips with
	// the reason, never emitting a suite that fails at runtime.
	entityResourceType := Singularize(resolved.TableName)
	granted := map[string]bool{}
	for _, rel := range createRels {
		if rel.SubjectFromContext && rel.ResourceType == entityResourceType {
			granted[rel.Relation] = true
		}
	}
	permissions := map[string]AuthSchemaPermission{}
	if len(resolved.AuthRelations) > 0 || len(resolved.AuthPermissions) > 0 {
		entity := buildAuthSchemaEntityFromResolved(resolved)
		e2e.AuthSchemaEntity = &entity
		for _, p := range entity.Permissions {
			permissions[p.Name] = p
		}
	}

	var needSession, needUser, needServiceAccount bool
	for funcName, ra := range suiteAuth {
		if spec := ra.authorize; spec != nil {
			routeDesc := fmt.Sprintf("route %s %s", ra.method, ra.path)
			// Mirror the bridge template's resource-type derivation exactly
			// (bridge_tmpl check branch): Entity override, else the path
			// param with "_id" stripped, else the entity singular.
			resourceType := spec.Entity
			if resourceType == "" && spec.Param != "" {
				resourceType = strings.TrimSuffix(spec.Param, "_id")
			}
			if resourceType == "" {
				resourceType = entityResourceType
			}
			if resourceType != entityResourceType {
				return BridgeE2EData{}, fmt.Sprintf("%s authorizes against resource type %q — cross-entity authorization is not yet generated", routeDesc, resourceType)
			}
			perm, ok := permissions[spec.Permission]
			if !ok {
				return BridgeE2EData{}, fmt.Sprintf("%s checks permission %q which the entity's auth schema does not define", routeDesc, spec.Permission)
			}
			satisfiable := false
			for _, check := range perm.Checks {
				if check.IsDirect && granted[check.Relation] {
					satisfiable = true
					break
				}
			}
			if !satisfiable {
				return BridgeE2EData{}, fmt.Sprintf("%s needs permission %q but the create route's @auth.create relations don't grant a satisfying direct relation", routeDesc, spec.Permission)
			}
			e2e.HasAuthorize = true
			if funcName == "Get" && spec.Pattern == "check" {
				e2e.NotFoundStatus = 403
			}
		}
		switch ra.mode {
		case "user_session":
			needSession = true
		case "user", "any":
			needUser = true
		case "service_account":
			needServiceAccount = true
		}
	}
	if e2e.HasAuthorize && !needSession && !needUser {
		return BridgeE2EData{}, "authorize-gated routes without a user-credential authenticate mode — not yet generated"
	}
	switch {
	case needServiceAccount && (needUser || needSession):
		return BridgeE2EData{}, "routes mix service_account and user credentials — per-route credential switching is not yet generated"
	case needServiceAccount:
		return BridgeE2EData{}, "service_account routes need an API-key-wired stack — not yet generated"
	case needSession && specMode:
		return BridgeE2EData{}, "user_session routes need the pgx auth stack — not available in spec mode"
	case needSession:
		e2e.AuthMode = "session"
	case needUser:
		e2e.AuthMode = "jwt"
	}
	e2e.BridgeAuthEnabled = data.AuthEnabled
	e2e.ModulePath = modulePath
	e2e.CreateAuthed = suiteAuth["Create"].mode != ""
	e2e.GetAuthed = suiteAuth["Get"].mode != ""
	if e2e.NotFoundStatus == 0 {
		e2e.NotFoundStatus = 404
	}
	if !e2e.HasAuthorize {
		// The local schema function is only emitted for authorize-wired
		// stacks; an anonymous or authenticate-only suite doesn't need it.
		e2e.AuthSchemaEntity = nil
	}

	// Pick a settable, non-server-set string update field for the update
	// mass-assignment probe (SEC1 already strips record_state/ownership from
	// the update model, so any remaining string field is a safe legit edit).
	if e2e.HasRecordState && e2e.HasUpdate && len(data.UpdateQueries) > 0 {
		for _, f := range data.UpdateQueries[0].Fields {
			if f.IsString && !f.IsEnum {
				e2e.UpdateLegitJSON = f.DBName
				e2e.UpdateLegitValue = `"edited-value"`
				break
			}
		}
	}

	// Without a paramless POST there is nothing to round-trip.
	if e2e.CreatePath == "" {
		return BridgeE2EData{}, "no paramless POST create route — nothing to round-trip"
	}
	return e2e, ""
}

// buildFKSeeds maps each required (NOT NULL) foreign-key create field to a
// parent fixture and the create-request field it fills. A non-empty
// skipReason means an FK can't be seeded from the request body (e.g. it was
// stripped as a URL path param, or its create field isn't in the request),
// so the caller falls back to skipping e2e for that entity. Self-referential
// FKs are nullable by design and need no seed.
func buildFKSeeds(data BridgeTemplateData, resolved *ResolvedFile) ([]FKSeed, string) {
	if resolved.Table == nil {
		return nil, ""
	}

	// Request fields present in the (path-param-stripped) create model.
	requestFields := map[string]string{} // dbName -> GoName
	for _, f := range data.CreateQueries[0].Fields {
		requestFields[f.DBName] = f.GoName
	}

	// Which create columns are NOT NULL (required for a successful insert).
	required := map[string]bool{}
	for _, rq := range resolved.Queries {
		for _, f := range rq.InsertFields {
			if !f.IsNullable {
				required[f.DBName] = true
			}
		}
	}

	var seeds []FKSeed
	idx := 0
	for _, fk := range resolved.Table.ForeignKeys {
		if len(fk.Columns) != 1 || len(fk.RefColumns) != 1 {
			return nil, fmt.Sprintf("composite FK %v — parent seeding not supported", fk.Columns)
		}
		col := fk.Columns[0]
		if !required[col] {
			continue // nullable FK — the insert succeeds without it
		}
		if fk.RefTable == resolved.TableName {
			return nil, fmt.Sprintf("required self-referential FK %s can't be seeded", col)
		}
		goName, ok := requestFields[col]
		if !ok {
			return nil, fmt.Sprintf("required FK %s is not settable from the create request body", col)
		}
		seeds = append(seeds, FKSeed{
			RequestField: goName,
			ParentEntity: ToPascalCase(Singularize(fk.RefTable)),
			ParentPKExpr: fmt.Sprintf("parent%d.%s", idx, ToPascalCase(fk.RefColumns[0])),
		})
		idx++
	}
	return seeds, ""
}

// pkOnlyPathExpr converts a route path whose only parameter is the PK into a
// Go expression over the `id` variable, e.g. "/widgets/{id}" →
// `"/widgets/" + id`. Paths with other params are rejected.
func pkOnlyPathExpr(path, pkParam string) (string, bool) {
	if !strings.Contains(path, pkParam) {
		return "", false
	}
	rest := strings.ReplaceAll(path, pkParam, "")
	if strings.Contains(rest, "{") {
		return "", false
	}
	parts := strings.Split(path, pkParam)
	exprs := make([]string, 0, len(parts)*2-1)
	for i, p := range parts {
		if i > 0 {
			exprs = append(exprs, "id")
		}
		if p != "" {
			exprs = append(exprs, fmt.Sprintf("%q", p))
		}
	}
	return strings.Join(exprs, " + "), true
}
