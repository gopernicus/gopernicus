package commands

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// featureTemplates holds the FS-charter skeleton templates. They are `.tmpl`
// files, never `.go`, so the repo's whole-tree guards (G4/G9/G10/G11) — all
// `--include='*.go'` — never scan template bodies.
//
//go:embed templates/feature
var featureTemplates embed.FS

// pinned driver versions the emitted store go.mods carry. They MUST match the
// versions this repo's own stores use so an emitted store builds offline against
// the GOMODCACHE `make check` already warmed (the warm-cache invariant, W3
// scaffold-test leg). Bump them together with the repo's store modules.
const (
	pgxVersion        = "v5.8.0"
	libsqlVersion     = "v0.0.0-20260528064733-9d5d30a29a60"
	scaffoldGoVersion = "1.26.1"
	defaultAggregate  = "item"
	identifierExplain = "must be a lowercase Go identifier: start with a letter, then letters or digits"
)

// identifierPattern is the accepted shape for the feature name and aggregate: a
// lowercase Go identifier, so it is valid as a package name, a module path
// segment, and (after title-casing) an exported type.
var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// featureParams is the identity-only input to the charter skeleton (review-gate
// fold item 6): a module-path ROOT, the feature name, and the aggregate name.
// There is ZERO structural/field input — a per-field loop is the workshop-v2b
// codegen trigger, not this slice.
type featureParams struct {
	ModuleRoot string // e.g. example.com/acme
	ModulePath string // e.g. example.com/acme/notes (the feature core module)
	Feature    string // notes  — the feature/core package name
	Agg        string // note   — the domain package name (also the table name)
	AggTitle   string // Note   — the exported entity type
	AggSvc     string // notesvc — the internal service package
	ReposField string // Notes  — the Repositories struct field (title-cased plural)
	Source     string // notes  — the migration source (== Feature)

	// Store-emission identity, carried into the store go.mods (pinned driver
	// versions — the warm-cache invariant).
	PgxVersion    string
	LibsqlVersion string
	GoVersion     string
}

func runNewFeature(args []string) int {
	// The name is a leading positional (gopernicus new feature <name> --module …).
	// stdlib flag stops parsing at the first non-flag arg, so pull the name out
	// before parsing; a trailing positional is still accepted as a fallback.
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}

	fs := flag.NewFlagSet("new feature", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		modulePath = fs.String("module", "", "Go module path ROOT for the feature (required), e.g. github.com/acme/app")
		aggregate  = fs.String("aggregate", defaultAggregate, "the aggregate (entity) name; defaults to a placeholder")
		dir        = fs.String("dir", "", "target directory (default: <feature> under the current directory)")
	)
	fs.Usage = func() { newFeatureUsage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}

	if name == "" {
		name = fs.Arg(0)
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "gopernicus new feature: feature name is required (the positional argument)")
		newFeatureUsage(os.Stderr)
		return 1
	}
	if *modulePath == "" {
		fmt.Fprintln(os.Stderr, "gopernicus new feature: --module is required (the module-path root the feature is emitted under)")
		newFeatureUsage(os.Stderr)
		return 1
	}

	params, err := buildFeatureParams(*modulePath, name, *aggregate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus new feature: %v\n", err)
		return 1
	}

	target := *dir
	if target == "" {
		target = params.Feature
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus new feature: %v\n", err)
		return 1
	}

	if err := emitFeature(absTarget, params); err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus new feature: %v\n", err)
		return 1
	}

	fmt.Printf("scaffolded feature %q (aggregate %q) into %s\n", params.Feature, params.Agg, absTarget)
	fmt.Printf("  module %s + stores/turso + stores/pgx\n", params.ModulePath)
	fmt.Println("next: add the pre-tag replace directives (see README.md), then `go mod tidy && go test ./...`")

	if root, ok := monorepoRoot(absTarget); ok {
		printMonorepoChecklist(os.Stdout, params, root, absTarget)
	}
	return 0
}

// buildFeatureParams validates the identifiers and derives the naming a feature
// core + its two stores need. The module root is trimmed of a trailing slash;
// the feature name becomes the last module-path segment.
func buildFeatureParams(moduleRoot, name, aggregate string) (featureParams, error) {
	moduleRoot = strings.TrimRight(strings.TrimSpace(moduleRoot), "/")
	if moduleRoot == "" {
		return featureParams{}, fmt.Errorf("--module cannot be empty")
	}
	if !identifierPattern.MatchString(name) {
		return featureParams{}, fmt.Errorf("feature name %q is invalid: %s", name, identifierExplain)
	}
	if aggregate == "" {
		aggregate = defaultAggregate
	}
	if !identifierPattern.MatchString(aggregate) {
		return featureParams{}, fmt.Errorf("aggregate %q is invalid: %s", aggregate, identifierExplain)
	}

	return featureParams{
		ModuleRoot:    moduleRoot,
		ModulePath:    moduleRoot + "/" + name,
		Feature:       name,
		Agg:           aggregate,
		AggTitle:      title(aggregate),
		AggSvc:        aggregate + "svc",
		ReposField:    title(aggregate) + "s",
		Source:        name,
		PgxVersion:    pgxVersion,
		LibsqlVersion: libsqlVersion,
		GoVersion:     scaffoldGoVersion,
	}, nil
}

// emitFeature renders the standalone feature tree: the sdk-only core (socket,
// domain rim, sealed service, storetest + in-core memstore reference) plus the
// stores/turso and stores/pgx sibling modules (Q2: both always).
func emitFeature(targetDir string, p featureParams) error {
	agg := p.Agg
	files := []templateFile{
		// Core module.
		{Template: "templates/feature/core.go.mod.tmpl", Out: "go.mod"},
		{Template: "templates/feature/socket.go.tmpl", Out: p.Feature + ".go", Format: true},
		{Template: "templates/feature/entity.go.tmpl", Out: filepath.Join("domain", agg, agg+".go"), Format: true},
		{Template: "templates/feature/order.go.tmpl", Out: filepath.Join("domain", agg, "order.go"), Format: true},
		{Template: "templates/feature/repository.go.tmpl", Out: filepath.Join("domain", agg, "repository.go"), Format: true},
		{Template: "templates/feature/service.go.tmpl", Out: filepath.Join("internal", "logic", p.AggSvc, "service.go"), Format: true},
		{Template: "templates/feature/storetest.go.tmpl", Out: filepath.Join("storetest", "storetest.go"), Format: true},
		{Template: "templates/feature/memstore.go.tmpl", Out: filepath.Join("memstore", "memstore.go"), Format: true},
		{Template: "templates/feature/memstore_conformance_test.go.tmpl", Out: filepath.Join("memstore", "conformance_test.go"), Format: true},
		{Template: "templates/feature/readme.md.tmpl", Out: "README.md"},

		// stores/turso.
		{Template: "templates/feature/stores/turso/go.mod.tmpl", Out: filepath.Join("stores", "turso", "go.mod")},
		{Template: "templates/feature/stores/turso/turso.go.tmpl", Out: filepath.Join("stores", "turso", "turso.go"), Format: true},
		{Template: "templates/feature/stores/turso/store.go.tmpl", Out: filepath.Join("stores", "turso", agg+".go"), Format: true},
		{Template: "templates/feature/stores/turso/migration.sql.tmpl", Out: filepath.Join("stores", "turso", "migrations", "0001_"+agg+".sql")},
		{Template: "templates/feature/stores/turso/conformance_test.go.tmpl", Out: filepath.Join("stores", "turso", "conformance_test.go"), Format: true},
		{Template: "templates/feature/stores/turso/export_test.go.tmpl", Out: filepath.Join("stores", "turso", "turso_test.go"), Format: true},
		{Template: "templates/feature/stores/turso/readme.md.tmpl", Out: filepath.Join("stores", "turso", "README.md")},

		// stores/pgx.
		{Template: "templates/feature/stores/pgx/go.mod.tmpl", Out: filepath.Join("stores", "pgx", "go.mod")},
		{Template: "templates/feature/stores/pgx/postgres.go.tmpl", Out: filepath.Join("stores", "pgx", "postgres.go"), Format: true},
		{Template: "templates/feature/stores/pgx/store.go.tmpl", Out: filepath.Join("stores", "pgx", agg+".go"), Format: true},
		{Template: "templates/feature/stores/pgx/migration.sql.tmpl", Out: filepath.Join("stores", "pgx", "migrations", "0001_"+agg+".sql")},
		{Template: "templates/feature/stores/pgx/conformance_test.go.tmpl", Out: filepath.Join("stores", "pgx", "conformance_test.go"), Format: true},
		{Template: "templates/feature/stores/pgx/export_test.go.tmpl", Out: filepath.Join("stores", "pgx", "postgres_test.go"), Format: true},
		{Template: "templates/feature/stores/pgx/readme.md.tmpl", Out: filepath.Join("stores", "pgx", "README.md")},
	}
	return emit(featureTemplates, targetDir, files, p)
}

// monorepoRoot reports whether targetDir sits inside a go.work workspace and
// returns that workspace root. v1 emits STANDALONE trees (review-gate fold item
// 5d); emitting INTO a monorepo is a named deferral, so when the target looks
// monorepo-shaped the CLI prints the manual registration checklist and continues.
func monorepoRoot(targetDir string) (string, bool) {
	dir := targetDir
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// printMonorepoChecklist emits the manual registration steps a monorepo host
// needs — the workspace + Makefile wiring the standalone emitter cannot perform.
func printMonorepoChecklist(w io.Writer, p featureParams, root, target string) {
	rel := p.Feature
	if r, err := filepath.Rel(root, target); err == nil {
		rel = filepath.ToSlash(r)
	}
	fmt.Fprintf(w, `
NOTE: the target is inside a go.work monorepo (%s). The standalone emitter does
not register modules — complete these steps by hand:

  1. go.work: add three `+"`use`"+` lines
       use ./%[2]s
       use ./%[2]s/stores/turso
       use ./%[2]s/stores/pgx
  2. Makefile MODULES: append the three module paths (%[2]s, %[2]s/stores/turso,
     %[2]s/stores/pgx).
  3. Makefile STORE_MODULES: append %[2]s/stores/pgx and %[2]s/stores/turso.
  4. Makefile test-stores: add a pgx (plain) and a turso (-tags=integration) leg
     for the two store modules.
  5. guard-feature-core-sdk-only (G5): add %[3]s to the hardcoded feature list —
     it is the one manually-extended guard.

Then drop the emitted go.mod pre-tag replace directives (go.work resolves the
sibling modules) and run `+"`make check`"+`.
`, root, rel, p.Feature)
}

// title upper-cases the first byte of a lowercase identifier. The input is
// constrained by identifierPattern, so byte-0 indexing is safe.
func title(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

func newFeatureUsage(w io.Writer) {
	fmt.Fprint(w, `gopernicus new feature — scaffold a feature module tree

Usage:
  gopernicus new feature <name> --module <root> [--aggregate <agg>] [--dir <target>]

Flags:
  --module      Go module-path ROOT the feature is emitted under (required)
  --aggregate   the aggregate (entity) name; defaults to "item"
  --dir         target directory (default: <name> under the current directory)

Emits a STANDALONE, born-conforming feature tree: an sdk-only core (the FS2
socket, the domain rim with an order allow-list, a sealed create/get/list/delete
service, a storetest conformance suite + an in-core memstore reference) plus
stores/turso and stores/pgx sibling modules. Mounts no routes — Register logs
only. See the emitted README for wiring + pre-tag instructions.
`)
}
