package pgx

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// ErasureStore implements user.EraseRepository over PostgreSQL: one transaction
// across every table that holds the user's personal data, in the port's order —
// lock the user, lock the invitations addressed to or claimed by the user,
// refuse on an accepting claim, then write — and the user_erased row appended
// inside it.
type ErasureStore struct {
	db *pgxdb.DB
	qualified
}

var _ user.EraseRepository = (*ErasureStore)(nil)

// NewErasureStore returns an ErasureStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewErasureStore(db *pgxdb.DB, opts ...Option) *ErasureStore {
	if db == nil {
		panic("authentication pgx: NewErasureStore received a nil database")
	}
	return &ErasureStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// lockedInvitation is one invitation the erase locked.
type lockedInvitation struct {
	ID     string `db:"id"`
	Status string `db:"status"`
}

// Erase erases in.ID in one transaction. See user.EraseRepository.
func (s *ErasureStore) Erase(ctx context.Context, in user.EraseInput) (user.Erasure, error) {
	if strings.TrimSpace(in.ID) == "" {
		return user.Erasure{}, fmt.Errorf("authentication pgx store: erase: empty id: %w", sdk.ErrInvalidInput)
	}
	// now is truncated to the microsecond PostgreSQL stores, so the ErasedAt
	// this call returns equals the one every replay reads back.
	id, now := in.ID, in.Now.UTC().Truncate(time.Microsecond)
	var out user.Erasure
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		// (1) The user row lock serializes against the fenced mint and the
		// service-account insert, which both take FOR SHARE on it.
		var erasedAt *time.Time
		if err := tx.QueryRow(ctx, `SELECT erased_at FROM `+s.table(usersTable)+` WHERE id = @id FOR UPDATE`,
			pgx.NamedArgs{"id": id}).Scan(&erasedAt); err != nil {
			return pgxdb.MapError(err)
		}
		if erasedAt != nil {
			out = user.Erasure{Changed: false, ErasedAt: erasedAt.UTC()}
			return nil
		}

		// (2)+(3) The user's (kind, value) pairs, active and retired, less any
		// pair another user holds ACTIVE now; then the invitations addressed to
		// a pair (unresolved or resolved to the user) or resolved to the user,
		// row-locked so a concurrent ClaimAcceptance waits and then finds the
		// row gone or unchanged.
		locked, err := pgxdb.Collect[lockedInvitation](ctx, tx, `WITH pairs AS (
				SELECT DISTINCT i.kind, i.normalized_value FROM `+s.table(identifiersTable)+` i
				WHERE i.user_id = @id AND i.normalized_value <> ''
					AND NOT EXISTS (SELECT 1 FROM `+s.table(identifiersTable)+` o
						WHERE o.kind = i.kind AND o.normalized_value = i.normalized_value
							AND o.user_id <> @id AND o.replaced_at IS NULL)
			)
			SELECT v.id, v.status FROM `+s.table(invitationsTable)+` v
			WHERE ((v.identifier_kind, v.identifier) IN (SELECT kind, normalized_value FROM pairs) AND v.resolved_subject_id IN ('', @id))
				OR (v.resolved_subject_type = 'user' AND v.resolved_subject_id = @id)
			ORDER BY v.id
			FOR UPDATE OF v`, pgx.NamedArgs{"id": id})
		if err != nil {
			return err
		}
		// (4) An accepting claim is the host's grant in flight: refuse.
		var pending, terminal []string
		for _, inv := range locked {
			switch inv.Status {
			case invitations.StatusAccepting:
				return user.ErrEraseBlockedByClaim
			case invitations.StatusPending:
				pending = append(pending, inv.ID)
			default:
				terminal = append(terminal, inv.ID)
			}
		}

		var c user.EraseCounts
		args := pgx.NamedArgs{"id": id, "now": now}
		// (5) The marker. The status column stays deactivated; readers project
		// erased from erased_at.
		if _, err := tx.Exec(ctx, `UPDATE `+s.table(usersTable)+`
			SET status_changed_at = CASE WHEN status = 'deactivated' THEN status_changed_at ELSE @now END,
				status = 'deactivated', erased_at = @now, display_name = '', updated_at = @now,
				auth_revision = auth_revision + 1
			WHERE id = @id`, args); err != nil {
			return err
		}
		// (6) Every identifier row, active and retired: retired and blanked.
		n, err := pgxdb.ExecAffecting(ctx, tx, `UPDATE `+s.table(identifiersTable)+`
			SET replaced_at = COALESCE(replaced_at, @now), normalized_value = '', verified_at = NULL,
				login_enabled = FALSE, recovery_enabled = FALSE, notification_enabled = FALSE,
				is_primary = FALSE, updated_at = @now
			WHERE user_id = @id`, args)
		if err != nil {
			return err
		}
		c.IdentifiersBlanked = int(n)

		// (7) Credentials, sessions and their satellites. Grants go before the
		// sessions they hang off, by user and by session.
		keys := slices.DeleteFunc(slices.Clone(in.SubjectKeys), func(k string) bool { return k == "" })
		for _, d := range []struct {
			count *int
			sql   string
			args  pgx.NamedArgs
		}{
			{&c.Passwords, `DELETE FROM ` + s.table(passwordsTable) + ` WHERE user_id = @id`, args},
			{&c.Passkeys, `DELETE FROM ` + s.table(passkeyCredentialsTable) + ` WHERE user_id = @id`, args},
			{&c.OAuthAccounts, `DELETE FROM ` + s.table(oauthAccountsTable) + ` WHERE user_id = @id`, args},
			{&c.ExternalSubjects, `DELETE FROM ` + s.table(externalSubjectsTable) + ` WHERE user_id = @id`, args},
			{&c.ContactChanges, `DELETE FROM ` + s.table(contactChangesTable) + ` WHERE user_id = @id`, args},
			{&c.Codes, `DELETE FROM ` + s.table(oauthCodesTable) + ` WHERE user_id = @id`, args},
			{&c.Challenges, `DELETE FROM ` + s.table(challengesTable) + ` WHERE user_id = @id OR subject_key = ANY(@keys)`,
				pgx.NamedArgs{"id": id, "keys": keys}},
			{&c.Grants, `DELETE FROM ` + s.table(authGrantsTable) + ` WHERE user_id = @id
				OR session_id IN (SELECT id FROM ` + s.table(sessionsTable) + ` WHERE user_id = @id)`, args},
			{&c.Sessions, `DELETE FROM ` + s.table(sessionsTable) + ` WHERE user_id = @id`, args},
		} {
			n, err := pgxdb.ExecAffecting(ctx, tx, d.sql, d.args)
			if err != nil {
				return err
			}
			*d.count = int(n)
		}
		// The TOTP tables only when the host wired TOTP: without it 0026 may
		// never have been applied, so neither table is named. The credential row
		// goes before its codes, the order every TOTP writer locks them in.
		if in.TOTP {
			for _, d := range []struct {
				count *int
				table string
			}{{&c.TOTP, totpCredentialsTable}, {&c.RecoveryCodes, recoveryCodesTable}} {
				n, err := pgxdb.ExecAffecting(ctx, tx, `DELETE FROM `+s.table(d.table)+` WHERE user_id = @id`, args)
				if err != nil {
					return err
				}
				*d.count = int(n)
			}
		}

		// (8) The locked invitations: pending ones go; terminal ones keep their
		// ids (invited_by, resolved_subject_id/type) and lose the address and
		// the host metadata. Then the pending invitations the user issued are
		// cancelled, so an offboarded issuer's invites stop granting.
		if len(pending) > 0 {
			n, err := pgxdb.ExecAffecting(ctx, tx, `DELETE FROM `+s.table(invitationsTable)+` WHERE id = ANY(@ids)`, pgx.NamedArgs{"ids": pending})
			if err != nil {
				return err
			}
			c.InvitationsDeleted = int(n)
		}
		if len(terminal) > 0 {
			n, err := pgxdb.ExecAffecting(ctx, tx, `UPDATE `+s.table(invitationsTable)+`
				SET identifier = '', metadata = '{}', updated_at = @now WHERE id = ANY(@ids)`, pgx.NamedArgs{"ids": terminal, "now": now})
			if err != nil {
				return err
			}
			c.InvitationsBlanked = int(n)
		}
		n, err = pgxdb.ExecAffecting(ctx, tx, `UPDATE `+s.table(invitationsTable)+`
			SET status = 'cancelled', updated_at = @now WHERE invited_by = @id AND status = 'pending'`, args)
		if err != nil {
			return err
		}
		c.InvitationsCancelled = int(n)

		// (9) The user's act-as service accounts and every key they hold; the
		// accounts the user owns without act-as are the host's to reassign.
		rows, err := tx.Query(ctx, `SELECT id FROM `+s.table(serviceAccountsTable)+`
			WHERE owner_user_id = @id AND act_as_user ORDER BY id FOR UPDATE`, args)
		if err != nil {
			return err
		}
		accounts, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return pgxdb.MapError(err)
		}
		if len(accounts) > 0 {
			n, err := pgxdb.ExecAffecting(ctx, tx, `DELETE FROM `+s.table(apiKeysTable)+` WHERE service_account_id = ANY(@ids)`, pgx.NamedArgs{"ids": accounts})
			if err != nil {
				return err
			}
			c.APIKeys = int(n)
			n, err = pgxdb.ExecAffecting(ctx, tx, `DELETE FROM `+s.table(serviceAccountsTable)+` WHERE id = ANY(@ids)`, pgx.NamedArgs{"ids": accounts})
			if err != nil {
				return err
			}
			c.ServiceAccounts = int(n)
		}

		// (10) The evidence, in the same transaction: a crash after commit cannot
		// lose the only row naming the administrator.
		if in.Event.EventType != "" {
			evt := in.Event
			evt.Details = c.Merge(evt.Details)
			if _, err := insertSecurityEvent(ctx, tx, s.table(securityEventsTable), evt); err != nil {
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
