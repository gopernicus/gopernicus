package decisionsvc

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

// stubDeclarer is a Declarer over an explicit "<type>/<permission>" set — the
// relationship schema's pair ownership, without the engine.
type stubDeclarer map[string]bool

func (s stubDeclarer) DeclaresPermission(resourceType, permission string) bool {
	return s[resourceType+"/"+permission]
}

// gpsModel is the reference host model: a singleton "platform" type for
// resource-type-independent permissions plus a scoped "organization" type, with
// the globally assigned "steward" listed EXPLICITLY on every permission it
// should grant.
func gpsModel() RoleModel {
	return RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"platform": {
			Roles: []string{"steward", "developer"},
			Permissions: map[string][]string{
				"steward":   {"steward"},
				"developer": {"steward", "developer"},
			},
		},
		"organization": {
			Roles: []string{"viewer", "contributor", "steward"},
			Permissions: map[string][]string{
				"view":       {"viewer", "contributor", "steward"},
				"contribute": {"contributor", "steward"},
			},
		},
	}}
}

func mustCompile(t *testing.T, model RoleModel, declared Declarer) *CompiledRoleModel {
	t.Helper()
	compiled, err := CompileRoleModel(model, declared)
	if err != nil {
		t.Fatalf("CompileRoleModel: %v", err)
	}
	return compiled
}

// =============================================================================
// Presence (rule 1)
// =============================================================================

func TestRoleModelIsSet(t *testing.T) {
	if (RoleModel{}).IsSet() {
		t.Fatal("the zero RoleModel must not report as set")
	}
	if (RoleModel{ResourceTypes: map[string]RoleTypeDef{}}).IsSet() {
		t.Fatal("an empty ResourceTypes map must not report as set")
	}
	if !gpsModel().IsSet() {
		t.Fatal("a model with resource types must report as set")
	}
}

func TestCompileRoleModelRejectsUnsetModel(t *testing.T) {
	_, err := CompileRoleModel(RoleModel{}, nil)
	if !errors.Is(err, ErrInvalidRoleModel) {
		t.Fatalf("want ErrInvalidRoleModel, got %v", err)
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("ErrInvalidRoleModel must wrap sdk.ErrInvalidInput, got %v", err)
	}
	if !strings.Contains(err.Error(), "no resource types") {
		t.Fatalf("message must name the gap, got %q", err)
	}
}

// =============================================================================
// Names (rule 2)
// =============================================================================

func TestCompileRoleModelRejectsMalformedNames(t *testing.T) {
	tests := []struct {
		name   string
		model  RoleModel
		symbol string
	}{
		{
			name: "resource type",
			model: RoleModel{ResourceTypes: map[string]RoleTypeDef{
				"": {Roles: []string{"viewer"}, Permissions: map[string][]string{"view": {"viewer"}}},
			}},
			symbol: "resource type",
		},
		{
			name: "role",
			model: RoleModel{ResourceTypes: map[string]RoleTypeDef{
				"org": {Roles: []string{"view\ner"}, Permissions: map[string][]string{"view": {"view\ner"}}},
			}},
			symbol: "role",
		},
		{
			name: "permission",
			model: RoleModel{ResourceTypes: map[string]RoleTypeDef{
				"org": {Roles: []string{"viewer"}, Permissions: map[string][]string{"": {"viewer"}}},
			}},
			symbol: "permission",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CompileRoleModel(tt.model, nil)
			if !errors.Is(err, ErrInvalidRoleModel) {
				t.Fatalf("want ErrInvalidRoleModel, got %v", err)
			}
			if !strings.Contains(err.Error(), tt.symbol) {
				t.Fatalf("message must name %q, got %q", tt.symbol, err)
			}
		})
	}
}

// =============================================================================
// Structure (rule 3)
// =============================================================================

func TestCompileRoleModelRejectsDuplicateRole(t *testing.T) {
	_, err := CompileRoleModel(RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"org": {Roles: []string{"viewer", "viewer"}, Permissions: map[string][]string{"view": {"viewer"}}},
	}}, nil)
	if !errors.Is(err, ErrInvalidRoleModel) {
		t.Fatalf("want ErrInvalidRoleModel, got %v", err)
	}
	if !strings.Contains(err.Error(), `"viewer"`) {
		t.Fatalf("message must name the duplicated role, got %q", err)
	}
}

func TestCompileRoleModelRejectsEmptyGrantorList(t *testing.T) {
	_, err := CompileRoleModel(RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"org": {Roles: []string{"viewer"}, Permissions: map[string][]string{"view": {}}},
	}}, nil)
	if !errors.Is(err, ErrInvalidRoleModel) {
		t.Fatalf("want ErrInvalidRoleModel, got %v", err)
	}
	if !strings.Contains(err.Error(), `"view"`) {
		t.Fatalf("message must name the permission, got %q", err)
	}
}

func TestCompileRoleModelRejectsUndeclaredGrantor(t *testing.T) {
	_, err := CompileRoleModel(RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"org": {Roles: []string{"viewer"}, Permissions: map[string][]string{"view": {"viewer", "editor"}}},
	}}, nil)
	if !errors.Is(err, ErrInvalidRoleModel) {
		t.Fatalf("want ErrInvalidRoleModel, got %v", err)
	}
	if !strings.Contains(err.Error(), `"editor"`) {
		t.Fatalf("message must name the undeclared grantor, got %q", err)
	}
}

func TestCompileRoleModelRejectsRoleThatGrantsNothing(t *testing.T) {
	_, err := CompileRoleModel(RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"org": {Roles: []string{"viewer", "ghost"}, Permissions: map[string][]string{"view": {"viewer"}}},
	}}, nil)
	if !errors.Is(err, ErrInvalidRoleModel) {
		t.Fatalf("want ErrInvalidRoleModel, got %v", err)
	}
	if !strings.Contains(err.Error(), `"ghost"`) {
		t.Fatalf("message must name the unused role, got %q", err)
	}
}

// =============================================================================
// Pair ownership (rule 4)
// =============================================================================

func TestCompileRoleModelRejectsPairDeclaredByBothModels(t *testing.T) {
	model := RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"project": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}},
	}}
	_, err := CompileRoleModel(model, stubDeclarer{"project/audit": true})
	if !errors.Is(err, ErrModelConflict) {
		t.Fatalf("want ErrModelConflict, got %v", err)
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("ErrModelConflict must wrap sdk.ErrInvalidInput, got %v", err)
	}
	if !strings.Contains(err.Error(), `"audit"`) || !strings.Contains(err.Error(), `"project"`) {
		t.Fatalf("message must name the conflicting pair, got %q", err)
	}
}

func TestCompileRoleModelAllowsSharedTypeWithSplitPermissions(t *testing.T) {
	// auth-cms's shape: "project" is a resource type in BOTH models —
	// project/view is relationship-owned, project/audit is role-owned. A shared
	// TYPE is legal; only a shared PAIR is not.
	model := RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"project": {Roles: []string{"auditor"}, Permissions: map[string][]string{"audit": {"auditor"}}},
	}}
	compiled := mustCompile(t, model, stubDeclarer{"project/view": true})
	if !compiled.DeclaresPermission("project", "audit") {
		t.Fatal("role model must own project/audit")
	}
	if compiled.DeclaresPermission("project", "view") {
		t.Fatal("role model must not claim the relationship-owned project/view")
	}
}

func TestCompileRoleModelNilDeclarerSkipsPairOwnership(t *testing.T) {
	// A roles-only host has no relationship schema to conflict with.
	mustCompile(t, gpsModel(), nil)
}

// =============================================================================
// Compiled artifact
// =============================================================================

func TestCompiledRoleModelDeclaresPermissionAndGrantors(t *testing.T) {
	compiled := mustCompile(t, gpsModel(), nil)

	if !compiled.DeclaresPermission("organization", "view") {
		t.Fatal("organization/view must be declared")
	}
	if compiled.DeclaresPermission("organization", "delete") {
		t.Fatal("organization/delete is not declared")
	}
	if compiled.DeclaresPermission("nope", "view") {
		t.Fatal("an unknown resource type declares nothing")
	}
	if got := compiled.grantors("organization", "delete"); got != nil {
		t.Fatalf("an undeclared pair must have nil grantors, got %v", got)
	}
	if got := compiled.grantors("nope", "view"); got != nil {
		t.Fatalf("an unknown type must have nil grantors, got %v", got)
	}
}

func TestCompiledRoleModelGrantorsAreSorted(t *testing.T) {
	// The host types the grantors in its own priority order; the compilation
	// stores them SORTED so probe order — and so the Reason and the explain
	// trace — is deterministic.
	model := RoleModel{ResourceTypes: map[string]RoleTypeDef{
		"organization": {
			Roles: []string{"viewer", "contributor", "steward"},
			Permissions: map[string][]string{
				"view": {"viewer", "contributor", "steward"},
			},
		},
	}}
	compiled := mustCompile(t, model, nil)

	want := []string{"contributor", "steward", "viewer"}
	got := compiled.grantors("organization", "view")
	if len(got) != len(want) {
		t.Fatalf("grantors = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("grantors = %v, want %v", got, want)
		}
	}
}

func TestCompiledRoleModelImmutableAfterSourceMutation(t *testing.T) {
	model := gpsModel()
	compiled := mustCompile(t, model, nil)
	before := append([]string(nil), compiled.grantors("organization", "view")...)

	// Mutate every layer of the source AFTER compilation: add a resource type,
	// add a permission, append a role, and mutate a grantor slice in place.
	model.ResourceTypes["injected"] = RoleTypeDef{
		Roles:       []string{"x"},
		Permissions: map[string][]string{"y": {"x"}},
	}
	model.ResourceTypes["organization"].Permissions["injected"] = []string{"viewer"}
	model.ResourceTypes["organization"].Permissions["view"][0] = "hacked"
	org := model.ResourceTypes["organization"]
	org.Roles = append(org.Roles, "injected")
	model.ResourceTypes["organization"] = org

	if compiled.DeclaresPermission("injected", "y") {
		t.Fatal("compiled model leaked an injected resource type")
	}
	if compiled.DeclaresPermission("organization", "injected") {
		t.Fatal("compiled model leaked an injected permission")
	}
	got := compiled.grantors("organization", "view")
	for i := range before {
		if got[i] != before[i] {
			t.Fatalf("compiled model absorbed an in-place grantor mutation: %v (was %v)", got, before)
		}
	}
}

func TestCompiledRoleModelNilReceiverIsInert(t *testing.T) {
	var compiled *CompiledRoleModel
	if compiled.DeclaresPermission("organization", "view") {
		t.Fatal("a nil compiled model declares nothing")
	}
	if got := compiled.grantors("organization", "view"); got != nil {
		t.Fatalf("a nil compiled model has nil grantors, got %v", got)
	}
}

// =============================================================================
// Assign-time declaration (D8)
// =============================================================================

// TestCompiledRoleModelDeclaresRole pins the assign-time predicate: a SCOPED
// assignment needs the role on that exact type, a GLOBAL one (empty type) needs
// it on ANY type — global scope is assignment data, not a second namespace.
func TestCompiledRoleModelDeclaresRole(t *testing.T) {
	compiled := mustCompile(t, gpsModel(), nil)
	cases := map[string]struct {
		resourceType, role string
		want               bool
	}{
		"scoped role on its own type":       {"organization", "viewer", true},
		"role shared by two types":          {"platform", "steward", true},
		"scoped role on the wrong type":     {"platform", "viewer", false},
		"unknown role":                      {"organization", "vewer", false},
		"unknown resource type":             {"comet", "viewer", false},
		"global assignment of a typed role": {"", "viewer", true},
		"global assignment of any role":     {"", "developer", true},
		"global assignment of a typo":       {"", "vewer", false},
	}
	for name, tc := range cases {
		if got := compiled.DeclaresRole(tc.resourceType, tc.role); got != tc.want {
			t.Fatalf("%s: DeclaresRole(%q, %q) = %v, want %v", name, tc.resourceType, tc.role, got, tc.want)
		}
	}

	// A nil model declares nothing (a host with no role model keeps assignments
	// opaque — the caller never builds a validator in that case).
	var none *CompiledRoleModel
	if none.DeclaresRole("organization", "viewer") || none.DeclaresRole("", "viewer") {
		t.Fatal("a nil compiled model must declare no role")
	}
}
