package pgx

import (
	"context"
	"fmt"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

// codeAuthenticationMigration is the file that adds the
// oauth_authorization_codes authentication columns; ProbeSchema names it.
const codeAuthenticationMigration = "0030_oauth_code_authentication.sql"

// oauthCodeAuthenticationColumns is the wired-only column list: oauthCodeColumns
// plus the 0030 copy. Only the CodeAuthenticationRepository methods name it, so
// a host that leaves delegated assurance unwired never reads a column it may
// not have migrated.
const oauthCodeAuthenticationColumns = oauthCodeColumns + ", authenticated_at, authentication_methods, assurance_level"

var _ oauth2.CodeAuthenticationRepository = (*OAuth2Store)(nil)

// codeAuthenticationColumnNames are the 0030 columns ProbeSchema requires.
var codeAuthenticationColumnNames = []string{"authenticated_at", "authentication_methods", "assurance_level"}

// oauthCodeAuthenticationRow is a code row plus its nullable 0030 copy. Every
// NULL reads back as the zero value: a code a plain CreateCode wrote records
// nothing.
type oauthCodeAuthenticationRow struct {
	oauthCodeRow
	AuthenticatedAt       *time.Time `db:"authenticated_at"`
	AuthenticationMethods *string    `db:"authentication_methods"`
	AssuranceLevel        *string    `db:"assurance_level"`
}

func (r oauthCodeAuthenticationRow) toDomain() (oauth2.Code, error) {
	code, err := r.oauthCodeRow.toDomain()
	if err != nil {
		return oauth2.Code{}, err
	}
	methods, err := decodeMethods(textFrom(r.AuthenticationMethods))
	if err != nil {
		return oauth2.Code{}, err
	}
	code.Authentication = session.AuthenticationMetadata{
		AuthenticatedAt: pgxdb.FromNullTime(r.AuthenticatedAt),
		Methods:         methods,
		Assurance:       session.AssuranceLevel(textFrom(r.AssuranceLevel)),
	}
	return code, nil
}

// codeAuthenticationArgs adds the 0030 values for recorded to args, in the
// sessions encodings; each part the session did not record is NULL.
func codeAuthenticationArgs(args pgx.NamedArgs, recorded session.AuthenticationMetadata) error {
	methods, err := encodeMethods(recorded.Methods)
	if err != nil {
		return err
	}
	args["authenticated_at"] = pgxdb.NullTime(recorded.AuthenticatedAt)
	args["authentication_methods"] = nullText(methods)
	args["assurance_level"] = nullText(string(recorded.Assurance))
	return nil
}

// CreateCodeWithAuthentication is CreateCode plus the approving session's
// Authentication, copied inside the same transaction from the row CreateCode
// already locks. code.Authentication is ignored.
func (s *OAuth2Store) CreateCodeWithAuthentication(ctx context.Context, code oauth2.Code, approvingSessionID string) error {
	return s.createCode(ctx, code, approvingSessionID, true)
}

// GetCodeWithAuthentication is GetCode plus the recorded Authentication (zero
// when the columns are NULL).
func (s *OAuth2Store) GetCodeWithAuthentication(ctx context.Context, hash string) (oauth2.Code, error) {
	row, err := pgxdb.QueryOne[oauthCodeAuthenticationRow](ctx, s.db, `SELECT `+oauthCodeAuthenticationColumns+` FROM `+s.table(oauthCodesTable)+` WHERE code_hash = @hash`, pgx.NamedArgs{"hash": hash})
	if err != nil {
		return oauth2.Code{}, oauthGrantError(err)
	}
	return row.toDomain()
}

// ProbeSchema refuses a database without migration 0030's columns with
// sdk.ErrNotFound naming the file. An infrastructure failure is returned via
// MapError, never as a missing column.
func (s *OAuth2Store) ProbeSchema(ctx context.Context) error {
	for _, column := range codeAuthenticationColumnNames {
		var exists bool
		if err := s.db.QueryRow(ctx, probeSessionActivitySQL, s.table(oauthCodesTable), column).Scan(&exists); err != nil {
			return pgxdb.MapError(err)
		}
		if !exists {
			return fmt.Errorf("authentication pgx store: %s.%s column missing — apply migration %s from the %q migration source before boot: %w",
				s.table(oauthCodesTable), column, codeAuthenticationMigration, "authentication", sdk.ErrNotFound)
		}
	}
	return nil
}
