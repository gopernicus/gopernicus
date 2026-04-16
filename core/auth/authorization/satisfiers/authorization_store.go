package satisfiers

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/core/auth/authorization"
	"github.com/gopernicus/gopernicus/core/repositories/rebac/rebacrelationships"
	"github.com/gopernicus/gopernicus/infrastructure/cryptids"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

type relationshipRepo interface {
	CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)
	GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]rebacrelationships.RebacRelationship, error)
	CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error)
	CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error)
	BulkCreate(ctx context.Context, inputs []rebacrelationships.CreateRebacRelationship) ([]rebacrelationships.RebacRelationship, error)
	DeleteAllForResource(ctx context.Context, resourceType, resourceID string) error
	DeleteByTuple(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error
	DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error
	CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error)
	ListBySubject(ctx context.Context, filter rebacrelationships.FilterListBySubject, subjectType, subjectID string, orderBy fop.Order, page fop.PageStringCursor) ([]rebacrelationships.RebacRelationship, fop.Pagination, error)
	ListByResource(ctx context.Context, filter rebacrelationships.FilterListByResource, resourceType, resourceID string, orderBy fop.Order, page fop.PageStringCursor) ([]rebacrelationships.RebacRelationship, fop.Pagination, error)
	LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error)
	LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error)
	LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error)
}

// AuthorizationStoreSatisfier satisfies authorization.Storer using the generated
// rebac_relationships repository.
type AuthorizationStoreSatisfier struct {
	repo relationshipRepo
}

func NewAuthorizationStoreSatisfier(repo relationshipRepo) *AuthorizationStoreSatisfier {
	return &AuthorizationStoreSatisfier{repo: repo}
}

var _ authorization.Storer = (*AuthorizationStoreSatisfier)(nil)

// =============================================================================
// Permission Checks
// =============================================================================

func (s *AuthorizationStoreSatisfier) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return s.repo.CheckRelationWithGroupExpansion(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

func (s *AuthorizationStoreSatisfier) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]authorization.RelationTarget, error) {
	rels, err := s.repo.GetRelationTargets(ctx, resourceType, resourceID, relation)
	if err != nil {
		return nil, err
	}

	targets := make([]authorization.RelationTarget, len(rels))
	for i, r := range rels {
		targets[i] = authorization.RelationTarget{
			SubjectType:     r.SubjectType,
			SubjectID:       r.SubjectID,
			SubjectRelation: r.SubjectRelation,
		}
	}
	return targets, nil
}

func (s *AuthorizationStoreSatisfier) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return s.repo.CheckRelationExists(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

func (s *AuthorizationStoreSatisfier) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	return s.repo.CheckBatchDirect(ctx, resourceType, resourceIDs, relation, subjectType, subjectID)
}

// =============================================================================
// Relationship CRUD
// =============================================================================

func (s *AuthorizationStoreSatisfier) CreateRelationships(ctx context.Context, relationships []authorization.CreateRelationship) error {
	if len(relationships) == 0 {
		return nil
	}

	inputs := make([]rebacrelationships.CreateRebacRelationship, len(relationships))
	for i, r := range relationships {
		id, err := cryptids.GenerateID()
		if err != nil {
			return fmt.Errorf("generate relationship id: %w", err)
		}
		inputs[i] = rebacrelationships.CreateRebacRelationship{
			RelationshipID:  id,
			ResourceType:    r.ResourceType,
			ResourceID:      r.ResourceID,
			Relation:        r.Relation,
			SubjectType:     r.SubjectType,
			SubjectID:       r.SubjectID,
			SubjectRelation: r.SubjectRelation,
		}
	}

	_, err := s.repo.BulkCreate(ctx, inputs)
	return err
}

func (s *AuthorizationStoreSatisfier) DeleteResourceRelationships(ctx context.Context, resourceType, resourceID string) error {
	return s.repo.DeleteAllForResource(ctx, resourceType, resourceID)
}

func (s *AuthorizationStoreSatisfier) DeleteRelationship(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return s.repo.DeleteByTuple(ctx, resourceType, resourceID, relation, subjectType, subjectID)
}

func (s *AuthorizationStoreSatisfier) DeleteByResourceAndSubject(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	return s.repo.DeleteByResourceAndSubject(ctx, resourceType, resourceID, subjectType, subjectID)
}

// =============================================================================
// Counts
// =============================================================================

func (s *AuthorizationStoreSatisfier) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	return s.repo.CountByResourceAndRelation(ctx, resourceType, resourceID, relation)
}

// =============================================================================
// Listing
// =============================================================================

func (s *AuthorizationStoreSatisfier) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter authorization.SubjectRelationshipFilter, orderBy fop.Order, page fop.PageStringCursor) ([]authorization.SubjectRelationship, fop.Pagination, error) {
	repoFilter := rebacrelationships.FilterListBySubject{
		ResourceType: filter.ResourceType,
		Relation:     filter.Relation,
	}

	rels, pagination, err := s.repo.ListBySubject(ctx, repoFilter, subjectType, subjectID, orderBy, page)
	if err != nil {
		return nil, fop.Pagination{}, err
	}

	result := make([]authorization.SubjectRelationship, len(rels))
	for i, r := range rels {
		result[i] = authorization.SubjectRelationship{
			ResourceType: r.ResourceType,
			ResourceID:   r.ResourceID,
			Relation:     r.Relation,
			CreatedAt:    r.CreatedAt,
		}
	}

	return result, pagination, nil
}

func (s *AuthorizationStoreSatisfier) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter authorization.ResourceRelationshipFilter, orderBy fop.Order, page fop.PageStringCursor) ([]authorization.ResourceRelationship, fop.Pagination, error) {
	repoFilter := rebacrelationships.FilterListByResource{
		SubjectType: filter.SubjectType,
		Relation:    filter.Relation,
	}

	rels, pagination, err := s.repo.ListByResource(ctx, repoFilter, resourceType, resourceID, orderBy, page)
	if err != nil {
		return nil, fop.Pagination{}, err
	}

	result := make([]authorization.ResourceRelationship, len(rels))
	for i, r := range rels {
		result[i] = authorization.ResourceRelationship{
			SubjectType: r.SubjectType,
			SubjectID:   r.SubjectID,
			Relation:    r.Relation,
			CreatedAt:   r.CreatedAt,
		}
	}

	return result, pagination, nil
}

// =============================================================================
// LookupResources
// =============================================================================

func (s *AuthorizationStoreSatisfier) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	return s.repo.LookupResourceIDs(ctx, resourceType, relations, subjectType, subjectID)
}

func (s *AuthorizationStoreSatisfier) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	return s.repo.LookupResourceIDsByRelationTarget(ctx, resourceType, relation, targetType, targetIDs)
}

func (s *AuthorizationStoreSatisfier) LookupDescendantResourceIDs(ctx context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error) {
	return s.repo.LookupDescendantResourceIDs(ctx, resourceType, relation, subjectType, rootIDs)
}
