package decisions

import (
	"context"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

// ResourceSet is a complete permission filter within the configured evaluation
// limits. IDs is non-nil on success. Empty IDs means no access unless Unrestricted
// is true. Unrestricted removes only the permission restriction; the host still
// applies its tenant and business filters.
type ResourceSet struct {
	IDs          []string
	Unrestricted bool
}

// ResourceIDPage is one page of authorized IDs in lexical ID order. It is not a
// complete permission filter and cannot supply a globally name/date-sorted
// business page. Use LookupAllResourceIDs or FilterPage for that operation.
//
// IDs is non-nil on success. Unrestricted means there are no IDs to enumerate;
// the host omits only the authorization restriction from its business query.
// NextCursor is present when HasMore is true. Each request observes current state;
// continuation does not preserve a snapshot across requests.
type ResourceIDPage struct {
	IDs          []string
	Unrestricted bool
	HasMore      bool
	NextCursor   string
}

// ResourceIDPageRequest selects one ID page. Limit 0 selects MaxLookupResults;
// it never asks for all IDs. After is the previous page's NextCursor, bound to
// principal, permission, resource type and model. Model changes require restart.
type ResourceIDPageRequest = authmodel.LookupRequest

// LookupAllResourceIDs returns the complete bounded permission set. An overflow
// returns ErrEvaluationLimit without a partial result. Apply the successful set
// together with tenant/search predicates before business sorting or pagination.
func (s *Service) LookupAllResourceIDs(ctx context.Context, principal authmodel.PrincipalRef, permission, resourceType string) (ResourceSet, error) {
	if s == nil {
		return ResourceSet{}, authmodel.ErrNoDecisionKind
	}
	result, err := s.LookupResources(ctx, principal, permission, resourceType)
	if err != nil {
		return ResourceSet{}, err
	}
	return ResourceSet{IDs: result.IDs, Unrestricted: result.Unrestricted}, nil
}

// LookupResourceIDPage returns one bounded ID page. Limit must not exceed
// MaxLookupResults. Intermediate graph limits still apply on every page;
// pagination does not bypass evaluation errors or guarantee bounded database I/O.
func (s *Service) LookupResourceIDPage(ctx context.Context, req ResourceIDPageRequest) (ResourceIDPage, error) {
	if s == nil {
		return ResourceIDPage{}, authmodel.ErrNoDecisionKind
	}
	result, err := s.LookupResourcesIn(ctx, req)
	if err != nil {
		return ResourceIDPage{}, err
	}
	return ResourceIDPage{
		IDs: result.IDs, Unrestricted: result.Unrestricted,
		HasMore: result.HasMore, NextCursor: result.NextCursor,
	}, nil
}
