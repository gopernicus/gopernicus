package commands

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gopernicus/gopernicus/workshop/codegen/generators"
	"github.com/gopernicus/gopernicus/workshop/codegen/manifest"
)

// runNewDeploy emits a deploy profile: workshop/deploy/<target>/ runbook +
// target files, plus a .github/workflows/ workflow for CI-driven targets.
// Everything is a created-once bootstrap with a drift marker; profiles
// never modify existing files.
func runNewDeploy(_ context.Context, args []string) error {
	target := firstPositional(args)
	if target == "" {
		return fmt.Errorf("deploy target required (valid: %s)\n\nUsage: gopernicus new deploy <target>",
			strings.Join(generators.DeployTargets, ", "))
	}

	root, m, err := loadProject()
	if err != nil {
		return err
	}

	projectName := filepath.Base(root)
	data := generators.DeployData{
		ProjectName:  projectName,
		AppNameUpper: appNameUpperFromManifest(m, projectName),
	}

	fmt.Printf("Emitting %s deploy profile for %s\n", target, projectName)
	if err := generators.GenerateDeployProfile(root, target, data); err != nil {
		return err
	}

	if !fileExists(filepath.Join(root, "workshop", "docker", "dockerfile."+projectName)) {
		fmt.Printf("  ! workshop/docker/dockerfile.%s not found — the profile builds that image; check the dockerfile name\n", projectName)
	}
	fmt.Printf("\nNext: read workshop/deploy/%s/README.md for one-time setup and the deploy procedure.\n", target)
	return nil
}

// appNameUpperFromManifest derives the project's env-var prefix. The
// primary database's url_env_var encodes it (<APP>_DB_DATABASE_URL on
// scaffolded projects); fall back to the project directory name.
func appNameUpperFromManifest(m *manifest.Manifest, projectName string) string {
	if db, ok := m.Databases["primary"]; ok && db != nil {
		if prefix, found := strings.CutSuffix(db.URLEnvVar, "_DB_DATABASE_URL"); found && prefix != "" {
			return prefix
		}
	}
	return generators.AppNameFromProject(projectName)
}
