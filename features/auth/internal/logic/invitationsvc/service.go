// Package invitationsvc holds the auth feature's invitation use cases (design
// §6): create, accept, decline, cancel, resend, list, and resolve-on-
// registration. It is a SIBLING of authsvc, not a part of it: the ReBAC-
// decoupled Granter seam is injected HERE only (authsvc never holds it), and
// authsvc reaches this service through exactly one narrow port —
// ResolveInvitations — so the authsvc↔invitationsvc coupling is that single
// interface (design §6 pin). It is internal, so it carries no public SemVer
// surface; the host-facing seams (auth.Granter, auth.MemberCheck) are aliased
// from here in package auth.
//
// Grant coupling: the grant on accept / direct-add / resolve rides the
// host-supplied Granter. Invitation VISIBILITY never touches a tuple — it rides
// this domain's own table columns (Identifier for "mine" and the resolve
// finder; InvitedBy for cancel/resend ownership). A host with no ReBAC has no
// "invitation" resource type, and nothing here pretends otherwise.
package invitationsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/auth/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/auth/internal/redirect"
	"github.com/gopernicus/gopernicus/features/auth/logic/invitation"
	"github.com/gopernicus/gopernicus/features/auth/logic/securityevent"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/id"
)

const (
	// defaultInvitationTTL is the lifetime of a minted invitation secret when
	// Deps.TTL is unset (salvaged 7-day default).
	defaultInvitationTTL = 7 * 24 * time.Hour
	// tokenSecretLen is the length of the generated invitation secret: 32 chars,
	// dotless (sdk/id's base32 alphabet), plaintext only in the mail.
	tokenSecretLen = 32
	// subjectTypeUser is the ReBAC subject-type convention for a human user —
	// the only subject class this service grants to (accept, direct-add, resolve).
	subjectTypeUser = "user"
	// resolvePageLimit bounds one page of the resolve-on-registration scan.
	resolvePageLimit = 100
)

// Errors surfaced to the transport (each wraps a stable errs kind so sdk/web
// maps it to a status; checked with errors.Is).
var (
	// ErrAlreadyMember is returned by Create when MemberCheck reports the invitee
	// already holds the relation on the resource (a duplicate invite is pointless).
	ErrAlreadyMember = fmt.Errorf("subject is already a member: %w", errs.ErrConflict)
	// ErrPendingInvitationExists is returned by Create when a pending invitation
	// already exists for the (resource, identifier, relation) tuple.
	ErrPendingInvitationExists = fmt.Errorf("a pending invitation already exists: %w", errs.ErrAlreadyExists)
	// ErrNotPending is returned when a transition (accept/decline/cancel/resend)
	// targets an invitation that is not in an eligible status.
	ErrNotPending = fmt.Errorf("invitation is not pending: %w", errs.ErrConflict)
	// ErrIdentifierMismatch is returned by Accept when the accepting user's email
	// does not match the invitation identifier.
	ErrIdentifierMismatch = fmt.Errorf("invitation identifier does not match: %w", errs.ErrForbidden)
	// ErrNotOwner is returned by Cancel/Resend when the caller is not the
	// invitation's InvitedBy owner.
	ErrNotOwner = fmt.Errorf("not the invitation owner: %w", errs.ErrForbidden)
)

// Granter grants a subject a relation on a resource — the ONE ReBAC-decoupled
// seam (design §2.2), called on accept, direct-add, and resolve-on-registration
// and NOTHING else. A ReBAC host adapts it to CreateRelationships; a role-column
// host to a role write; the proof host to a toy in-memory membership map. Grants
// must be idempotent in the Granter's world (a duplicate accept must not error).
type Granter interface {
	Grant(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error
}

// MemberCheck is the optional duplicate-membership predicate consulted before a
// direct-add grant. Nil → no dup check (idempotent grants absorb duplicates).
type MemberCheck func(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) (bool, error)

// UserLookup resolves an invitee email to an existing user's subject id for the
// direct-add path. found=false means no such user (→ a pending invitation is
// created instead). It is an INTERNAL collaborator wired by package auth from
// the Users repository — not a host Config seam.
type UserLookup func(ctx context.Context, email string) (subjectID string, found bool, err error)

// CreateInput is the input to Create. Identifier is the invitee email (the
// service normalizes it). Redirect is the requested post-accept destination,
// guarded by the feature's redirect allowlist before it reaches the mail.
type CreateInput struct {
	ResourceType string
	ResourceID   string
	Relation     string
	Identifier   string
	InvitedBy    string
	AutoAccept   bool
	Redirect     string
}

// CreateResult reports the outcome of Create. DirectlyAdded is true when a known
// invitee was granted immediately (no pending record); otherwise Invitation
// carries the created pending record.
type CreateResult struct {
	DirectlyAdded bool
	Invitation    invitation.Invitation
}

// AcceptInput is the input to Accept. Token is the plaintext secret from the
// invitation mail; SubjectType/SubjectID is the accepting caller; Identifier is
// the caller's email, checked against the invitation identifier.
type AcceptInput struct {
	Token       string
	SubjectType string
	SubjectID   string
	Identifier  string
}

// AcceptResult reports the granted tuple's resource/relation.
type AcceptResult struct {
	ResourceType string
	ResourceID   string
	Relation     string
}

// Deps are the collaborators the Service needs, assembled by package auth after
// it validates the required wiring. Granter and Invitations are required (auth
// enforces both at construction); MemberCheck, UserLookup, and SecurityEvents
// are optional.
type Deps struct {
	Invitations    invitation.InvitationRepository
	Granter        Granter
	MemberCheck    MemberCheck
	UserLookup     UserLookup
	Mailer         email.Sender
	MailFrom       string
	Redirects      redirect.Allowlist
	SecurityEvents securityevent.SecurityEventRepository
	Clock          func() time.Time
	Logger         *slog.Logger
	TTL            time.Duration
}

// Service implements the invitation use cases over its ports.
type Service struct {
	invitations    invitation.InvitationRepository
	granter        Granter
	memberCheck    MemberCheck
	userLookup     UserLookup
	mailer         email.Sender
	mailFrom       string
	redirects      redirect.Allowlist
	securityEvents securityevent.SecurityEventRepository
	now            func() time.Time
	logger         *slog.Logger
	ttl            time.Duration
	tokenHasher    *cryptids.SHA256Hasher
}

// New builds a Service, applying a time.Now clock, slog default logger, and the
// default TTL when unset.
func New(d Deps) *Service {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ttl := d.TTL
	if ttl <= 0 {
		ttl = defaultInvitationTTL
	}
	return &Service{
		invitations:    d.Invitations,
		granter:        d.Granter,
		memberCheck:    d.MemberCheck,
		userLookup:     d.UserLookup,
		mailer:         d.Mailer,
		mailFrom:       d.MailFrom,
		redirects:      d.Redirects,
		securityEvents: d.SecurityEvents,
		now:            clock,
		logger:         logger,
		ttl:            ttl,
		tokenHasher:    cryptids.NewSHA256Hasher(),
	}
}

// Create invites Identifier to a resource. When AutoAccept is set and the
// invitee is a known user, it is a direct add — an immediate grant with no
// pending record (MemberCheck may veto a duplicate). Otherwise a pending
// invitation is minted and its secret mailed.
func (s *Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	identifier, err := normalizeIdentifier(in.Identifier)
	if err != nil {
		return CreateResult{}, err
	}
	in.Identifier = identifier

	subjectID := ""
	if s.userLookup != nil {
		id, found, err := s.userLookup(ctx, identifier)
		if err != nil {
			return CreateResult{}, fmt.Errorf("lookup invitee: %w", err)
		}
		if found {
			subjectID = id
		}
	}

	if in.AutoAccept && subjectID != "" {
		return s.directAdd(ctx, in, subjectID)
	}
	return s.createPending(ctx, in, subjectID)
}

// directAdd grants a known invitee immediately (AutoAccept). A MemberCheck veto
// (already a member) → ErrAlreadyMember; a Granter failure is audited and
// returned. No invitation record is created (the original's direct-add shape).
func (s *Service) directAdd(ctx context.Context, in CreateInput, subjectID string) (CreateResult, error) {
	if s.memberCheck != nil {
		isMember, err := s.memberCheck(ctx, in.ResourceType, in.ResourceID, subjectTypeUser, subjectID)
		if err != nil {
			return CreateResult{}, fmt.Errorf("check member: %w", err)
		}
		if isMember {
			return CreateResult{}, ErrAlreadyMember
		}
	}
	if err := s.granter.Grant(ctx, in.ResourceType, in.ResourceID, in.Relation, subjectTypeUser, subjectID); err != nil {
		s.recordGrant(ctx, subjectID, in.ResourceType, in.ResourceID, in.Relation, in.Identifier, securityevent.StatusFailure)
		return CreateResult{}, fmt.Errorf("grant: %w", err)
	}
	s.recordGrant(ctx, subjectID, in.ResourceType, in.ResourceID, in.Relation, in.Identifier, securityevent.StatusSuccess)
	s.sendMemberAdded(ctx, in.Identifier, in.ResourceType, in.ResourceID, in.Relation, in.Redirect)
	return CreateResult{DirectlyAdded: true}, nil
}

// createPending mints a pending invitation and mails its secret. A pending-tuple
// collision → ErrPendingInvitationExists. When the invitee is already a known
// user its subject id is pre-recorded (ResolvedSubjectID) for later attribution.
// A mail failure is returned with the (persisted) record, mirroring Register.
func (s *Service) createPending(ctx context.Context, in CreateInput, subjectID string) (CreateResult, error) {
	secret := mintSecret()
	tokenHash, err := s.hashSecret(secret)
	if err != nil {
		return CreateResult{}, err
	}
	inv, err := invitation.New(in.ResourceType, in.ResourceID, in.Relation, in.Identifier, in.InvitedBy, tokenHash, in.AutoAccept, s.ttl, s.now())
	if err != nil {
		return CreateResult{}, err
	}
	if subjectID != "" {
		inv.ResolvedSubjectID = subjectID
	}
	created, err := s.invitations.Create(ctx, inv)
	if err != nil {
		if errors.Is(err, errs.ErrAlreadyExists) {
			return CreateResult{}, ErrPendingInvitationExists
		}
		return CreateResult{}, err
	}
	s.recordCreated(ctx, created)
	if err := s.sendInviteSent(ctx, created, secret, in.Redirect); err != nil {
		return CreateResult{Invitation: created}, err
	}
	return CreateResult{Invitation: created}, nil
}

// Accept redeems a token: it grants the accepting subject the invitation's
// relation, then marks the invitation accepted with the resolved subject id. An
// unknown/expired token surfaces errs.ErrNotFound/ErrExpired; a non-pending
// invitation → ErrNotPending; an identifier mismatch → ErrIdentifierMismatch. A
// Granter failure is audited and returned (the invitation stays pending).
func (s *Service) Accept(ctx context.Context, in AcceptInput) (AcceptResult, error) {
	tokenHash, err := s.hashSecret(in.Token)
	if err != nil {
		return AcceptResult{}, invitationNotFound()
	}
	inv, err := s.invitations.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		return AcceptResult{}, err
	}
	if inv.Status != invitation.StatusPending {
		return AcceptResult{}, ErrNotPending
	}
	identifier, err := normalizeIdentifier(in.Identifier)
	if err != nil || identifier != inv.Identifier {
		return AcceptResult{}, ErrIdentifierMismatch
	}

	subjectType := in.SubjectType
	if subjectType == "" {
		subjectType = subjectTypeUser
	}
	if err := s.granter.Grant(ctx, inv.ResourceType, inv.ResourceID, inv.Relation, subjectType, in.SubjectID); err != nil {
		s.recordGrant(ctx, in.SubjectID, inv.ResourceType, inv.ResourceID, inv.Relation, inv.Identifier, securityevent.StatusFailure)
		return AcceptResult{}, fmt.Errorf("grant: %w", err)
	}
	s.recordGrant(ctx, in.SubjectID, inv.ResourceType, inv.ResourceID, inv.Relation, inv.Identifier, securityevent.StatusSuccess)

	now := s.now()
	if _, err := s.invitations.UpdateStatus(ctx, inv.ID, invitation.StatusUpdate{
		Status:            invitation.StatusAccepted,
		TokenHash:         inv.TokenHash,
		ExpiresAt:         inv.ExpiresAt,
		AcceptedAt:        now,
		ResolvedSubjectID: in.SubjectID,
		UpdatedAt:         now,
	}); err != nil {
		return AcceptResult{}, err
	}
	s.sendMemberAdded(ctx, inv.Identifier, inv.ResourceType, inv.ResourceID, inv.Relation, "")
	return AcceptResult{ResourceType: inv.ResourceType, ResourceID: inv.ResourceID, Relation: inv.Relation}, nil
}

// Decline marks a pending invitation declined. It is a PUBLIC route, so the
// caller proves they are the invitee by presenting the token; a wrong token
// leaks nothing (errs.ErrNotFound). No grant happens.
func (s *Service) Decline(ctx context.Context, id, token string) error {
	inv, err := s.invitations.Get(ctx, id)
	if err != nil {
		return err
	}
	tokenHash, err := s.hashSecret(token)
	if err != nil || tokenHash != inv.TokenHash {
		return invitationNotFound()
	}
	if inv.Status != invitation.StatusPending {
		return ErrNotPending
	}
	now := s.now()
	if _, err := s.invitations.UpdateStatus(ctx, id, invitation.StatusUpdate{
		Status:            invitation.StatusDeclined,
		TokenHash:         inv.TokenHash,
		ExpiresAt:         inv.ExpiresAt,
		ResolvedSubjectID: inv.ResolvedSubjectID,
		UpdatedAt:         now,
	}); err != nil {
		return err
	}
	s.recordLifecycle(ctx, inv, securityevent.TypeInvitationDeclined, inv.ResolvedSubjectID)
	return nil
}

// Cancel marks a pending invitation cancelled. Authorization is a plain
// ownership check — the caller must be the InvitedBy owner (design §6: no tuple,
// no invitation-as-resource). A non-owner → ErrNotOwner.
func (s *Service) Cancel(ctx context.Context, id, currentUserID string) error {
	inv, err := s.invitations.Get(ctx, id)
	if err != nil {
		return err
	}
	if inv.InvitedBy != currentUserID {
		return ErrNotOwner
	}
	if inv.Status != invitation.StatusPending {
		return ErrNotPending
	}
	now := s.now()
	if _, err := s.invitations.UpdateStatus(ctx, id, invitation.StatusUpdate{
		Status:            invitation.StatusCancelled,
		TokenHash:         inv.TokenHash,
		ExpiresAt:         inv.ExpiresAt,
		ResolvedSubjectID: inv.ResolvedSubjectID,
		UpdatedAt:         now,
	}); err != nil {
		return err
	}
	s.recordLifecycle(ctx, inv, securityevent.TypeInvitationCancelled, currentUserID)
	return nil
}

// Resend regenerates the secret and resets the expiry on an owner's pending (or
// expired) invitation in place — no new record — and re-mails it. Authorization
// is the InvitedBy ownership check.
func (s *Service) Resend(ctx context.Context, id, currentUserID, redirectTo string) (invitation.Invitation, error) {
	inv, err := s.invitations.Get(ctx, id)
	if err != nil {
		return invitation.Invitation{}, err
	}
	if inv.InvitedBy != currentUserID {
		return invitation.Invitation{}, ErrNotOwner
	}
	if inv.Status != invitation.StatusPending && inv.Status != invitation.StatusExpired {
		return invitation.Invitation{}, ErrNotPending
	}
	secret := mintSecret()
	tokenHash, err := s.hashSecret(secret)
	if err != nil {
		return invitation.Invitation{}, err
	}
	now := s.now()
	updated, err := s.invitations.UpdateStatus(ctx, id, invitation.StatusUpdate{
		Status:            invitation.StatusPending,
		TokenHash:         tokenHash,
		ExpiresAt:         now.UTC().Add(s.ttl),
		ResolvedSubjectID: inv.ResolvedSubjectID,
		UpdatedAt:         now,
	})
	if err != nil {
		return invitation.Invitation{}, err
	}
	s.recordCreated(ctx, updated)
	if err := s.sendInviteSent(ctx, updated, secret, redirectTo); err != nil {
		return updated, err
	}
	return updated, nil
}

// ListByResource returns a cursor-paginated page of a resource's invitations
// (ordered created_at DESC, id DESC).
func (s *Service) ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	return s.invitations.ListByResource(ctx, resourceType, resourceID, req)
}

// Mine returns a cursor-paginated page of the invitations addressed to an
// invitee identifier (email) — the caller's own invitations (design §6: rides
// the Identifier table column, never a tuple).
func (s *Service) Mine(ctx context.Context, identifier string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	normalized, err := normalizeIdentifier(identifier)
	if err != nil {
		return crud.Page[invitation.Invitation]{}, err
	}
	return s.invitations.ListBySubject(ctx, normalized, req)
}

// ResolveInvitations grants all pending AUTO-ACCEPT invitations for a just-
// registered/verified email to (subjectType, subjectID), best-effort: one
// failed grant never aborts the caller (a registration), and each failure is
// audited. It returns the number resolved. This is the sole port authsvc holds
// on this service (design §6 pin).
func (s *Service) ResolveInvitations(ctx context.Context, email, subjectType, subjectID string) (int, error) {
	identifier, err := normalizeIdentifier(email)
	if err != nil {
		return 0, nil // an unparseable email resolves nothing; never abort registration
	}
	if subjectType == "" {
		subjectType = subjectTypeUser
	}

	resolved := 0
	cursor := ""
	for i := 0; i < 1000; i++ { // bound against a runaway cursor
		page, err := s.invitations.ListBySubject(ctx, identifier, crud.ListRequest{Limit: resolvePageLimit, Cursor: cursor})
		if err != nil {
			return resolved, err
		}
		for _, inv := range page.Items {
			if inv.Status != invitation.StatusPending || !inv.AutoAccept || inv.Expired(s.now()) {
				continue
			}
			if err := s.granter.Grant(ctx, inv.ResourceType, inv.ResourceID, inv.Relation, subjectType, subjectID); err != nil {
				s.recordGrant(ctx, subjectID, inv.ResourceType, inv.ResourceID, inv.Relation, inv.Identifier, securityevent.StatusFailure)
				continue // best-effort — one failed grant never aborts registration
			}
			s.recordGrant(ctx, subjectID, inv.ResourceType, inv.ResourceID, inv.Relation, inv.Identifier, securityevent.StatusSuccess)
			now := s.now()
			if _, err := s.invitations.UpdateStatus(ctx, inv.ID, invitation.StatusUpdate{
				Status:            invitation.StatusAccepted,
				TokenHash:         inv.TokenHash,
				ExpiresAt:         inv.ExpiresAt,
				AcceptedAt:        now,
				ResolvedSubjectID: subjectID,
				UpdatedAt:         now,
			}); err != nil {
				s.logger.Warn("resolve invitation status update failed", "error_kind", errKind(err))
				continue
			}
			resolved++
		}
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return resolved, nil
}

// --- audit ---

// recordGrant appends an invitation_granted audit row for a grant attempt
// (StatusSuccess or StatusFailure). Details carries identifiers only — never the
// token (design §5.1 WI3).
func (s *Service) recordGrant(ctx context.Context, subjectID, resourceType, resourceID, relation, identifier, status string) {
	s.record(ctx, subjectID, securityevent.TypeInvitationGranted, status, map[string]any{
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"relation":      relation,
		"identifier":    identifier,
	})
}

// recordCreated appends an invitation_created audit row (a pending invite minted
// or resent), attributed to the InvitedBy owner.
func (s *Service) recordCreated(ctx context.Context, inv invitation.Invitation) {
	s.record(ctx, inv.InvitedBy, securityevent.TypeInvitationCreated, securityevent.StatusSuccess, map[string]any{
		"resource_type": inv.ResourceType,
		"resource_id":   inv.ResourceID,
		"relation":      inv.Relation,
		"identifier":    inv.Identifier,
	})
}

// recordLifecycle appends a decline/cancel audit row, attributed to userID.
func (s *Service) recordLifecycle(ctx context.Context, inv invitation.Invitation, eventType, userID string) {
	s.record(ctx, userID, eventType, securityevent.StatusSuccess, map[string]any{
		"resource_type": inv.ResourceType,
		"resource_id":   inv.ResourceID,
		"relation":      inv.Relation,
	})
}

// record appends one audit row synchronously, reading IP/UA from the shared
// client-info carrier. Nil repository → no-op (ratified AV9); a write failure is
// logged at WARN with coarse fields only and NEVER fails the invitation flow
// (design §5.1's non-negotiable, reused here).
func (s *Service) record(ctx context.Context, userID, eventType, status string, details map[string]any) {
	if s.securityEvents == nil {
		return
	}
	ip, ua := authsvc.ClientInfoFromContext(ctx)
	evt := securityevent.New(eventType, status, s.now())
	evt.UserID = userID
	evt.Details = details
	evt.IPAddress = ip
	evt.UserAgent = ua
	if _, err := s.securityEvents.Create(ctx, evt); err != nil {
		s.logger.Warn("security event write failed",
			"event_type", eventType,
			"status", status,
			"error_kind", errKind(err),
		)
	}
}

// --- mail ---

// sendInviteSent mails the invitation secret to the invitee. The requested
// redirect destination is passed through the feature's allowlist first (design
// §3's open-redirect guard), so a non-allowlisted target falls back to "/".
func (s *Service) sendInviteSent(ctx context.Context, inv invitation.Invitation, secret, redirectTo string) error {
	dest := s.redirects.Resolve(redirectTo)
	msg := email.Message{
		From:    s.mailFrom,
		To:      []string{inv.Identifier},
		Subject: "You have an invitation",
		Text: fmt.Sprintf("You were invited to %s %s as %s.\nAccept: %s (token: %s)",
			inv.ResourceType, inv.ResourceID, inv.Relation, dest, secret),
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		return fmt.Errorf("send invitation email: %w", err)
	}
	return nil
}

// sendMemberAdded notifies an added member. It is best-effort — the grant has
// already happened, so a mail failure is logged, never surfaced.
func (s *Service) sendMemberAdded(ctx context.Context, identifier, resourceType, resourceID, relation, redirectTo string) {
	dest := s.redirects.Resolve(redirectTo)
	msg := email.Message{
		From:    s.mailFrom,
		To:      []string{identifier},
		Subject: "You were added",
		Text: fmt.Sprintf("You were added to %s %s as %s.\n%s",
			resourceType, resourceID, relation, dest),
	}
	if err := s.mailer.Send(ctx, msg); err != nil {
		s.logger.Warn("member-added email failed", "error_kind", errKind(err))
	}
}

// --- helpers ---

// hashSecret returns the stored form of an invitation secret — its SHA-256 hex
// digest (cryptids.SHA256Hasher, the same primitive used for API keys and
// session tokens). An empty secret is rejected.
func (s *Service) hashSecret(secret string) (string, error) {
	return s.tokenHasher.Hash(secret)
}

// mintSecret builds a fresh 32-char dotless invitation secret from sdk/id's
// base32 alphabet — no dots, so it never collides with the JWT-detection
// heuristic and is URL-safe in the mailed link.
func mintSecret() string {
	return (id.New() + id.New())[:tokenSecretLen]
}

// normalizeIdentifier trims and lowercases the invitee identifier (email) so a
// stored invitation matches what resolve-on-registration and "mine" look it up
// by. A blank identifier is invalid input.
func normalizeIdentifier(identifier string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(identifier))
	if normalized == "" {
		return "", fmt.Errorf("identifier is required: %w", errs.ErrInvalidInput)
	}
	return normalized, nil
}

// invitationNotFound is the generic not-found returned when a token does not
// resolve, so a public caller cannot probe invitation existence.
func invitationNotFound() error {
	return fmt.Errorf("invitation not found: %w", errs.ErrNotFound)
}

// errKind reduces err to a coarse, secret-free label for a WARN line (design
// §5.1 WI3 — the log carries event type, status, and error kind only).
func errKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, errs.ErrAlreadyExists):
		return "already_exists"
	case errors.Is(err, errs.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, errs.ErrNotFound):
		return "not_found"
	case errors.Is(err, errs.ErrConflict):
		return "conflict"
	default:
		return "unknown"
	}
}
