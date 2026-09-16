package decisions

import (
	"fmt"
	"sort"
	"strings"
)

// SchemaValidationError aggregates structural errors, including the first cycle witness.
type SchemaValidationError struct {
	Errors []string
}

func (e *SchemaValidationError) Error() string {
	return fmt.Sprintf("schema validation failed with %d error(s):\n  - %s",
		len(e.Errors), strings.Join(e.Errors, "\n  - "))
}

// ValidateSchema is the shared structural validator over a source Model; it is
// NOT the construction boot gate — NewService compiles through Compile, the sole
// path that produces a Service (Compile is a strict superset that also deep-copies
// into an immutable artifact and adds a digest). ValidateSchema is retained for
// the focused cycle/unsatisfiable/self-hierarchy coverage below and shares its
// Through/cycle helpers with Compile. It returns nil when valid or a
// *SchemaValidationError listing structural issues and the first cycle witness. It verifies:
//   - Through references point to defined relations on the resource type;
//   - the permission named by a through-check exists on EVERY possible resource
//     target of that relation (a mixed-target rule missing it on some target is
//     rejected);
//   - direct relation references exist on the resource type;
//   - no circular through-relation would cause infinite recursion, except a
//     direct self-loop on the same permission (a self-referential hierarchy
//     like space.view = Through("parent","view")), which terminates at runtime
//     and is permitted;
//   - no permission rule is unsatisfiable — composed only of self-loops that
//     never bottom out on a concrete grant.
func ValidateSchema(schema Model) error {
	var errs []string

	for resourceType, rtDef := range schema.ResourceTypes {
		for permName, permRule := range rtDef.Permissions {
			for _, check := range expressionLeaves(permRule) {
				if check.Through != "" {
					errs = append(errs, validateThrough(schema, resourceType, permName, check, rtDef)...)
				} else if check.Relation != "" {
					if _, ok := rtDef.Relations[check.Relation]; !ok {
						errs = append(errs,
							fmt.Sprintf("%s.%s: direct relation %q is not defined on %s",
								resourceType, permName, check.Relation, resourceType))
					}
				}
			}
		}
	}

	errs = append(errs, detectCircularThrough(schema)...)
	errs = append(errs, detectUnsatisfiable(schema)...)

	// The passes above range mutable source maps, so their append order follows
	// map iteration. Sort and de-duplicate so equivalent input always reports the
	// same ordered error list — matching Compile's deterministic aggregation.
	errs = dedupeSortStrings(errs)
	if len(errs) > 0 {
		return &SchemaValidationError{Errors: errs}
	}
	return nil
}

func validateThrough(schema Model, resourceType, permName string, check Expression, rtDef ResourceTypeDef) []string {
	var errs []string

	rel, ok := rtDef.Relations[check.Through]
	if !ok {
		return append(errs, fmt.Sprintf("%s.%s: through-relation %q is not defined on %s",
			resourceType, permName, check.Through, resourceType))
	}

	targetTypes := getTargetResourceTypes(rel, schema)
	if len(targetTypes) == 0 {
		return append(errs, fmt.Sprintf("%s.%s: through-relation %q has no resource type subjects",
			resourceType, permName, check.Through))
	}

	// A Through traversal may land on ANY of the relation's possible resource
	// targets at runtime, so the permission must exist on EVERY one of them — a
	// permission present on only some targets is an ambiguous mixed-target rule
	// that silently denies on the missing branch. Report the specific gaps.
	var missing []string
	for _, targetType := range targetTypes {
		targetDef, ok := schema.ResourceTypes[targetType]
		if !ok {
			missing = append(missing, targetType)
			continue
		}
		if _, ok := targetDef.Permissions[check.Permission]; !ok {
			missing = append(missing, targetType)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		errs = append(errs,
			fmt.Sprintf("%s.%s: through(%s).%s - permission %q not found on target type(s) %v",
				resourceType, permName, check.Through, check.Permission,
				check.Permission, missing))
	}

	return errs
}

// getTargetResourceTypes returns the subject types of a relation that are
// themselves defined resource types in the schema.
func getTargetResourceTypes(rel RelationDef, schema Model) []string {
	var types []string
	for _, subject := range rel.AllowedSubjects {
		if _, hasResourceDef := schema.ResourceTypes[subject.Type]; hasResourceDef {
			types = append(types, subject.Type)
		}
	}
	return dedupeSortStrings(types)
}

type permissionKey struct {
	resourceType string
	permission   string
}

// detectCircularThrough reports the first deterministic cycle witness. Each
// permission and edge is visited once after the adjacency lists are sorted;
// completed suffixes are shared instead of retraversed for every incoming path.
func detectCircularThrough(schema Model) []string {
	nodes, edges := throughGraph(schema)
	cycle := findThroughCycle(nodes, func(key permissionKey) []permissionKey { return edges[key] })
	if len(cycle) == 0 {
		return nil
	}
	labels := make([]string, len(cycle))
	for i, key := range cycle {
		labels[i] = key.resourceType + "." + key.permission
	}
	return []string{"circular through-relation detected: " + strings.Join(labels, " -> ")}
}

// throughGraph canonicalizes the distinct permission dependencies. The allowed
// same-permission self hierarchy is omitted: runtime depth bounds handle it,
// and detectUnsatisfiable rejects a rule with no concrete way to grant access.
func throughGraph(schema Model) ([]permissionKey, map[permissionKey][]permissionKey) {
	var nodes []permissionKey
	edges := make(map[permissionKey][]permissionKey)
	for resourceType, def := range schema.ResourceTypes {
		for permission, rule := range def.Permissions {
			key := permissionKey{resourceType, permission}
			nodes = append(nodes, key)
			seen := make(map[permissionKey]bool)
			for _, check := range expressionLeaves(rule) {
				if check.NamedPermission != "" {
					target := permissionKey{resourceType, check.NamedPermission}
					if !seen[target] {
						edges[key] = append(edges[key], target)
						seen[target] = true
					}
					continue
				}
				if check.Through == "" {
					continue
				}
				for _, targetType := range getTargetResourceTypes(def.Relations[check.Through], schema) {
					target := permissionKey{targetType, check.Permission}
					if target == key || seen[target] {
						continue
					}
					if _, exists := schema.ResourceTypes[targetType].Permissions[check.Permission]; !exists {
						continue // the structural reference pass reports this error
					}
					seen[target] = true
					edges[key] = append(edges[key], target)
				}
			}
			sortPermissionKeys(edges[key])
		}
	}
	sortPermissionKeys(nodes)
	return nodes, edges
}

func sortPermissionKeys(keys []permissionKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].resourceType != keys[j].resourceType {
			return keys[i].resourceType < keys[j].resourceType
		}
		return keys[i].permission < keys[j].permission
	})
}

// findThroughCycle expands each vertex at most once. Active vertices detect a
// back edge; completed vertices avoid repeating work on convergent DAG paths.
func findThroughCycle(nodes []permissionKey, edges func(permissionKey) []permissionKey) []permissionKey {
	const (
		active = 1
		done   = 2
	)
	state := make(map[permissionKey]int)
	var path []permissionKey
	var visit func(permissionKey) []permissionKey
	visit = func(key permissionKey) []permissionKey {
		switch state[key] {
		case active:
			return append(append([]permissionKey(nil), path...), key)
		case done:
			return nil
		}
		state[key] = active
		path = append(path, key)
		for _, target := range edges(key) {
			if cycle := visit(target); cycle != nil {
				return cycle
			}
		}
		path = path[:len(path)-1]
		state[key] = done
		return nil
	}
	for _, key := range nodes {
		if cycle := visit(key); cycle != nil {
			return cycle
		}
	}
	return nil
}

// detectUnsatisfiable flags permission rules that terminate but can never
// evaluate true: every check is a through-relation that loops back to the same
// permission on the same resource type, so no grant ever bottoms out on a
// concrete relation. The cycle pass sanctions this self-loop shape because it
// terminates, so this pass rejects the always-false rule it would otherwise admit.
func detectUnsatisfiable(schema Model) []string {
	var errs []string
	var possible func(string, string, Expression, map[permissionKey]bool) bool
	possible = func(rt, p string, e Expression, stack map[permissionKey]bool) bool {
		if e.AnyOf != nil {
			for _, child := range e.AnyOf {
				if possible(rt, p, child, stack) {
					return true
				}
			}
			return false
		}
		if e.AllOf != nil {
			if len(e.AllOf) == 0 {
				return false
			}
			for _, child := range e.AllOf {
				if !possible(rt, p, child, stack) {
					return false
				}
			}
			return true
		}
		if e.RoleName != "" || e.Relation != "" {
			return true
		}
		targets := []string{rt}
		name := e.NamedPermission
		if e.Through != "" {
			targets = getTargetResourceTypes(schema.ResourceTypes[rt].Relations[e.Through], schema)
			name = e.Permission
		}
		for _, target := range targets {
			key := permissionKey{target, name}
			if stack[key] {
				continue
			}
			definition, ok := schema.ResourceTypes[target].Permissions[name]
			if !ok {
				continue
			}
			stack[key] = true
			ok = possible(target, name, definition, stack)
			delete(stack, key)
			if ok {
				return true
			}
		}
		return false
	}
	for rt, def := range schema.ResourceTypes {
		for p, e := range def.Permissions {
			if !possible(rt, p, e, map[permissionKey]bool{{rt, p}: true}) {
				errs = append(errs, fmt.Sprintf("unsatisfiable through-relation detected: %s.%s has no terminating grant", rt, p))
			}
		}
	}
	return errs
}
