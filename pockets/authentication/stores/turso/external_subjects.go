package turso

import (
	"context"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/sdk"
)

const externalSubjectColumns = "provider, subject, user_id, issuer, email, first_seen_at"

// ExternalSubjectStore implements externalsubject.Repository over a libSQL
// database. Uniqueness is on the (provider, subject) primary key, surfaced as
// sdk.ErrAlreadyExists via MapError. Rows normally arrive inside
// UserStore.Provision; Link is the administrative path to an existing user.
type ExternalSubjectStore struct {
	db *tursodb.DB
}

var _ externalsubject.Repository = (*ExternalSubjectStore)(nil)

// NewExternalSubjectStore returns an ExternalSubjectStore backed by db.
// It panics if db is nil; the caller owns the database lifecycle.
func NewExternalSubjectStore(db *tursodb.DB) *ExternalSubjectStore {
	if db == nil {
		panic("authentication turso: NewExternalSubjectStore received a nil database")
	}
	return &ExternalSubjectStore{db: db}
}

// externalSubjectRow is the store-local, db-tagged projection of an
// external_subjects row.
type externalSubjectRow struct {
	Provider    string       `db:"provider"`
	Subject     string       `db:"subject"`
	UserID      string       `db:"user_id"`
	Issuer      string       `db:"issuer"`
	Email       string       `db:"email"`
	FirstSeenAt tursodb.Time `db:"first_seen_at"`
}

func (r externalSubjectRow) toDomain() externalsubject.ExternalSubject {
	return externalsubject.ExternalSubject{
		Provider:    r.Provider,
		Subject:     r.Subject,
		UserID:      r.UserID,
		Issuer:      r.Issuer,
		Email:       r.Email,
		FirstSeenAt: r.FirstSeenAt.Time,
	}
}

func insertExternalSubject(ctx context.Context, tx *tursodb.Tx, s externalsubject.ExternalSubject) (externalsubject.ExternalSubject, error) {
	const q = `INSERT INTO external_subjects (` + externalSubjectColumns + `) VALUES (?, ?, ?, ?, ?, ?)`
	s.FirstSeenAt = s.FirstSeenAt.UTC()
	if _, err := tx.Exec(ctx, q, s.Provider, s.Subject, s.UserID, s.Issuer, s.Email, tursodb.FormatTime(s.FirstSeenAt)); err != nil {
		return externalsubject.ExternalSubject{}, err
	}
	return s, nil
}

// Get returns the mapping for a provider subject, or sdk.ErrNotFound.
func (s *ExternalSubjectStore) Get(ctx context.Context, provider, subject string) (externalsubject.ExternalSubject, error) {
	const q = `SELECT ` + externalSubjectColumns + ` FROM external_subjects WHERE provider = ? AND subject = ?`
	row, err := tursodb.QueryOne[externalSubjectRow](ctx, s.db, q, provider, subject)
	if err != nil {
		return externalsubject.ExternalSubject{}, err
	}
	return row.toDomain(), nil
}

// Link maps a provider subject to an existing ACTIVE user inside one write
// transaction: a missing user → sdk.ErrNotFound, an inactive one →
// session.ErrUserNotActive, a taken subject → sdk.ErrAlreadyExists. It never
// touches credentials or sessions.
func (s *ExternalSubjectStore) Link(ctx context.Context, es externalsubject.ExternalSubject) (externalsubject.ExternalSubject, error) {
	var linked externalsubject.ExternalSubject
	err := s.db.InTx(ctx, func(tx *tursodb.Tx) error {
		if _, err := lockCredentialUser(ctx, tx, es.UserID); err != nil {
			return err
		}
		var err error
		linked, err = insertExternalSubject(ctx, tx, es)
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
	const q = `SELECT ` + externalSubjectColumns + ` FROM external_subjects WHERE user_id = ? ORDER BY first_seen_at, provider, subject`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []externalsubject.ExternalSubject{}
	for rows.Next() {
		row, err := tursodb.ScanStruct[externalSubjectRow](rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row.toDomain())
	}
	if err := rows.Err(); err != nil {
		return nil, tursodb.MapError(err)
	}
	return out, nil
}

// Unlink removes a mapping; no such mapping → sdk.ErrNotFound.
func (s *ExternalSubjectStore) Unlink(ctx context.Context, provider, subject string) error {
	n, err := tursodb.ExecAffecting(ctx, s.db, `DELETE FROM external_subjects WHERE provider = ? AND subject = ?`, provider, subject)
	if err != nil {
		return err
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}
