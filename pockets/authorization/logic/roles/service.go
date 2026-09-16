// Package roles exposes exact concrete membership over canonical tuples.
package roles

import (
	"context"
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type Service struct {
	store     tuples.Storer
	snapshots tuples.Snapshotter
}
type Option func(*Service)

// WithSnapshots supplies an explicit whole-operation freshness view.
func WithSnapshots(view tuples.Snapshotter) Option { return func(s *Service) { s.snapshots = view } }

func NewService(store tuples.Storer, opts ...Option) (*Service, error) {
	if isNil(store) {
		return nil, fmt.Errorf("authorization: tuple store is required: %w", sdk.ErrInvalidInput)
	}
	s := &Service{store: store, snapshots: store}
	for _, opt := range opts {
		if opt == nil {
			return nil, sdk.ErrInvalidInput
		}
		opt(s)
	}
	if isNil(s.snapshots) {
		return nil, sdk.ErrInvalidInput
	}
	return s, nil
}

// HasRole checks exact global membership, without graph expansion.
func (s *Service) HasRole(ctx context.Context, principal authmodel.PrincipalRef, role string) (bool, error) {
	return s.has(ctx, principal, role, tuples.Global())
}

// HasRoleIn checks exact resource membership. Global facts never satisfy it.
func (s *Service) HasRoleIn(ctx context.Context, principal authmodel.PrincipalRef, role string, resource authmodel.Resource) (bool, error) {
	return s.has(ctx, principal, role, tuples.On(resource.Type, resource.ID))
}
func (s *Service) has(ctx context.Context, p authmodel.PrincipalRef, role string, scope tuples.Scope) (bool, error) {
	if s == nil {
		return false, ErrRolesNotConfigured
	}
	fact := (Assignment{SubjectType: p.Type, SubjectID: p.ID, Role: role, Scope: scope}).Tuple()
	if err := fact.Validate(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var held bool
	err := s.snapshots.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
		var err error
		held, err = r.Contains(ctx, fact)
		return err
	})
	if err != nil {
		return false, err
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}
	return held, nil
}

// HasRoleInOrGlobal explicitly composes two exact probes in one snapshot.
func (s *Service) HasRoleInOrGlobal(ctx context.Context, p authmodel.PrincipalRef, role string, resource authmodel.Resource) (bool, error) {
	if s == nil {
		return false, ErrRolesNotConfigured
	}
	scoped := (Assignment{SubjectType: p.Type, SubjectID: p.ID, Role: role, Scope: tuples.On(resource.Type, resource.ID)}).Tuple()
	if err := scoped.Validate(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	global := scoped
	global.Scope = tuples.Global()
	var held bool
	err := s.snapshots.ReadTupleSnapshot(ctx, func(ctx context.Context, r tuples.Reader) error {
		values, err := r.ContainsMany(ctx, []tuples.Tuple{scoped, global})
		if err != nil {
			return err
		}
		if len(values) != 2 {
			return fmt.Errorf("incomplete tuple membership result: %w", sdk.ErrUnavailable)
		}
		held = values[0] || values[1]
		return ctx.Err()
	})
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return held, nil
}

func (s *Service) ListRoleAssignmentsBySubject(ctx context.Context, p authmodel.PrincipalRef, req list.Request) (list.Page[Assignment], error) {
	if s == nil {
		return list.Page[Assignment]{}, ErrRolesNotConfigured
	}
	if err := p.Validate(); err != nil {
		return list.Page[Assignment]{}, err
	}
	subject := tuples.SubjectRef{Type: p.Type, ID: p.ID}
	return s.list(ctx, tuples.Query{Subject: &subject}, req)
}
func (s *Service) ListRoleAssignmentsByScope(ctx context.Context, scope tuples.Scope, req list.Request) (list.Page[Assignment], error) {
	if s == nil {
		return list.Page[Assignment]{}, ErrRolesNotConfigured
	}
	if err := scope.Validate(); err != nil {
		return list.Page[Assignment]{}, err
	}
	return s.list(ctx, tuples.Query{Scope: &scope, ConcreteOnly: true}, req)
}
func (s *Service) list(ctx context.Context, q tuples.Query, req list.Request) (list.Page[Assignment], error) {
	page, err := s.store.ListTuples(ctx, q, req)
	if err != nil {
		return list.Page[Assignment]{}, err
	}
	out := list.Page[Assignment]{NextCursor: page.NextCursor, PreviousCursor: page.PreviousCursor, HasMore: page.HasMore, HasPrev: page.HasPrev, Total: page.Total}
	for _, t := range page.Items {
		out.Items = append(out.Items, assignment(t))
	}
	return out, nil
}
