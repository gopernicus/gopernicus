package generators

import (
	"bytes"
	"go/format"
	"strings"
	"testing"
	"text/template"

	"github.com/gopernicus/gopernicus/workshop/codegen/schema"
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

// Authenticated modes (phase B): jwt drives routes with a minted token and
// no extra wiring; session wires real user/session repos and seeds a session
// row whose hash matches the minted token. Both render gofmt-clean with the
// auth-enabled NewBridge signature and the anonymous-401 probe.
func TestBridgeE2EAuthModesRender(t *testing.T) {
	jwt := fullE2EData(false)
	jwt.AuthMode = "jwt"
	jwt.BridgeAuthEnabled = true
	jwt.CreateAuthed = true
	jwt.GetAuthed = true
	jwt.ModulePath = "github.com/x/app"

	out := renderTemplateString(t, "bridge_e2e", bridgeE2EGeneratedTemplate, jwt)
	for _, want := range []string{
		`testauth.Authenticator("e2etest")`,
		`client.SetBearerToken(testauth.MintAccessToken(signer, "e2e-test-user"))`,
		"authenticator, testauth.Authorizer())",
		"TestE2EThingRequiresAuthentication",
		"client.Anonymous()",
		"RequireStatus(t, 401)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("jwt mode: missing %q", want)
		}
	}
	if strings.Contains(out, "AuthenticatorWithRepositories") {
		t.Error("jwt mode must not wire repositories")
	}

	session := jwt
	session.AuthMode = "session"
	out = renderTemplateString(t, "bridge_e2e", bridgeE2EGeneratedTemplate, session)
	for _, want := range []string{
		"testauth.AuthenticatorWithRepositories",
		"satisfiers.NewUserSatisfier",
		"satisfiers.NewSessionSatisfier",
		"authentication.HashToken(token)",
		`"session_token_hash": tokenHash`,
		"fixtures.CreateTestUserWithDefaults",
		`"github.com/x/app/core/repositories/auth/sessions/sessionspgx"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("session mode: missing %q", want)
		}
	}

	// Auth-enabled bridge with anonymous suite routes still passes the
	// authenticator/authorizer constructor args, mints nothing.
	anon := fullE2EData(false)
	anon.BridgeAuthEnabled = true
	out = renderTemplateString(t, "bridge_e2e", bridgeE2EGeneratedTemplate, anon)
	if !strings.Contains(out, "authenticator, _ := testauth.Authenticator") {
		t.Error("auth-enabled anonymous suite must still construct the authenticator")
	}
	if strings.Contains(out, "SetBearerToken") {
		t.Error("anonymous suite must not set a credential")
	}
}

// Suite-route auth requirements drive credential selection and the
// remaining skip classes.
func TestBuildBridgeE2EAuthSelection(t *testing.T) {
	route := func(funcName, method, path, mode string, authorize bool) BridgeRoute {
		r := BridgeRoute{FuncName: funcName, Method: method, Path: path}
		if mode != "" {
			r.MiddlewareChain = append(r.MiddlewareChain, MiddlewareEntry{Authenticate: mode})
		}
		if authorize {
			r.MiddlewareChain = append(r.MiddlewareChain, MiddlewareEntry{Authorize: &AuthorizeEntry{}})
		}
		return r
	}
	base := func(routes ...BridgeRoute) BridgeTemplateData {
		return BridgeTemplateData{
			BridgePackage: "thingsbridge",
			EntityName:    "Thing",
			AuthEnabled:   true,
			CreateQueries: []BridgeCreateQuery{{FuncName: "Create", Fields: []BridgeField{{DBName: "title", GoName: "Title", IsString: true}}}},
			Routes:        routes,
		}
	}
	resolved := &ResolvedFile{
		TableName: "things",
		PKColumn:  "id",
		AllColumns: []schema.ColumnInfo{
			{Name: "id", DBType: "text", GoType: "string", IsPrimaryKey: true},
			{Name: "title", DBType: "text", GoType: "string"},
		},
	}

	cases := []struct {
		name     string
		routes   []BridgeRoute
		wantMode string
		wantSkip string
	}{
		{"anonymous", []BridgeRoute{route("Create", "POST", "/things", "", false)}, "", ""},
		{"user → jwt", []BridgeRoute{route("Create", "POST", "/things", "user", false)}, "jwt", ""},
		{"any → jwt", []BridgeRoute{route("Create", "POST", "/things", "any", false)}, "jwt", ""},
		{"user_session → session", []BridgeRoute{route("Create", "POST", "/things", "user_session", false)}, "session", ""},
		{"authorize skips", []BridgeRoute{route("Create", "POST", "/things", "user", true)}, "", "requires authorization"},
		{"service_account skips", []BridgeRoute{route("Create", "POST", "/things", "service_account", false)}, "", "API-key-wired"},
		{"mixed skips", []BridgeRoute{
			route("Create", "POST", "/things", "service_account", false),
			route("Get", "GET", "/things/{id}", "user", false),
		}, "", "mix service_account and user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e2e, skip := buildBridgeE2EData(base(tc.routes...), resolved, "github.com/x/app", "primary", false)
			if tc.wantSkip != "" {
				if !strings.Contains(skip, tc.wantSkip) {
					t.Fatalf("skip = %q, want containing %q", skip, tc.wantSkip)
				}
				return
			}
			if skip != "" {
				t.Fatalf("unexpected skip: %s", skip)
			}
			if e2e.AuthMode != tc.wantMode {
				t.Errorf("AuthMode = %q, want %q", e2e.AuthMode, tc.wantMode)
			}
			if !e2e.BridgeAuthEnabled {
				t.Error("BridgeAuthEnabled must mirror data.AuthEnabled")
			}
		})
	}

	// session + spec mode skips: the sqlite stack has no auth wiring.
	_, skip := buildBridgeE2EData(base(route("Create", "POST", "/things", "user_session", false)), resolved, "github.com/x/app", "litedb", true)
	if !strings.Contains(skip, "spec mode") {
		t.Errorf("spec-mode session skip = %q", skip)
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
