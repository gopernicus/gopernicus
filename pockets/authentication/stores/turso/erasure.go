package turso

import (
	"context"
	"fmt"
	"strings"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
)

// ErasureStore implements user.EraseRepository over libSQL: one BEGIN
// IMMEDIATE transaction across every table that holds the user's personal
// data, in the port's order — read the user, read the invitations addressed to
// or claimed by the user, refuse on an accepting claim, then write — with the
// user_erased row appended inside it. SQLite has one writer, so the write lock
// taken at BEGIN is the row locks the pgx sibling takes.
type ErasureStore struct {
	db *tursodb.DB
}

var _ user.EraseRepository = (*ErasureStore)(nil)

// NewErasureStore returns an ErasureStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewErasureStore(db *tursodb.DB) *ErasureStore {
	if db == nil {
		panic("authentication turso: NewErasureStore received a nil database")
	}
	return &ErasureStore{db: db}
}

// placeholders renders n bound parameters for an IN list.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// stringArgs converts values to a bind-argument slice.
func stringArgs(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// Erase erases in.ID in one transaction. See user.EraseRepository.
func (s *ErasureStore) Erase(ctx context.Context, in user.EraseInput) (user.Erasure, error) {
	if strings.TrimSpace(in.ID) == "" {
		return user.Erasure{}, fmt.Errorf("authentication turso store: erase: empty id: %w", sdk.ErrInvalidInput)
	}
	id, now := in.ID, in.Now.UTC()
	at := tursodb.FormatTime(now)
	var out user.Erasure
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		// (1) The user, under the write lock BEGIN IMMEDIATE already holds.
		var erasedAt tursodb.NullTime
		if err := tx.QueryRow(ctx, `SELECT erased_at FROM users WHERE id = ?`, id).Scan(&erasedAt); err != nil {
			return tursodb.MapError(err)
		}
		if erasedAt.Valid {
			out = user.Erasure{Changed: false, ErasedAt: erasedAt.Time}
			return nil
		}

		// (2)+(3) The invitations addressed to one of the user's (kind, value)
		// pairs, active or retired, that no other user holds ACTIVE now —
		// unresolved or resolved to the user — and those resolved to the user.
		rows, err := tx.Query(ctx, `SELECT v.id, v.status FROM invitations v
			WHERE (v.resolved_subject_id IN ('', ?) AND EXISTS (
					SELECT 1 FROM user_identifiers i
					WHERE i.user_id = ? AND i.normalized_value <> ''
						AND i.kind = v.identifier_kind AND i.normalized_value = v.identifier
						AND NOT EXISTS (SELECT 1 FROM user_identifiers o
							WHERE o.kind = i.kind AND o.normalized_value = i.normalized_value
								AND o.user_id <> ? AND o.replaced_at IS NULL)))
				OR (v.resolved_subject_type = 'user' AND v.resolved_subject_id = ?)
			ORDER BY v.id`, id, id, id, id)
		if err != nil {
			return err
		}
		var pending, terminal []string
		blocked := false
		for rows.Next() {
			var invID, status string
			if err := rows.Scan(&invID, &status); err != nil {
				rows.Close()
				return tursodb.MapError(err)
			}
			switch status {
			case invitations.StatusAccepting:
				blocked = true
			case invitations.StatusPending:
				pending = append(pending, invID)
			default:
				terminal = append(terminal, invID)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return tursodb.MapError(err)
		}
		if err := rows.Close(); err != nil {
			return tursodb.MapError(err)
		}
		// (4) An accepting claim is the host's grant in flight: refuse.
		if blocked {
			return user.ErrEraseBlockedByClaim
		}

		exec := func(count *int, q string, args ...any) error {
			n, err := tursodb.ExecAffecting(ctx, tx, q, args...)
			if err != nil {
				return err
			}
			if count != nil {
				*count = int(n)
			}
			return nil
		}
		var c user.EraseCounts
		// (5) The marker. The status column stays deactivated; readers project
		// erased from erased_at.
		if err := exec(nil, `UPDATE users
			SET status_changed_at = CASE WHEN status = 'deactivated' THEN status_changed_at ELSE ? END,
				status = 'deactivated', erased_at = ?, display_name = '', updated_at = ?,
				auth_revision = auth_revision + 1
			WHERE id = ?`, at, at, at, id); err != nil {
			return err
		}
		// (6) Every identifier row, active and retired: retired and blanked.
		if err := exec(&c.IdentifiersBlanked, `UPDATE user_identifiers
			SET replaced_at = COALESCE(replaced_at, ?), normalized_value = '', verified_at = NULL,
				login_enabled = 0, recovery_enabled = 0, notification_enabled = 0, is_primary = 0, updated_at = ?
			WHERE user_id = ?`, at, at, id); err != nil {
			return err
		}

		// (7) Credentials, sessions and their satellites. Grants go before the
		// sessions they hang off, by user and by session.
		var keys []string
		for _, k := range in.SubjectKeys {
			if k != "" {
				keys = append(keys, k)
			}
		}
		challenges, challengeArgs := `DELETE FROM challenges WHERE user_id = ?`, []any{id}
		if len(keys) > 0 {
			challenges += ` OR subject_key IN (` + placeholders(len(keys)) + `)`
			challengeArgs = append(challengeArgs, stringArgs(keys)...)
		}
		for _, d := range []struct {
			count *int
			q     string
			args  []any
		}{
			{&c.Passwords, `DELETE FROM user_passwords WHERE user_id = ?`, []any{id}},
			{&c.OAuthAccounts, `DELETE FROM oauth_accounts WHERE user_id = ?`, []any{id}},
			{&c.ExternalSubjects, `DELETE FROM external_subjects WHERE user_id = ?`, []any{id}},
			{&c.ContactChanges, `DELETE FROM contact_changes WHERE user_id = ?`, []any{id}},
			{&c.Codes, `DELETE FROM oauth_authorization_codes WHERE user_id = ?`, []any{id}},
			{&c.Challenges, challenges, challengeArgs},
			{&c.Grants, `DELETE FROM authentication_grants WHERE user_id = ? OR session_id IN (SELECT id FROM sessions WHERE user_id = ?)`, []any{id, id}},
			{&c.Sessions, `DELETE FROM sessions WHERE user_id = ?`, []any{id}},
		} {
			if err := exec(d.count, d.q, d.args...); err != nil {
				return err
			}
		}
		// The passkeys only when the host wired passkeys: without them 0025 may
		// never have been applied, so the table is not named.
		if in.Passkeys {
			if err := exec(&c.Passkeys, `DELETE FROM passkey_credentials WHERE user_id = ?`, id); err != nil {
				return err
			}
		}
		// The TOTP tables only when the host wired TOTP: without it 0026 may
		// never have been applied, so neither table is named.
		if in.TOTP {
			if err := exec(&c.TOTP, `DELETE FROM totp_credentials WHERE user_id = ?`, id); err != nil {
				return err
			}
			if err := exec(&c.RecoveryCodes, `DELETE FROM recovery_codes WHERE user_id = ?`, id); err != nil {
				return err
			}
		}

		// (8) The read invitations: pending ones go; terminal ones keep their ids
		// (invited_by, resolved_subject_id/type) and lose the address and the
		// host metadata. Then the pending invitations the user issued are
		// cancelled.
		if len(pending) > 0 {
			if err := exec(&c.InvitationsDeleted, `DELETE FROM invitations WHERE id IN (`+placeholders(len(pending))+`)`, stringArgs(pending)...); err != nil {
				return err
			}
		}
		if len(terminal) > 0 {
			args := append([]any{at}, stringArgs(terminal)...)
			if err := exec(&c.InvitationsBlanked, `UPDATE invitations SET identifier = '', metadata = '{}', updated_at = ?
				WHERE id IN (`+placeholders(len(terminal))+`)`, args...); err != nil {
				return err
			}
		}
		if err := exec(&c.InvitationsCancelled, `UPDATE invitations SET status = 'cancelled', updated_at = ?
			WHERE invited_by = ? AND status = 'pending'`, at, id); err != nil {
			return err
		}

		// (9) The user's act-as service accounts and every key they hold.
		accountsOf := `SELECT id FROM service_accounts WHERE owner_user_id = ? AND act_as_user = 1`
		if err := exec(&c.APIKeys, `DELETE FROM api_keys WHERE service_account_id IN (`+accountsOf+`)`, id); err != nil {
			return err
		}
		if err := exec(&c.ServiceAccounts, `DELETE FROM service_accounts WHERE owner_user_id = ? AND act_as_user = 1`, id); err != nil {
			return err
		}

		// (10) The evidence, in the same transaction.
		if in.Event.EventType != "" {
			evt := in.Event
			evt.Details = c.Merge(evt.Details)
			if _, err := insertSecurityEvent(ctx, tx, evt); err != nil {
				return err
			}
		}
		out = user.Erasure{Changed: true, ErasedAt: now, Counts: c}
		return nil
	})
	if err != nil {
		return user.Erasure{}, err
	}
	return out, nil
}
