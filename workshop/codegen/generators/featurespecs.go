package generators

// ShippedSpec returns the framework-shipped queries.sql spec for a feature
// entity, keyed the same way nestedBindings addresses an entity:
// domain + "/" + ToPackageName(table). Feature specs are version-locked with
// the framework — generation parses the shipped spec when the project has no
// local queries.sql for the entity, and a project-local queries.sql always
// wins (creating the file ejects that entity's spec).
func ShippedSpec(domain, pkg string) (string, bool) {
	spec, ok := featureSpecSources[domain+"/"+pkg]
	return spec, ok
}
