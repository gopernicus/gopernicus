package initialize

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gopernicus/gopernicus/workshop/codegen/generators"
	"github.com/gopernicus/gopernicus/workshop/codegen/goversion"
	"github.com/gopernicus/gopernicus/workshop/codegen/manifest"
)

const defaultGitignore = `# Binaries
*.exe
*.dll
*.so
*.dylib

# Go
*.test
*.out
/vendor/

# Environment
.env
.env.*
!.env.example

# Dev infrastructure data
workshop/dev/data/

# Editor
.DS_Store
.idea/
.vscode/
`

// Run bootstraps a project from fully-resolved Options: scaffold the
// directory, copy feature assets, link the framework dependency, pin the
// in-framework generator tool, tidy, and generate the feature repositories.
func Run(opts Options) error {
	target, err := scaffoldProject(opts)
	if err != nil {
		return err
	}

	// Copy feature assets (migrations, repos, bridges) from gopernicus source.
	if opts.Features.Any() {
		if err := copyFeatureAssets(target, opts.ModulePath, opts.ProjectName, opts.FrameworkVersion, opts.Features, opts.AI); err != nil {
			return err
		}
	}

	// Add gopernicus as a dependency.
	if devSource := os.Getenv("GOPERNICUS_DEV_SOURCE"); devSource != "" {
		// Dev mode: replace directive pointing to local gopernicus source.
		fmt.Printf("  → linking local gopernicus (%s)\n", devSource)
		replace := exec.Command("go", "mod", "edit",
			"-replace=github.com/gopernicus/gopernicus="+devSource,
		)
		replace.Dir = target
		replace.Stdout = os.Stdout
		replace.Stderr = os.Stderr
		if err := replace.Run(); err != nil {
			return fmt.Errorf("go mod edit -replace: %w", err)
		}
	} else {
		fwRef := "github.com/gopernicus/gopernicus@latest"
		if opts.FrameworkVersion != "" {
			fwRef = "github.com/gopernicus/gopernicus@" + opts.FrameworkVersion
		}
		fmt.Printf("  → adding gopernicus framework\n")
		goGet := exec.Command("go", "get", fwRef)
		goGet.Dir = target
		goGet.Stdout = os.Stdout
		goGet.Stderr = os.Stderr
		if err := goGet.Run(); err != nil {
			fmt.Printf("  warning: go get failed: %v\n", err)
		}
	}

	// Pin the in-framework generator tool: `go tool gopernicus` then runs the
	// exact generator that ships with the framework version the project links,
	// so emitted code and runtime can never drift.
	fmt.Printf("  → pinning the gopernicus tool\n")
	toolEdit := exec.Command("go", "mod", "edit",
		"-tool="+generators.FrameworkModulePath+"/workshop/gopernicus",
	)
	toolEdit.Dir = target
	toolEdit.Stdout = os.Stdout
	toolEdit.Stderr = os.Stderr
	if err := toolEdit.Run(); err != nil {
		return fmt.Errorf("go mod edit -tool: %w", err)
	}

	// Run go mod tidy to resolve the framework dependency and sums. The
	// scaffolded server wiring imports satisfier packages that are emitted by
	// the generate step below, so unresolvable project-internal imports are
	// tolerated here (-e); the post-generation tidy below settles the module
	// files once those packages exist.
	fmt.Printf("  → running go mod tidy\n")
	tidy := exec.Command("go", "mod", "tidy", "-e")
	tidy.Dir = target
	tidy.Stdout = os.Stdout
	tidy.Stderr = os.Stderr
	if err := tidy.Run(); err != nil {
		fmt.Printf("  warning: go mod tidy failed: %v\n", err)
	}

	// Generate the feature repositories' code, tests, fixtures, and feature
	// satisfiers from the framework-shipped specs + reflected schema, so the
	// project is complete out of the box. Best-effort: a failure here leaves
	// a valid scaffold the user can regenerate manually, so it must not fail
	// init.
	if opts.Features.Any() {
		fmt.Printf("  → generating repositories\n")
		if err := runInitGenerate(target); err != nil {
			fmt.Printf("  warning: generation skipped (%v)\n", err)
			fmt.Printf("           run 'gopernicus generate' after 'db reflect' to populate tests/fixtures\n")
		}

		// Final tidy now that generation has emitted the satisfier packages
		// the server wiring imports.
		finalTidy := exec.Command("go", "mod", "tidy")
		finalTidy.Dir = target
		finalTidy.Stdout = os.Stdout
		finalTidy.Stderr = os.Stderr
		if err := finalTidy.Run(); err != nil {
			fmt.Printf("  warning: go mod tidy failed: %v\n", err)
		}
	}

	fmt.Println()
	fmt.Printf("  ✓ created %s\n\n", opts.ProjectName)
	fmt.Printf("  cd %s\n", opts.ProjectName)
	fmt.Printf("  gopernicus doctor   # check project health\n")
	fmt.Println()

	return nil
}

func scaffoldProject(opts Options) (string, error) {
	target, err := filepath.Abs(opts.ProjectName)
	if err != nil {
		return "", err
	}

	// Refuse to overwrite an existing directory that has content.
	if entries, err := os.ReadDir(target); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("directory %q already exists and is not empty", opts.ProjectName)
	}

	// Build the manifest with features and domain mappings.
	m := manifest.NewWithProject(opts.ProjectName)
	if opts.FrameworkVersion != "" {
		m.GopernicusVersion = opts.FrameworkVersion
	}
	applyFeatureSelection(m, opts.Features)

	steps := []struct {
		desc string
		fn   func() error
	}{
		{"creating project directory", func() error {
			return os.MkdirAll(target, 0755)
		}},
		{"initializing go module", func() error {
			cmd := exec.Command("go", "mod", "init", opts.ModulePath)
			cmd.Dir = target
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return err
			}
			// Pin to the minimum supported Go version so all projects are consistent.
			pin := exec.Command("go", "mod", "edit", "-go="+goversion.MinGoVersion)
			pin.Dir = target
			return pin.Run()
		}},
		{"creating directory layout", func() error {
			dirs := []string{
				manifest.MigrationsDir("primary"),
				"core/repositories",
				"core/cases",
				"core/auth",
				"bridge/repositories",
				"bridge/cases",
				"bridge/transit",
				"infrastructure",
				"sdk",
				"workshop/dev",
				"workshop/testing/fixtures",
				"workshop/testing/e2e",
			}
			for _, d := range dirs {
				if err := os.MkdirAll(filepath.Join(target, d), 0755); err != nil {
					return err
				}
			}
			return nil
		}},
		{"writing gopernicus.yml", func() error {
			return manifest.Save(target, m)
		}},
		{"writing .gitignore", func() error {
			return os.WriteFile(
				filepath.Join(target, ".gitignore"),
				[]byte(defaultGitignore),
				0644,
			)
		}},
		{"scaffolding app server", func() error {
			hasStorage := opts.Infra.HasStorageDisk || opts.Infra.HasStorageGCS || opts.Infra.HasStorageS3
			return generators.GenerateAppScaffold(target, generators.AppScaffoldData{
				ProjectName:       opts.ProjectName,
				ModulePath:        opts.ModulePath,
				AppNameUpper:      generators.AppNameFromProject(opts.ProjectName),
				HasAuthentication: opts.Features.Authentication,
				HasAuthorization:  opts.Features.Authorization,
				HasTenancy:        opts.Features.Tenancy,
				HasOutbox:         opts.Features.EventOutbox,
				HasJobQueue:       opts.Features.JobQueue,
				HasRedis:          opts.Infra.HasRedis,
				HasRedisStreams:   opts.Infra.HasRedisStreams,
				HasStorageDisk:    opts.Infra.HasStorageDisk,
				HasStorageGCS:     opts.Infra.HasStorageGCS,
				HasStorageS3:      opts.Infra.HasStorageS3,
				HasSendGrid:       opts.Infra.HasSendGrid,
				HasTelemetry:      opts.Infra.HasTelemetry,
				HasStorage:        hasStorage,
			})
		}},
	}

	fmt.Println()
	for _, step := range steps {
		fmt.Printf("  → %s\n", step.desc)
		if err := step.fn(); err != nil {
			return "", fmt.Errorf("%s: %w", step.desc, err)
		}
	}

	return target, nil
}

// applyFeatureSelection configures the manifest based on selected features.
func applyFeatureSelection(m *manifest.Manifest, features FeatureSelection) {
	if m.Features == nil {
		m.Features = &manifest.FeaturesConfig{}
	}

	if features.Authentication {
		m.Features.Authentication = manifest.FeatureGopernicus
	} else {
		m.Features.Authentication = ""
	}

	if features.Authorization {
		m.Features.Authorization = manifest.FeatureGopernicus
	} else {
		m.Features.Authorization = ""
	}

	if features.Tenancy {
		m.Features.Tenancy = manifest.FeatureGopernicus
	} else {
		m.Features.Tenancy = ""
	}

	if features.EventOutbox || features.JobQueue {
		if m.Events == nil {
			m.Events = &manifest.EventsConfig{}
		}
		if features.EventOutbox {
			m.Events.Outbox = manifest.FeatureGopernicus
		}
		if features.JobQueue {
			m.Events.JobQueue = manifest.FeatureGopernicus
		}
	}

	// Set domain mappings for selected features.
	db := m.DatabaseOrDefault("")
	if db == nil {
		return
	}
	if db.Domains == nil {
		db.Domains = make(map[string][]string)
	}

	if features.Authentication {
		db.Domains["auth"] = []string{
			"api_keys",
			"oauth_accounts",
			"principals",
			"security_events",
			"service_accounts",
			"sessions",
			"user_passwords",
			"users",
			"verification_codes",
			"verification_tokens",
		}
	}

	if features.Authorization {
		db.Domains["rebac"] = []string{
			"groups",
			"invitations",
			"rebac_relationships",
			"rebac_relationship_metadata",
		}
	}

	if features.Tenancy {
		db.Domains["tenancy"] = []string{
			"tenants",
		}
	}

	if features.EventOutbox {
		db.Domains["events"] = []string{
			"event_outbox",
		}
	}

	if features.JobQueue {
		db.Domains["jobs"] = []string{
			"job_queue",
			"job_schedules",
		}
	}
}

// runInitGenerate runs code generation over a freshly-scaffolded project,
// using the framework-shipped feature specs + reflected schema. Kept quiet
// (no verbose per-file output) so it reads as a single init step.
// runInitGenerate runs the project's pinned generator so even init-time
// generation matches the framework version the project just pinned — the
// caller's own build is never the generator.
func runInitGenerate(target string) error {
	gen := exec.Command("go", "tool", "gopernicus", "generate")
	gen.Dir = target
	gen.Stdout = os.Stdout
	gen.Stderr = os.Stderr
	return gen.Run()
}
