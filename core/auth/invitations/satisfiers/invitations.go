// Package satisfiers adapts generated repositories to the interfaces the
// invitations engine consumes. Each satisfier wraps a generated repository
// and converts between engine-owned types and generated repository types.
package satisfiers

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/core/auth/invitations"
	invitationsrepo "github.com/gopernicus/gopernicus/core/repositories/rebac/invitations"
	"github.com/gopernicus/gopernicus/sdk/fop"
)

var _ invitations.InvitationRepository = (*InvitationSatisfier)(nil)

type invitationRepo interface {
	Create(ctx context.Context, input invitationsrepo.CreateInvitation) (invitationsrepo.Invitation, error)
	Get(ctx context.Context, invitationID string) (invitationsrepo.Invitation, error)
	GetByToken(ctx context.Context, tokenHash string, now time.Time) (invitationsrepo.Invitation, error)
	Update(ctx context.Context, invitationID string, input invitationsrepo.UpdateInvitation) (invitationsrepo.Invitation, error)
	Delete(ctx context.Context, invitationID string) error
	ListByResource(ctx context.Context, filter invitationsrepo.FilterListByResource, resourceType string, resourceID string, orderBy fop.Order, page fop.PageStringCursor) ([]invitationsrepo.Invitation, fop.Pagination, error)
	ListBySubject(ctx context.Context, filter invitationsrepo.FilterListBySubject, resolvedSubjectID string, orderBy fop.Order, page fop.PageStringCursor) ([]invitationsrepo.Invitation, fop.Pagination, error)
	ListByIdentifier(ctx context.Context, filter invitationsrepo.FilterListByIdentifier, identifier string, identifierType string, now time.Time, orderBy fop.Order, page fop.PageStringCursor) ([]invitationsrepo.Invitation, fop.Pagination, error)
}

// InvitationSatisfier satisfies invitations.InvitationRepository using the
// generated invitations repository. Engine types are field-identical to the
// generated types, so all mappings are plain struct conversions — the
// compiler flags any drift between the two.
type InvitationSatisfier struct {
	repo invitationRepo
}

func NewInvitationSatisfier(repo invitationRepo) *InvitationSatisfier {
	return &InvitationSatisfier{repo: repo}
}

func (s *InvitationSatisfier) Create(ctx context.Context, input invitations.CreateInvitation) (invitations.Invitation, error) {
	created, err := s.repo.Create(ctx, invitationsrepo.CreateInvitation(input))
	if err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation(created), nil
}

func (s *InvitationSatisfier) Get(ctx context.Context, invitationID string) (invitations.Invitation, error) {
	inv, err := s.repo.Get(ctx, invitationID)
	if err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation(inv), nil
}

func (s *InvitationSatisfier) GetByToken(ctx context.Context, tokenHash string, now time.Time) (invitations.Invitation, error) {
	inv, err := s.repo.GetByToken(ctx, tokenHash, now)
	if err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation(inv), nil
}

func (s *InvitationSatisfier) Update(ctx context.Context, invitationID string, input invitations.UpdateInvitation) (invitations.Invitation, error) {
	updated, err := s.repo.Update(ctx, invitationID, invitationsrepo.UpdateInvitation(input))
	if err != nil {
		return invitations.Invitation{}, err
	}
	return invitations.Invitation(updated), nil
}

func (s *InvitationSatisfier) Delete(ctx context.Context, invitationID string) error {
	return s.repo.Delete(ctx, invitationID)
}

func (s *InvitationSatisfier) ListByResource(ctx context.Context, filter invitations.FilterListByResource, resourceType string, resourceID string, orderBy fop.Order, page fop.PageStringCursor) ([]invitations.Invitation, fop.Pagination, error) {
	records, pagination, err := s.repo.ListByResource(ctx, invitationsrepo.FilterListByResource(filter), resourceType, resourceID, orderBy, page)
	if err != nil {
		return nil, fop.Pagination{}, err
	}
	return toEngineInvitations(records), pagination, nil
}

func (s *InvitationSatisfier) ListBySubject(ctx context.Context, filter invitations.FilterListBySubject, resolvedSubjectID string, orderBy fop.Order, page fop.PageStringCursor) ([]invitations.Invitation, fop.Pagination, error) {
	records, pagination, err := s.repo.ListBySubject(ctx, invitationsrepo.FilterListBySubject(filter), resolvedSubjectID, orderBy, page)
	if err != nil {
		return nil, fop.Pagination{}, err
	}
	return toEngineInvitations(records), pagination, nil
}

func (s *InvitationSatisfier) ListByIdentifier(ctx context.Context, filter invitations.FilterListByIdentifier, identifier string, identifierType string, now time.Time, orderBy fop.Order, page fop.PageStringCursor) ([]invitations.Invitation, fop.Pagination, error) {
	records, pagination, err := s.repo.ListByIdentifier(ctx, invitationsrepo.FilterListByIdentifier(filter), identifier, identifierType, now, orderBy, page)
	if err != nil {
		return nil, fop.Pagination{}, err
	}
	return toEngineInvitations(records), pagination, nil
}

func toEngineInvitations(records []invitationsrepo.Invitation) []invitations.Invitation {
	out := make([]invitations.Invitation, len(records))
	for i, record := range records {
		out[i] = invitations.Invitation(record)
	}
	return out
}
