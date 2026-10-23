package pgx

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/sdk"
)

// InquiryStore implements messaging.InquiryRepository and the optional
// messaging.InquiryEraser over a PostgreSQL database.
type InquiryStore struct {
	db     *pgxdb.DB
	schema pgxdb.Schema
}

var (
	_ messaging.InquiryRepository = (*InquiryStore)(nil)
	_ messaging.InquiryEraser     = (*InquiryStore)(nil)
)

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
	// Empty ID → the sdk.DatabaseID strategy (amended D10): omit the id
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

// Delete removes the inquiry with id; an absent id is sdk.ErrNotFound.
func (s *InquiryStore) Delete(ctx context.Context, id string) error {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM "+s.table(inquiriesTable)+" WHERE id = @id", pgx.NamedArgs{"id": id})
	if err != nil {
		return pgxdb.MapError(err)
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}

// DeleteByEmail removes every inquiry whose email equals email under lower()
// on both sides — at least the ASCII case fold the port promises.
func (s *InquiryStore) DeleteByEmail(ctx context.Context, email string) (int, error) {
	n, err := pgxdb.ExecAffecting(ctx, s.db, "DELETE FROM "+s.table(inquiriesTable)+" WHERE lower(email) = lower(@email)", pgx.NamedArgs{"email": email})
	if err != nil {
		return 0, pgxdb.MapError(err)
	}
	return int(n), nil
}

// PurgeBefore removes at most limit inquiries with created_at <= before, oldest
// first. The subselect locks its batch with SKIP LOCKED so concurrent purges
// take disjoint rows; 0022's created_at index is its access path.
func (s *InquiryStore) PurgeBefore(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("cms pgx: inquiry purge limit %d must be positive: %w", limit, sdk.ErrInvalidInput)
	}
	q := `DELETE FROM ` + s.table(inquiriesTable) + ` WHERE id IN (
		SELECT id FROM ` + s.table(inquiriesTable) + `
		WHERE created_at <= @before
		ORDER BY created_at, id
		LIMIT @limit
		FOR UPDATE SKIP LOCKED)`
	n, err := pgxdb.ExecAffecting(ctx, s.db, q, pgx.NamedArgs{"before": before.UTC(), "limit": limit})
	if err != nil {
		return 0, pgxdb.MapError(err)
	}
	return int(n), nil
}
