package relationships

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

// RelationshipWriter is a separately held trusted capability. It validates
// current schema additions but bypasses actor guards and guardian minimums.
// Stores preserve their own atomic fact/audit and ambient transaction contracts.
type RelationshipWriter struct {
	store   tuples.Storer
	service *Service
}

// Components separates ordinary checks/reads from trusted baseline writes.
type Components struct {
	Service            *Service
	RelationshipWriter *RelationshipWriter
}

// NewService separates raw views from the trusted writer capability.
func NewService(store tuples.Storer, opts ...Option) (Components, error) {
	if isNilReader(store) {
		return Components{}, fmt.Errorf("authorization: tuple store is required: %w", sdk.ErrInvalidInput)
	}
	cfg := serviceConfig{}
	for _, opt := range opts {
		if opt == nil {
			return Components{}, fmt.Errorf("authorization relationships: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if cfg.validator != nil && isNilReader(cfg.validator) {
		return Components{}, sdk.ErrInvalidInput
	}
	svc := &Service{store: store, validator: cfg.validator}
	return Components{Service: svc, RelationshipWriter: &RelationshipWriter{store: store, service: svc}}, nil
}

func (s *RelationshipWriter) CreateRelationships(ctx context.Context, relationships []CreateRelationship) error {
	if err := s.service.ValidateRelationships(relationships); err != nil {
		return err
	}

	out := make([]tuples.Tuple, len(relationships))
	for i, row := range relationships {
		out[i] = row.Tuple()
	}
	return s.store.ApplyTuples(ctx, tuples.Changes{Add: out})
}

func (s *RelationshipWriter) SetRelationTargets(ctx context.Context, resource authmodel.Resource, relationName string, targets []SubjectRef) error {
	if err := tuples.ValidateRefField("resource type", resource.Type); err != nil {
		return err
	}
	if err := tuples.ValidateRefField("resource id", resource.ID); err != nil {
		return err
	}
	if err := tuples.ValidateRefField("relation", relationName); err != nil {
		return err
	}

	seen := make(map[SubjectRef]struct{}, len(targets))
	subjects := make([]SubjectRef, 0, len(targets))
	for _, target := range targets {
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		row := CreateRelationship{
			ResourceType:    resource.Type,
			ResourceID:      resource.ID,
			Relation:        relationName,
			SubjectType:     target.Type,
			SubjectID:       target.ID,
			SubjectRelation: target.Relation,
		}
		if err := row.Validate(); err != nil {
			return fmt.Errorf("relationship %s:%s#%s@%s: %w",
				row.ResourceType, row.ResourceID, row.Relation, row.Subject(), err)
		}
		if err := s.service.ValidateRelation(row.ResourceType, row.Relation, row.SubjectType, row.SubjectRelation); err != nil {
			return fmt.Errorf("relationship %s:%s#%s@%s: %w",
				row.ResourceType, row.ResourceID, row.Relation, row.Subject(), err)
		}
		subjects = append(subjects, target)
	}
	return s.store.ReconcileTuples(ctx, tuples.On(resource.Type, resource.ID), relationName, subjects)
}

func (s *RelationshipWriter) DeleteResourceRelationships(ctx context.Context, resource authmodel.Resource) error {
	if err := tuples.ValidateRefField("resource type", resource.Type); err != nil {
		return err
	}
	if err := tuples.ValidateRefField("resource id", resource.ID); err != nil {
		return err
	}
	return s.store.DeleteScope(ctx, tuples.On(resource.Type, resource.ID))
}

func (s *RelationshipWriter) DeleteRelationship(ctx context.Context, resource authmodel.Resource, relationName string, target SubjectRef) error {
	if err := tuples.ValidateRefField("resource type", resource.Type); err != nil {
		return err
	}
	if err := tuples.ValidateRefField("resource id", resource.ID); err != nil {
		return err
	}
	if err := tuples.ValidateRefField("relation", relationName); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	return s.store.ApplyTuples(ctx, tuples.Changes{Remove: []tuples.Tuple{{Scope: tuples.On(resource.Type, resource.ID), Relation: relationName, Subject: target}}})
}
