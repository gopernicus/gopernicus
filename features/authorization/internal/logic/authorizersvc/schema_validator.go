package authorizersvc

import (
	"fmt"
	"strings"
)

// SchemaValidationError aggregates every structural error found in a schema.
type SchemaValidationError struct {
	Errors []string
}

func (e *SchemaValidationError) Error() string {
	return fmt.Sprintf("schema validation failed with %d error(s):\n  - %s",
		len(e.Errors), strings.Join(e.Errors, "\n  - "))
}

// ValidateSchema checks the schema for configuration errors, returning nil when
// valid or a *SchemaValidationError listing every issue. It verifies:
//   - Through references point to defined relations on the resource type;
//   - the permission named by a through-check exists on the target type;
//   - direct relation references exist on the resource type;
//   - no circular through-relation would cause infinite recursion, except a
//     direct self-loop on the same permission (a self-referential hierarchy
//     like space.view = Through("parent","view")), which terminates at runtime
//     and is permitted;
//   - no permission rule is unsatisfiable — composed only of self-loops that
//     never bottom out on a concrete grant.
func ValidateSchema(schema Schema) error {
	var errs []string

	for resourceType, rtDef := range schema.ResourceTypes {
		for permName, permRule := range rtDef.Permissions {
			for _, check := range permRule.AnyOf {
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

	if len(errs) > 0 {
		return &SchemaValidationError{Errors: errs}
	}
	return nil
}

func validateThrough(schema Schema, resourceType, permName string, check PermissionCheck, rtDef ResourceTypeDef) []string {
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

	permFound := false
	for _, targetType := range targetTypes {
		if targetDef, ok := schema.ResourceTypes[targetType]; ok {
			if _, ok := targetDef.Permissions[check.Permission]; ok {
				permFound = true
				break
			}
		}
	}
	if !permFound {
		errs = append(errs,
			fmt.Sprintf("%s.%s: through(%s).%s - permission %q not found on target type(s) %v",
				resourceType, permName, check.Through, check.Permission,
				check.Permission, targetTypes))
	}

	return errs
}

// getTargetResourceTypes returns the subject types of a relation that are
// themselves defined resource types in the schema.
func getTargetResourceTypes(rel RelationDef, schema Schema) []string {
	var types []string
	for _, subject := range rel.AllowedSubjects {
		if _, hasResourceDef := schema.ResourceTypes[subject.Type]; hasResourceDef {
			types = append(types, subject.Type)
		}
	}
	return types
}

// detectCircularThrough finds through-relation chains that would recurse
// forever during evaluation.
func detectCircularThrough(schema Schema) []string {
	var errs []string

	for resourceType, rtDef := range schema.ResourceTypes {
		for permName, permRule := range rtDef.Permissions {
			for _, check := range permRule.AnyOf {
				if check.Through != "" {
					visited := make(map[string]bool)
					path := []string{fmt.Sprintf("%s.%s", resourceType, permName)}

					if cycle := findCycle(schema, resourceType, check, visited, path, permName); cycle != "" {
						errs = append(errs, cycle)
					}
				}
			}
		}
	}

	return errs
}

// findCycle walks through-relations depth-first. sourcePerm carries the
// permission of the outer frame so the self-loop sanction below can tell an
// intentional self-referential hierarchy apart from a cross-permission cycle.
func findCycle(schema Schema, resourceType string, check PermissionCheck, visited map[string]bool, path []string, sourcePerm string) string {
	if check.Through == "" {
		return ""
	}

	rtDef, ok := schema.ResourceTypes[resourceType]
	if !ok {
		return ""
	}

	rel, ok := rtDef.Relations[check.Through]
	if !ok {
		return ""
	}

	for _, targetType := range getTargetResourceTypes(rel, schema) {
		// A direct self-loop on the same permission (the canonical
		// space.view = Through("parent","view") hierarchy) terminates at
		// runtime: Check bounds it by MaxTraversalDepth, Lookup expands
		// descendants with a cycle-safe store walk. Sanction it without
		// marking the key visited or recursing so the validator itself
		// terminates. A rule composed ONLY of such self-loops never bottoms
		// out and is caught by detectUnsatisfiable instead.
		if targetType == resourceType && check.Permission == sourcePerm {
			continue
		}

		key := fmt.Sprintf("%s.%s", targetType, check.Permission)

		if visited[key] {
			return fmt.Sprintf("circular through-relation detected: %s -> %s",
				strings.Join(path, " -> "), key)
		}

		targetDef, ok := schema.ResourceTypes[targetType]
		if !ok {
			continue
		}

		targetPerm, ok := targetDef.Permissions[check.Permission]
		if !ok {
			continue
		}

		visited[key] = true
		newPath := append(path, key)

		for _, targetCheck := range targetPerm.AnyOf {
			if targetCheck.Through != "" {
				if cycle := findCycle(schema, targetType, targetCheck, visited, newPath, check.Permission); cycle != "" {
					return cycle
				}
			}
		}

		delete(visited, key)
	}

	return ""
}

// detectUnsatisfiable flags permission rules that terminate but can never
// evaluate true: every check is a through-relation that loops back to the same
// permission on the same resource type, so no grant ever bottoms out on a
// concrete relation. findCycle deliberately sanctions this self-loop shape (it
// terminates), so this pass rejects the always-false rule findCycle would
// otherwise admit.
func detectUnsatisfiable(schema Schema) []string {
	var errs []string

	for resourceType, rtDef := range schema.ResourceTypes {
		for permName, permRule := range rtDef.Permissions {
			if len(permRule.AnyOf) == 0 {
				continue
			}

			allSelfLoops := true
			for _, check := range permRule.AnyOf {
				if !isSelfLoop(schema, resourceType, permName, rtDef, check) {
					allSelfLoops = false
					break
				}
			}

			if allSelfLoops {
				errs = append(errs, fmt.Sprintf(
					"unsatisfiable through-relation detected: %s.%s only traverses back to itself and can never grant access",
					resourceType, permName))
			}
		}
	}

	return errs
}

// isSelfLoop reports whether check is a through-relation whose every target type
// is the resource type itself and whose checked permission is permName — the
// exact shape findCycle sanctions. A through with any non-self target, or that
// checks a different permission, can bottom out elsewhere and is not a self-loop.
func isSelfLoop(schema Schema, resourceType, permName string, rtDef ResourceTypeDef, check PermissionCheck) bool {
	if check.Through == "" || check.Permission != permName {
		return false
	}

	rel, ok := rtDef.Relations[check.Through]
	if !ok {
		return false
	}

	targetTypes := getTargetResourceTypes(rel, schema)
	if len(targetTypes) == 0 {
		return false
	}

	for _, targetType := range targetTypes {
		if targetType != resourceType {
			return false
		}
	}
	return true
}
