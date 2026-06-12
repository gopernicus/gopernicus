package testauth

import (
	"context"
	"sync"

	"github.com/gopernicus/gopernicus/core/auth/authorization"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

var _ authorization.Storer = (*MemoryStore)(nil)

// tuple is one relationship: subject --relation--> resource.
type tuple struct {
	resourceType, resourceID, relation, subjectType, subjectID string
}

// MemoryStore is an in-memory authorization.Storer for test stacks: the
// authorizer reads and writes plain tuples, no database. Group expansion is
// direct-match only and the recursive lookups walk the tuple slice — enough
// for generated test suites seeding a handful of relationships, not a ReBAC
// engine. The zero value is not usable; call NewMemoryStore.
type MemoryStore struct {
	mu     sync.Mutex
	tuples []tuple
}

// NewMemoryStore returns an empty in-memory authorization store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Seed inserts one relationship — the test-side convenience for granting a
// subject a relation before driving a route.
func (s *MemoryStore) Seed(resourceType, resourceID, relation, subjectType, subjectID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tuples = append(s.tuples, tuple{resourceType, resourceID, relation, subjectType, subjectID})
}

func (s *MemoryStore) CheckRelationWithGroupExpansion(_ context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return s.exists(resourceType, resourceID, relation, subjectType, subjectID), nil
}

func (s *MemoryStore) GetRelationTargets(_ context.Context, resourceType, resourceID, relation string) ([]authorization.RelationTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var targets []authorization.RelationTarget
	for _, t := range s.tuples {
		if t.resourceType == resourceType && t.resourceID == resourceID && t.relation == relation {
			targets = append(targets, authorization.RelationTarget{SubjectType: t.subjectType, SubjectID: t.subjectID})
		}
	}
	return targets, nil
}

func (s *MemoryStore) CheckRelationExists(_ context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	return s.exists(resourceType, resourceID, relation, subjectType, subjectID), nil
}

func (s *MemoryStore) CheckBatchDirect(_ context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	result := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		result[id] = s.exists(resourceType, id, relation, subjectType, subjectID)
	}
	return result, nil
}

func (s *MemoryStore) CreateRelationships(_ context.Context, relationships []authorization.CreateRelationship) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range relationships {
		s.tuples = append(s.tuples, tuple{r.ResourceType, r.ResourceID, r.Relation, r.SubjectType, r.SubjectID})
	}
	return nil
}

func (s *MemoryStore) DeleteResourceRelationships(_ context.Context, resourceType, resourceID string) error {
	s.filter(func(t tuple) bool {
		return !(t.resourceType == resourceType && t.resourceID == resourceID)
	})
	return nil
}

func (s *MemoryStore) DeleteRelationship(_ context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	s.filter(func(t tuple) bool {
		return t != tuple{resourceType, resourceID, relation, subjectType, subjectID}
	})
	return nil
}

func (s *MemoryStore) DeleteByResourceAndSubject(_ context.Context, resourceType, resourceID, subjectType, subjectID string) error {
	s.filter(func(t tuple) bool {
		return !(t.resourceType == resourceType && t.resourceID == resourceID &&
			t.subjectType == subjectType && t.subjectID == subjectID)
	})
	return nil
}

func (s *MemoryStore) CountByResourceAndRelation(_ context.Context, resourceType, resourceID, relation string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, t := range s.tuples {
		if t.resourceType == resourceType && t.resourceID == resourceID && t.relation == relation {
			n++
		}
	}
	return n, nil
}

func (s *MemoryStore) ListRelationshipsBySubject(_ context.Context, subjectType, subjectID string, _ authorization.SubjectRelationshipFilter, _ fop.Order, _ fop.PageStringCursor) ([]authorization.SubjectRelationship, fop.Pagination, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []authorization.SubjectRelationship
	for _, t := range s.tuples {
		if t.subjectType == subjectType && t.subjectID == subjectID {
			out = append(out, authorization.SubjectRelationship{
				ResourceType: t.resourceType, ResourceID: t.resourceID, Relation: t.relation,
			})
		}
	}
	return out, fop.Pagination{}, nil
}

func (s *MemoryStore) ListRelationshipsByResource(_ context.Context, resourceType, resourceID string, _ authorization.ResourceRelationshipFilter, _ fop.Order, _ fop.PageStringCursor) ([]authorization.ResourceRelationship, fop.Pagination, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []authorization.ResourceRelationship
	for _, t := range s.tuples {
		if t.resourceType == resourceType && t.resourceID == resourceID {
			out = append(out, authorization.ResourceRelationship{
				SubjectType: t.subjectType, SubjectID: t.subjectID, Relation: t.relation,
			})
		}
	}
	return out, fop.Pagination{}, nil
}

func (s *MemoryStore) LookupResourceIDs(_ context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	var ids []string
	for _, t := range s.tuples {
		if t.resourceType != resourceType || t.subjectType != subjectType || t.subjectID != subjectID {
			continue
		}
		for _, rel := range relations {
			if t.relation == rel && !seen[t.resourceID] {
				seen[t.resourceID] = true
				ids = append(ids, t.resourceID)
			}
		}
	}
	return ids, nil
}

func (s *MemoryStore) LookupResourceIDsByRelationTarget(_ context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]bool{}
	for _, id := range targetIDs {
		want[id] = true
	}
	seen := map[string]bool{}
	var ids []string
	for _, t := range s.tuples {
		if t.resourceType == resourceType && t.relation == relation && t.subjectType == targetType && want[t.subjectID] && !seen[t.resourceID] {
			seen[t.resourceID] = true
			ids = append(ids, t.resourceID)
		}
	}
	return ids, nil
}

func (s *MemoryStore) LookupDescendantResourceIDs(_ context.Context, resourceType, relation, subjectType string, rootIDs []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	frontier := map[string]bool{}
	for _, id := range rootIDs {
		frontier[id] = true
	}
	found := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, t := range s.tuples {
			if t.resourceType == resourceType && t.relation == relation && t.subjectType == subjectType &&
				frontier[t.subjectID] && !found[t.resourceID] {
				found[t.resourceID] = true
				frontier[t.resourceID] = true
				changed = true
			}
		}
	}
	ids := make([]string, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *MemoryStore) exists(resourceType, resourceID, relation, subjectType, subjectID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tuples {
		if t == (tuple{resourceType, resourceID, relation, subjectType, subjectID}) {
			return true
		}
	}
	return false
}

func (s *MemoryStore) filter(keep func(tuple) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.tuples[:0]
	for _, t := range s.tuples {
		if keep(t) {
			kept = append(kept, t)
		}
	}
	s.tuples = kept
}
