package authmem

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	user "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	invitation "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ user.EraseRepository = erasureRepo{}

// erasureRepo is the user-erasure capability (SOC 2 S3). The whole Erase runs
// in one critical section under the shared mutex — this store's stand-in for
// the one transaction a SQL store runs — in the port's order: lock, capture,
// refuse on an accepting claim, then write.
type erasureRepo struct{ *data }

// identifierPair is one (kind, normalized value) an erased user held.
type identifierPair struct{ kind, value string }

func (r erasureRepo) Erase(ctx context.Context, in user.EraseInput) (user.Erasure, error) {
	if strings.TrimSpace(in.ID) == "" {
		return user.Erasure{}, fmt.Errorf("authmem: erase: empty id: %w", sdk.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return user.Erasure{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := in.ID
	u, ok := r.users[id]
	if !ok {
		return user.Erasure{}, sdk.ErrNotFound
	}
	if at, erased := r.erasedAt[id]; erased {
		return user.Erasure{Changed: false, ErasedAt: at}, nil
	}

	// The user's pairs, active and retired, minus any another user holds now:
	// a retired address someone else owns must not reach their invitations.
	pairs := map[identifierPair]bool{}
	for _, it := range r.identifiers {
		if it.UserID == id && it.NormalizedValue != "" {
			pairs[identifierPair{string(it.Kind), it.NormalizedValue}] = true
		}
	}
	for _, it := range r.identifiers {
		if it.UserID != id && it.Active() {
			delete(pairs, identifierPair{string(it.Kind), it.NormalizedValue})
		}
	}
	var addressed []string
	for invID, inv := range r.invitations {
		matched := pairs[identifierPair{inv.IdentifierKind, inv.Identifier}] && (inv.ResolvedSubjectID == "" || inv.ResolvedSubjectID == id)
		resolved := inv.ResolvedSubjectType == "user" && inv.ResolvedSubjectID == id
		if !matched && !resolved {
			continue
		}
		if inv.Status == invitation.StatusAccepting {
			return user.Erasure{}, user.ErrEraseBlockedByClaim
		}
		addressed = append(addressed, invID)
	}

	now := in.Now.UTC()
	var c user.EraseCounts
	if user.NormalizeStatus(u.Status) == user.StatusActive {
		u.Status = user.StatusDeactivated
		u.StatusChangedAt = now
	}
	u.DisplayName = ""
	u.UpdatedAt = now
	u.AuthRevision++
	r.users[id] = u
	r.erasedAt[id] = now

	for iid, it := range r.identifiers {
		if it.UserID != id {
			continue
		}
		if it.ReplacedAt.IsZero() {
			it.ReplacedAt = now
		}
		it.NormalizedValue = ""
		it.VerifiedAt = time.Time{}
		it.LoginEnabled, it.RecoveryEnabled, it.NotificationEnabled, it.IsPrimary = false, false, false, false
		it.UpdatedAt = now
		r.identifiers[iid] = it
		c.IdentifiersBlanked++
	}
	if _, ok := r.passwords[id]; ok {
		delete(r.passwords, id)
		c.Passwords = 1
	}
	if in.Passkeys {
		for _, p := range r.passkeysOfLocked(id) {
			delete(r.passkeys, p.ID)
			c.Passkeys++
		}
	}
	if in.TOTP {
		if _, ok := r.totpCreds[id]; ok {
			delete(r.totpCreds, id)
			c.TOTP = 1
		}
		c.RecoveryCodes = len(r.totpCodes[id])
		delete(r.totpCodes, id)
	}
	accounts := r.oauthAccounts[:0:0]
	for _, a := range r.oauthAccounts {
		if a.UserID == id {
			c.OAuthAccounts++
			continue
		}
		accounts = append(accounts, a)
	}
	r.oauthAccounts = accounts
	for key, s := range r.externalSubjects {
		if s.UserID == id {
			delete(r.externalSubjects, key)
			c.ExternalSubjects++
		}
	}
	for key, p := range r.contactChanges {
		if p.UserID == id {
			delete(r.contactChanges, key)
			c.ContactChanges++
		}
	}
	for hash, code := range r.oauthCodes {
		if code.UserID == id {
			delete(r.oauthCodes, hash)
			c.Codes++
		}
	}
	for cid, ch := range r.challenges {
		if ch.UserID == id || slices.Contains(in.SubjectKeys, ch.SubjectKey) {
			delete(r.challenges, cid)
			c.Challenges++
		}
	}
	sessions := map[string]bool{}
	for sid, s := range r.sessions {
		if s.UserID == id {
			sessions[sid] = true
			r.deleteSession(sid)
			c.Sessions++
		}
	}
	for gid, g := range r.authGrants {
		if g.UserID == id || sessions[g.SessionID] {
			delete(r.authGrants, gid)
			c.Grants++
		}
	}
	for _, invID := range addressed {
		inv := r.invitations[invID]
		if inv.Status == invitation.StatusPending {
			delete(r.invitations, invID)
			c.InvitationsDeleted++
			continue
		}
		inv.Identifier = ""
		inv.Metadata = map[string]string{}
		inv.UpdatedAt = now
		r.invitations[invID] = inv
		c.InvitationsBlanked++
	}
	for invID, inv := range r.invitations {
		if inv.InvitedBy == id && inv.Status == invitation.StatusPending {
			inv.Status = invitation.StatusCancelled
			inv.UpdatedAt = now
			r.invitations[invID] = inv
			c.InvitationsCancelled++
		}
	}
	for said, sa := range r.serviceAccounts {
		if sa.OwnerUserID == id && sa.ActAsUser {
			c.APIKeys += r.deleteAccountLocked(said).KeyCount
			c.ServiceAccounts++
		}
	}
	if in.Event.EventType != "" {
		evt := in.Event
		evt.Details = c.Merge(evt.Details)
		if evt.ID == "" {
			evt.ID = ids.MustGenerate()
		}
		r.securityEvents = append(r.securityEvents, evt)
	}
	return user.Erasure{Changed: true, ErasedAt: now, Counts: c}, nil
}
