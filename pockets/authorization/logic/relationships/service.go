// Package relationships exposes raw resource-scoped relationship views.
package relationships

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

var (
	ErrInvalidRelation = fmt.Errorf("authorization relation: %w", sdk.ErrInvalidInput)
	ErrInvalidSchema   = fmt.Errorf("authorization model: %w", sdk.ErrInvalidInput)
)

type TupleValidator interface{ ValidateTuple(tuples.Tuple) error }
type Option func(*serviceConfig)
type serviceConfig struct{ validator TupleValidator }

func WithValidator(v TupleValidator) Option { return func(c *serviceConfig) { c.validator = v } }

type Service struct {
	store     tuples.Storer
	validator TupleValidator
}

func isNilReader(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}
func (s *Service) ValidateRelationships(rows []CreateRelationship) error {
	for _, r := range rows {
		if err := r.Validate(); err != nil {
			return err
		}
		if s.validator != nil {
			if err := s.validator.ValidateTuple(r.Tuple()); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Service) ValidateRelation(resourceType, relation, subjectType, subjectRelation string) error {
	if s.validator == nil {
		return nil
	}
	return s.validator.ValidateTuple(tuples.Tuple{Scope: tuples.On(resourceType, "validation"), Relation: relation, Subject: tuples.SubjectRef{Type: subjectType, ID: "validation", Relation: subjectRelation}})
}
func (s *Service) CheckRelationExists(ctx context.Context, rt, id, relation, st, sid string) (bool, error) {
	return s.store.Contains(ctx, tuples.Tuple{Scope: tuples.On(rt, id), Relation: relation, Subject: tuples.SubjectRef{Type: st, ID: sid}})
}
func (s *Service) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]RelationTarget, error) {
	q, err := relationQuery(resourceType, resourceID, relation)
	if err != nil {
		return nil, err
	}
	facts, err := s.store.Lookup(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]RelationTarget, len(facts))
	for i, fact := range facts {
		out[i] = fact.Subject
	}
	return out, nil
}

// ListRelationshipsBySubject pages resource-scoped facts naming this subject
// type and ID, including concrete and userset references. Listings use the
// canonical tuple store's snapshot and cursor contracts.
func (s *Service) ListRelationshipsBySubject(ctx context.Context, subjectType, subjectID string, filter SubjectRelationshipFilter, req list.Request) (list.Page[SubjectRelationship], error) {
	if err := (SubjectRef{Type: subjectType, ID: subjectID}).Validate(); err != nil {
		return list.Page[SubjectRelationship]{}, err
	}
	q := tuples.Query{ResourceOnly: true, SubjectType: subjectType, SubjectID: subjectID}
	if err := optionalFilter("resource type", filter.ResourceType, &q.ResourceType); err != nil {
		return list.Page[SubjectRelationship]{}, err
	}
	if err := optionalFilter("relation", filter.Relation, &q.Relation); err != nil {
		return list.Page[SubjectRelationship]{}, err
	}
	page, err := s.store.ListTuples(ctx, q, req)
	if err != nil {
		return list.Page[SubjectRelationship]{}, err
	}
	return list.MapPage(page, func(t tuples.Tuple) SubjectRelationship {
		return SubjectRelationship{ResourceType: t.Scope.Type, ResourceID: t.Scope.ID, Relation: t.Relation, SubjectRelation: t.Subject.Relation}
	}), nil
}

// ListRelationshipsByResource pages the exact subjects related to a resource.
func (s *Service) ListRelationshipsByResource(ctx context.Context, resourceType, resourceID string, filter ResourceRelationshipFilter, req list.Request) (list.Page[ResourceRelationship], error) {
	scope := tuples.On(resourceType, resourceID)
	if err := scope.Validate(); err != nil {
		return list.Page[ResourceRelationship]{}, err
	}
	q := tuples.Query{Scope: &scope}
	if err := optionalFilter("subject type", filter.SubjectType, &q.SubjectType); err != nil {
		return list.Page[ResourceRelationship]{}, err
	}
	if err := optionalFilter("relation", filter.Relation, &q.Relation); err != nil {
		return list.Page[ResourceRelationship]{}, err
	}
	page, err := s.store.ListTuples(ctx, q, req)
	if err != nil {
		return list.Page[ResourceRelationship]{}, err
	}
	return list.MapPage(page, func(t tuples.Tuple) ResourceRelationship {
		return ResourceRelationship{SubjectType: t.Subject.Type, SubjectID: t.Subject.ID, SubjectRelation: t.Subject.Relation, Relation: t.Relation}
	}), nil
}

// CountByResourceAndRelation counts stored facts, including userset references,
// without expanding membership. It uses the canonical listing snapshot contract.
func (s *Service) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	q, err := relationQuery(resourceType, resourceID, relation)
	if err != nil {
		return 0, err
	}
	page, err := s.store.ListTuples(ctx, q, list.Request{Limit: 1, WithCount: true})
	if err != nil {
		return 0, err
	}
	if page.Total == nil || *page.Total < 0 || uint64(*page.Total) > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("invalid tuple count: %w", sdk.ErrUnavailable)
	}
	return int(*page.Total), nil
}

func relationQuery(resourceType, resourceID, relation string) (tuples.Query, error) {
	scope := tuples.On(resourceType, resourceID)
	if err := scope.Validate(); err != nil {
		return tuples.Query{}, err
	}
	if err := tuples.ValidateRefField("relation", relation); err != nil {
		return tuples.Query{}, err
	}
	return tuples.Query{Scope: &scope, Relation: relation}, nil
}

func optionalFilter(name string, value *string, target *string) error {
	if value == nil {
		return nil
	}
	if err := tuples.ValidateRefField(name, *value); err != nil {
		return err
	}
	*target = *value
	return nil
}
