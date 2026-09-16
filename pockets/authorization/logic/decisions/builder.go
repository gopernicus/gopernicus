package decisions

import (
	"fmt"
	"maps"
)

// NewSchema combines resource declarations and records duplicate permission
// names as compilation errors. Compose intentional replacements first with
// MergeResourceType; independently contributed named permissions must be unique.
func NewSchema(schemaSlices ...[]ResourceSchema) Model {
	schema := Model{
		ResourceTypes: make(map[string]ResourceTypeDef),
	}

	for _, schemas := range schemaSlices {
		for _, rs := range schemas {
			if existing, ok := schema.ResourceTypes[rs.Name]; ok {
				for permission := range rs.Def.Permissions {
					if _, duplicate := existing.Permissions[permission]; duplicate {
						schema.assemblyErrors = append(schema.assemblyErrors, fmt.Sprintf("duplicate permission declaration %s.%s", rs.Name, permission))
					}
				}
				schema.ResourceTypes[rs.Name] = mergeResourceType(existing, rs.Def)
			} else {
				schema.ResourceTypes[rs.Name] = copyResourceType(rs.Def)
			}
		}
	}

	return schema
}

// MergeResourceType merges override into base, override taking precedence.
// Relations and permissions merge individually (override adds to or replaces);
// use Remove to delete a permission during the merge.
func MergeResourceType(base, override ResourceTypeDef) ResourceTypeDef {
	return mergeResourceType(base, override)
}

func mergeResourceType(base, override ResourceTypeDef) ResourceTypeDef {
	result := copyResourceType(base)
	override = copyResourceType(override)

	maps.Copy(result.Relations, override.Relations)

	for permName, permRule := range override.Permissions {
		if permRule.IsRemove() {
			delete(result.Permissions, permName)
		} else {
			result.Permissions[permName] = permRule
		}
	}

	return result
}

func copyResourceType(rt ResourceTypeDef) ResourceTypeDef {
	result := ResourceTypeDef{
		Relations:   make(map[string]RelationDef),
		Permissions: make(map[string]Expression),
	}

	for k, v := range rt.Relations {
		subjects := make([]SubjectTypeRef, len(v.AllowedSubjects))
		copy(subjects, v.AllowedSubjects)
		result.Relations[k] = RelationDef{AllowedSubjects: subjects}
	}

	for k, v := range rt.Permissions {
		result.Permissions[k] = copyExpression(v)
	}

	return result
}
