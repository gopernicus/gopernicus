package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// PasswordlessStore implements passwordless.Repository over PostgreSQL: the
// ONE-transaction magic-link redemption (CHAU-6.3).
//
// Everything happens inside a single transaction — consuming the challenge,
// deciding the branch, creating or adopting the identity, revoking a squatter's
// credentials, and inserting the session. Any failure rolls ALL of it back,
// INCLUDING the token consumption, so a transient error leaves the link
// redeemable rather than burning it.
//
// Concurrency is settled by the guarded `DELETE ... RETURNING` on the challenge
// row: two simultaneous redemptions of one token contend on that row and exactly
// one gets it. The loser sees no row and returns the generic rejection, so there
// is never a second session or a duplicate provisioned user.
//
// It is deliberately implemented over the EXISTING tables (users, identifiers,
// passwords, sessions, challenges, grants). No `pending_users` table and no second
// secret store: those exist only to avoid a composite transaction, and avoiding
// the composite transaction is what makes provisioning unsafe.
type PasswordlessStore struct {
	db *pgxdb.DB
	qualified
}

var _ passwordless.Repository = (*PasswordlessStore)(nil)

// NewPasswordlessStore returns a PasswordlessStore backed by db.
func NewPasswordlessStore(db *pgxdb.DB, opts ...Option) *PasswordlessStore {
	return &PasswordlessStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// Redeem executes the atomic redemption. See passwordless.Repository for the
// contract; every stable rejection is passwordless.ErrRedemption with nothing
// written.
func (s *PasswordlessStore) Redeem(ctx context.Context, in passwordless.RedeemInput) (passwordless.RedeemResult, error) {
	if in.TokenDigest == "" {
		return passwordless.RedeemResult{}, passwordless.ErrRedemption
	}
	now := in.Now.UTC()

	var out passwordless.RedeemResult
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		// 1. Consume the LIVE challenge. The expires_at guard folds unknown, used,
		//    and expired into one no-row outcome, and the DELETE is what serializes
		//    concurrent redemptions of the same token.
		var contextBlob *string
		selErr := tx.QueryRow(ctx,
			`DELETE FROM `+s.table(challengesTable)+`
				WHERE purpose = @purpose AND secret_digest = @digest AND expires_at > @now
				RETURNING context`,
			pgx.NamedArgs{"purpose": in.Purpose, "digest": in.TokenDigest, "now": now}).
			Scan(&contextBlob)
		if selErr != nil {
			if errors.Is(selErr, pgx.ErrNoRows) {
				return passwordless.ErrRedemption
			}
			return pgxdb.MapError(selErr)
		}

		// 2. Decode and validate the versioned binding.
		var binding passwordless.Binding
		if contextBlob == nil || json.Unmarshal([]byte(*contextBlob), &binding) != nil {
			return passwordless.ErrRedemption
		}
		if binding.Version != passwordless.BindingVersion || binding.NormalizedValue == "" {
			return passwordless.ErrRedemption
		}

		// 3. Re-read the CURRENT active claim for the bound address, locking it so a
		//    concurrent identifier mutation cannot change it under us. The binding's
		//    recorded ids are an expectation, not an authority: the address may have
		//    gained an owner between send and consume, and the now-current owner wins.
		var (
			identID      string
			identUserID  string
			verifiedAt   *time.Time
			loginEnabled bool
		)
		claimQ := `SELECT id, user_id, verified_at, login_enabled
			FROM ` + s.table(identifiersTable) + `
			WHERE kind = @kind AND normalized_value = @value AND replaced_at IS NULL
				AND (login_enabled = TRUE OR recovery_enabled = TRUE)
			FOR UPDATE`
		claimErr := tx.QueryRow(ctx, claimQ, pgx.NamedArgs{"kind": binding.Kind, "value": binding.NormalizedValue}).
			Scan(&identID, &identUserID, &verifiedAt, &loginEnabled)
		switch {
		case errors.Is(claimErr, pgx.ErrNoRows):
			if !binding.ProvisionIfAbsent {
				return passwordless.ErrRedemption
			}
			return s.provision(ctx, tx, in, binding, now, &out)
		case claimErr != nil:
			return pgxdb.MapError(claimErr)
		}

		if verifiedAt != nil {
			if !loginEnabled {
				return passwordless.ErrRedemption
			}
			return s.login(ctx, tx, in, binding, identID, identUserID, *verifiedAt, now, &out)
		}
		return s.adopt(ctx, tx, in, binding, identID, identUserID, now, &out)
	})
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	return out, nil
}

// provision creates one active user and one VERIFIED primary identifier, then
// inserts the session. The identifier insert relies on the partial unique
// authentication-claim index: if another transaction claimed the address in
// between, the insert conflicts and the whole redemption rolls back to a generic
// rejection rather than creating a duplicate subject.
func (s *PasswordlessStore) provision(ctx context.Context, tx *pgxdb.Tx, in passwordless.RedeemInput,
	binding passwordless.Binding, now time.Time, out *passwordless.RedeemResult) error {

	u := in.NewUser
	u.Status = user.NormalizeStatus(u.Status)
	u.CreatedAt, u.UpdatedAt = now, now

	userArgs := pgx.NamedArgs{
		"display_name":  u.DisplayName,
		"auth_revision": u.AuthRevision,
		"status":        string(u.Status),
		"created_at":    now,
		"updated_at":    now,
	}
	if u.ID == "" {
		q := `INSERT INTO ` + s.table(usersTable) + ` (display_name, auth_revision, status, created_at, updated_at)
			VALUES (@display_name, @auth_revision, @status, @created_at, @updated_at) RETURNING id`
		if err := tx.QueryRow(ctx, q, userArgs).Scan(&u.ID); err != nil {
			return pgxdb.MapError(err)
		}
	} else {
		userArgs["id"] = u.ID
		q := `INSERT INTO ` + s.table(usersTable) + ` (id, display_name, auth_revision, status, created_at, updated_at)
			VALUES (@id, @display_name, @auth_revision, @status, @created_at, @updated_at)`
		if _, err := tx.Exec(ctx, q, userArgs); err != nil {
			return pgxdb.MapError(err)
		}
	}

	ident := in.NewIdentifier
	ident.UserID = u.ID
	ident.Kind = identifier.Kind(binding.Kind)
	ident.NormalizedValue = binding.NormalizedValue
	ident.VerifiedAt = now
	ident.CreatedAt, ident.UpdatedAt = now, now

	created, err := insertIdentifier(ctx, tx, s.table(identifiersTable), ident)
	if err != nil {
		// A lost authentication claim is a stable rejection, not an infrastructure
		// failure: someone else owns the address now.
		if errors.Is(err, sdk.ErrAlreadyExists) {
			return passwordless.ErrRedemption
		}
		return err
	}

	sess, err := insertRedemptionSession(ctx, tx, s.table(sessionsTable), in.Session, u.ID)
	if err != nil {
		return err
	}
	*out = passwordless.RedeemResult{
		Outcome:     passwordless.OutcomeProvisionNew,
		User:        u,
		Identifier:  created,
		Session:     sess,
		Provisioned: true,
	}
	return nil
}

// login mints a session for the verified identifier's current owner.
func (s *PasswordlessStore) login(ctx context.Context, tx *pgxdb.Tx, in passwordless.RedeemInput,
	binding passwordless.Binding, identID, userID string, verifiedAt, now time.Time, out *passwordless.RedeemResult) error {

	u, err := lockActiveUser(ctx, tx, s.table(usersTable), userID)
	if err != nil {
		return err
	}
	sess, err := insertRedemptionSession(ctx, tx, s.table(sessionsTable), in.Session, userID)
	if err != nil {
		return err
	}
	*out = passwordless.RedeemResult{
		Outcome: passwordless.OutcomeLoginExistingVerified,
		User:    u,
		Identifier: identifier.Identifier{
			ID:              identID,
			UserID:          userID,
			Kind:            identifier.Kind(binding.Kind),
			NormalizedValue: binding.NormalizedValue,
			VerifiedAt:      verifiedAt.UTC(),
		},
		Session: sess,
	}
	return nil
}

// adopt verifies the previously-unverified claim and revokes everything that
// predates the proof — password, sessions, grants, and the listed challenge
// purposes — BEFORE inserting the session.
//
// The ordering is the invariant, not a preference: the address was claimed
// without being proven, so anything created under it may belong to a squatter. A
// completed adoption cannot leave one of their credentials alive, because the new
// session is written only after they are gone.
func (s *PasswordlessStore) adopt(ctx context.Context, tx *pgxdb.Tx, in passwordless.RedeemInput,
	binding passwordless.Binding, identID, userID string, now time.Time, out *passwordless.RedeemResult) error {

	u, err := lockActiveUser(ctx, tx, s.table(usersTable), userID)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM `+s.table(passwordsTable)+` WHERE user_id = @user_id`,
		pgx.NamedArgs{"user_id": userID}); err != nil {
		return pgxdb.MapError(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM `+s.table(authGrantsTable)+` WHERE user_id = @user_id`,
		pgx.NamedArgs{"user_id": userID}); err != nil {
		return pgxdb.MapError(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM `+s.table(sessionsTable)+` WHERE user_id = @user_id`,
		pgx.NamedArgs{"user_id": userID}); err != nil {
		return pgxdb.MapError(err)
	}
	if len(in.RevokeChallengePurposes) > 0 {
		if _, err := tx.Exec(ctx,
			`DELETE FROM `+s.table(challengesTable)+` WHERE user_id = @user_id AND purpose = ANY(@purposes)`,
			pgx.NamedArgs{"user_id": userID, "purposes": in.RevokeChallengePurposes}); err != nil {
			return pgxdb.MapError(err)
		}
	}

	updateIdent := `UPDATE ` + s.table(identifiersTable) + `
		SET verified_at = @now, login_enabled = @login, recovery_enabled = @recovery,
			notification_enabled = @notification, updated_at = @now
		WHERE id = @id`
	if _, err := tx.Exec(ctx, updateIdent, pgx.NamedArgs{
		"now":          now,
		"login":        in.AdoptedIdentifierUses.Login,
		"recovery":     in.AdoptedIdentifierUses.Recovery,
		"notification": in.AdoptedIdentifierUses.Notification,
		"id":           identID,
	}); err != nil {
		return pgxdb.MapError(err)
	}

	// Bump the revision so any in-flight credential mutation loses its CAS.
	if _, err := tx.Exec(ctx,
		`UPDATE `+s.table(usersTable)+` SET auth_revision = auth_revision + 1, updated_at = @now WHERE id = @id`,
		pgx.NamedArgs{"now": now, "id": userID}); err != nil {
		return pgxdb.MapError(err)
	}
	u.AuthRevision++
	u.UpdatedAt = now

	sess, err := insertRedemptionSession(ctx, tx, s.table(sessionsTable), in.Session, userID)
	if err != nil {
		return err
	}
	*out = passwordless.RedeemResult{
		Outcome: passwordless.OutcomeVerifyAndAdoptExistingUnverified,
		User:    u,
		Identifier: identifier.Identifier{
			ID:                  identID,
			UserID:              userID,
			Kind:                identifier.Kind(binding.Kind),
			NormalizedValue:     binding.NormalizedValue,
			VerifiedAt:          now,
			LoginEnabled:        in.AdoptedIdentifierUses.Login,
			RecoveryEnabled:     in.AdoptedIdentifierUses.Recovery,
			NotificationEnabled: in.AdoptedIdentifierUses.Notification,
			UpdatedAt:           now,
		},
		Session: sess,
	}
	return nil
}

// lockActiveUser reads and row-locks the user in the already-qualified table,
// rejecting a missing or deactivated subject. The lock is the same fence
// ActiveSessionStore takes, so a deactivation racing a redemption cannot
// interleave with it.
func lockActiveUser(ctx context.Context, tx *pgxdb.Tx, table, userID string) (user.User, error) {
	var (
		displayName  string
		authRevision int64
		status       string
		createdAt    time.Time
		updatedAt    time.Time
	)
	q := `SELECT display_name, auth_revision, status, created_at, updated_at
		FROM ` + table + ` WHERE id = @id FOR UPDATE`
	if err := tx.QueryRow(ctx, q, pgx.NamedArgs{"id": userID}).
		Scan(&displayName, &authRevision, &status, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return user.User{}, passwordless.ErrRedemption
		}
		return user.User{}, pgxdb.MapError(err)
	}
	u := user.User{
		ID:           userID,
		DisplayName:  displayName,
		AuthRevision: authRevision,
		Status:       user.NormalizeStatus(user.Status(status)),
		CreatedAt:    createdAt.UTC(),
		UpdatedAt:    updatedAt.UTC(),
	}
	if !u.Active() {
		return user.User{}, passwordless.ErrRedemption
	}
	return u, nil
}

// insertRedemptionSession writes the proposed session into the already-qualified
// table inside the redemption transaction, applying the ordinary uniqueness
// contract.
func insertRedemptionSession(ctx context.Context, tx *pgxdb.Tx, table string, sess session.Session, userID string) (session.Session, error) {
	sess.UserID = userID
	methods, err := encodeMethods(sess.Authentication.Methods)
	if err != nil {
		return session.Session{}, err
	}
	q := `INSERT INTO ` + table + ` (` + sessionColumns + `)
		VALUES (@id, @user_id, @refresh_token_hash, @previous_refresh_token_hash, @previous_used, @rotation_count, @authenticated_at, @authentication_methods, @assurance_level, @created_at, @expires_at)`
	if _, err := tx.Exec(ctx, q, pgx.NamedArgs{
		"id":                          sess.ID,
		"user_id":                     sess.UserID,
		"refresh_token_hash":          sess.RefreshTokenHash,
		"previous_refresh_token_hash": nullHash(sess.PreviousRefreshTokenHash),
		"previous_used":               sess.PreviousUsed,
		"rotation_count":              sess.RotationCount,
		"authenticated_at":            pgxdb.NullTime(sess.Authentication.AuthenticatedAt),
		"authentication_methods":      methods,
		"assurance_level":             string(sess.Authentication.Assurance),
		"created_at":                  sess.CreatedAt.UTC(),
		"expires_at":                  sess.ExpiresAt.UTC(),
	}); err != nil {
		return session.Session{}, pgxdb.MapError(err)
	}
	return sess, nil
}
