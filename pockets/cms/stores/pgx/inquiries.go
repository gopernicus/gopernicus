package pgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/pockets/cms/domain/messaging"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// InquiryStore implements messaging.InquiryRepository over a PostgreSQL database.
type InquiryStore struct {
	db     *pgxdb.DB
	schema pgxdb.Schema
}

var _ messaging.InquiryRepository = (*InquiryStore)(nil)

// NewInquiryStore returns an InquiryStore backed by db.
func NewInquiryStore(db *pgxdb.DB, opts ...Option) *InquiryStore {
	return &InquiryStore{db: db, schema: newConfig(opts).schema}
}

// table renders name under the store's schema — the one chokepoint every
// statement in this file goes through.
func (s *InquiryStore) table(name string) string { return s.schema.Table(name) }

const inquiryColumns = "id, name, email, message, created_at"

// inquiryRow is the store-local, db-tagged projection of an inquiries row.
type inquiryRow struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	Email     string    `db:"email"`
	Message   string    `db:"message"`
	CreatedAt time.Time `db:"created_at"`
}

func (r inquiryRow) toDomain() messaging.Inquiry {
	return messaging.Inquiry{
		ID:        r.ID,
		Name:      r.Name,
		Email:     r.Email,
		Message:   r.Message,
		CreatedAt: r.CreatedAt.UTC(),
	}
}

// Create persists a new inquiry.
func (s *InquiryStore) Create(ctx context.Context, in messaging.Inquiry) (messaging.Inquiry, error) {
	args := pgx.NamedArgs{
		"name":       in.Name,
		"email":      in.Email,
		"message":    in.Message,
		"created_at": in.CreatedAt.UTC(),
	}
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if in.ID == "" {
		q := `INSERT INTO ` + s.table(inquiriesTable) + ` (name, email, message, created_at)
			VALUES (@name, @email, @message, @created_at)
			RETURNING id`
		if err := s.db.QueryRow(ctx, q, args).Scan(&in.ID); err != nil {
			return messaging.Inquiry{}, pgxdb.MapError(err)
		}
		return in, nil
	}
	q := `INSERT INTO ` + s.table(inquiriesTable) + ` (` + inquiryColumns + `)
		VALUES (@id, @name, @email, @message, @created_at)`
	args["id"] = in.ID
	if _, err := s.db.Exec(ctx, q, args); err != nil {
		return messaging.Inquiry{}, err
	}
	return in, nil
}

// List returns all inquiries, newest first.
func (s *InquiryStore) List(ctx context.Context) ([]messaging.Inquiry, error) {
	q := `SELECT ` + inquiryColumns + ` FROM ` + s.table(inquiriesTable) + ` ORDER BY created_at DESC, id DESC`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	inquiries, err := pgx.CollectRows(rows, pgx.RowToStructByName[inquiryRow])
	if err != nil {
		return nil, pgxdb.MapError(err)
	}
	var out []messaging.Inquiry
	for _, in := range inquiries {
		out = append(out, in.toDomain())
	}
	return out, nil
}
