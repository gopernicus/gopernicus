package generators

import (
	"bytes"
	"go/format"
	"strings"
	"testing"
	"text/template"
)

// fullE2EData returns BridgeE2EData with every optional probe enabled, so a
// render exercises all conditional blocks (imports included) at once.
func fullE2EData(specMode bool) BridgeE2EData {
	return BridgeE2EData{
		BridgePackage:      "thingsbridge",
		EntityName:         "Thing",
		SpecMode:           specMode,
		RepoPkg:            "things",
		RepoImport:         "github.com/x/app/core/repositories/app/things",
		StorePkg:           "thingsstore",
		StoreImport:        "github.com/x/app/core/repositories/app/things/thingsstore",
		MigrationsDir:      "workshop/migrations/primary",
		FixturePkg:         "fixtures",
		FixtureImport:      "github.com/x/app/workshop/testing/fixtures",
		FKSeeds:            []FKSeed{{RequestField: "AccountID", ParentEntity: "Account", ParentPKExpr: "parent0.AccountID"}},
		PKJSON:             "thing_id",
		NotFoundID:         "nonexistent-e2e-id",
		CreatePath:         "/things",
		CreateMaxBodySize:  4096,
		HasGet:             true,
		GetPathExpr:        `"/things/" + id`,
		HasList:            true,
		ListPath:           "/things",
		HasDelete:          true,
		DeletePathExpr:     `"/things/" + id`,
		HasRecordState:     true,
		HasUpdate:          true,
		UpdatePathExpr:     `"/things/" + id`,
		UpdateLegitJSON:    "name",
		UpdateLegitValue:   `"edited-value"`,
		StringFilterParams: []string{"name", "search"},
		OtherProbeParams:   []string{"count"},
	}
}

// minimalE2EData returns BridgeE2EData with every optional probe disabled, so
// the render exercises the opposite side of each conditional block.
func minimalE2EData(specMode bool) BridgeE2EData {
	return BridgeE2EData{
		BridgePackage: "thingsbridge",
		EntityName:    "Thing",
		SpecMode:      specMode,
		RepoPkg:       "things",
		RepoImport:    "github.com/x/app/core/repositories/app/things",
		StorePkg:      "thingsstore",
		StoreImport:   "github.com/x/app/core/repositories/app/things/thingsstore",
		MigrationsDir: "workshop/migrations/primary",
		PKJSON:        "thing_id",
		NotFoundID:    "nonexistent-e2e-id",
		CreatePath:    "/things",
	}
}

func renderTemplateString(t *testing.T, name, tmplText string, data any) string {
	t.Helper()
	tmpl, err := template.New(name).Parse(tmplText)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	if _, err := format.Source(buf.Bytes()); err != nil {
		t.Fatalf("%s output is not gofmt-clean: %v\n--- output ---\n%s", name, err, buf.String())
	}
	return buf.String()
}

// TestBridgeE2ETemplatesRenderGofmtClean renders the e2e templates in every
// mode/flag combination and pins that the output formats — and that the
// shared stack wiring comes from the framework helpers instead of inline
// boilerplate.
func TestBridgeE2ETemplatesRenderGofmtClean(t *testing.T) {
	for _, specMode := range []bool{true, false} {
		for _, full := range []bool{true, false} {
			data := minimalE2EData(specMode)
			if full {
				data = fullE2EData(specMode)
			}
			out := renderTemplateString(t, "bridge_e2e", bridgeE2EGeneratedTemplate, data)
			for _, want := range []string{
				"testserver.ServeBridge(t, bridge)",
				"testserver.NewRateLimiter()",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("specMode=%v full=%v: generated e2e test missing %q", specMode, full, want)
				}
			}
			for _, banned := range []string{"httptest.NewServer", "memorylimiter", "web.NewWebHandler"} {
				if strings.Contains(out, banned) {
					t.Errorf("specMode=%v full=%v: generated e2e test still inlines %q", specMode, full, banned)
				}
			}

			boot := renderTemplateString(t, "bridge_e2e_bootstrap", bridgeE2EBootstrapTemplate, data)
			if !strings.Contains(boot, "testenv.ProjectRoot()") {
				t.Errorf("specMode=%v: e2e bootstrap must resolve the root via testenv.ProjectRoot", specMode)
			}
			if strings.Contains(boot, "runtime.Caller") {
				t.Errorf("specMode=%v: e2e bootstrap still inlines the go.mod walk", specMode)
			}
		}
	}
}

// TestBridgeSecurityBootstrapRendersGofmtClean covers the security bootstrap
// for both store modes (the generation-level test only exercises pgx).
func TestBridgeSecurityBootstrapRendersGofmtClean(t *testing.T) {
	for _, specMode := range []bool{true, false} {
		sec := BridgeSecurityData{
			BridgePackage: "thingsbridge",
			EntityName:    "Thing",
			SpecMode:      specMode,
			RepoPkg:       "things",
			RepoImport:    "github.com/x/app/core/repositories/app/things",
			StorePkg:      "thingsstore",
			StoreImport:   "github.com/x/app/core/repositories/app/things/thingsstore",
			MigrationsDir: "workshop/migrations/primary",
			Routes:        []SecurityRoute{{Name: "Get", Call: `client.Get(t, "/things/probe-id")`}},
		}
		boot := renderTemplateString(t, "bridge_security_bootstrap", bridgeSecurityBootstrapTemplate, sec)
		for _, want := range []string{
			"testserver.ServeBridge(t, bridge)",
			"testserver.NewRateLimiter()",
			"testenv.ProjectRoot()",
		} {
			if !strings.Contains(boot, want) {
				t.Errorf("specMode=%v: security bootstrap missing %q", specMode, want)
			}
		}
		for _, banned := range []string{"httptest.NewServer", "memorylimiter", "web.NewWebHandler", "runtime.Caller"} {
			if strings.Contains(boot, banned) {
				t.Errorf("specMode=%v: security bootstrap still inlines %q", specMode, banned)
			}
		}
		renderTemplateString(t, "bridge_security", bridgeSecurityGeneratedTemplate, sec)
	}
}

// TestIntegrationBootstrapRendersGofmtClean pins both store modes of the
// integration bootstrap after the projectRoot extraction.
func TestIntegrationBootstrapRendersGofmtClean(t *testing.T) {
	for _, specMode := range []bool{true, false} {
		data := map[string]any{
			"EntityName":    "Thing",
			"StorePkg":      "thingsstore",
			"SpecMode":      specMode,
			"MigrationsDir": "workshop/migrations/primary",
		}
		boot := renderTemplateString(t, "integration_bootstrap", integrationTestBootstrapTemplate, data)
		if !strings.Contains(boot, "testenv.ProjectRoot()") {
			t.Errorf("specMode=%v: integration bootstrap must resolve the root via testenv.ProjectRoot", specMode)
		}
		if strings.Contains(boot, "runtime.Caller") {
			t.Errorf("specMode=%v: integration bootstrap still inlines the go.mod walk", specMode)
		}
	}
}
