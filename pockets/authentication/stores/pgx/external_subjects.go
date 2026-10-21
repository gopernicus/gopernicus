package pgx

import (
	"context"
	"time"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/jackc/pgx/v5"
)

const externalSubjectColumns = "provider, subject, user_id, issuer, email, first_seen_at"

// ExternalSubjectStore implements externalsubject.Repository over a PostgreSQL
// database. Uniqueness is on the (provider, subject) primary key, surfaced as
// sdk.ErrAlreadyExists via MapError. Rows normally arrive inside
// UserStore.Provision; Link is the administrative path to an existing user.
type ExternalSubjectStore struct {
	db *pgxdb.DB
	qualified
}

var _ externalsubject.Repository = (*ExternalSubjectStore)(nil)

// NewExternalSubjectStore returns an ExternalSubjectStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewExternalSubjectStore(db *pgxdb.DB, opts ...Option) *ExternalSubjectStore {
	if db == nil {
		panic("authentication pgx: NewExternalSubjectStore received a nil database")
	}
	return &ExternalSubjectStore{db: db, qualified: qualified{schema: applyOptions(opts).schema}}
}

// externalSubjectRow is the store-local, db-tagged projection of an
// external_subjects row.
type externalSubjectRow struct {
	Provider    string    `db:"provider"`
	Subject     string    `db:"subject"`
	UserID      string    `db:"user_id"`
	Issuer      string    `db:"issuer"`
	Email       string    `db:"email"`
	FirstSeenAt time.Time `db:"first_seen_at"`
}

func (r externalSubjectRow) toDomain() externalsubject.ExternalSubject {
	return externalsubject.ExternalSubject{
		Provider:    r.Provider,
		Subject:     r.Subject,
		UserID:      r.UserID,
		Issuer:      r.Issuer,
		Email:       r.Email,
		FirstSeenAt: r.FirstSeenAt.UTC(),
	}
}

func insertExternalSubject(ctx context.Context, tx *pgxdb.Tx, qualifiedTables qualified, s externalsubject.ExternalSubject) (externalsubject.ExternalSubject, error) {
	q := `INSERT INTO ` + qualifiedTables.table(externalSubjectsTable) + ` (` + externalSubjectColumns + `)
		VALUES (@provider, @subject, @user_id, @issuer, @email, @first_seen_at)`
	s.FirstSeenAt = s.FirstSeenAt.UTC()
	_, err := tx.Exec(ctx, q, pgx.NamedArgs{
		"provider":      s.Provider,
		"subject":       s.Subject,
		"user_id":       s.UserID,
		"issuer":        s.Issuer,
		"email":         s.Email,
		"first_seen_at": s.FirstSeenAt,
	})
	if err != nil {
		return externalsubject.ExternalSubject{}, err
	}
	return s, nil
}

// Get returns the mapping for a provider subject, or sdk.ErrNotFound.
func (s *ExternalSubjectStore) Get(ctx context.Context, provider, subject string) (externalsubject.ExternalSubject, error) {
	q := `SELECT ` + externalSubjectColumns + ` FROM ` + s.table(externalSubjectsTable) + ` WHERE provider = @provider AND subject = @subject`
	row, err := pgxdb.QueryOne[externalSubjectRow](ctx, s.db, q, pgx.NamedArgs{"provider": provider, "subject": subject})
	if err != nil {
		return externalsubject.ExternalSubject{}, err
	}
	return row.toDomain(), nil
}

// Link maps a provider subject to an existing ACTIVE user. The user row is
// locked so a concurrent deactivation cannot interleave; a missing user →
// sdk.ErrNotFound, an inactive one → session.ErrUserNotActive, a taken subject →
// sdk.ErrAlreadyExists. It never touches credentials or sessions.
func (s *ExternalSubjectStore) Link(ctx context.Context, es externalsubject.ExternalSubject) (externalsubject.ExternalSubject, error) {
	var linked externalsubject.ExternalSubject
	err := s.db.InTx(ctx, func(tx *pgxdb.Tx) error {
		if _, err := lockCredentialUser(ctx, tx, s.qualified, es.UserID); err != nil {
			return err
		}
		var err error
		linked, err = insertExternalSubject(ctx, tx, s.qualified, es)
		return err
	})
	if err != nil {
		return externalsubject.ExternalSubject{}, err
	}
	return linked, nil
}

// ListByUser returns every mapping owned by userID (empty slice, nil error when
// none), oldest first.
func (s *ExternalSubjectStore) ListByUser(ctx context.Context, userID string) ([]externalsubject.ExternalSubject, error) {
	q := `SELECT ` + externalSubjectColumns + ` FROM ` + s.table(externalSubjectsTable) + ` WHERE user_id = @user_id ORDER BY first_seen_at, provider, subject`
	rows, err := s.db.Query(ctx, q, pgx.NamedArgs{"user_id": userID})
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[externalSubjectRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	out := make([]externalsubject.ExternalSubject, 0, len(list))
	for _, r := range list {
		out = append(out, r.toDomain())
	}
	return out, nil
}

// Unlink removes a mapping; no such mapping → sdk.ErrNotFound.
func (s *ExternalSubjectStore) Unlink(ctx context.Context, provider, subject string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, `DELETE FROM `+s.table(externalSubjectsTable)+` WHERE provider = @provider AND subject = @subject`, pgx.NamedArgs{"provider": provider, "subject": subject})
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}
