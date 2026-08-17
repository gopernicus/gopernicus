package turso

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/passwordless"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/sdk"
)

// PasswordlessStore implements passwordless.Repository over libSQL: the
// ONE-transaction magic-link redemption (CHAU-6.3). It is the dialect sibling of
// the pgx PasswordlessStore — same contract, same outcomes, same conformance
// suite.
//
// The transaction is opened with the connector's BEGIN IMMEDIATE write intent, so
// concurrent redemptions serialize at the transaction rather than at a row lock:
// SQLite has no SELECT ... FOR UPDATE, and the write-intent posture is the
// dialect's equivalent guarantee. The guarded `DELETE ... RETURNING` on the
// challenge row is still what decides the winner — exactly one transaction can
// delete it.
//
// Everything rolls back together on any failure, INCLUDING the token consumption,
// so a transient error leaves the link redeemable instead of burning it.
type PasswordlessStore struct {
	db *tursodb.DB
}

var _ passwordless.Repository = (*PasswordlessStore)(nil)

// NewPasswordlessStore returns a PasswordlessStore backed by db.
func NewPasswordlessStore(db *tursodb.DB) *PasswordlessStore {
	return &PasswordlessStore{db: db}
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
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		// 1. Consume the LIVE challenge; the expires_at guard folds unknown, used,
		//    and expired into one no-row outcome.
		var contextBlob sql.NullString
		selErr := tx.QueryRow(ctx,
			`DELETE FROM challenges
				WHERE purpose = ? AND secret_digest = ? AND expires_at > ?
				RETURNING context`,
			in.Purpose, in.TokenDigest, tursodb.FormatTime(now)).Scan(&contextBlob)
		if selErr != nil {
			if errors.Is(selErr, sql.ErrNoRows) {
				return passwordless.ErrRedemption
			}
			return tursodb.MapError(selErr)
		}

		// 2. Decode and validate the versioned binding.
		var binding passwordless.Binding
		if !contextBlob.Valid || json.Unmarshal([]byte(contextBlob.String), &binding) != nil {
			return passwordless.ErrRedemption
		}
		if binding.Version != passwordless.BindingVersion || binding.NormalizedValue == "" {
			return passwordless.ErrRedemption
		}

		// 3. Re-read the CURRENT active claim for the bound address. The binding's
		//    recorded ids are an expectation, not an authority: the address may have
		//    gained an owner between send and consume, and the current owner wins.
		var (
			identID      string
			identUserID  string
			verifiedAt   tursodb.NullTime
			loginEnabled int
		)
		const claimQ = `SELECT id, user_id, verified_at, login_enabled
			FROM user_identifiers
			WHERE kind = ? AND normalized_value = ? AND replaced_at IS NULL
				AND (login_enabled = 1 OR recovery_enabled = 1)`
		claimErr := tx.QueryRow(ctx, claimQ, binding.Kind, binding.NormalizedValue).
			Scan(&identID, &identUserID, &verifiedAt, &loginEnabled)
		switch {
		case errors.Is(claimErr, sql.ErrNoRows):
			if !binding.ProvisionIfAbsent {
				return passwordless.ErrRedemption
			}
			return provisionTurso(ctx, tx, in, binding, now, &out)
		case claimErr != nil:
			return tursodb.MapError(claimErr)
		}

		if verifiedAt.Valid {
			if loginEnabled == 0 {
				return passwordless.ErrRedemption
			}
			return loginTurso(ctx, tx, in, binding, identID, identUserID, verifiedAt.Time, &out)
		}
		return adoptTurso(ctx, tx, in, binding, identID, identUserID, now, &out)
	})
	if err != nil {
		return passwordless.RedeemResult{}, err
	}
	return out, nil
}

// provisionTurso creates one active user and one VERIFIED primary identifier,
// then inserts the session. The identifier insert relies on the partial unique
// authentication-claim index: a claim lost to a concurrent registration conflicts
// and rolls the whole redemption back to a generic rejection.
func provisionTurso(ctx context.Context, tx *tursodb.Tx, in passwordless.RedeemInput,
	binding passwordless.Binding, now time.Time, out *passwordless.RedeemResult) error {

	u := in.NewUser
	u.Status = user.NormalizeStatus(u.Status)
	u.CreatedAt, u.UpdatedAt = now, now

	if u.ID == "" {
		const q = `INSERT INTO users (display_name, auth_revision, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?) RETURNING id`
		if err := tx.QueryRow(ctx, q, u.DisplayName, u.AuthRevision, string(u.Status),
			tursodb.FormatTime(now), tursodb.FormatTime(now)).Scan(&u.ID); err != nil {
			return tursodb.MapError(err)
		}
	} else {
		const q = `INSERT INTO users (id, display_name, auth_revision, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`
		if _, err := tx.Exec(ctx, q, u.ID, u.DisplayName, u.AuthRevision, string(u.Status),
			tursodb.FormatTime(now), tursodb.FormatTime(now)); err != nil {
			return tursodb.MapError(err)
		}
	}

	ident := in.NewIdentifier
	ident.UserID = u.ID
	ident.Kind = identifier.Kind(binding.Kind)
	ident.NormalizedValue = binding.NormalizedValue
	ident.VerifiedAt = now
	ident.CreatedAt, ident.UpdatedAt = now, now

	created, err := insertIdentifier(ctx, tx, ident)
	if err != nil {
		if errors.Is(err, sdk.ErrAlreadyExists) {
			return passwordless.ErrRedemption
		}
		return err
	}

	sess, err := insertRedemptionSessionTurso(ctx, tx, in.Session, u.ID)
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

// loginTurso mints a session for the verified identifier's current owner.
func loginTurso(ctx context.Context, tx *tursodb.Tx, in passwordless.RedeemInput,
	binding passwordless.Binding, identID, userID string, verifiedAt time.Time, out *passwordless.RedeemResult) error {

	u, err := readActiveUserTurso(ctx, tx, userID)
	if err != nil {
		return err
	}
	sess, err := insertRedemptionSessionTurso(ctx, tx, in.Session, userID)
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
			VerifiedAt:      verifiedAt,
		},
		Session: sess,
	}
	return nil
}

// adoptTurso verifies the previously-unverified claim and revokes everything that
// predates the proof — password, sessions, grants, and the listed challenge
// purposes — BEFORE inserting the session. The ordering is the invariant: a
// completed adoption cannot leave a squatter credential alive.
func adoptTurso(ctx context.Context, tx *tursodb.Tx, in passwordless.RedeemInput,
	binding passwordless.Binding, identID, userID string, now time.Time, out *passwordless.RedeemResult) error {

	u, err := readActiveUserTurso(ctx, tx, userID)
	if err != nil {
		return err
	}

	for _, q := range []string{
		`DELETE FROM user_passwords WHERE user_id = ?`,
		`DELETE FROM authentication_grants WHERE user_id = ?`,
		`DELETE FROM sessions WHERE user_id = ?`,
	} {
		if _, err := tx.Exec(ctx, q, userID); err != nil {
			return tursodb.MapError(err)
		}
	}
	if len(in.RevokeChallengePurposes) > 0 {
		args := make([]any, 0, len(in.RevokeChallengePurposes)+1)
		args = append(args, userID)
		placeholders := make([]string, len(in.RevokeChallengePurposes))
		for i, p := range in.RevokeChallengePurposes {
			placeholders[i] = "?"
			args = append(args, p)
		}
		q := `DELETE FROM challenges WHERE user_id = ? AND purpose IN (` + strings.Join(placeholders, ", ") + `)`
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return tursodb.MapError(err)
		}
	}

	const updateIdent = `UPDATE user_identifiers
		SET verified_at = ?, login_enabled = ?, recovery_enabled = ?, notification_enabled = ?, updated_at = ?
		WHERE id = ?`
	if _, err := tx.Exec(ctx, updateIdent,
		tursodb.FormatTime(now),
		tursodb.BoolToInt(in.AdoptedIdentifierUses.Login),
		tursodb.BoolToInt(in.AdoptedIdentifierUses.Recovery),
		tursodb.BoolToInt(in.AdoptedIdentifierUses.Notification),
		tursodb.FormatTime(now), identID); err != nil {
		return tursodb.MapError(err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET auth_revision = auth_revision + 1, updated_at = ? WHERE id = ?`,
		tursodb.FormatTime(now), userID); err != nil {
		return tursodb.MapError(err)
	}
	u.AuthRevision++
	u.UpdatedAt = now

	sess, err := insertRedemptionSessionTurso(ctx, tx, in.Session, userID)
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

// readActiveUserTurso reads the user inside the write-intent transaction,
// rejecting a missing or deactivated subject. No explicit row lock is taken (or
// available): BEGIN IMMEDIATE already serializes this transaction against any
// concurrent writer, which is the same guarantee the pgx sibling gets from
// FOR UPDATE.
func readActiveUserTurso(ctx context.Context, tx *tursodb.Tx, userID string) (user.User, error) {
	var (
		displayName  string
		authRevision int64
		status       string
		createdAt    tursodb.Time
		updatedAt    tursodb.Time
	)
	const q = `SELECT display_name, auth_revision, status, created_at, updated_at FROM users WHERE id = ?`
	if err := tx.QueryRow(ctx, q, userID).Scan(&displayName, &authRevision, &status, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return user.User{}, passwordless.ErrRedemption
		}
		return user.User{}, tursodb.MapError(err)
	}
	u := user.User{
		ID:           userID,
		DisplayName:  displayName,
		AuthRevision: authRevision,
		Status:       user.NormalizeStatus(user.Status(status)),
		CreatedAt:    createdAt.Time,
		UpdatedAt:    updatedAt.Time,
	}
	if !u.Active() {
		return user.User{}, passwordless.ErrRedemption
	}
	return u, nil
}

// insertRedemptionSessionTurso writes the proposed session inside the redemption
// transaction, applying the ordinary uniqueness contract.
func insertRedemptionSessionTurso(ctx context.Context, tx *tursodb.Tx, sess session.Session, userID string) (session.Session, error) {
	sess.UserID = userID
	methods, err := encodeMethods(sess.Authentication.Methods)
	if err != nil {
		return session.Session{}, err
	}
	const q = `INSERT INTO sessions (` + sessionColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := tx.Exec(ctx, q,
		sess.ID, sess.UserID, sess.RefreshTokenHash, nullHash(sess.PreviousRefreshTokenHash),
		tursodb.BoolToInt(sess.PreviousUsed), sess.RotationCount,
		tursodb.FormatNullTime(sess.Authentication.AuthenticatedAt), methods, string(sess.Authentication.Assurance),
		tursodb.FormatTime(sess.CreatedAt), tursodb.FormatTime(sess.ExpiresAt),
	); err != nil {
		return session.Session{}, tursodb.MapError(err)
	}
	return sess, nil
}
