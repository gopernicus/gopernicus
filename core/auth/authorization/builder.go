package authorization

// NewSchema creates a new authorization schema from resource schemas.
//
// Each domain provides its schema via a Schema() function in its bridge layer.
// These are composed together in the app layer:
//
//	schema := authorization.NewSchema(
//	    tenancybridge.Schema(),
//	    cmsbridge.Schema(),
//	)
//	authorizer := authorization.NewAuthorizer(store, schema, cfg)
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

// MergeResourceType merges override into base, with override taking precedence.
// Both relations and permissions are merged individually (override adds to or
// replaces base). Use [Remove] to explicitly delete a permission during merge.
//
//	overrides := authorization.ResourceTypeDef{
//	    Relations: map[string]authorization.RelationDef{
//	        "super_admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
//	    },
//	    Permissions: map[string]authorization.PermissionRule{
//	        "delete": authorization.AnyOf(authorization.Direct("owner"), authorization.Direct("super_admin")),
//	        "dangerous_action": authorization.Remove(), // Remove this permission
//	    },
//	}
func MergeResourceType(base, override ResourceTypeDef) ResourceTypeDef {
	return mergeResourceType(base, override)
}

func mergeResourceType(base, override ResourceTypeDef) ResourceTypeDef {
	result := copyResourceType(base)

	// Merge relations — override adds to or replaces.
	for relName, relDef := range override.Relations {
		result.Relations[relName] = relDef
	}

	// Merge permissions — override adds to or replaces; Remove() deletes.
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
