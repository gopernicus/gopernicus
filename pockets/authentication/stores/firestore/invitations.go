package firestore

import (
	"context"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	invitation "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

var _ invitation.InvitationRepository = (*invitationStore)(nil)

// invitationStore fills invitation.InvitationRepository over the invitations
// collection and its two claims — the total token-hash claim and the PARTIAL
// pending-tuple claim (SCHEMA.md §5.5, §5.6). Every write goes through
// invitations_doc.go's putInvitation/updateInvitation, so a row and both of its
// claims can never move apart.
type invitationStore struct {
	db *firestoredb.DB
}

func newInvitationStore(db *firestoredb.DB) *invitationStore {
	return &invitationStore{db: db}
}

// Create persists a new invitation with its token claim and — while the stored
// status is pending — its pending-tuple claim. A collision on EITHER claim is
// sdk.ErrAlreadyExists and nothing is written.
//
// It performs NO read. Every rule it can break is a document that does not yet
// exist — the invitations primary key, the token hash, the pending tuple — and
// each is written with Create, whose precondition the SERVER evaluates at
// commit. A check-then-write would be slower AND weaker (ruling R3: a claim is
// READ only when it is being handed over). The three writes still take one
// transaction, because a row whose claim did not commit is a duplicate waiting
// to happen.
func (s *invitationStore) Create(ctx context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitation.Invitation{}, err
	}
	if inv.Status != invitation.StatusPending {
		return invitation.Invitation{}, sdk.ErrInvalidInput
	}
	var row invitationDoc
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		var err error
		if row, err = newInvitationDoc(inv); err != nil {
			return err
		}
		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := putInvitation(ctx, s.db, w, plan, row); err != nil {
			return err
		}
		return plan.commit(ctx, w)
	})
	if err != nil {
		return invitation.Invitation{}, err
	}
	inv.ID = row.ID
	return inv, nil
}

// Get returns the invitation for id, or sdk.ErrNotFound. Expiry is NOT a filter
// here: only the token-hash read surfaces the read-time expired state, exactly
// as the SQL adapters do.
func (s *invitationStore) Get(ctx context.Context, id string) (invitation.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitation.Invitation{}, err
	}
	row, err := readInvitation(ctx, s.db, s.db.ReaderFrom(ctx), id)
	if err != nil {
		return invitation.Invitation{}, err
	}
	return row.toDomain()
}

// GetByTokenHash resolves the mailed secret's hash through its claim document;
// unknown → sdk.ErrNotFound, present-but-past-ExpiresAt → sdk.ErrExpired.
//
// The claim and the row are read under ONE snapshot: they are two documents
// describing one fact, and a resend landing between the two reads would
// otherwise report a live invitation as unknown while its token is merely
// moving.
//
// The expiry decision is deliberately OUTSIDE the snapshot's transaction — it
// changes nothing, and the row's stored status stays pending. That is the
// asymmetry SCHEMA.md §5.6 pins: this read observes expiry, and the pending
// claim is untouched by it.
func (s *invitationStore) GetByTokenHash(ctx context.Context, tokenHash string) (invitation.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitation.Invitation{}, err
	}
	var row invitationDoc
	err := s.db.ReadSnapshot(ctx, func(ctx context.Context, r firestoredb.Reader) error {
		found, err := readInvitationByTokenClaim(ctx, s.db, r, tokenHash)
		if err != nil {
			return err
		}
		row = found
		return nil
	})
	if err != nil {
		return invitation.Invitation{}, err
	}
	inv, err := row.toDomain()
	if err != nil {
		return invitation.Invitation{}, err
	}
	if inv.Status != invitation.StatusAccepting && inv.Status != invitation.StatusAccepted && inv.Expired(time.Now()) {
		return invitation.Invitation{}, sdk.ErrExpired
	}
	return inv, nil
}

// ListByResource pages a resource's invitations on the derived resource key,
// ordered (created_at DESC, id DESC) by default.
func (s *invitationStore) ListByResource(ctx context.Context, resourceType, resourceID string, req list.Request) (list.Page[invitation.Invitation], error) {
	if err := refuseAmbient(ctx); err != nil {
		return list.Page[invitation.Invitation]{}, err
	}
	return firestoredb.List(ctx, s.db.ReaderFrom(ctx), listInvitationsByResource(s.db, resourceType, resourceID), req)
}

// ListBySubject pages the invitee's invitations on the derived subject key,
// which carries the KIND as well as the address — a value shared across kinds
// never cross-resolves.
func (s *invitationStore) ListBySubject(ctx context.Context, kind, identifierValue string, req list.Request) (list.Page[invitation.Invitation], error) {
	if err := refuseAmbient(ctx); err != nil {
		return list.Page[invitation.Invitation]{}, err
	}
	return firestoredb.List(ctx, s.db.ReaderFrom(ctx), listInvitationsBySubject(s.db, kind, identifierValue), req)
}

// UpdateStatus changes an unclaimed invitation only while its token matches.
func (s *invitationStore) UpdateStatus(ctx context.Context, id string, upd invitation.StatusUpdate) (invitation.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitation.Invitation{}, err
	}
	if err := upd.Validate(); err != nil {
		return invitation.Invitation{}, err
	}
	return s.transition(ctx, id, func(current invitation.Invitation) (invitation.Invitation, error) { return current.UpdateStatus(upd) })
}

// ClaimAcceptance binds the current token to a subject before the host grant.
func (s *invitationStore) ClaimAcceptance(ctx context.Context, id string, claim invitation.Acceptance) (invitation.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitation.Invitation{}, err
	}
	if err := claim.Validate(); err != nil {
		return invitation.Invitation{}, err
	}
	return s.transition(ctx, id, func(current invitation.Invitation) (invitation.Invitation, error) {
		return current.ClaimAcceptance(claim)
	})
}

// CompleteAcceptance finalizes a matching claim, preserving repeated completion timestamps.
func (s *invitationStore) CompleteAcceptance(ctx context.Context, id string, claim invitation.Acceptance) (invitation.Invitation, error) {
	if err := refuseAmbient(ctx); err != nil {
		return invitation.Invitation{}, err
	}
	if err := claim.Validate(); err != nil {
		return invitation.Invitation{}, err
	}
	return s.transition(ctx, id, func(current invitation.Invitation) (invitation.Invitation, error) {
		return current.CompleteAcceptance(claim)
	})
}

// The lifecycle decision and both uniqueness claims commit against the same read.
func (s *invitationStore) transition(ctx context.Context, id string, apply func(invitation.Invitation) (invitation.Invitation, error)) (invitation.Invitation, error) {
	var result invitation.Invitation
	err := retryTransact(ctx, s.db, func(ctx context.Context) error {
		result = invitation.Invitation{}
		old, err := readInvitation(ctx, s.db, s.db.ReaderFrom(ctx), id)
		if err != nil {
			return err
		}
		current, err := old.toDomain()
		if err != nil {
			return err
		}
		next, err := apply(current)
		if err != nil {
			return err
		}
		row, err := newInvitationDoc(next)
		if err != nil {
			return err
		}
		w := s.db.WriterFrom(ctx)
		plan := newClaimPlan()
		if err := updateInvitation(ctx, s.db, w, plan, old, row); err != nil {
			return err
		}
		if err := plan.commit(ctx, w); err != nil {
			return err
		}
		result, err = row.toDomain()
		return err
	})
	if err != nil {
		return invitation.Invitation{}, err
	}
	return result, nil
}
