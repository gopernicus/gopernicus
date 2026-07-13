package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/sdk"
)

// CredentialMutationStore implements credential.MutationRepository over a
// PostgreSQL database (design §5.6). Snapshot projects the typed MethodSet from
// the credential source tables (user_passwords, oauth_accounts, user_identifiers)
// alongside the user's auth_revision. Apply performs one revision-CAS mutation in
// a single transaction: it locks the users row FOR UPDATE, rejects a stale
// revision as sdk.ErrConflict, mutates exactly the targeted typed source, and
// increments auth_revision exactly once — so a concurrent double-apply produces
// exactly one winner and never a partial mutation. The policy is the service's
// job before Apply; this store only serializes.
type CredentialMutationStore struct {
	db *pgxdb.DB
}

var _ credential.MutationRepository = (*CredentialMutationStore)(nil)

// NewCredentialMutationStore returns a CredentialMutationStore backed by db.
func NewCredentialMutationStore(db *pgxdb.DB) *CredentialMutationStore {
	return &CredentialMutationStore{db: db}
}

// Snapshot returns the user's current MethodSet, including the AuthRevision it was
// read at. An unknown user returns sdk.ErrNotFound.
func (s *CredentialMutationStore) Snapshot(ctx context.Context, userID string) (credential.MethodSet, error) {
	var set credential.MethodSet
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT auth_revision FROM users WHERE id = @id`, pgx.NamedArgs{"id": userID}).
			Scan(&revision); err != nil {
			if err == pgx.ErrNoRows {
				return sdk.ErrNotFound
			}
			return pgxdb.MapError(err)
		}
		set.AuthRevision = revision

		var hasPassword bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_passwords WHERE user_id = @id)`, pgx.NamedArgs{"id": userID}).
			Scan(&hasPassword); err != nil {
			return pgxdb.MapError(err)
		}
		set.HasPassword = hasPassword

		oauth, err := tx.Query(ctx, `SELECT provider FROM oauth_accounts WHERE user_id = @id ORDER BY provider`, pgx.NamedArgs{"id": userID})
		if err != nil {
			return err
		}
		providers, err := pgx.CollectRows(oauth, pgx.RowTo[string])
		if err != nil {
			return pgxdb.MapError(err)
		}
		for _, p := range providers {
			set.OAuth = append(set.OAuth, credential.OAuthMethod{Provider: p, Assurance: session.AssuranceAAL1})
		}

		idents, err := tx.Query(ctx,
			`SELECT id, kind, login_enabled, recovery_enabled, notification_enabled, is_primary, verified_at
				FROM user_identifiers WHERE user_id = @id AND replaced_at IS NULL ORDER BY created_at, id`,
			pgx.NamedArgs{"id": userID})
		if err != nil {
			return err
		}
		methods, err := pgx.CollectRows(idents, func(row pgx.CollectableRow) (credential.IdentifierMethod, error) {
			var (
				m          credential.IdentifierMethod
				verifiedAt *time.Time
			)
			var uses credential.IdentifierUses
			if err := row.Scan(&m.ID, &m.Kind, &uses.Login, &uses.Recovery, &uses.Notification, &m.Primary, &verifiedAt); err != nil {
				return credential.IdentifierMethod{}, err
			}
			m.Uses = uses
			m.Verified = verifiedAt != nil
			return m, nil
		})
		if err != nil {
			return pgxdb.MapError(err)
		}
		set.Identifiers = append(set.Identifiers, methods...)
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
	return s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		var current int64
		if err := tx.QueryRow(ctx, `SELECT auth_revision FROM users WHERE id = @id FOR UPDATE`, pgx.NamedArgs{"id": userID}).
			Scan(&current); err != nil {
			if err == pgx.ErrNoRows {
				return sdk.ErrNotFound
			}
			return pgxdb.MapError(err)
		}
		if current != expectedAuthRevision {
			return sdk.ErrConflict
		}

		now := time.Now().UTC()
		switch m := mutation.(type) {
		case credential.RemovePassword:
			if _, err := tx.Exec(ctx, `DELETE FROM user_passwords WHERE user_id = @id`, pgx.NamedArgs{"id": userID}); err != nil {
				return err
			}
		case credential.UnlinkOAuth:
			if _, err := tx.Exec(ctx, `DELETE FROM oauth_accounts WHERE user_id = @id AND provider = @provider`,
				pgx.NamedArgs{"id": userID, "provider": m.Provider}); err != nil {
				return err
			}
		case credential.RetireIdentifier:
			if _, err := tx.Exec(ctx,
				`UPDATE user_identifiers SET replaced_at = @now, updated_at = @now WHERE id = @id AND replaced_at IS NULL`,
				pgx.NamedArgs{"now": now, "id": m.IdentifierID}); err != nil {
				return err
			}
			if m.ReplacementPrimaryID != "" {
				if _, err := tx.Exec(ctx,
					`UPDATE user_identifiers SET is_primary = TRUE, updated_at = @now WHERE id = @id`,
					pgx.NamedArgs{"now": now, "id": m.ReplacementPrimaryID}); err != nil {
					return err
				}
			}
		case credential.ChangeIdentifierUses:
			if m.MakePrimary {
				if _, err := tx.Exec(ctx,
					`UPDATE user_identifiers SET is_primary = FALSE, updated_at = @now
						WHERE user_id = @id AND is_primary = TRUE AND replaced_at IS NULL
						AND kind = (SELECT kind FROM user_identifiers WHERE id = @identifier_id)`,
					pgx.NamedArgs{"now": now, "id": userID, "identifier_id": m.IdentifierID}); err != nil {
					return err
				}
			}
			args := pgx.NamedArgs{
				"now":           now,
				"identifier_id": m.IdentifierID,
				"login":         m.Uses.Login,
				"recovery":      m.Uses.Recovery,
				"notification":  m.Uses.Notification,
			}
			q := `UPDATE user_identifiers
				SET login_enabled = @login, recovery_enabled = @recovery, notification_enabled = @notification, updated_at = @now
				WHERE id = @identifier_id`
			if m.MakePrimary {
				q = `UPDATE user_identifiers
					SET login_enabled = @login, recovery_enabled = @recovery, notification_enabled = @notification, is_primary = TRUE, updated_at = @now
					WHERE id = @identifier_id`
			}
			if _, err := tx.Exec(ctx, q, args); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `UPDATE users SET auth_revision = auth_revision + 1, updated_at = @now WHERE id = @id`,
			pgx.NamedArgs{"now": now, "id": userID}); err != nil {
			return err
		}
		return nil
	})
}
