package relationships

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

// RelationshipWriter is a separately held trusted capability. It validates
// current schema additions but bypasses actor guards and guardian minimums.
// Stores preserve their own atomic fact/audit and ambient transaction contracts.
type RelationshipWriter struct {
	store   Storer
	service *Service
}

// Components separates ordinary checks/reads from trusted baseline writes.
type Components struct {
	Service            *Service
	RelationshipWriter *RelationshipWriter
}

// NewService validates the store, schema and budgets and returns both capabilities.
// The host controls which callers receive the separate trusted writer.
func NewService(store Storer, schema Schema, opts ...Option) (Components, error) {
	cfg := serviceConfig{}
	for _, opt := range opts {
		if opt == nil {
			return Components{}, fmt.Errorf("authorization relationships: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	svc, err := newService(store, schema, cfg)
	if err != nil {
		return Components{}, err
	}
	return Components{Service: svc, RelationshipWriter: &RelationshipWriter{store: store, service: svc}}, nil
}

func (s *RelationshipWriter) CreateRelationships(ctx context.Context, relationships []CreateRelationship) error {
	if err := s.service.ValidateRelationships(relationships); err != nil {
		return err
	}

	out := make([]CreateRelationship, len(relationships))
	copy(out, relationships)
	return s.store.CreateRelationships(ctx, out)
}

func (s *RelationshipWriter) SetRelationTargets(ctx context.Context, resource authmodel.Resource, relationName string, targets []SubjectRef) error {
	if err := authmodel.ValidateRefField("resource type", resource.Type); err != nil {
		return err
	}
	if err := authmodel.ValidateRefField("resource id", resource.ID); err != nil {
		return err
	}
	if err := authmodel.ValidateRefField("relation", relationName); err != nil {
		return err
	}
	if err := s.service.ValidateRelationName(resource.Type, relationName); err != nil {
		return err
	}

	seen := make(map[SubjectRef]struct{}, len(targets))
	rows := make([]CreateRelationship, 0, len(targets))
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
		rows = append(rows, row)
	}
	return s.store.SetRelationTargets(ctx, resource.Type, resource.ID, relationName, rows)
}

func (s *RelationshipWriter) DeleteResourceRelationships(ctx context.Context, resource authmodel.Resource) error {
	if err := authmodel.ValidateRefField("resource type", resource.Type); err != nil {
		return err
	}
	if err := authmodel.ValidateRefField("resource id", resource.ID); err != nil {
		return err
	}
	return s.store.DeleteResourceRelationships(ctx, resource.Type, resource.ID)
}

func (s *RelationshipWriter) DeleteRelationship(ctx context.Context, resource authmodel.Resource, relationName string, target SubjectRef) error {
	if err := authmodel.ValidateRefField("resource type", resource.Type); err != nil {
		return err
	}
	if err := authmodel.ValidateRefField("resource id", resource.ID); err != nil {
		return err
	}
	if err := authmodel.ValidateRefField("relation", relationName); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	return s.store.DeleteRelationshipTarget(ctx, resource.Type, resource.ID, relationName, target)
}
