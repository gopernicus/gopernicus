package authorization

import (
	"context"
	"fmt"
)

// RemoveMember removes a subject's relationship to a resource with last-owner
// protection. If the subject is the only owner, the operation is rejected.
func (a *Authorizer) RemoveMember(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	// Check if subject is an owner.
	isOwner, err := a.store.CheckRelationExists(ctx, resourceType, resourceID, "owner", subjectType, subjectID)
	if err != nil {
		return fmt.Errorf("check owner relation: %w", err)
	}

	// If owner, ensure they're not the last one.
	if isOwner {
		count, err := a.store.CountByResourceAndRelation(ctx, resourceType, resourceID, "owner")
		if err != nil {
			return fmt.Errorf("count owners: %w", err)
		}
		if count <= 1 {
			return ErrCannotRemoveLastOwner
		}
	}

	return a.store.DeleteByResourceAndSubject(ctx, resourceType, resourceID, subjectType, subjectID)
}

// ChangeMemberRole changes a subject's relation on a resource.
// Guards against self-role-change and last-owner orphaning.
func (a *Authorizer) ChangeMemberRole(ctx context.Context, resourceType, resourceID, subjectType, subjectID, oldRelation, newRelation, actorID string) error {
	if subjectID == actorID {
		return ErrCannotChangeOwnRole
	}

	// If changing away from owner, ensure they're not the last one.
	if oldRelation == "owner" {
		count, err := a.store.CountByResourceAndRelation(ctx, resourceType, resourceID, "owner")
		if err != nil {
			return fmt.Errorf("count owners: %w", err)
		}
		if count <= 1 {
			return ErrCannotChangeLastOwner
		}
	}

	// Delete old, create new. ReBAC tuples are semantically immutable —
	// changing a relation means creating a fundamentally different relationship.
	if err := a.store.DeleteRelationship(ctx, resourceType, resourceID, oldRelation, subjectType, subjectID); err != nil {
		return fmt.Errorf("delete old relationship: %w", err)
	}

	return a.store.CreateRelationships(ctx, []CreateRelationship{{
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Relation:     newRelation,
		SubjectType:  subjectType,
		SubjectID:    subjectID,
	}})
}
