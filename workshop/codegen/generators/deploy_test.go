package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateDeployProfileDoApp(t *testing.T) {
	root := t.TempDir()
	data := DeployData{ProjectName: "myapp", AppNameUpper: "MYAPP"}

	if err := GenerateDeployProfile(root, "do-app", data); err != nil {
		t.Fatal(err)
	}

	wf := filepath.Join(root, ".github", "workflows", "deploy-myapp-do.yml")
	body, err := os.ReadFile(wf)
	if err != nil {
		t.Fatalf("workflow not emitted: %v", err)
	}
	s := string(body)

	// Marker first, # style, parseable, correct kind.
	first := strings.SplitN(s, "\n", 2)[0]
	kind, hash, ok := ParseBootstrapMarker(first)
	if !ok || kind != "deploy/do-app/workflow.yml" || hash == "" {
		t.Fatalf("workflow marker = %q (kind=%q ok=%v)", first, kind, ok)
	}

	// Template data substituted; GitHub Actions syntax left intact.
	for _, want := range []string{
		`tags:`, `"prod.myapp.*"`,
		"${{ github.sha }}",
		"dockerfile.myapp",
		"MYAPP_DB_DATABASE_URL: ${{ secrets.PROD_MYAPP_PG_URL }}",
		"go tool gopernicus db migrate",
		"app_name: prod-myapp",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("workflow missing %q", want)
		}
	}
	if strings.Contains(s, "[[") || strings.Contains(s, "{{.") {
		t.Error("unrendered template syntax left in workflow")
	}

	// README carries an HTML-comment marker (a # line would be a heading).
	readme, err := os.ReadFile(filepath.Join(root, "workshop", "deploy", "do-app", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	mdFirst := strings.SplitN(string(readme), "\n", 2)[0]
	if kind, _, ok := ParseBootstrapMarker(mdFirst); !ok || kind != "deploy/do-app/README.md" {
		t.Errorf("README marker = %q", mdFirst)
	}
	if !strings.HasPrefix(mdFirst, "<!-- ") || !strings.HasSuffix(mdFirst, " -->") {
		t.Errorf("README marker must be an HTML comment, got %q", mdFirst)
	}

	// Created-once: re-emitting must not touch existing files.
	if err := os.WriteFile(wf, []byte("user-owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateDeployProfile(root, "do-app", data); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(wf)
	if string(after) != "user-owned" {
		t.Error("re-emission overwrote a user-owned bootstrap")
	}
}

func TestGenerateDeployProfileCloudRun(t *testing.T) {
	root := t.TempDir()
	data := DeployData{ProjectName: "myapp", AppNameUpper: "MYAPP"}

	if err := GenerateDeployProfile(root, "cloud-run", data); err != nil {
		t.Fatal(err)
	}

	mk, err := os.ReadFile(filepath.Join(root, "workshop", "deploy", "cloud-run", "makefile.cloud-run"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(mk)
	if kind, _, ok := ParseBootstrapMarker(strings.SplitN(s, "\n", 2)[0]); !ok || kind != "deploy/cloud-run/makefile.cloud-run" {
		t.Fatalf("makefile marker bad: %q", strings.SplitN(s, "\n", 2)[0])
	}
	for _, want := range []string{
		"cloud-bootstrap:", "cloud-build:", "cloud-migrate:", "cloud-deploy:", "cloud-ship:", "cloud-url:",
		"dockerfile.myapp",
		"MYAPP_DB_DATABASE_URL",
		"--set-env-vars=MYAPP_PORT=8080",
		"$(CLOUD_RUN_IMAGE)", // make syntax intact
	} {
		if !strings.Contains(s, want) {
			t.Errorf("makefile missing %q", want)
		}
	}
	if strings.Contains(s, "[[") {
		t.Error("unrendered template syntax left in makefile")
	}
}

func TestGenerateDeployProfileUnknownTarget(t *testing.T) {
	if err := GenerateDeployProfile(t.TempDir(), "k8s", DeployData{}); err == nil {
		t.Fatal("unknown target must error")
	}
}

// The three marker comment styles all round-trip through the parser, and
// a shebang keeps line 1 of shell files.
func TestStyledMarkers(t *testing.T) {
	cases := []struct {
		filename string
		prefix   string
	}{
		{"workflow.yml", "# gopernicus:bootstrap "},
		{"README.md", "<!-- gopernicus:bootstrap "},
		{"client.ts", "// gopernicus:bootstrap "},
	}
	for _, tc := range cases {
		out := prependBootstrapMarkerStyled("deploy/do-app/workflow.yml", tc.filename, []byte("body\n"))
		first := strings.SplitN(string(out), "\n", 2)[0]
		if !strings.HasPrefix(first, tc.prefix) {
			t.Errorf("%s: marker = %q, want prefix %q", tc.filename, first, tc.prefix)
		}
		if kind, hash, ok := ParseBootstrapMarker(first); !ok || kind == "" || hash == "" {
			t.Errorf("%s: marker does not round-trip: %q", tc.filename, first)
		}
	}

	sh := prependBootstrapMarkerStyled("deploy/do-app/workflow.yml", "deploy.sh", []byte("#!/usr/bin/env bash\nset -e\n"))
	lines := strings.SplitN(string(sh), "\n", 3)
	if lines[0] != "#!/usr/bin/env bash" {
		t.Errorf("shebang displaced: %q", lines[0])
	}
	if _, _, ok := ParseBootstrapMarker(lines[1]); !ok {
		t.Errorf("marker not on line 2 of shell file: %q", lines[1])
	}
}

func TestGenerateDeployProfileComposeProd(t *testing.T) {
	root := t.TempDir()
	data := DeployData{ProjectName: "myapp", AppNameUpper: "MYAPP"}

	if err := GenerateDeployProfile(root, "compose-prod", data); err != nil {
		t.Fatal(err)
	}

	base := filepath.Join(root, "workshop", "deploy", "compose-prod")
	for _, rel := range []string{
		"compose.prod.yml", "caddy/Caddyfile", "deploy.sh", "backup.sh",
		"systemd/myapp-compose.service", "README.md",
	} {
		if _, err := os.Stat(filepath.Join(base, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	// Shell scripts: shebang on line 1, marker on line 2, executable.
	for _, sh := range []string{"deploy.sh", "backup.sh"} {
		body, err := os.ReadFile(filepath.Join(base, sh))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitN(string(body), "\n", 3)
		if !strings.HasPrefix(lines[0], "#!") {
			t.Errorf("%s: line 1 = %q, want shebang", sh, lines[0])
		}
		if _, _, ok := ParseBootstrapMarker(lines[1]); !ok {
			t.Errorf("%s: line 2 = %q, want marker", sh, lines[1])
		}
		info, _ := os.Stat(filepath.Join(base, sh))
		if info.Mode()&0o111 == 0 {
			t.Errorf("%s not executable: %v", sh, info.Mode())
		}
	}

	compose, _ := os.ReadFile(filepath.Join(base, "compose.prod.yml"))
	for _, want := range []string{
		"dockerfile.myapp",
		"MYAPP_DB_DATABASE_URL: postgres://postgres:${POSTGRES_PASSWORD}@postgres:5432/myapp",
		"go tool gopernicus db migrate",
		`profiles: ["deploy"]`,
		"BUILD_REF: ${BUILD_REF:-dev}",
	} {
		if !strings.Contains(string(compose), want) {
			t.Errorf("compose missing %q", want)
		}
	}
}
