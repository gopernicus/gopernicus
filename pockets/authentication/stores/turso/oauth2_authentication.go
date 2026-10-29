package turso

import (
	"context"
	"database/sql"
	"encoding/json"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
)

// codeAuthenticationMigration is the file that adds the
// oauth_authorization_codes authentication columns; ProbeSchema names it.
const codeAuthenticationMigration = "0030_oauth_code_authentication.sql"

// oauthCodeAuthenticationColumns is the wired-only column list: the 0019 code
// columns plus the 0030 copy. Only the CodeAuthenticationRepository methods
// name it, so a host that leaves delegated assurance unwired never reads a
// column it may not have migrated.
const oauthCodeAuthenticationColumns = "code_hash, user_id, auth_revision, delegation, redirect_uri, code_challenge, created_at, expires_at, authenticated_at, authentication_methods, assurance_level"

var _ oauth2.CodeAuthenticationRepository = (*OAuth2Store)(nil)

// codeAuthenticationColumnNames are the 0030 columns ProbeSchema requires.
var codeAuthenticationColumnNames = []string{"authenticated_at", "authentication_methods", "assurance_level"}

// codeAuthenticationValues are the 0030 values for recorded, in the sessions
// encodings (authenticated_at in created_at's fixed-width TEXT); each part the
// session did not record is NULL.
func codeAuthenticationValues(recorded session.AuthenticationMetadata) ([]any, error) {
	methods, err := encodeMethods(recorded.Methods)
	if err != nil {
		return nil, err
	}
	return []any{tursodb.FormatNullTime(recorded.AuthenticatedAt), nullText(methods), nullText(string(recorded.Assurance))}, nil
}

// CreateCodeWithAuthentication is CreateCode plus the approving session's
// Authentication, copied inside the same BEGIN IMMEDIATE transaction from the
// row CreateCode already reads. code.Authentication is ignored.
func (s *OAuth2Store) CreateCodeWithAuthentication(ctx context.Context, code oauth2.Code, approvingSessionID string) error {
	return s.createCode(ctx, code, approvingSessionID, true)
}

// GetCodeWithAuthentication is GetCode plus the recorded Authentication (zero
// when the columns are NULL).
func (s *OAuth2Store) GetCodeWithAuthentication(ctx context.Context, hash string) (oauth2.Code, error) {
	var out oauth2.Code
	var delegation string
	var created, expires tursodb.Time
	var authenticated tursodb.NullTime
	var methods, assurance sql.NullString
	if err := s.db.QueryRow(ctx, `SELECT `+oauthCodeAuthenticationColumns+` FROM oauth_authorization_codes WHERE code_hash = ?`, hash).
		Scan(&out.Hash, &out.UserID, &out.AuthRevision, &delegation, &out.RedirectURI, &out.CodeChallenge, &created, &expires,
			&authenticated, &methods, &assurance); err != nil {
		return oauth2.Code{}, oauthGrantError(tursodb.MapError(err))
	}
	if err := json.Unmarshal([]byte(delegation), &out.Delegation); err != nil {
		return oauth2.Code{}, err
	}
	decoded, err := decodeMethods(methods.String)
	if err != nil {
		return oauth2.Code{}, err
	}
	out.CreatedAt, out.ExpiresAt = created.Time, expires.Time
	out.Authentication = session.AuthenticationMetadata{
		AuthenticatedAt: authenticated.Time,
		Methods:         decoded,
		Assurance:       session.AssuranceLevel(assurance.String),
	}
	return out, nil
}

// ProbeSchema refuses a database without migration 0030's columns with
// sdk.ErrNotFound naming the file. An infrastructure failure is returned via
// MapError, never as a missing column.
func (s *OAuth2Store) ProbeSchema(ctx context.Context) error {
	for _, column := range codeAuthenticationColumnNames {
		if err := probeColumn(ctx, s.db, "oauth_authorization_codes", column, codeAuthenticationMigration); err != nil {
			return err
		}
	}
	return nil
}
