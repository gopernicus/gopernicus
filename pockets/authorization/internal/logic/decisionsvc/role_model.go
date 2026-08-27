// Package decisionsvc is the authorization pocket's COMPOSITIONS tier: the
// roles kind's permission model + decision engine, and the composite decider
// that owns the ONE decision surface across kinds. It depends downward on the
// relationship engine (authorizersvc — which also owns the shared decision
// VOCABULARY) and on the roles service — reached only through this package's
// own roleProbe port, never a rolesvc import; neither depends on it.
//
// Pair ownership is the dispatch rule: a (resource type, permission) pair is
// declared by the relationship Schema or by the RoleModel, never both
// (ErrModelConflict at construction), so the composite routes rather than
// merges.
package decisionsvc

import (
	"fmt"
	"sort"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/pockets/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/sdk"
)

var (
	// ErrInvalidRoleModel reports a RoleModel violation. It wraps
	// sdk.ErrInvalidInput and is returned at two points:
	//
	//   - at NewService, for a STRUCTURALLY invalid model — an unset model, a
	//     malformed resource type / role / permission name, a duplicate role, an
	//     empty grantor list, a grantor the type does not declare, or a declared
	//     role that grants nothing. It mirrors ErrInvalidSchema for the
	//     relationship kind; first failure wins and the message names the
	//     offending symbol.
	//   - at ROLE-ASSIGN time (D8), while a model is set, for a (resource type,
	//     role) pair the model does not declare: a scoped assignment needs the
	//     role in that type's Roles, a global one needs it declared in some type.
	ErrInvalidRoleModel = fmt.Errorf("authorization role model: %w", sdk.ErrInvalidInput)

	// ErrModelConflict reports a (resource type, permission) pair declared by BOTH
	// the relationship Schema and the RoleModel. A resource TYPE may appear in
	// both models — only a PAIR may not, because pair ownership is what makes the
	// composite a dispatch rather than a merge. It wraps sdk.ErrInvalidInput.
	ErrModelConflict = fmt.Errorf("authorization model conflict: %w", sdk.ErrInvalidInput)
)

// Declarer reports whether a model declares a permission on a resource type. The
// relationship engine (authorizersvc.Service) satisfies it, so the role model's
// pair-ownership rule is checked against the relationship schema without this
// package knowing the engine's shape. It is the SAME contract the coordinate
// gates use — one definition, aliased here.
type Declarer = authorizersvc.Declarer

// RoleModel is the roles kind's permission model: which roles exist on each
// resource type and which permissions those roles grant. Hand-typed, one
// RoleTypeDef per resource type — the same shape hosts already write for the
// relationship Schema.
//
// The model is SET when ResourceTypes is non-empty; the zero value is "no model"
// and is never validated. Global scope is assignment DATA, not a second
// permission namespace: a globally assigned role satisfies a scoped check for
// every permission whose grantor list explicitly names it (the roles service's
// scope rule), and resource-type-independent permissions are modeled as a
// singleton resource type.
type RoleModel struct {
	ResourceTypes map[string]RoleTypeDef
}

// RoleTypeDef is one resource type's roles and grants. Roles lists the roles
// assignable at (type, id); Permissions maps each permission to the roles that
// grant it.
type RoleTypeDef struct {
	Roles       []string            // roles assignable at (type, id)
	Permissions map[string][]string // permission → roles that grant it
}

// IsSet reports whether the model is configured. An unset model means the roles
// kind answers HasRole and the listings only and takes no part in the decision
// surface.
func (m RoleModel) IsSet() bool {
	return len(m.ResourceTypes) > 0
}

// compiledRoleType is one resource type's immutable compilation: its declared
// role set and its permission → SORTED grantors index.
type compiledRoleType struct {
	roles    map[string]struct{}
	grantors map[string][]string
}

// CompiledRoleModel is the immutable, validated compilation of a RoleModel. It
// shares no memory with the source model, so a later mutation of the caller's
// maps or slices cannot alter a decision. Grantor lists are SORTED, so probe
// order — and therefore the debug Reason and the explain trace — is
// deterministic.
type CompiledRoleModel struct {
	resourceTypes map[string]compiledRoleType
}

// CompileRoleModel validates a RoleModel and returns its immutable compilation.
// It applies, in order and first-failure-wins:
//
//  1. presence — an unset model is ErrInvalidRoleModel (callers check
//     RoleModel.IsSet before compiling);
//  2. names — every resource type, role, and permission name passes
//     relationship.ValidateRefField (non-empty, bounded, UTF-8, control-char
//     free — the same rule the check path applies to request fields);
//  3. structure — no duplicate role within a type; every permission's grantor
//     list is non-empty and names only roles the type declares; every declared
//     role grants at least one of that type's permissions;
//  4. pair ownership — when declared is non-nil, a (resource type, permission)
//     pair it also declares is ErrModelConflict.
//
// Passing a nil declared skips rule 4 (a roles-only host has no relationship
// schema to conflict with). The caller's RoleModel is never retained.
func CompileRoleModel(model RoleModel, declared Declarer) (*CompiledRoleModel, error) {
	if !model.IsSet() {
		return nil, fmt.Errorf("%w: model declares no resource types", ErrInvalidRoleModel)
	}

	// Iterate every level in sorted order so first-failure-wins is deterministic
	// regardless of map iteration.
	compiled := &CompiledRoleModel{resourceTypes: make(map[string]compiledRoleType, len(model.ResourceTypes))}
	for _, typeName := range sortedKeys(model.ResourceTypes) {
		def := model.ResourceTypes[typeName]
		if err := relationship.ValidateRefField("resource type", typeName); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidRoleModel, err)
		}

		roles := make(map[string]struct{}, len(def.Roles))
		for _, roleName := range def.Roles {
			if err := relationship.ValidateRefField("role", roleName); err != nil {
				return nil, fmt.Errorf("%w: resource type %q: %s", ErrInvalidRoleModel, typeName, err)
			}
			if _, dup := roles[roleName]; dup {
				return nil, fmt.Errorf("%w: resource type %q declares role %q twice", ErrInvalidRoleModel, typeName, roleName)
			}
			roles[roleName] = struct{}{}
		}

		granted := make(map[string]struct{}, len(roles))
		grantors := make(map[string][]string, len(def.Permissions))
		for _, permission := range sortedKeys(def.Permissions) {
			if err := relationship.ValidateRefField("permission", permission); err != nil {
				return nil, fmt.Errorf("%w: resource type %q: %s", ErrInvalidRoleModel, typeName, err)
			}
			names := def.Permissions[permission]
			if len(names) == 0 {
				return nil, fmt.Errorf("%w: permission %q on %q lists no granting role", ErrInvalidRoleModel, permission, typeName)
			}
			list := make([]string, 0, len(names))
			for _, roleName := range names {
				if _, ok := roles[roleName]; !ok {
					return nil, fmt.Errorf("%w: permission %q on %q is granted by role %q, which %q does not declare",
						ErrInvalidRoleModel, permission, typeName, roleName, typeName)
				}
				granted[roleName] = struct{}{}
				list = append(list, roleName)
			}
			// Sorted grantors: the probe order, and so the Reason and the explain
			// trace, must not depend on the order a host happened to type.
			sort.Strings(list)
			grantors[permission] = list
		}

		// An unused role is a typo until proven otherwise: this kind fails loud.
		for _, roleName := range def.Roles {
			if _, ok := granted[roleName]; !ok {
				return nil, fmt.Errorf("%w: role %q on %q grants no permission", ErrInvalidRoleModel, roleName, typeName)
			}
		}

		compiled.resourceTypes[typeName] = compiledRoleType{roles: roles, grantors: grantors}
	}

	// Rule 4 runs as its own pass, AFTER the model is known structurally sound:
	// a conflict is a cross-model fact, so it is never reported for a pair the
	// role model could not have declared legally in the first place.
	if declared != nil {
		for _, typeName := range sortedKeys(compiled.resourceTypes) {
			for _, permission := range sortedKeys(compiled.resourceTypes[typeName].grantors) {
				if declared.DeclaresPermission(typeName, permission) {
					return nil, fmt.Errorf("%w: permission %q on %q is declared by both the relationship schema and the role model",
						ErrModelConflict, permission, typeName)
				}
			}
		}
	}

	return compiled, nil
}

// DeclaresPermission reports whether the compiled role model declares permission
// on resourceType — the roles kind's half of the pair-ownership predicate.
func (m *CompiledRoleModel) DeclaresPermission(resourceType, permission string) bool {
	if m == nil {
		return false
	}
	rt, ok := m.resourceTypes[resourceType]
	if !ok {
		return false
	}
	_, ok = rt.grantors[permission]
	return ok
}

// DeclaresRole reports whether the model permits an assignment of roleName at a
// scope of resourceType — the assign-time half of the model (D8), separate from
// the decision-time DeclaresPermission.
//
// A GLOBAL assignment passes an empty resourceType and is declared when ANY
// resource type declares the role: global scope is assignment DATA, not a second
// permission namespace, so any modeled role may be held globally. A scoped
// assignment needs the role in that exact type's Roles.
func (m *CompiledRoleModel) DeclaresRole(resourceType, roleName string) bool {
	if m == nil {
		return false
	}
	if resourceType == "" {
		for _, typeName := range sortedKeys(m.resourceTypes) {
			if _, ok := m.resourceTypes[typeName].roles[roleName]; ok {
				return true
			}
		}
		return false
	}
	rt, ok := m.resourceTypes[resourceType]
	if !ok {
		return false
	}
	_, ok = rt.roles[roleName]
	return ok
}

// grantors returns the SORTED roles that grant permission on resourceType, or
// nil when the pair is undeclared. The returned slice is the engine's read-only
// view; it is never mutated in place.
func (m *CompiledRoleModel) grantors(resourceType, permission string) []string {
	if m == nil {
		return nil
	}
	rt, ok := m.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return rt.grantors[permission]
}

// sortedKeys returns a map's keys in ascending order, so every pass over a
// source map is deterministic.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
