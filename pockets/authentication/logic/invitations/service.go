// Package invitations implements resource invitation workflows and owns their
// entities, repository ports and host grant policy. New constructs an independent
// service; the pocket root also composes it with verified identity resolution.
// CreateAuthorized and ListByResourceAuthorized require the host InviteCheck.
// Trusted composition methods leave authorization to their caller.
//
// Grant coupling: the grant on accept / direct-add / resolve rides the
// host-supplied Granter. Invitation VISIBILITY never touches a tuple — it rides
// this domain's own table columns (Identifier for "mine" and the resolve
// finder; InvitedBy for cancel/resend ownership). A host with no ReBAC has no
// "invitation" resource type, and nothing here pretends otherwise.
package invitations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/internal/redirect"
	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
	"github.com/gopernicus/gopernicus/sdk/pkg/cryptids"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// secrets generates the opaque invitation secrets this service mints with the
// default nanoid shape. Deliberately NOT the app's entity-ID strategy
// (WithIDs): secret entropy must never follow a wiring choice like
// sdk.DatabaseID.
var secrets = sdk.IDGenerator{}

const (
	// defaultInvitationTTL is the lifetime of a minted invitation secret when
	// WithTTL is unset (salvaged 7-day default).
	defaultInvitationTTL = 7 * 24 * time.Hour
	// tokenSecretLen is the length of the generated invitation secret: 32 chars,
	// dotless (sdk' alphabet), plaintext only in the mail.
	tokenSecretLen = 32
	// subjectTypeUser is the ReBAC subject-type convention for a human user —
	// the only subject class this service grants to (accept, direct-add, resolve).
	subjectTypeUser = "user"
	// resolvePageLimit bounds one page of the resolve-on-registration scan.
	resolvePageLimit = 100
)

// Errors surfaced to the transport (each wraps a stable errs kind so sdk/pkg/web
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
	// iff it is sdk.AddressKindEmail with the Mailer wired, OR a notifier of that
	// kind is wired. It wraps sdk.ErrInvalidInput so the transport maps it to 400,
	// and the invitation is NOT created.
	ErrKindNotSupported = fmt.Errorf("invitation identifier kind is not supported by this host: %w", sdk.ErrInvalidInput)
	// ErrDeliveryDisabled is returned by a send site when no delivery queue is
	// wired: the durable outbox is required to deliver any invitation message
	// (design §6.1.1), so a send with the subsystem off fails loudly.
	ErrDeliveryDisabled = fmt.Errorf("delivery outbox not wired: %w", sdk.ErrForbidden)
	// errEmptyOperationID is the internal fail-closed guard: every grant carries a
	// non-empty operation ID (the durable invitation row ID for accept/resolve, a
	// minted high-entropy ID for direct-add). An empty one is a wiring/logic bug,
	// so the grant is refused rather than sent with an unidentified operation. It
	// wraps no domain sentinel, so the transport maps it to 500 (an internal fault),
	// never a caller-actionable status.
	errEmptyOperationID = errors.New("invitation grant operation id is empty")
	// errInviteCheckNotWired is the fail-closed guard on the AUTHORIZED operations:
	// package auth requires an InviteCheck whenever a Granter enables invitations
	// (ErrInviteCheckRequired), so reaching an authorized operation without one is a
	// wiring bug — refused, never allowed by default. It wraps no domain sentinel, so
	// the transport maps it to 500.
	errInviteCheckNotWired = errors.New("invitation authorization check is not wired")
)

// deliveryQueue is the durable outbound outbox seam (design §6.1.1): invitation and
// member-added messages enqueue here instead of a request-time provider send, so the
// host-owned worker delivers them off the request path. The shared delivery.Service
// satisfies it; declared here (structural) so invitation service accepts an interface.
type deliveryQueue interface {
	Enqueue(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error)
	Replace(ctx context.Context, cmd delivery.Command) (delivery.Receipt, error)
}

// GrantInput is the structured request for one invitation grant (design §2.2, D1).
// OperationID is an opaque, non-empty, non-secret identifier of THIS logical
// invitation grant: the persisted invitation row ID for pending-accept and
// resolve-on-registration (a retry of the same invitation reuses the same ID; a
// later invitation row for the same tuple gets a different ID), and a freshly
// minted high-entropy value for direct-add (no invitation row exists). It is not
// authority and the pocket does not dictate how the host uses it: guarded
// adapters may derive durable mutation idempotency while baseline state adapters
// may ignore it. The remaining fields are the ReBAC tuple: grant
// SubjectType/SubjectID the Relation on
// (ResourceType, ResourceID).
//
// Metadata is opaque, host-owned routing data the inviter set at create and the
// pocket round-trips verbatim to every grant path (accept, direct-add, resolve).
// The pocket never interprets it. It is a DEFENSIVE COPY and always non-nil (an
// empty map when there is no metadata), so a Granter may read it freely. It is
// UNTRUSTED inviter-supplied input, never an authorization claim by itself: a
// Granter applying any security-sensitive side effect from it must revalidate.
type GrantInput struct {
	OperationID  string
	ResourceType string
	ResourceID   string
	Relation     string
	SubjectType  string
	SubjectID    string
	Metadata     map[string]string
}

// Granter grants a subject a relation on a resource — the ONE ReBAC-decoupled
// seam (design §2.2), called on accept, direct-add, and resolve-on-registration
// and NOTHING else. A ReBAC host adapts it to CreateRelationships; a role-column
// host to a role write; the proof host to a toy in-memory membership map. Grants
// must be idempotent for concurrent and repeated calls with one OperationID.
// The invitation claim is durable, but cannot atomically commit the host grant.
// nil means the EXACT requested relation was applied or was already exactly
// present; a different existing relation, an invariant refusal, and a
// missing/deleted host resource are NOT success and must return an error, and
// infrastructure failures propagate (design §6/D2).
type Granter interface {
	Grant(ctx context.Context, in GrantInput) error
}

// MemberCheck is the optional duplicate-membership predicate consulted before a
// direct-add grant. Nil → no dup check (idempotent grants absorb duplicates).
type MemberCheck func(ctx context.Context, resourceType, resourceID, subjectType, subjectID string) (bool, error)

// InviteAction is the invitation operation a host authorization policy
// (InviteCheck) is asked about (design §6/D3): creating an invitation or listing
// a resource's invitations.
type InviteAction string

const (
	// InviteCreate is the create-an-invitation action; the check carries the exact
	// requested Relation so the host can prevent privilege escalation (e.g. an
	// editor inviting an owner).
	InviteCreate InviteAction = "create"
	// InviteList is the list-a-resource's-invitations action; the check carries an
	// empty Relation.
	InviteList InviteAction = "list"
)

// InviteCheckRequest is the parsed, principal-resolved authorization question the
// authorized invitation operations pose to the host policy (design §6/D3).
// Relation is set for InviteCreate and empty for InviteList. The pocket owns
// parsing, normalization, and the invitee lookup, so the host sees the caller,
// resource, action, and — for create — the exact validated relation and the
// complete invitee context, which a RouteRegistrar decorator cannot.
type InviteCheckRequest struct {
	Principal    sdk.Principal
	Action       InviteAction
	ResourceType string
	ResourceID   string
	Relation     string
	// Metadata is the parsed, opaque host routing data of a create request (empty
	// for InviteList). It is UNTRUSTED inviter-supplied input the pocket does not
	// interpret, surfaced here so the host can authorize the COMPLETE invitation —
	// including a routing key it will later act on in its Granter. It is a defensive
	// copy; empty when the request carried none.
	Metadata map[string]string
	// Identifier is the pocket-normalized invitee identifier. It is empty for
	// InviteList.
	Identifier string
	// IdentifierKind is the normalized identifier kind. It is empty for InviteList
	// and makes an empty ResolvedSubjectID unambiguous for kinds the pocket cannot
	// resolve today.
	IdentifierKind string
	// ResolvedSubjectID is the existing subject the identifier resolves to, or ""
	// when it is unknown or the kind is not resolvable. The pocket's lookup is
	// EMAIL-KIND ONLY today, so an empty value must never be read as proof that the
	// invitee is new.
	ResolvedSubjectID string
}

// InviteCheck is the host authorization seam the authorized invitation operations
// (CreateAuthorized, ListByResourceAuthorized) call after live-session validation,
// principal resolution, request parsing, metadata validation, identifier
// normalization, and the invitee lookup — and always before any row exists or a
// grant is attempted (design §6/D3). It is REQUIRED whenever a Granter enables
// invitations — package auth rejects a nil InviteCheck at construction
// (ErrInviteCheckRequired), never an allow-by-default. A nil return authorizes; a
// denial (wrapping sdk.ErrForbidden) or an infrastructure error fails closed
// through the normal web/sdk error path. Authority is issuance-time: a create-time
// authorization is a durable capability and acceptance never re-runs inviter
// authority.
type InviteCheck func(ctx context.Context, req InviteCheckRequest) error

// UserLookup resolves an invitee email to an existing user's subject id for the
// direct-add path. It returns only a user with active verified email ownership.
// found=false means no such verified owner (→ a pending invitation is
// created instead). It is an INTERNAL collaborator wired by package auth from
// the Users repository — not a host configuration seam.
type UserLookup func(ctx context.Context, email string) (subjectID string, found bool, err error)

// IdentifierLookup resolves a caller's active VERIFIED identifier value of kind
// for the accept-time account match (design §7/V11): a phone-kind invitation is
// accepted only by the subject whose verified phone identifier equals the invited
// address. It is an INTERNAL collaborator wired by package auth from
// authlogic.ActiveVerifiedIdentifier, so invitation service stays decoupled from the
// identifier store. No active verified identifier of that kind → sdk.ErrNotFound
// (the accept match then fails ErrIdentifierMismatch). Nil → no email/phone
// accept-time match is possible (fail closed).
type IdentifierLookup func(ctx context.Context, userID, kind string) (string, error)

// CreateInput is the input to Create. Identifier is the invitee address (the
// service normalizes it kind-aware). IdentifierKind is the address kind; empty
// defaults to sdk.AddressKindEmail. Redirect is the requested post-accept
// destination, guarded by the pocket's redirect allowlist before delivery.
type CreateInput struct {
	ResourceType   string
	ResourceID     string
	Relation       string
	Identifier     string
	IdentifierKind string
	InvitedBy      string
	AutoAccept     bool
	Redirect       string
	// Metadata is opaque, host-owned routing data that rides the invitation from
	// create to the Granter seam. The pocket validates only shape/size (see
	// ValidateMetadata) and never interprets it; nil/empty is the
	// no-metadata case.
	Metadata map[string]string
}

// CreateResult reports the outcome of Create. DirectlyAdded is true when a known
// invitee was granted immediately (no pending record); otherwise Invitation
// carries the created pending record.
type CreateResult struct {
	DirectlyAdded bool
	Invitation    Invitation
}

// preparedCreate is the result of create preparation: the normalized, validated
// CreateInput and the subject the invitee identifier resolved to ("" when unknown
// or the kind is not resolvable). It is what the authorization seam is shown and
// what the side-effect path consumes, so the host authorizes exactly the
// invitation the pocket would then act on.
type preparedCreate struct {
	input     CreateInput
	subjectID string
}

// AcceptInput is the input to Accept. Token is the plaintext secret from the
// invitation mail; SubjectType/SubjectID is the accepting caller; Identifier is
// retained for source compatibility; verified ownership comes from the configured
// identifier reader, never this caller-supplied value.
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

// constructorConfig holds the collaborators assembled by the root after
// it validates the required wiring. Granter and Invitations are required (auth
// enforces both at construction); MemberCheck, UserLookup, and SecurityEvents
// are optional.
type constructorConfig struct {
	Invitations       InvitationRepository
	Granter           Granter
	MemberCheck       MemberCheck
	UserLookup        UserLookup
	InviteCheck       InviteCheck
	CallerIdentifiers IdentifierLookup
	Normalizer        identifier.Normalizer
	Mailer            email.Sender
	MailFrom          string
	Deliver           *delivery.Router
	Queue             deliveryQueue
	RedirectAllowlist []string
	SecurityEvents    securityevent.SecurityEventRepository
	Clock             func() time.Time
	Logger            *slog.Logger
	TTL               time.Duration
	BodySenders       map[string]delivery.BodySender
	IDs               sdk.IDGenerator
}

// Service implements the invitation use cases over its ports.
type Service struct {
	invitations InvitationRepository
	granter     Granter
	memberCheck MemberCheck
	userLookup  UserLookup
	// inviteCheck is the host invitation authorization policy (AccessConfig.InviteCheck),
	// consulted by the authorized operations only.
	inviteCheck InviteCheck
	// callerIdentifiers resolves the accepting caller's active verified identifier
	// value of a kind for the accept-time account match (AccessConfig.CallerIdentifiers).
	callerIdentifiers IdentifierLookup
	// normalizer canonicalizes invitation identifier values at creation
	// (WithNormalizer); email/phone route through it, open kinds keep trim-only.
	normalizer identifier.Normalizer
	mailer     email.Sender
	mailFrom   string
	// deliverer is the shared kind-aware delivery renderer/router (DeliveryConfig.Deliver): send
	// sites render an envelope through it and enqueue it on queue. Nil until wired.
	deliverer *delivery.Router
	// queue is the durable delivery outbox (DeliveryConfig.Queue) send sites enqueue through.
	queue          deliveryQueue
	redirects      redirect.Allowlist
	securityEvents securityevent.SecurityEventRepository
	now            func() time.Time
	logger         *slog.Logger
	ttl            time.Duration
	// bodySenders is the host's wired delivery set keyed by kind (DeliveryConfig.BodySenders).
	// A wired kind is a supported kind (deny-by-absence); the delivery fork routes
	// through it, falling back to the Mailer only for the email kind.
	bodySenders map[string]delivery.BodySender
	// ids is the app-chosen entity-ID strategy (WithIDs); entity keys only,
	// never the mailed secret.
	ids sdk.IDGenerator
}

// newService builds a Service, applying a time.Now clock, slog default logger, and the
// default TTL when unset.
func newService(d constructorConfig) *Service {
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
		inviteCheck:       d.InviteCheck,
		callerIdentifiers: d.CallerIdentifiers,
		normalizer:        norm,
		mailer:            d.Mailer,
		mailFrom:          d.MailFrom,
		deliverer:         d.Deliver,
		queue:             d.Queue,
		redirects:         redirect.New(d.RedirectAllowlist),
		securityEvents:    d.SecurityEvents,
		now:               clock,
		logger:            logger,
		ttl:               ttl,

		bodySenders: d.BodySenders,
		ids:         d.IDs,
	}
}

// Create invites Identifier to a resource. The identifier kind (default
// sdk.AddressKindEmail) must be a supported kind (kindSupported) or Create fails
// loudly with ErrKindNotSupported before touching the store. When AutoAccept is
// set and the invitee is a known user (email kind only), it is a direct add — an
// immediate grant with no pending record (MemberCheck may veto a duplicate).
// Otherwise a pending invitation is minted and its secret delivered.
//
// This is the TRUSTED composition entry point: it never poses the host
// InviteCheck. A caller driving it directly owns that authorization decision;
// CreateAuthorized is the policy-carrying twin the shipped HTTP adapter uses.
func (s *Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	prepared, err := s.prepareCreate(ctx, in)
	if err != nil {
		return CreateResult{}, err
	}
	return s.createPrepared(ctx, prepared)
}

// CreateAuthorized is Create with the host invitation policy posed in between:
// it prepares the request (metadata validation, kind default, identifier
// normalization, supported-kind check, invitee lookup), poses InviteCheck with the
// COMPLETE invitee context — normalized identifier, normalized kind, and the
// resolved subject when the lookup found one — and only on a nil check calls the
// side-effect path. A denial or an infrastructure error therefore leaves NO
// pending row and attempts NO grant on either branch. principal is the resolved
// caller (the inviter), never the invitee.
func (s *Service) CreateAuthorized(ctx context.Context, principal sdk.Principal, in CreateInput) (CreateResult, error) {
	prepared, err := s.prepareCreate(ctx, in)
	if err != nil {
		return CreateResult{}, err
	}
	if err := s.authorizeInvite(ctx, InviteCheckRequest{
		Principal:    principal,
		Action:       InviteCreate,
		ResourceType: prepared.input.ResourceType,
		ResourceID:   prepared.input.ResourceID,
		Relation:     prepared.input.Relation,
		// The policy gets its OWN clone: a check that mutates the map it is handed can
		// never alter the value this call then persists and grants.
		Metadata:          CloneMetadata(prepared.input.Metadata),
		Identifier:        prepared.input.Identifier,
		IdentifierKind:    prepared.input.IdentifierKind,
		ResolvedSubjectID: prepared.subjectID,
	}); err != nil {
		return CreateResult{}, err
	}
	return s.createPrepared(ctx, prepared)
}

// prepareCreate runs every side-effect-free step of a create: host-metadata
// validation FIRST (an oversized/invalid map is rejected before anything else),
// then the identifier kind default, the kind-aware normalization, the
// deny-by-absence supported-kind check, and the invitee lookup.
func (s *Service) prepareCreate(ctx context.Context, in CreateInput) (preparedCreate, error) {
	// Validate host metadata FIRST — before identifier normalization, user lookup,
	// or membership checks — so an oversized/invalid map is rejected with no side
	// effects. The validated defensive copy is what the pending row or direct-add
	// grant carries downstream.
	metadata, err := ValidateMetadata(in.Metadata)
	if err != nil {
		return preparedCreate{}, err
	}
	in.Metadata = metadata

	kind := strings.TrimSpace(in.IdentifierKind)
	if kind == "" {
		kind = sdk.AddressKindEmail
	}
	in.IdentifierKind = kind

	normalized, err := s.normalizeIdentifier(in.Identifier, kind)
	if err != nil {
		return preparedCreate{}, err
	}
	in.Identifier = normalized

	// Deny-by-absence (delta-fold 1): email is always-on via the required Mailer;
	// every other kind requires a wired notifier of that kind. Unsupported kinds
	// never reach the store.
	if !s.kindSupported(kind) {
		return preparedCreate{}, ErrKindNotSupported
	}

	subjectID := ""
	// The direct-add resolution is email-keyed (fold 3): only an email identifier
	// can resolve to an account (accounts are email-keyed), so AutoAccept never
	// direct-adds for a non-email kind — it always mints a pending record instead.
	// A non-email kind therefore leaves subjectID empty without ever asking.
	if s.userLookup != nil && kind == sdk.AddressKindEmail {
		id, found, err := s.userLookup(ctx, normalized)
		if err != nil {
			return preparedCreate{}, fmt.Errorf("lookup invitee: %w", err)
		}
		if found {
			subjectID = id
		}
	}
	return preparedCreate{input: in, subjectID: subjectID}, nil
}

// createPrepared is the create SIDE-EFFECT path: a known invitee on an
// auto-accept request is granted immediately, everything else mints a pending
//
//	Nothing here validates or normalizes — prepareCreate already did.
func (s *Service) createPrepared(ctx context.Context, p preparedCreate) (CreateResult, error) {
	if p.input.AutoAccept && p.subjectID != "" {
		return s.directAdd(ctx, p.input, p.subjectID)
	}
	return s.createPending(ctx, p.input, p.subjectID)
}

// authorizeInvite poses one question to the host invitation policy. It fails
// CLOSED on an unwired check: package auth requires one whenever a Granter enables
// invitations, so an authorized operation without a policy is a wiring bug, never
// an allow.
func (s *Service) authorizeInvite(ctx context.Context, req InviteCheckRequest) error {
	if s.inviteCheck == nil {
		return errInviteCheckNotWired
	}
	return s.inviteCheck(ctx, req)
}

// kindSupported reports whether the host can deliver an invitation of kind
// (deny-by-absence, ruling 6 / delta-fold 1): the email kind is always supported
// while the required Mailer is wired, and every other kind is supported iff a
// notifier of that kind is wired.
func (s *Service) kindSupported(kind string) bool {
	if kind == sdk.AddressKindEmail && s.mailer != nil {
		return true
	}
	_, ok := s.bodySenders[kind]
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
	// Direct-add has no invitation row, so it mints a fresh high-entropy operation
	// ID immediately before the grant (D1): a distinct logical operation each time,
	// from the unconditional secret generator — NOT the entity WithIDs strategy,
	// whose sdk.DatabaseID mode yields an empty ID until an entity is inserted.
	operationID := mintOperationID()
	if err := s.grant(ctx, operationID, in.ResourceType, in.ResourceID, in.Relation, subjectTypeUser, subjectID, in.Metadata); err != nil {
		s.recordGrant(ctx, subjectID, in.ResourceType, in.ResourceID, in.Relation, in.Identifier, securityevent.StatusFailure)
		return CreateResult{}, fmt.Errorf("grant: %w", err)
	}
	s.recordGrant(ctx, subjectID, in.ResourceType, in.ResourceID, in.Relation, in.Identifier, securityevent.StatusSuccess)
	s.sendMemberAdded(ctx, memberAdded{
		kind: in.IdentifierKind, identifier: in.Identifier,
		resourceType: in.ResourceType, resourceID: in.ResourceID, relation: in.Relation,
		invitedBy: in.InvitedBy, operationID: operationID, metadata: in.Metadata,
		redirectTo: in.Redirect, key: "member_added:" + mintSecret(),
	})
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
	inv, err := NewWithMetadata(s.ids, in.ResourceType, in.ResourceID, in.Relation, in.Identifier, in.IdentifierKind, in.InvitedBy, tokenHash, in.AutoAccept, s.ttl, s.now(), in.Metadata)
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

// Accept claims the current invitation token before granting. The claim binds
// its verified subject durably, so an ambiguous grant/finalization failure can
// resume with the same operation ID even after token expiry or service restart.
func (s *Service) Accept(ctx context.Context, in AcceptInput) (AcceptResult, error) {
	tokenHash, err := s.hashSecret(in.Token)
	if err != nil {
		return AcceptResult{}, invitationNotFound()
	}
	inv, err := s.invitations.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		return AcceptResult{}, err
	}
	subjectType := in.SubjectType
	if subjectType == "" {
		subjectType = subjectTypeUser
	}
	if strings.TrimSpace(in.SubjectID) == "" {
		return AcceptResult{}, sdk.ErrInvalidInput
	}
	if inv.Status == StatusPending {
		switch inv.IdentifierKind {
		case sdk.AddressKindEmail, sdk.AddressKindPhone:
			if err := s.requireVerifiedIdentifier(ctx, in.SubjectID, inv.IdentifierKind, inv.Identifier); err != nil {
				return AcceptResult{}, err
			}
		}
	}
	accepted, err := s.acceptClaim(ctx, inv, subjectType, in.SubjectID)
	if err != nil {
		return AcceptResult{}, err
	}
	return AcceptResult{ResourceType: accepted.ResourceType, ResourceID: accepted.ResourceID, Relation: accepted.Relation}, nil
}

func (s *Service) acceptClaim(ctx context.Context, inv Invitation, subjectType, subjectID string) (Invitation, error) {
	claim := Acceptance{TokenHash: inv.TokenHash, SubjectType: subjectType, SubjectID: subjectID, Now: s.now()}
	claimed, err := s.invitations.ClaimAcceptance(ctx, inv.ID, claim)
	if err != nil {
		return Invitation{}, err
	}
	if claimed.Status == StatusAccepted {
		return claimed, nil
	}
	// The host must deduplicate concurrent as well as sequential repeats. An error
	// cannot prove that an external side effect did not commit, so never unclaim.
	if err := s.grant(ctx, claimed.ID, claimed.ResourceType, claimed.ResourceID, claimed.Relation, subjectType, subjectID, claimed.Metadata); err != nil {
		s.recordGrant(ctx, subjectID, claimed.ResourceType, claimed.ResourceID, claimed.Relation, claimed.Identifier, securityevent.StatusFailure)
		return Invitation{}, fmt.Errorf("grant: %w", err)
	}
	claim.Now = s.now()
	accepted, err := s.invitations.CompleteAcceptance(ctx, claimed.ID, claim)
	if err != nil {
		return Invitation{}, err
	}
	s.recordGrant(ctx, subjectID, claimed.ResourceType, claimed.ResourceID, claimed.Relation, claimed.Identifier, securityevent.StatusSuccess)
	s.sendMemberAdded(ctx, memberAdded{
		kind: claimed.IdentifierKind, identifier: claimed.Identifier,
		resourceType: claimed.ResourceType, resourceID: claimed.ResourceID, relation: claimed.Relation,
		invitedBy: claimed.InvitedBy, invitationID: claimed.ID, operationID: claimed.ID, metadata: claimed.Metadata,
		key: "member_added:" + claimed.ID,
	})
	return accepted, nil
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
	if inv.Status != StatusPending {
		return ErrNotPending
	}
	now := s.now()
	if _, err := s.invitations.UpdateStatus(ctx, id, StatusUpdate{
		ExpectedTokenHash: inv.TokenHash,
		Status:            StatusDeclined,
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
	if inv.Status != StatusPending {
		return ErrNotPending
	}
	now := s.now()
	if _, err := s.invitations.UpdateStatus(ctx, id, StatusUpdate{
		ExpectedTokenHash: inv.TokenHash,
		Status:            StatusCancelled,
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
func (s *Service) Resend(ctx context.Context, id, currentUserID, redirectTo string) (Invitation, error) {
	inv, err := s.invitations.Get(ctx, id)
	if err != nil {
		return Invitation{}, err
	}
	if inv.InvitedBy != currentUserID {
		return Invitation{}, ErrNotOwner
	}
	if inv.Status != StatusPending && inv.Status != StatusExpired {
		return Invitation{}, ErrNotPending
	}
	secret := mintSecret()
	tokenHash, err := s.hashSecret(secret)
	if err != nil {
		return Invitation{}, err
	}
	now := s.now()
	updated, err := s.invitations.UpdateStatus(ctx, id, StatusUpdate{
		ExpectedTokenHash: inv.TokenHash,
		Status:            StatusPending,
		TokenHash:         tokenHash,
		ExpiresAt:         now.UTC().Add(s.ttl),
		ResolvedSubjectID: inv.ResolvedSubjectID,
		UpdatedAt:         now,
	})
	if err != nil {
		return Invitation{}, err
	}
	s.recordCreated(ctx, updated)
	if err := s.sendInviteSent(ctx, updated, secret, redirectTo, true); err != nil {
		return updated, err
	}
	return updated, nil
}

// ListByResource returns a cursor-paginated page of a resource's invitations
// (ordered created_at DESC, id DESC).
//
// This is the TRUSTED composition entry point: it never poses the host
// InviteCheck. ListByResourceAuthorized is the policy-carrying twin the shipped
// HTTP adapter uses.
func (s *Service) ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[Invitation], error) {
	return s.invitations.ListByResource(ctx, resourceType, resourceID, req)
}

// ListByResourceAuthorized is ListByResource with the host invitation policy posed
// first: an InviteList question carrying the resolved caller and the resource, and
// — per the seam's contract — an empty Relation, Metadata, Identifier,
// IdentifierKind, and ResolvedSubjectID (there is no invitee in a list). A denial
// or an infrastructure error fails closed before the repository is read.
func (s *Service) ListByResourceAuthorized(ctx context.Context, principal sdk.Principal, resourceType, resourceID string, req list.Request) (list.Page[Invitation], error) {
	if err := s.authorizeInvite(ctx, InviteCheckRequest{
		Principal:    principal,
		Action:       InviteList,
		ResourceType: resourceType,
		ResourceID:   resourceID,
	}); err != nil {
		return list.Page[Invitation]{}, err
	}
	return s.ListByResource(ctx, resourceType, resourceID, req)
}

// Mine returns a cursor-paginated page of the invitations addressed to an
// invitee identifier (email) — the caller's own invitations (design §6: rides
// the Identifier table column, never a tuple). It is email-keyed (fold 3): the
// identifier is normalized as an email (the caller's own address, resolved from
// their account), so the lookup structurally hits email-kind rows only — a
// non-email identifier lives in a different string space (a phone number, a Slack
// id), never the lowercased-email keyspace this pages over.
func (s *Service) Mine(ctx context.Context, address string, req list.Request) (list.Page[Invitation], error) {
	normalized, err := s.normalizeIdentifier(address, sdk.AddressKindEmail)
	if err != nil {
		return list.Page[Invitation]{}, err
	}
	return s.invitations.ListBySubject(ctx, sdk.AddressKindEmail, normalized, req)
}

// ResolveInvitations resumes auto-accept invitations only for a verified email
// owner. Individual grant/finalization failures remain resumable and do not
// prevent other invitations from resolving.
func (s *Service) ResolveInvitations(ctx context.Context, email, subjectType, subjectID string) (int, error) {
	normalized, err := s.normalizeIdentifier(email, sdk.AddressKindEmail)
	if err != nil {
		return 0, nil
	}
	if subjectType == "" {
		subjectType = subjectTypeUser
	}
	if err := s.requireVerifiedIdentifier(ctx, subjectID, sdk.AddressKindEmail, normalized); err != nil {
		if errors.Is(err, ErrIdentifierMismatch) {
			return 0, nil
		}
		return 0, err
	}
	resolved := 0
	cursor := ""
	for i := 0; i < 1000; i++ {
		page, err := s.invitations.ListBySubject(ctx, sdk.AddressKindEmail, normalized, list.Request{Limit: resolvePageLimit, Cursor: cursor})
		if err != nil {
			return resolved, err
		}
		for _, inv := range page.Items {
			if inv.IdentifierKind != sdk.AddressKindEmail || !inv.AutoAccept {
				continue
			}
			if inv.Status != StatusPending && inv.Status != StatusAccepting {
				continue
			}
			if inv.Status == StatusPending && inv.Expired(s.now()) {
				continue
			}
			if _, err := s.acceptClaim(ctx, inv, subjectType, subjectID); err != nil {
				s.logger.Warn("resolve invitation failed", "error_kind", errKind(err))
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
func (s *Service) recordCreated(ctx context.Context, inv Invitation) {
	s.record(ctx, inv.InvitedBy, securityevent.TypeInvitationCreated, securityevent.StatusSuccess, map[string]any{
		"resource_type": inv.ResourceType,
		"resource_id":   inv.ResourceID,
		"relation":      inv.Relation,
		"identifier":    inv.Identifier,
	})
}

// recordLifecycle appends a decline/cancel audit row, attributed to userID.
func (s *Service) recordLifecycle(ctx context.Context, inv Invitation, eventType, userID string) {
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
	ip, ua := authlogic.ClientInfoFromContext(ctx)
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
// owns. The requested redirect destination is passed through the pocket's allowlist
// first (design §3's open-redirect guard), and the accept token rides the rendered
// link. A user-requested resend supersedes the prior pending job (Replace) so an
// invitee never receives two live secrets; a fresh invite enqueues idempotently by
// invitation ID.
func (s *Service) sendInviteSent(ctx context.Context, inv Invitation, secret, redirectTo string, resend bool) error {
	if s.deliverer == nil || s.queue == nil {
		return ErrDeliveryDisabled
	}
	dest := s.redirects.Resolve(redirectTo)
	env, err := s.deliverer.Render(ctx, delivery.Request{
		Kind:        inv.IdentifierKind,
		Purpose:     delivery.PurposeInvitation,
		Destination: inv.Identifier,
		Secret:      secret,
		Data:        deliveryData(inv.ID, inv.ID, inv.ResourceType, inv.ResourceID, inv.Relation, inv.InvitedBy, inv.Metadata, inviteLink(dest, secret)),
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

// memberAdded is the context sendMemberAdded renders from: the delivery address,
// the granted tuple, and the grant's provenance. invitationID is the persisted
// invitation row for an accepted invitation and EMPTY for a direct add (no row
// exists); operationID is the grant's operation ID either way (the row ID for an
// accept, the freshly minted ID for a direct add). key deduplicates the notice.
type memberAdded struct {
	kind, identifier                     string
	resourceType, resourceID, relation   string
	invitedBy, invitationID, operationID string
	metadata                             map[string]string
	redirectTo, key                      string
}

// sendMemberAdded renders and enqueues the you-were-added notice through the durable
// outbox. It is best-effort — the grant has already happened, so a render/enqueue
// failure (a host DeliveryData hook error included) is logged, never surfaced.
// m.key deduplicates the notice; callers pass the invitation ID (Accept) or a
// fresh unique key (direct add).
func (s *Service) sendMemberAdded(ctx context.Context, m memberAdded) {
	if s.deliverer == nil || s.queue == nil {
		s.logger.Warn("member-added notification skipped: delivery outbox not wired")
		return
	}
	dest := s.redirects.Resolve(m.redirectTo)
	env, err := s.deliverer.Render(ctx, delivery.Request{
		Kind:        m.kind,
		Purpose:     delivery.PurposeMemberAdded,
		Destination: m.identifier,
		Data:        deliveryData(m.invitationID, m.operationID, m.resourceType, m.resourceID, m.relation, m.invitedBy, m.metadata, dest),
	})
	if err != nil {
		s.logger.Warn("member-added notification failed", "error_kind", errKind(err))
		return
	}
	if _, err := s.queue.Enqueue(ctx, delivery.Command{
		Kind:           m.kind,
		Purpose:        delivery.PurposeMemberAdded,
		IdempotencyKey: m.key,
		Envelope:       env,
	}); err != nil {
		s.logger.Warn("member-added notification failed", "error_kind", errKind(err))
	}
}

// deliveryData builds the secret-free template data for an invitation or
// member-added render — the per-purpose contract the README documents and a
// host DeliveryData hook receives. ResourceName, ResourceKind, RelationLabel, and
// InviterName default to empty: the pocket has no source for them, and the
// bundled bodies render {{or .ResourceName .ResourceID}} so a hook that supplies
// only a name is immediately useful. Metadata is a defensive copy (non-nil).
func deliveryData(invitationID, operationID, resourceType, resourceID, relation, invitedBy string, metadata map[string]string, link string) map[string]any {
	return map[string]any{
		"InvitationID":  invitationID,
		"OperationID":   operationID,
		"ResourceType":  resourceType,
		"ResourceID":    resourceID,
		"ResourceName":  "",
		"ResourceKind":  "",
		"Relation":      relation,
		"RelationLabel": "",
		"InvitedBy":     invitedBy,
		"InviterName":   "",
		"Metadata":      CloneMetadata(metadata),
		"Link":          link,
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

// grant invokes the host Granter for one logical invitation grant (D1). It
// enforces the non-empty operation-ID invariant BEFORE any Granter call: an empty
// operationID is an internal bug (the durable invitation ID and the minted
// direct-add ID are always non-empty), so the grant is refused fail-closed with
// errEmptyOperationID rather than sent as an unidentified operation. A nil return
// carries the strengthened success contract (D2): the exact requested relation is
// effective; any non-applied outcome is a non-nil error the caller propagates.
// metadata is the opaque host routing data for this grant; it is copied defensively
// onto GrantInput so the Granter cannot mutate persisted/delivered state, and the
// Granter must treat it as UNTRUSTED and reject invalid or unauthorized routing.
func (s *Service) grant(ctx context.Context, operationID, resourceType, resourceID, relation, subjectType, subjectID string, metadata map[string]string) error {
	if operationID == "" {
		return errEmptyOperationID
	}
	return s.granter.Grant(ctx, GrantInput{
		OperationID:  operationID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Relation:     relation,
		SubjectType:  subjectType,
		SubjectID:    subjectID,
		// A non-nil defensive copy so the host receives exactly what the inviter set
		// and cannot mutate persisted or subsequently delivered state (D-metadata).
		Metadata: CloneMetadata(metadata),
	})
}

// requireVerifiedIdentifier resolves ownership from the verified identifier
// rail; an email supplied in AcceptInput is never proof of account ownership.
func (s *Service) requireVerifiedIdentifier(ctx context.Context, userID, kind, want string) error {
	if s.callerIdentifiers == nil || want == "" || userID == "" {
		return ErrIdentifierMismatch
	}
	got, err := s.callerIdentifiers(ctx, userID, kind)
	if errors.Is(err, sdk.ErrNotFound) {
		return ErrIdentifierMismatch
	}
	if err != nil {
		return fmt.Errorf("lookup verified invitation identifier: %w", err)
	}
	if got != want {
		return ErrIdentifierMismatch
	}
	return nil
}

// hashSecret returns the stored form of an invitation secret — its SHA-256 hex
// digest (cryptids.SHA256, the same primitive used for API keys and
// session tokens). An empty secret is rejected.
func (s *Service) hashSecret(secret string) (string, error) {
	return cryptids.SHA256(secret)
}

// mintSecret builds a fresh 32-char dotless invitation secret from
// sdk' alphabet — no dots, so it never collides with the JWT-detection
// heuristic and is URL-safe in the mailed link.
func mintSecret() string {
	return (secrets.MustGenerate() + secrets.MustGenerate())[:tokenSecretLen]
}

// mintOperationID mints a fresh, non-empty, high-entropy operation ID for the
// direct-add grant path, which has no persisted invitation row to identify it
// (D1). It draws from the SAME unconditional random generator as mintSecret —
// deliberately NOT the entity WithIDs strategy, whose sdk.DatabaseID mode
// yields an empty ID until an entity is inserted (and direct-add inserts none).
// It is an operation identity, not a secret, so entropy sufficiency is all that
// matters here.
func mintOperationID() string {
	return (secrets.MustGenerate() + secrets.MustGenerate())[:tokenSecretLen]
}

// normalizeIdentifier canonicalizes the invitee identifier through the SAME
// injected normalizer the identifier domain and authentication service use (design §2.2/§7), so a
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
	case sdk.AddressKindEmail, sdk.AddressKindPhone:
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
