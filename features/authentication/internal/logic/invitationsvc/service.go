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

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/features/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// secrets generates the opaque invitation secrets this service mints with the
// default nanoid shape. Deliberately NOT the app's entity-ID strategy
// (Deps.IDs): secret entropy must never follow a wiring choice like
// cryptids.Database.
var secrets = cryptids.IDGenerator{}

const (
	// defaultInvitationTTL is the lifetime of a minted invitation secret when
	// Deps.TTL is unset (salvaged 7-day default).
	defaultInvitationTTL = 7 * 24 * time.Hour
	// tokenSecretLen is the length of the generated invitation secret: 32 chars,
	// dotless (sdk/foundation/cryptids' alphabet), plaintext only in the mail.
	tokenSecretLen = 32
	// subjectTypeUser is the ReBAC subject-type convention for a human user —
	// the only subject class this service grants to (accept, direct-add, resolve).
	subjectTypeUser = "user"
	// resolvePageLimit bounds one page of the resolve-on-registration scan.
	resolvePageLimit = 100
)

// Errors surfaced to the transport (each wraps a stable errs kind so sdk/foundation/web
// maps it to a status; checked with errors.Is).
var (
	// ErrAlreadyMember is returned by Create when MemberCheck reports the invitee
	// already holds the relation on the resource (a duplicate invite is pointless).
	ErrAlreadyMember = fmt.Errorf("subject is already a member: %w", sdk.ErrConflict)
	// ErrPendingInvitationExists is returned by Create when a pending invitation
	// already exists for the (resource, identifier, relation) tuple.
	ErrPendingInvitationExists = fmt.Errorf("a pending invitation already exists: %w", sdk.ErrAlreadyExists)
	// ErrNotPending is returned when a transition (accept/decline/cancel/resend)
	// targets an invitation that is not in an eligible status.
	ErrNotPending = fmt.Errorf("invitation is not pending: %w", sdk.ErrConflict)
	// ErrIdentifierMismatch is returned by Accept when the accepting user's
	// identifier of the invitation's kind (their email for an email invitation,
	// their active verified phone for a phone invitation) does not match the
	// invitation identifier.
	ErrIdentifierMismatch = fmt.Errorf("invitation identifier does not match: %w", sdk.ErrForbidden)
	// ErrNotOwner is returned by Cancel/Resend when the caller is not the
	// invitation's InvitedBy owner.
	ErrNotOwner = fmt.Errorf("not the invitation owner: %w", sdk.ErrForbidden)
	// ErrKindNotSupported is returned by Create for an identifier kind the host
	// is not set up to deliver to (deny-by-absence, ruling 6): a kind is supported
	// iff it is identity.KindEmail with the Mailer wired, OR a notifier of that
	// kind is wired. It wraps sdk.ErrInvalidInput so the transport maps it to 400,
	// and the invitation is NOT created. Package auth re-exports it.
	ErrKindNotSupported = fmt.Errorf("invitation identifier kind is not supported by this host: %w", sdk.ErrInvalidInput)
	// ErrDeliveryDisabled is returned by a send site when no delivery queue is
	// wired: the durable outbox is required to deliver any invitation message
	// (design §6.1.1), so a send with the subsystem off fails loudly.
	ErrDeliveryDisabled = fmt.Errorf("delivery outbox not wired: %w", sdk.ErrForbidden)
)

// deliveryQueue is the durable outbound outbox seam (design §6.1.1): invitation and
// member-added messages enqueue here instead of a request-time provider send, so the
// host-owned worker delivers them off the request path. The shared delivery.Service
// satisfies it; declared here (structural) so invitationsvc accepts an interface.
type deliveryQueue interface {
	Enqueue(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error)
	Replace(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error)
}

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

// IdentifierLookup resolves a caller's active VERIFIED identifier value of kind
// for the accept-time account match (design §7/V11): a phone-kind invitation is
// accepted only by the subject whose verified phone identifier equals the invited
// address. It is an INTERNAL collaborator wired by package auth from
// authsvc.ActiveVerifiedIdentifier, so invitationsvc stays decoupled from the
// identifier store. No active verified identifier of that kind → sdk.ErrNotFound
// (the accept match then fails ErrIdentifierMismatch). Nil → no non-email
// accept-time match is possible (fail closed).
type IdentifierLookup func(ctx context.Context, userID, kind string) (string, error)

// CreateInput is the input to Create. Identifier is the invitee address (the
// service normalizes it kind-aware). IdentifierKind is the address kind; empty
// defaults to identity.KindEmail. Redirect is the requested post-accept
// destination, guarded by the feature's redirect allowlist before delivery.
type CreateInput struct {
	ResourceType   string
	ResourceID     string
	Relation       string
	Identifier     string
	IdentifierKind string
	InvitedBy      string
	AutoAccept     bool
	Redirect       string
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
	Invitations invitation.InvitationRepository
	Granter     Granter
	MemberCheck MemberCheck
	UserLookup  UserLookup
	// CallerIdentifiers resolves the accepting caller's active verified identifier
	// value of a kind for the accept-time account match (design §7/V11). Wired by
	// package auth from authsvc.ActiveVerifiedIdentifier; nil disables the non-email
	// accept-time match (fail closed).
	CallerIdentifiers IdentifierLookup
	// Normalizer canonicalizes invitation identifier values at creation through the
	// SAME injected policy the identifier domain and authsvc use (design §2.2/§7):
	// one normalization result for persistence, lookup, invitations, and the
	// accept-time match. Email and phone route through it (strict addr-spec / strict
	// E.164); open non-email/phone kinds keep trim-only. Nil → the bundled strict
	// identifier.DefaultNormalizer.
	Normalizer identifier.Normalizer
	Mailer     email.Sender
	MailFrom   string
	// Deliver is the shared kind-aware delivery renderer/router (design §6.1),
	// constructor-injected by package auth and shared with authsvc so the two
	// services route outbound through one kind policy instead of two drifting copies.
	// It renders an encrypted-job-ready Envelope and routes a send through the
	// email/notify kind fork; the durable worker (phase 4) consumes it. The
	// invitation/member-added send sites enqueue rendered commands through Queue
	// (AV3-4.3).
	Deliver *delivery.Router
	// Queue is the delivery dispatch seam the send sites enqueue through. Wired
	// whenever a delivery dispatcher is (package auth builds it); nil → outbound
	// disabled.
	Queue          deliveryQueue
	Redirects      redirect.Allowlist
	SecurityEvents securityevent.SecurityEventRepository
	Clock          func() time.Time
	Logger         *slog.Logger
	TTL            time.Duration
	// Notifiers is the host's wired delivery set keyed by kind, built by package
	// auth from Config.Notifiers (duplicate kinds are rejected loudly there). It
	// defines the supported non-email kinds (deny-by-absence, ruling 6) and routes
	// non-email delivery; a wired email-kind notifier also routes invitation mail.
	Notifiers map[string]notify.Notifier
	// IDs is the app-chosen entity-ID strategy (amended D9): it mints
	// invitation and security-event record IDs. Zero value → default nanoids;
	// cryptids.Database delegates to the store. It never mints the mailed
	// invitation secret, which keeps its own unconditional random generator.
	IDs cryptids.IDGenerator
}

// Service implements the invitation use cases over its ports.
type Service struct {
	invitations invitation.InvitationRepository
	granter     Granter
	memberCheck MemberCheck
	userLookup  UserLookup
	// callerIdentifiers resolves the accepting caller's active verified identifier
	// value of a kind for the accept-time account match (Deps.CallerIdentifiers).
	callerIdentifiers IdentifierLookup
	// normalizer canonicalizes invitation identifier values at creation
	// (Deps.Normalizer); email/phone route through it, open kinds keep trim-only.
	normalizer identifier.Normalizer
	mailer     email.Sender
	mailFrom   string
	// deliverer is the shared kind-aware delivery renderer/router (Deps.Deliver): send
	// sites render an envelope through it and enqueue it on queue. Nil until wired.
	deliverer *delivery.Router
	// queue is the durable delivery outbox (Deps.Queue) send sites enqueue through.
	queue          deliveryQueue
	redirects      redirect.Allowlist
	securityEvents securityevent.SecurityEventRepository
	now            func() time.Time
	logger         *slog.Logger
	ttl            time.Duration
	tokenHasher    *cryptids.SHA256Hasher
	// notifiers is the host's wired delivery set keyed by kind (Deps.Notifiers).
	// A wired kind is a supported kind (deny-by-absence); the delivery fork routes
	// through it, falling back to the Mailer only for the email kind.
	notifiers map[string]notify.Notifier
	// ids is the app-chosen entity-ID strategy (Deps.IDs); entity keys only,
	// never the mailed secret.
	ids cryptids.IDGenerator
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
	norm := d.Normalizer
	if norm == nil {
		norm = identifier.DefaultNormalizer{}
	}
	return &Service{
		invitations:       d.Invitations,
		granter:           d.Granter,
		memberCheck:       d.MemberCheck,
		userLookup:        d.UserLookup,
		callerIdentifiers: d.CallerIdentifiers,
		normalizer:        norm,
		mailer:            d.Mailer,
		mailFrom:          d.MailFrom,
		deliverer:         d.Deliver,
		queue:             d.Queue,
		redirects:         d.Redirects,
		securityEvents:    d.SecurityEvents,
		now:               clock,
		logger:            logger,
		ttl:               ttl,
		tokenHasher:       cryptids.NewSHA256Hasher(),
		notifiers:         d.Notifiers,
		ids:               d.IDs,
	}
}

// Create invites Identifier to a resource. The identifier kind (default
// identity.KindEmail) must be a supported kind (kindSupported) or Create fails
// loudly with ErrKindNotSupported before touching the store. When AutoAccept is
// set and the invitee is a known user (email kind only), it is a direct add — an
// immediate grant with no pending record (MemberCheck may veto a duplicate).
// Otherwise a pending invitation is minted and its secret delivered.
func (s *Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	kind := strings.TrimSpace(in.IdentifierKind)
	if kind == "" {
		kind = identity.KindEmail
	}
	in.IdentifierKind = kind

	normalized, err := s.normalizeIdentifier(in.Identifier, kind)
	if err != nil {
		return CreateResult{}, err
	}
	in.Identifier = normalized

	// Deny-by-absence (delta-fold 1): email is always-on via the required Mailer;
	// every other kind requires a wired notifier of that kind. Unsupported kinds
	// never reach the store.
	if !s.kindSupported(kind) {
		return CreateResult{}, ErrKindNotSupported
	}

	subjectID := ""
	// The direct-add resolution is email-keyed (fold 3): only an email identifier
	// can resolve to an account (accounts are email-keyed), so AutoAccept never
	// direct-adds for a non-email kind — it always mints a pending record instead.
	if s.userLookup != nil && kind == identity.KindEmail {
		id, found, err := s.userLookup(ctx, normalized)
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

// kindSupported reports whether the host can deliver an invitation of kind
// (deny-by-absence, ruling 6 / delta-fold 1): the email kind is always supported
// while the required Mailer is wired, and every other kind is supported iff a
// notifier of that kind is wired.
func (s *Service) kindSupported(kind string) bool {
	if kind == identity.KindEmail && s.mailer != nil {
		return true
	}
	_, ok := s.notifiers[kind]
	return ok
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
	s.sendMemberAdded(ctx, in.IdentifierKind, in.Identifier, in.ResourceType, in.ResourceID, in.Relation, in.Redirect, "member_added:"+mintSecret())
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
	inv, err := invitation.New(s.ids, in.ResourceType, in.ResourceID, in.Relation, in.Identifier, in.IdentifierKind, in.InvitedBy, tokenHash, in.AutoAccept, s.ttl, s.now())
	if err != nil {
		return CreateResult{}, err
	}
	if subjectID != "" {
		inv.ResolvedSubjectID = subjectID
	}
	created, err := s.invitations.Create(ctx, inv)
	if err != nil {
		if errors.Is(err, sdk.ErrAlreadyExists) {
			return CreateResult{}, ErrPendingInvitationExists
		}
		return CreateResult{}, err
	}
	s.recordCreated(ctx, created)
	if err := s.sendInviteSent(ctx, created, secret, in.Redirect, false); err != nil {
		return CreateResult{Invitation: created}, err
	}
	return CreateResult{Invitation: created}, nil
}

// Accept redeems a token: it grants the accepting subject the invitation's
// relation, then marks the invitation accepted with the resolved subject id. An
// unknown/expired token surfaces sdk.ErrNotFound/ErrExpired; a non-pending
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
	// Accept-time account match (design §7). EMAIL: the acceptor's email is the
	// trusted claim the inbound handler passes; it must equal the invited address.
	// PHONE (V11): the invited address is matched against the caller's own active
	// VERIFIED phone identifier, resolved through the injected accessor — so a phone
	// invitation is accepted only by the subject who proved that number, not merely
	// by whoever holds the delivered token. Any other (host-declared notifier) kind
	// still binds by address-possession alone (no acceptor-address-of-kind source).
	switch inv.IdentifierKind {
	case identity.KindEmail:
		normalized, err := s.normalizeIdentifier(in.Identifier, inv.IdentifierKind)
		if err != nil || normalized != inv.Identifier {
			return AcceptResult{}, ErrIdentifierMismatch
		}
	case identity.KindPhone:
		if !s.callerIdentifierMatches(ctx, in.SubjectID, inv.IdentifierKind, inv.Identifier) {
			return AcceptResult{}, ErrIdentifierMismatch
		}
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
	s.sendMemberAdded(ctx, inv.IdentifierKind, inv.Identifier, inv.ResourceType, inv.ResourceID, inv.Relation, "", "member_added:"+inv.ID)
	return AcceptResult{ResourceType: inv.ResourceType, ResourceID: inv.ResourceID, Relation: inv.Relation}, nil
}

// Decline marks a pending invitation declined. It is a PUBLIC route, so the
// caller proves they are the invitee by presenting the token; a wrong token
// leaks nothing (sdk.ErrNotFound). No grant happens.
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
	if err := s.sendInviteSent(ctx, updated, secret, redirectTo, true); err != nil {
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
// the Identifier table column, never a tuple). It is email-keyed (fold 3): the
// identifier is normalized as an email (the caller's own address, resolved from
// their account), so the lookup structurally hits email-kind rows only — a
// non-email identifier lives in a different string space (a phone number, a Slack
// id), never the lowercased-email keyspace this pages over.
func (s *Service) Mine(ctx context.Context, address string, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	normalized, err := s.normalizeIdentifier(address, identity.KindEmail)
	if err != nil {
		return crud.Page[invitation.Invitation]{}, err
	}
	return s.invitations.ListBySubject(ctx, identity.KindEmail, normalized, req)
}

// ResolveInvitations grants all pending AUTO-ACCEPT invitations for a just-
// registered/verified email to (subjectType, subjectID), best-effort: one
// failed grant never aborts the caller (a registration), and each failure is
// audited. It returns the number resolved. This is the sole port authsvc holds
// on this service (design §6 pin).
func (s *Service) ResolveInvitations(ctx context.Context, email, subjectType, subjectID string) (int, error) {
	normalized, err := s.normalizeIdentifier(email, identity.KindEmail)
	if err != nil {
		return 0, nil // an unparseable email resolves nothing; never abort registration
	}
	if subjectType == "" {
		subjectType = subjectTypeUser
	}

	resolved := 0
	cursor := ""
	for i := 0; i < 1000; i++ { // bound against a runaway cursor
		page, err := s.invitations.ListBySubject(ctx, identity.KindEmail, normalized, crud.ListRequest{Limit: resolvePageLimit, Cursor: cursor})
		if err != nil {
			return resolved, err
		}
		for _, inv := range page.Items {
			// Resolve-on-registration is email-keyed (fold 3): a just-verified email
			// only ever auto-accepts email-kind rows. A non-email row sharing this
			// identifier string is never granted here (its binding is
			// address-possession at accept, not account-email match).
			if inv.IdentifierKind != identity.KindEmail {
				continue
			}
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
	evt := securityevent.New(s.ids, eventType, status, s.now())
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

// --- delivery ---

// sendInviteSent renders the invitation secret through the shared kind-aware router
// and enqueues the sealed message on the durable outbox (design §6.1.1): the worker
// delivers it off the request path, through the email/notify kind fork the router
// owns. The requested redirect destination is passed through the feature's allowlist
// first (design §3's open-redirect guard), and the accept token rides the rendered
// link. A user-requested resend supersedes the prior pending job (Replace) so an
// invitee never receives two live secrets; a fresh invite enqueues idempotently by
// invitation ID.
func (s *Service) sendInviteSent(ctx context.Context, inv invitation.Invitation, secret, redirectTo string, resend bool) error {
	if s.deliverer == nil || s.queue == nil {
		return ErrDeliveryDisabled
	}
	dest := s.redirects.Resolve(redirectTo)
	env, err := s.deliverer.Render(ctx, delivery.Request{
		Kind:        inv.IdentifierKind,
		Purpose:     delivery.PurposeInvitation,
		Destination: inv.Identifier,
		Secret:      secret,
		Data: map[string]any{
			"ResourceType": inv.ResourceType,
			"ResourceID":   inv.ResourceID,
			"Relation":     inv.Relation,
			"Link":         inviteLink(dest, secret),
		},
	})
	if err != nil {
		return fmt.Errorf("render invitation notification: %w", err)
	}
	cmd := delivery.Command{
		Kind:           inv.IdentifierKind,
		Purpose:        delivery.PurposeInvitation,
		IdempotencyKey: "invitation:" + inv.ID,
		Envelope:       env,
	}
	if resend {
		_, err = s.queue.Replace(ctx, cmd)
	} else {
		_, err = s.queue.Enqueue(ctx, cmd)
	}
	if err != nil {
		return fmt.Errorf("enqueue invitation notification: %w", err)
	}
	return nil
}

// sendMemberAdded renders and enqueues the you-were-added notice through the durable
// outbox. It is best-effort — the grant has already happened, so a render/enqueue
// failure is logged, never surfaced. key deduplicates the notice; callers pass the
// invitation ID (Accept) or a fresh unique key (direct add).
func (s *Service) sendMemberAdded(ctx context.Context, kind, identifier, resourceType, resourceID, relation, redirectTo, key string) {
	if s.deliverer == nil || s.queue == nil {
		s.logger.Warn("member-added notification skipped: delivery outbox not wired")
		return
	}
	dest := s.redirects.Resolve(redirectTo)
	env, err := s.deliverer.Render(ctx, delivery.Request{
		Kind:        kind,
		Purpose:     delivery.PurposeMemberAdded,
		Destination: identifier,
		Data: map[string]any{
			"ResourceType": resourceType,
			"ResourceID":   resourceID,
			"Relation":     relation,
			"Link":         dest,
		},
	})
	if err != nil {
		s.logger.Warn("member-added notification failed", "error_kind", errKind(err))
		return
	}
	if _, err := s.queue.Enqueue(ctx, delivery.Command{
		Kind:           kind,
		Purpose:        delivery.PurposeMemberAdded,
		IdempotencyKey: key,
		Envelope:       env,
	}); err != nil {
		s.logger.Warn("member-added notification failed", "error_kind", errKind(err))
	}
}

// inviteLink builds the accept link the rendered invitation message points the
// invitee at: the allowlisted destination with the single-use token attached as a
// query parameter (the token is what Accept redeems). It is deliberately a simple
// join — the host's landing page reads the token and POSTs it to accept.
func inviteLink(dest, secret string) string {
	sep := "?"
	if strings.Contains(dest, "?") {
		sep = "&"
	}
	return dest + sep + "token=" + secret
}

// --- helpers ---

// callerIdentifierMatches reports whether userID owns an active VERIFIED
// identifier of kind whose normalized value equals want (design §7/V11). It fails
// CLOSED: an unwired accessor, a resolution error (including no such verified
// identifier → sdk.ErrNotFound), or an empty want never matches. Both sides are
// already normalized — the invited address by Create's normalizer, the caller's by
// the identifier rail — so this is a normalized-value equality.
func (s *Service) callerIdentifierMatches(ctx context.Context, userID, kind, want string) bool {
	if s.callerIdentifiers == nil || want == "" {
		return false
	}
	got, err := s.callerIdentifiers(ctx, userID, kind)
	if err != nil {
		return false
	}
	return got == want
}

// hashSecret returns the stored form of an invitation secret — its SHA-256 hex
// digest (cryptids.SHA256Hasher, the same primitive used for API keys and
// session tokens). An empty secret is rejected.
func (s *Service) hashSecret(secret string) (string, error) {
	return s.tokenHasher.Hash(secret)
}

// mintSecret builds a fresh 32-char dotless invitation secret from
// sdk/foundation/cryptids' alphabet — no dots, so it never collides with the JWT-detection
// heuristic and is URL-safe in the mailed link.
func mintSecret() string {
	return (secrets.MustGenerate() + secrets.MustGenerate())[:tokenSecretLen]
}

// normalizeIdentifier canonicalizes the invitee identifier through the SAME
// injected normalizer the identifier domain and authsvc use (design §2.2/§7), so a
// stored invitation matches what resolve-on-registration, "mine", and the accept-
// time match look it up by. Email and phone route through the strict normalizer
// (addr-spec email / strict E.164 phone) — the E.164 convergence is what makes the
// V11 phone accept-time match fire. Open kinds outside {email, phone} keep the
// prior trim-only behavior (the strict normalizer only speaks the closed identity
// vocabulary), so a host-declared notifier kind still works. A blank identifier is
// invalid input. Normalization lives in the service; the entity stores the
// identifier verbatim.
func (s *Service) normalizeIdentifier(value, kind string) (string, error) {
	switch kind {
	case identity.KindEmail, identity.KindPhone:
		return s.normalizer.Normalize(kind, value)
	default:
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			return "", fmt.Errorf("identifier is required: %w", sdk.ErrInvalidInput)
		}
		return normalized, nil
	}
}

// invitationNotFound is the generic not-found returned when a token does not
// resolve, so a public caller cannot probe invitation existence.
func invitationNotFound() error {
	return fmt.Errorf("invitation not found: %w", sdk.ErrNotFound)
}

// errKind reduces err to a coarse, secret-free label for a WARN line (design
// §5.1 WI3 — the log carries event type, status, and error kind only).
func errKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, sdk.ErrAlreadyExists):
		return "already_exists"
	case errors.Is(err, sdk.ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, sdk.ErrNotFound):
		return "not_found"
	case errors.Is(err, sdk.ErrConflict):
		return "conflict"
	default:
		return "unknown"
	}
}
