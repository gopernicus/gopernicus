package authorizersvc

// SchemaSnapshot is a deep-copied, read-only projection of a CompiledSchema. It
// is the ONLY policy view a caller receives: the internal compiled schema is
// never returned, so no consumer can reach its maps or race the engine. A
// snapshot shares no memory with the compiled schema it was taken from; its
// accessors return freshly copied slices, so a caller cannot mutate it into an
// inconsistent policy either.
type SchemaSnapshot struct {
	digest          string
	encodingVersion string
	resourceTypes   map[string]resourceTypeSnapshot
}

type resourceTypeSnapshot struct {
	relations   map[string]relationSnapshot
	permissions map[string][]PermissionCheck
}

type relationSnapshot struct {
	subjects     []SubjectTypeRef
	navigational bool
}

// Snapshot returns a deep-copied, read-only projection of the compiled schema.
// Mutating the returned snapshot (or any slice it hands out) cannot affect this
// compiled schema, its digest, or any decision.
func (c *CompiledSchema) Snapshot() SchemaSnapshot {
	rts := make(map[string]resourceTypeSnapshot, len(c.resourceTypes))
	for rtName, rt := range c.resourceTypes {
		rels := make(map[string]relationSnapshot, len(rt.relations))
		for relName, rel := range rt.relations {
			rels[relName] = relationSnapshot{
				subjects:     append([]SubjectTypeRef(nil), rel.subjects...),
				navigational: rel.kind == relationNavigational,
			}
		}
		perms := make(map[string][]PermissionCheck, len(rt.permissions))
		for permName, perm := range rt.permissions {
			perms[permName] = append([]PermissionCheck(nil), perm.checks...)
		}
		rts[rtName] = resourceTypeSnapshot{relations: rels, permissions: perms}
	}
	return SchemaSnapshot{
		digest:          c.digest,
		encodingVersion: SchemaEncodingVersion,
		resourceTypes:   rts,
	}
}

// Digest returns the digest of the compiled schema this snapshot projects.
func (s SchemaSnapshot) Digest() string { return s.digest }

// EncodingVersion returns the canonical encoding version the digest was computed
// under (SchemaEncodingVersion).
func (s SchemaSnapshot) EncodingVersion() string { return s.encodingVersion }

// ResourceTypes returns the sorted resource-type names in the schema.
func (s SchemaSnapshot) ResourceTypes() []string {
	return sortedMapKeys(s.resourceTypes)
}

// Relations returns the sorted relation names on a resource type, or nil if the
// type is unknown.
func (s SchemaSnapshot) Relations(resourceType string) []string {
	rt, ok := s.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return sortedMapKeys(rt.relations)
}

// Permissions returns the sorted permission names on a resource type, or nil if
// the type is unknown.
func (s SchemaSnapshot) Permissions(resourceType string) []string {
	rt, ok := s.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return sortedMapKeys(rt.permissions)
}

// AllowedSubjects returns a copy of the sorted, duplicate-free allowed subjects
// of a relation, or nil if the type or relation is unknown.
func (s SchemaSnapshot) AllowedSubjects(resourceType, relation string) []SubjectTypeRef {
	rt, ok := s.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	rel, ok := rt.relations[relation]
	if !ok {
		return nil
	}
	return append([]SubjectTypeRef(nil), rel.subjects...)
}

// RelationIsNavigational reports whether a relation is navigational (referenced
// by a Through traversal, so it carries concrete resource subjects only). ok is
// false when the type or relation is unknown.
func (s SchemaSnapshot) RelationIsNavigational(resourceType, relation string) (navigational, ok bool) {
	rt, found := s.resourceTypes[resourceType]
	if !found {
		return false, false
	}
	rel, found := rt.relations[relation]
	if !found {
		return false, false
	}
	return rel.navigational, true
}

// Checks returns a copy of the sorted, duplicate-free checks of a permission, or
// nil if the type or permission is unknown.
func (s SchemaSnapshot) Checks(resourceType, permission string) []PermissionCheck {
	rt, ok := s.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return append([]PermissionCheck(nil), rt.permissions[permission]...)
}
