package authorization

import (
	"fmt"
	"strings"
)

// SchemaValidationError contains all validation errors found in a schema.
type SchemaValidationError struct {
	Errors []string
}

func (e *SchemaValidationError) Error() string {
	return fmt.Sprintf("schema validation failed with %d error(s):\n  - %s",
		len(e.Errors), strings.Join(e.Errors, "\n  - "))
}

// ValidateSchema checks the schema for configuration errors.
// Returns nil if valid, or a *SchemaValidationError with all issues found.
//
// Validations performed:
//   - Through references point to defined relations on the resource type
//   - Permission references in through-checks exist on the target type
//   - Direct relation references exist on the resource type
//   - No circular through-relations that would cause infinite recursion
func ValidateSchema(schema Schema) error {
	var errs []string

	for resourceType, rtDef := range schema.ResourceTypes {
		for permName, permRule := range rtDef.Permissions {
			for _, check := range permRule.AnyOf {
				if check.Through != "" {
					errs = append(errs, validateThrough(schema, resourceType, permName, check, rtDef)...)
				} else if check.Relation != "" {
					// Validate direct relation exists.
					if _, ok := rtDef.Relations[check.Relation]; !ok {
						errs = append(errs,
							fmt.Sprintf("%s.%s: direct relation %q is not defined on %s",
								resourceType, permName, check.Relation, resourceType))
					}
				}
			}
		}
	}

	// Check for circular through-relations.
	errs = append(errs, detectCircularThrough(schema)...)

	if len(errs) > 0 {
		return &SchemaValidationError{Errors: errs}
	}
	return nil
}

func validateThrough(schema Schema, resourceType, permName string, check PermissionCheck, rtDef ResourceTypeDef) []string {
	var errs []string

	// Through-relation must be defined on this resource type.
	rel, ok := rtDef.Relations[check.Through]
	if !ok {
		return append(errs, fmt.Sprintf("%s.%s: through-relation %q is not defined on %s",
			resourceType, permName, check.Through, resourceType))
	}

	// Find target resource types from the relation's allowed subjects.
	targetTypes := getTargetResourceTypes(rel, schema)
	if len(targetTypes) == 0 {
		return append(errs, fmt.Sprintf("%s.%s: through-relation %q has no resource type subjects",
			resourceType, permName, check.Through))
	}

	// Permission must exist on at least one target type.
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

// getTargetResourceTypes extracts resource types from a relation's allowed subjects.
// Returns all subject types that have a corresponding resource type definition in the schema.
func getTargetResourceTypes(rel RelationDef, schema Schema) []string {
	var types []string
	for _, subject := range rel.AllowedSubjects {
		if _, hasResourceDef := schema.ResourceTypes[subject.Type]; hasResourceDef {
			types = append(types, subject.Type)
		}
	}
	return types
}

// detectCircularThrough finds circular through-relation chains that would
// cause infinite recursion during permission evaluation.
func detectCircularThrough(schema Schema) []string {
	var errs []string

	for resourceType, rtDef := range schema.ResourceTypes {
		for permName, permRule := range rtDef.Permissions {
			for _, check := range permRule.AnyOf {
				if check.Through != "" {
					visited := make(map[string]bool)
					path := []string{fmt.Sprintf("%s.%s", resourceType, permName)}

					if cycle := findCycle(schema, resourceType, check, visited, path); cycle != "" {
						errs = append(errs, cycle)
					}
				}
			}
		}
	}

	return errs
}

func findCycle(schema Schema, resourceType string, check PermissionCheck, visited map[string]bool, path []string) string {
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
				if cycle := findCycle(schema, targetType, targetCheck, visited, newPath); cycle != "" {
					return cycle
				}
			}
		}

		delete(visited, key)
	}

	return ""
}
