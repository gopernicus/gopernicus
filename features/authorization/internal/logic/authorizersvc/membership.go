package authorizersvc

import (
	"context"
	"fmt"
)

// RemoveMember removes a subject's relationship to a resource with last-owner
// protection: if the subject is the resource's only owner, the removal is
// rejected with ErrCannotRemoveLastOwner. The owner count is a DIRECT count
// (CountByResourceAndRelation), never expanded membership — the §2.5 pin.
//
// The original's ChangeMemberRole affordance is deliberately DROPPED (Z1
// task-4, 2026-07-09): it is not part of the promoted feature surface, so
// building it would add dead public API. A role change stays delete+create at
// the call site.
func (s *Service) RemoveMember(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	isOwner, err := s.store.CheckRelationExists(ctx, resourceType, resourceID, "owner", subjectType, subjectID)
	if err != nil {
		return fmt.Errorf("check owner relation: %w", err)
	}

	if isOwner {
		count, err := s.store.CountByResourceAndRelation(ctx, resourceType, resourceID, "owner")
		if err != nil {
			return fmt.Errorf("count owners: %w", err)
		}
		if count <= 1 {
			return ErrCannotRemoveLastOwner
		}
	}

	return s.store.DeleteByResourceAndSubject(ctx, resourceType, resourceID, subjectType, subjectID)
}
