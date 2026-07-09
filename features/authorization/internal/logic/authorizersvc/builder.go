package authorizersvc

import "maps"

// NewSchema composes resource schemas into one Schema. Each domain contributes
// its []ResourceSchema; duplicate resource-type names are merged (later slices
// override earlier ones, per MergeResourceType).
//
//	schema := authorizersvc.NewSchema(tenantSchema, projectSchema)
func NewSchema(schemaSlices ...[]ResourceSchema) Schema {
	schema := Schema{
		ResourceTypes: make(map[string]ResourceTypeDef),
	}

	for _, schemas := range schemaSlices {
		for _, rs := range schemas {
			if existing, ok := schema.ResourceTypes[rs.Name]; ok {
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
		Permissions: make(map[string]PermissionRule),
	}

	for k, v := range rt.Relations {
		subjects := make([]SubjectTypeRef, len(v.AllowedSubjects))
		copy(subjects, v.AllowedSubjects)
		result.Relations[k] = RelationDef{AllowedSubjects: subjects}
	}

	for k, v := range rt.Permissions {
		checks := make([]PermissionCheck, len(v.AnyOf))
		copy(checks, v.AnyOf)
		result.Permissions[k] = PermissionRule{AnyOf: checks}
	}

	return result
}
