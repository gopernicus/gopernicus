package turso

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// CredentialMutationStore implements credential.MutationRepository over a libSQL
// database (design §5.6). Snapshot projects the typed MethodSet from the credential
// source tables (user_passwords, oauth_accounts, user_identifiers) alongside the
// user's auth_revision. Apply performs one revision-CAS mutation in a single
// transaction: it reads the users row's auth_revision, rejects a stale revision as
// sdk.ErrConflict, mutates exactly the targeted typed source, and increments
// auth_revision exactly once. libSQL/SQLite has no FOR UPDATE, so single-winner
// rests on the connector's serialized writes (the identifier ApplyVerifiedChange
// convention): the losing concurrent Apply's transaction aborts. The policy is the
// service's job before Apply; this store only serializes.
type CredentialMutationStore struct {
	db *tursodb.DB
}

var _ credential.MutationRepository = (*CredentialMutationStore)(nil)

// NewCredentialMutationStore returns a CredentialMutationStore backed by db.
func NewCredentialMutationStore(db *tursodb.DB) *CredentialMutationStore {
	return &CredentialMutationStore{db: db}
}

// Snapshot returns the user's current MethodSet, including the AuthRevision it was
// read at. An unknown user returns sdk.ErrNotFound.
func (s *CredentialMutationStore) Snapshot(ctx context.Context, userID string) (credential.MethodSet, error) {
	var set credential.MethodSet
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT auth_revision FROM users WHERE id = ?`, userID).Scan(&revision); err != nil {
			return tursodb.MapError(err)
		}
		set.AuthRevision = revision

		var hasPassword tursodb.Bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_passwords WHERE user_id = ?)`, userID).
			Scan(&hasPassword); err != nil {
			return tursodb.MapError(err)
		}
		set.HasPassword = bool(hasPassword)

		oauth, err := tx.Query(ctx, `SELECT provider FROM oauth_accounts WHERE user_id = ? ORDER BY provider`, userID)
		if err != nil {
			return err
		}
		for oauth.Next() {
			var provider string
			if err := oauth.Scan(&provider); err != nil {
				oauth.Close()
				return tursodb.MapError(err)
			}
			set.OAuth = append(set.OAuth, credential.OAuthMethod{Provider: provider, Assurance: session.AssuranceAAL1})
		}
		if err := oauth.Err(); err != nil {
			oauth.Close()
			return tursodb.MapError(err)
		}
		oauth.Close()

		idents, err := tx.Query(ctx,
			`SELECT id, kind, login_enabled, recovery_enabled, notification_enabled, is_primary, verified_at
				FROM user_identifiers WHERE user_id = ? AND replaced_at IS NULL ORDER BY created_at, id`, userID)
		if err != nil {
			return err
		}
		for idents.Next() {
			var (
				m            credential.IdentifierMethod
				login        tursodb.Bool
				recovery     tursodb.Bool
				notification tursodb.Bool
				primary      tursodb.Bool
				verifiedAt   tursodb.NullTime
			)
			if err := idents.Scan(&m.ID, &m.Kind, &login, &recovery, &notification, &primary, &verifiedAt); err != nil {
				idents.Close()
				return tursodb.MapError(err)
			}
			m.Uses = credential.IdentifierUses{Login: bool(login), Recovery: bool(recovery), Notification: bool(notification)}
			m.Primary = bool(primary)
			m.Verified = verifiedAt.Valid
			set.Identifiers = append(set.Identifiers, m)
		}
		if err := idents.Err(); err != nil {
			idents.Close()
			return tursodb.MapError(err)
		}
		idents.Close()
		return nil
	})
	if err != nil {
		return credential.MethodSet{}, err
	}
	return set, nil
}

// Apply performs mutation atomically, incrementing auth_revision exactly once on
// success. A stale expectedAuthRevision → sdk.ErrConflict; an unknown user →
// sdk.ErrNotFound; the mutation is never partially applied.
func (s *CredentialMutationStore) Apply(ctx context.Context, userID string, expectedAuthRevision int64, mutation credential.Mutation) error {
	return s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		var current int64
		if err := tx.QueryRow(ctx, `SELECT auth_revision FROM users WHERE id = ?`, userID).Scan(&current); err != nil {
			return tursodb.MapError(err)
		}
		if current != expectedAuthRevision {
			return sdk.ErrConflict
		}

		now := tursodb.FormatTime(time.Now())
		switch m := mutation.(type) {
		case credential.RemovePassword:
			if _, err := tx.Exec(ctx, `DELETE FROM user_passwords WHERE user_id = ?`, userID); err != nil {
				return err
			}
		case credential.UnlinkOAuth:
			if _, err := tx.Exec(ctx, `DELETE FROM oauth_accounts WHERE user_id = ? AND provider = ?`, userID, m.Provider); err != nil {
				return err
			}
		case credential.RetireIdentifier:
			if _, err := tx.Exec(ctx,
				`UPDATE user_identifiers SET replaced_at = ?, updated_at = ? WHERE id = ? AND replaced_at IS NULL`,
				now, now, m.IdentifierID); err != nil {
				return err
			}
			if m.ReplacementPrimaryID != "" {
				if _, err := tx.Exec(ctx,
					`UPDATE user_identifiers SET is_primary = 1, updated_at = ? WHERE id = ?`,
					now, m.ReplacementPrimaryID); err != nil {
					return err
				}
			}
		case credential.ChangeIdentifierUses:
			if m.MakePrimary {
				if _, err := tx.Exec(ctx,
					`UPDATE user_identifiers SET is_primary = 0, updated_at = ?
						WHERE user_id = ? AND is_primary = 1 AND replaced_at IS NULL
						AND kind = (SELECT kind FROM user_identifiers WHERE id = ?)`,
					now, userID, m.IdentifierID); err != nil {
					return err
				}
			}
			q := `UPDATE user_identifiers
				SET login_enabled = ?, recovery_enabled = ?, notification_enabled = ?, updated_at = ?
				WHERE id = ?`
			if m.MakePrimary {
				q = `UPDATE user_identifiers
					SET login_enabled = ?, recovery_enabled = ?, notification_enabled = ?, is_primary = 1, updated_at = ?
					WHERE id = ?`
			}
			if _, err := tx.Exec(ctx, q,
				tursodb.BoolToInt(m.Uses.Login), tursodb.BoolToInt(m.Uses.Recovery), tursodb.BoolToInt(m.Uses.Notification),
				now, m.IdentifierID); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `UPDATE users SET auth_revision = auth_revision + 1, updated_at = ? WHERE id = ?`,
			now, userID); err != nil {
			return err
		}
		return nil
	})
}
