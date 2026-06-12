package generators

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

// DeployTargets lists the deploy profiles `gopernicus new deploy` can emit,
// in recommendation order.
var DeployTargets = []string{"do-app", "cloud-run", "compose-prod"}

// deployProfiles maps each target to the files it emits. Workflow files
// must live under .github/workflows/ to fire; everything else lives under
// workshop/deploy/<target>/. All files are created-once bootstraps with
// drift markers; profiles never modify existing files.
var deployProfiles = map[string][]deployFile{
	"do-app": {
		{relPath: ".github/workflows/deploy-%s-do.yml", kind: "deploy/do-app/workflow.yml", altDelims: true},
		{relPath: "workshop/deploy/do-app/app-spec.yaml", kind: "deploy/do-app/app-spec.yaml"},
		{relPath: "workshop/deploy/do-app/README.md", kind: "deploy/do-app/README.md"},
	},
	"cloud-run": {
		{relPath: "workshop/deploy/cloud-run/makefile.cloud-run", kind: "deploy/cloud-run/makefile.cloud-run", altDelims: true},
		{relPath: "workshop/deploy/cloud-run/README.md", kind: "deploy/cloud-run/README.md"},
	},
	"compose-prod": {
		{relPath: "workshop/deploy/compose-prod/compose.prod.yml", kind: "deploy/compose-prod/compose.prod.yml"},
		{relPath: "workshop/deploy/compose-prod/caddy/Caddyfile", kind: "deploy/compose-prod/Caddyfile"},
		{relPath: "workshop/deploy/compose-prod/deploy.sh", kind: "deploy/compose-prod/deploy.sh", executable: true},
		{relPath: "workshop/deploy/compose-prod/backup.sh", kind: "deploy/compose-prod/backup.sh", executable: true},
		{relPath: "workshop/deploy/compose-prod/systemd/%s-compose.service", kind: "deploy/compose-prod/systemd.service"},
		{relPath: "workshop/deploy/compose-prod/README.md", kind: "deploy/compose-prod/README.md"},
	},
}

// deployFile is one emitted profile file. relPath may carry one %s, filled
// with the project name. altDelims selects [[ ]] template delimiters for
// payloads whose syntax collides with {{ }} (GitHub Actions, make).
type deployFile struct {
	relPath    string
	kind       string
	altDelims  bool
	executable bool
}

// DeployData is the template data for deploy profile files.
type DeployData struct {
	ProjectName  string
	AppNameUpper string
}

// GenerateDeployProfile emits one deploy target's files into the project.
// Existing files are never touched — emitting is repeatable and additive.
func GenerateDeployProfile(root, target string, data DeployData) error {
	files, ok := deployProfiles[target]
	if !ok {
		return fmt.Errorf("unknown deploy target %q (valid: %v)", target, DeployTargets)
	}

	for _, f := range files {
		rel := f.relPath
		if contains := bytes.Contains([]byte(rel), []byte("%s")); contains {
			rel = fmt.Sprintf(rel, data.ProjectName)
		}
		dst := filepath.Join(root, filepath.FromSlash(rel))

		if _, err := os.Stat(dst); err == nil {
			fmt.Printf("  • %s exists, skipping (bootstrap)\n", rel)
			continue
		}

		tmplSrc := bootstrapTemplates[f.kind]
		tmpl := template.New(f.kind)
		if f.altDelims {
			tmpl = tmpl.Delims("[[", "]]")
		}
		tmpl, err := tmpl.Parse(tmplSrc)
		if err != nil {
			return fmt.Errorf("parsing deploy template %s: %w", f.kind, err)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return fmt.Errorf("rendering %s: %w", rel, err)
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", rel, err)
		}
		out := prependBootstrapMarkerStyled(f.kind, rel, buf.Bytes())
		mode := os.FileMode(0o644)
		if f.executable {
			mode = 0o755
		}
		if err := os.WriteFile(dst, out, mode); err != nil {
			return fmt.Errorf("writing %s: %w", rel, err)
		}
		fmt.Printf("  ✓ %s\n", rel)
	}
	return nil
}
