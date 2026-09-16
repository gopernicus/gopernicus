package decisions

// ModelSnapshot is a deep-copied, read-only projection of a CompiledModel. It
// is the ONLY policy view a caller receives: the internal compiled schema is
// never returned, so no consumer can reach its maps or race the engine. A
// snapshot shares no memory with the compiled schema it was taken from; its
// accessors return freshly copied slices, so a caller cannot mutate it into an
// inconsistent policy either.
type ModelSnapshot struct {
	digest          string
	encodingVersion string
	resourceTypes   map[string]resourceTypeSnapshot
}

type resourceTypeSnapshot struct {
	relations   map[string]relationSnapshot
	permissions map[string]Expression
}

type relationSnapshot struct {
	subjects     []SubjectTypeRef
	navigational bool
}

// Snapshot returns a deep-copied, read-only projection of the compiled schema.
// Mutating the returned snapshot (or any slice it hands out) cannot affect this
// compiled schema, its digest, or any decision.
func (c *CompiledModel) Snapshot() ModelSnapshot {
	rts := make(map[string]resourceTypeSnapshot, len(c.resourceTypes))
	for rtName, rt := range c.resourceTypes {
		rels := make(map[string]relationSnapshot, len(rt.relations))
		for relName, rel := range rt.relations {
			rels[relName] = relationSnapshot{
				subjects:     append([]SubjectTypeRef(nil), rel.subjects...),
				navigational: rel.kind == relationNavigational,
			}
		}
		perms := make(map[string]Expression, len(rt.permissions))
		for permName, perm := range rt.permissions {
			perms[permName] = copyExpression(perm.expression)
		}
		rts[rtName] = resourceTypeSnapshot{relations: rels, permissions: perms}
	}
	return ModelSnapshot{
		digest:          c.digest,
		encodingVersion: ModelEncodingVersion,
		resourceTypes:   rts,
	}
}

// Digest returns the digest of the compiled schema this snapshot projects.
func (s ModelSnapshot) Digest() string { return s.digest }

// EncodingVersion returns the canonical encoding version the digest was computed
// under (ModelEncodingVersion).
func (s ModelSnapshot) EncodingVersion() string { return s.encodingVersion }

// ResourceTypes returns the sorted resource-type names in the schema.
func (s ModelSnapshot) ResourceTypes() []string {
	return sortedMapKeys(s.resourceTypes)
}

// Relations returns the sorted relation names on a resource type, or nil if the
// type is unknown.
func (s ModelSnapshot) Relations(resourceType string) []string {
	rt, ok := s.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return sortedMapKeys(rt.relations)
}

// Permissions returns the sorted permission names on a resource type, or nil if
// the type is unknown.
func (s ModelSnapshot) Permissions(resourceType string) []string {
	rt, ok := s.resourceTypes[resourceType]
	if !ok {
		return nil
	}
	return sortedMapKeys(rt.permissions)
}

// AllowedSubjects returns a copy of the sorted, duplicate-free allowed subjects
// of a relation, or nil if the type or relation is unknown.
func (s ModelSnapshot) AllowedSubjects(resourceType, relation string) []SubjectTypeRef {
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
func (s ModelSnapshot) RelationIsNavigational(resourceType, relation string) (navigational, ok bool) {
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

// Expression returns an owned copy of the complete permission expression.
func (s ModelSnapshot) Expression(resourceType, permission string) (Expression, bool) {
	rt, ok := s.resourceTypes[resourceType]
	if !ok {
		return Expression{}, false
	}
	expr, ok := rt.permissions[permission]
	return copyExpression(expr), ok
}

// Checks projects flat graph disjunctions for consumers that explicitly support
// only Direct and Through. General expressions return nil; use Expression to
// inspect the complete policy. Child order is preserved.
func (s ModelSnapshot) Checks(resourceType, permission string) []Expression {
	expr, ok := s.Expression(resourceType, permission)
	if !ok {
		return nil
	}
	return graphChecks(expr)
}
