package commands

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// initTemplates holds the host-scaffold templates. They are `.tmpl` files, never
// `.go`, so the repo's whole-tree guards (G4/G9/G10) never scan template bodies.
//
//go:embed templates/init
var initTemplates embed.FS

// initParams is the identity-only input to the host templates: a module path and
// a datastore choice. No structural or per-field input — a richer input is the
// workshop-v2b codegen trigger, not this slice.
type initParams struct {
	ModulePath     string
	DB             string // none | turso | pgx
	Port           string
	HasDB          bool
	ConnectorPath  string
	ConnectorAlias string
	ConnectorRel   string
	LedgerDir      string
}

func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		modulePath = fs.String("module", "", "Go module path for the new host (required), e.g. github.com/acme/app")
		dir        = fs.String("dir", "", "target directory (default: the positional arg, else the current directory)")
		db         = fs.String("db", "none", "datastore connector to wire: turso | pgx | none")
	)
	fs.Usage = func() { initUsage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}

	if *modulePath == "" {
		fmt.Fprintln(os.Stderr, "gopernicus init: --module is required")
		initUsage(os.Stderr)
		return 1
	}

	params, err := buildInitParams(*modulePath, *db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus init: %v\n", err)
		return 1
	}

	target := *dir
	if target == "" {
		target = fs.Arg(0)
	}
	if target == "" {
		target = "."
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus init: %v\n", err)
		return 1
	}

	if err := emitInit(absTarget, params); err != nil {
		fmt.Fprintf(os.Stderr, "gopernicus init: %v\n", err)
		return 1
	}

	fmt.Printf("scaffolded %s (--db=%s) into %s\n", params.ModulePath, params.DB, absTarget)
	fmt.Println("next: add the pre-tag replace directives (see README.md), then `go mod tidy && make run`")
	return 0
}

// buildInitParams validates the datastore choice and resolves the connector
// identity (empty for --db=none).
func buildInitParams(modulePath, db string) (initParams, error) {
	p := initParams{
		ModulePath: modulePath,
		DB:         db,
		Port:       "8080",
		LedgerDir:  "primary",
	}
	switch db {
	case "none":
		// sdk-only host; no connector.
	case "turso":
		p.HasDB = true
		p.ConnectorAlias = "tursodb"
		p.ConnectorRel = "integrations/datastores/turso"
	case "pgx":
		p.HasDB = true
		p.ConnectorAlias = "pgxdb"
		p.ConnectorRel = "integrations/datastores/pgxdb"
	default:
		return initParams{}, fmt.Errorf("unknown --db %q (want: turso | pgx | none)", db)
	}
	if p.HasDB {
		p.ConnectorPath = baseModule + "/" + p.ConnectorRel
	}
	return p, nil
}

// emitInit builds the host manifest and renders it. The migration runner is
// emitted only when a datastore is wired; the ledger dir + README are always
// scaffolded so the host owns its migration tree from day one.
func emitInit(targetDir string, p initParams) error {
	files := []templateFile{
		{Template: "templates/init/go.mod.tmpl", Out: "go.mod"},
		{Template: "templates/init/main.go.tmpl", Out: filepath.Join("cmd", "server", "main.go"), Format: true},
		{Template: "templates/init/Makefile.tmpl", Out: "Makefile"},
		{Template: "templates/init/env.example.tmpl", Out: ".env.example"},
		{Template: "templates/init/readme.md.tmpl", Out: "README.md"},
		{Template: "templates/init/ledger.readme.md.tmpl", Out: filepath.Join("workshop", "migrations", p.LedgerDir, "README.md")},
	}
	if p.HasDB {
		files = append(files, templateFile{
			Template: "templates/init/migrations.main.go.tmpl",
			Out:      filepath.Join("workshop", "migrations", "main.go"),
			Format:   true,
		})
	}
	return emit(initTemplates, targetDir, files, p)
}

func initUsage(w io.Writer) {
	fmt.Fprint(w, `gopernicus init — scaffold a new host application

Usage:
  gopernicus init --module <path> [--db turso|pgx|none] [--dir <target> | <target>]

Flags:
  --module   Go module path for the new host (required)
  --db       datastore connector to wire: turso | pgx | none (default none)
  --dir      target directory (default: the positional arg, else .)

Emits an sdk-only composition root (cmd/server), a host Makefile, .env.example,
a host-owned migration ledger, and a README. Mounts no features — wire your own
in cmd/server/main.go. See the emitted README for pre-tag wiring.
`)
}
