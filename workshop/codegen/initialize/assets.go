package initialize

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gopernicus/gopernicus/workshop/codegen/fwsource"
	"github.com/gopernicus/gopernicus/workshop/codegen/generators"
	"github.com/gopernicus/gopernicus/workshop/codegen/manifest"
)

// copyFeatureAssets copies migrations, core repositories, and bridge
// repositories from the gopernicus framework source into the new project.
// Go files have their import paths rewritten from the gopernicus module to
// the user's module path.
func copyFeatureAssets(target, modulePath, projectName, fwVersion string, features FeatureSelection, ai AICompanionSelection) error {
	source, err := gopernicusSourceDir(fwVersion)
	if err != nil {
		return fmt.Errorf("resolving gopernicus source: %w", err)
	}

	const gopernicusModule = generators.FrameworkModulePath

	// Copy migrations.
	type migration struct {
		name string
		file string
	}
	migrations := []migration{
		{"authentication", "0001_auth.sql"},
		{"authorization", "0002_rebac.sql"},
		{"tenancy", "0003_tenants.sql"},
		{"event-outbox", "0004_event_outbox.sql"},
		{"job-queue", "0005_job_queue.sql"},
		{"job-queue", "0006_job_schedules.sql"},
	}

	for _, mig := range migrations {
		if !features.EnabledFor(mig.name) {
			continue
		}

		src := filepath.Join(source, "workshop", "migrations", "primary", mig.file)
		dst := filepath.Join(target, manifest.MigrationsDir("primary"), mig.file)

		fmt.Printf("  → copying %s migration\n", mig.name)
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copying %s migration: %w", mig.name, err)
		}
	}

	// Copy the reflected schema (_public.json) so 'gopernicus generate' can
	// regenerate the feature repositories' tests/fixtures offline — without
	// the user first standing up a database and running 'db reflect'.
	for _, art := range []string{"_public.json", "_public.sql"} {
		src := filepath.Join(source, "workshop", "migrations", "primary", art)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(target, manifest.MigrationsDir("primary"), art)
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copying reflected schema %s: %w", art, err)
		}
	}

	// Copy core repositories and bridge repositories.
	type domainSource struct {
		featureName string
		domain      string // directory name under core/repositories/
		bridgeDir   string // directory name under bridge/repositories/
	}
	domains := []domainSource{
		{"authentication", "auth", "authreposbridge"},
		{"authorization", "rebac", "rebacreposbridge"},
		{"tenancy", "tenancy", "tenancyreposbridge"},
		{"event-outbox", "events", ""},
		{"job-queue", "jobs", ""},
	}

	for _, d := range domains {
		if !features.EnabledFor(d.featureName) {
			continue
		}

		// Copy core/repositories/{domain}/ — except queries.sql: feature
		// entity specs are spec-shipped (version-locked with the framework
		// and parsed by the generator directly). Creating a project-local
		// queries.sql ejects an entity's shipped spec. usersstore is the
		// framework's generator-v2 golden reference, not a project file.
		fmt.Printf("  → copying %s core repositories\n", d.featureName)
		coreSrc := filepath.Join(source, "core", "repositories", d.domain)
		coreDst := filepath.Join(target, "core", "repositories", d.domain)
		if err := copyDirRecursiveSkip(coreSrc, coreDst, "queries.sql", "usersstore"); err != nil {
			return fmt.Errorf("copying %s core repositories: %w", d.featureName, err)
		}

		// Copy bridge/repositories/{domain}reposbridge/
		if d.bridgeDir == "" {
			continue
		}
		bridgeSrc := filepath.Join(source, "bridge", "repositories", d.bridgeDir)
		if _, err := os.Stat(bridgeSrc); err == nil {
			fmt.Printf("  → copying %s bridge repositories\n", d.featureName)
			bridgeDst := filepath.Join(target, "bridge", "repositories", d.bridgeDir)
			if err := copyDirRecursive(bridgeSrc, bridgeDst); err != nil {
				return fmt.Errorf("copying %s bridge repositories: %w", d.featureName, err)
			}
		}
	}

	// Satisfiers are GENERATED into the project by 'gopernicus generate'
	// (they wrap project repo types), and the hand-written auth bridges
	// (bridge/auth/authentication, bridge/auth/invitations) are IMPORTED from
	// the framework — neither is copied anymore.

	// Copy AI companion files when Claude is selected.
	if ai.Claude {
		claudeMDSrc := filepath.Join(source, "CLAUDE.md")
		if _, err := os.Stat(claudeMDSrc); err == nil {
			fmt.Printf("  → copying CLAUDE.md\n")
			data, err := os.ReadFile(claudeMDSrc)
			if err != nil {
				return fmt.Errorf("reading CLAUDE.md: %w", err)
			}
			// Replace the placeholder with the actual project name.
			content := strings.ReplaceAll(string(data), "__PROJECT_NAME__", projectName)
			dst := filepath.Join(target, "CLAUDE.md")
			if err := os.WriteFile(dst, []byte(content), 0644); err != nil {
				return fmt.Errorf("writing CLAUDE.md: %w", err)
			}
		}

		skillsSrc := filepath.Join(source, ".claude", "skills")
		if _, err := os.Stat(skillsSrc); err == nil {
			fmt.Printf("  → copying Claude skills\n")
			skillsDst := filepath.Join(target, ".claude", "skills")
			if err := copyDirRecursive(skillsSrc, skillsDst); err != nil {
				return fmt.Errorf("copying Claude skills: %w", err)
			}
		}
	}

	// Copy framework documentation into the scaffolded project.
	// Markdown files have YAML frontmatter stripped so they read cleanly
	// outside the Docusaurus context.
	docsSrc := filepath.Join(source, "workshop", "documentation", "docs")
	if _, err := os.Stat(docsSrc); err == nil {
		fmt.Printf("  → copying gopernicus documentation\n")
		docsDst := filepath.Join(target, "workshop", "documentation", "gopernicus")
		if err := copyDirStripFrontmatter(docsSrc, docsDst); err != nil {
			return fmt.Errorf("copying documentation: %w", err)
		}
	}

	// Rewrite import paths in all copied .go files.
	if modulePath != gopernicusModule {
		fmt.Printf("  → rewriting import paths\n")
		for _, layer := range []string{"core/repositories", "bridge/repositories"} {
			dir := filepath.Join(target, layer)
			if _, err := os.Stat(dir); err != nil {
				continue
			}
			if err := rewriteImports(dir, gopernicusModule, modulePath); err != nil {
				return fmt.Errorf("rewriting imports in %s: %w", layer, err)
			}
		}
	}

	return nil
}

// gopernicusSourceDir returns the path to the gopernicus framework source.
// Uses GOPERNICUS_DEV_SOURCE if set, otherwise resolves from the Go module
// cache via `go mod download -json`. When version is non-empty it fetches
// that specific version; otherwise it fetches @latest.
func gopernicusSourceDir(version string) (string, error) {
	return fwsource.ResolveDirVersion(version)
}

// copyDirRecursive copies all files and subdirectories from src to dst.
func copyDirRecursive(src, dst string) error {
	return copyDirRecursiveSkip(src, dst)
}

// copyDirRecursiveSkip copies all files and subdirectories from src to dst,
// skipping files and directories whose base name is in skipNames.
func copyDirRecursiveSkip(src, dst string, skipNames ...string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		for _, skip := range skipNames {
			if info.Name() == skip {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		return copyFile(path, target)
	})
}

// copyDirStripFrontmatter copies all files and subdirectories from src to dst.
// For .md files, YAML frontmatter (delimited by --- lines) is stripped so the
// files read cleanly outside a Docusaurus context.
func copyDirStripFrontmatter(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}

		if strings.HasSuffix(info.Name(), ".md") {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			data = stripFrontmatter(data)
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			return os.WriteFile(target, data, 0644)
		}

		return copyFile(path, target)
	})
}

// stripFrontmatter removes YAML frontmatter delimited by --- lines from the
// start of a markdown file. Leading blank lines after removal are also trimmed.
func stripFrontmatter(data []byte) []byte {
	if !bytes.HasPrefix(data, []byte("---")) {
		return data
	}
	// Find the closing --- after the opening one.
	rest := data[3:]
	idx := bytes.Index(rest, []byte("\n---"))
	if idx == -1 {
		return data
	}
	// Skip past the closing --- and its newline.
	stripped := rest[idx+4:]
	return bytes.TrimLeft(stripped, "\n")
}

// rewriteImports replaces oldModule with newModule in all .go files under dir.
// Only rewrites imports for core/repositories and bridge/repositories paths —
// framework SDK/infrastructure imports are left pointing at gopernicus.
func rewriteImports(dir, oldModule, newModule string) error {
	oldCore := oldModule + "/core/repositories/"
	newCore := newModule + "/core/repositories/"
	oldAuthenticationSatisfiers := oldModule + "/core/auth/authentication/satisfiers"
	newAuthenticationSatisfiers := newModule + "/core/auth/authentication/satisfiers"
	oldAuthorizationSatisfiers := oldModule + "/core/auth/authorization/satisfiers"
	newAuthorizationSatisfiers := newModule + "/core/auth/authorization/satisfiers"
	oldBridge := oldModule + "/bridge/repositories/"
	newBridge := newModule + "/bridge/repositories/"

	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		content := string(data)
		updated := strings.ReplaceAll(content, oldCore, newCore)
		updated = strings.ReplaceAll(updated, oldAuthenticationSatisfiers, newAuthenticationSatisfiers)
		updated = strings.ReplaceAll(updated, oldAuthorizationSatisfiers, newAuthorizationSatisfiers)
		updated = strings.ReplaceAll(updated, oldBridge, newBridge)

		if updated != content {
			return os.WriteFile(path, []byte(updated), info.Mode())
		}
		return nil
	})
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
