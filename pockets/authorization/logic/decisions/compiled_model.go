package decisions

import "fmt"

// ValidateRelation checks a relationship is allowed by the schema against the
// full (subject type, subject relation) pair — the exact userset. subjectRelation
// is "" for a concrete subject and the userset relation otherwise. A concrete
// group is NOT accepted where only group#member is allowed, and group#admin never
// satisfies a group#member requirement: the pair must match an AllowedSubjects
// entry exactly.
func (s *Service) ValidateRelation(resourceType, relation, subjectType, subjectRelation string) error {
	subjects, resourceTypeOK, relationOK := s.compiled.relationSubjects(resourceType, relation)
	if !resourceTypeOK {
		return fmt.Errorf("unknown resource type %q: %w", resourceType, ErrInvalidRelation)
	}
	if !relationOK {
		return fmt.Errorf("unknown relation %q on %q: %w", relation, resourceType, ErrInvalidRelation)
	}

	for _, allowed := range subjects {
		if allowed.Type == subjectType && allowed.Relation == subjectRelation {
			return nil
		}
	}

	subj := subjectType
	if subjectRelation != "" {
		subj = subjectType + "#" + subjectRelation
	}
	return fmt.Errorf("subject %q not allowed for %q on %q: %w", subj, relation, resourceType, ErrInvalidRelation)
}

// ValidateRelationName reports whether a resource type and relation exist in
// the compiled schema. It is needed for an empty desired target set: there is no
// subject row through which ValidateRelation could otherwise validate the key.
func (s *Service) ValidateRelationName(resourceType, relationName string) error {
	_, resourceTypeOK, relationOK := s.compiled.relationSubjects(resourceType, relationName)
	if !resourceTypeOK {
		return fmt.Errorf("unknown resource type %q: %w", resourceType, ErrInvalidRelation)
	}
	if !relationOK {
		return fmt.Errorf("unknown relation %q on %q: %w", relationName, resourceType, ErrInvalidRelation)
	}
	return nil
}

// ValidateRelationships validates every relationship: first the structural
// shape (non-empty, bounded, UTF-8, control-char-free — CreateRelationship.Validate),
// then schema conformance. A non-empty stored userset relation is preserved, never
// erased, by the structural pass.
func (s *Service) ValidateRelationships(relationships []CreateRelationship) error {
	for _, r := range relationships {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("relationship %s:%s#%s@%s: %w",
				r.ResourceType, r.ResourceID, r.Relation, r.Subject(), err)
		}
		if err := s.ValidateRelation(r.ResourceType, r.Relation, r.SubjectType, r.SubjectRelation); err != nil {
			return fmt.Errorf("relationship %s:%s#%s@%s: %w",
				r.ResourceType, r.ResourceID, r.Relation, r.Subject(), err)
		}
	}
	return nil
}

// =============================================================================
// Model queries
// =============================================================================

// GetSchema returns a deep, read-only snapshot of the compiled schema. The
// snapshot shares no memory with the engine's compiled artifact, so a caller can
// neither reach the runtime policy maps nor race the engine.
func (s *Service) GetSchema() ModelSnapshot {
	return s.compiled.Snapshot()
}

// SchemaDigest returns the compiled schema's stable digest. Two engines built
// from semantically equal schemas report the same digest.
func (s *Service) SchemaDigest() string {
	return s.compiled.Digest()
}
