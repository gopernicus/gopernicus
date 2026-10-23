package turso

import (
	"context"
	"fmt"
	"time"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/sdk"
)

// InquiryStore implements messaging.InquiryRepository and the optional
// messaging.InquiryEraser over a libSQL database.
type InquiryStore struct {
	db *tursodb.DB
}

var (
	_ messaging.InquiryRepository = (*InquiryStore)(nil)
	_ messaging.InquiryEraser     = (*InquiryStore)(nil)
)

// NewInquiryStore returns an InquiryStore backed by db.
func NewInquiryStore(db *tursodb.DB) *InquiryStore {
	return &InquiryStore{db: db}
}

const inquiryColumns = "id, name, email, message, created_at"

// inquiryRow is the store-local, db-tagged projection of an inquiries row.
type inquiryRow struct {
	ID        string       `db:"id"`
	Name      string       `db:"name"`
	Email     string       `db:"email"`
	Message   string       `db:"message"`
	CreatedAt tursodb.Time `db:"created_at"`
}

func (r inquiryRow) toDomain() messaging.Inquiry {
	return messaging.Inquiry{
		ID:        r.ID,
		Name:      r.Name,
		Email:     r.Email,
		Message:   r.Message,
		CreatedAt: r.CreatedAt.Time,
	}
}

// Create persists a new inquiry.
func (s *InquiryStore) Create(ctx context.Context, in messaging.Inquiry) (messaging.Inquiry, error) {
	// Empty ID → the sdk.DatabaseID strategy (amended D10): omit the id
	// column so the schema default generates the key, read back with RETURNING.
	if in.ID == "" {
		const q = `INSERT INTO inquiries (name, email, message, created_at) VALUES (?, ?, ?, ?) RETURNING id`
		if err := s.db.QueryRow(ctx, q, in.Name, in.Email, in.Message, tursodb.FormatTime(in.CreatedAt)).Scan(&in.ID); err != nil {
			return messaging.Inquiry{}, tursodb.MapError(err)
		}
		return in, nil
	}
	const q = `INSERT INTO inquiries (` + inquiryColumns + `) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, in.ID, in.Name, in.Email, in.Message, tursodb.FormatTime(in.CreatedAt))
	if err != nil {
		return messaging.Inquiry{}, err
	}
	return in, nil
}

// List returns all inquiries, newest first.
func (s *InquiryStore) List(ctx context.Context) ([]messaging.Inquiry, error) {
	const q = `SELECT ` + inquiryColumns + ` FROM inquiries ORDER BY created_at DESC, id DESC`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []messaging.Inquiry
	for rows.Next() {
		row, err := tursodb.ScanStruct[inquiryRow](rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row.toDomain())
	}
	return out, tursodb.MapError(rows.Err())
}

// Delete removes the inquiry with id; an absent id is sdk.ErrNotFound.
func (s *InquiryStore) Delete(ctx context.Context, id string) error {
	n, err := tursodb.ExecAffecting(ctx, s.db, "DELETE FROM inquiries WHERE id = ?", id)
	if err != nil {
		return tursodb.MapError(err)
	}
	if n == 0 {
		return sdk.ErrNotFound
	}
	return nil
}

// DeleteByEmail removes every inquiry whose email equals email under SQLite's
// lower() on both sides — exactly the ASCII case fold the port promises.
func (s *InquiryStore) DeleteByEmail(ctx context.Context, email string) (int, error) {
	n, err := tursodb.ExecAffecting(ctx, s.db, "DELETE FROM inquiries WHERE lower(email) = lower(?)", email)
	if err != nil {
		return 0, tursodb.MapError(err)
	}
	return int(n), nil
}

// PurgeBefore removes at most limit inquiries with created_at <= before, oldest
// first. created_at is fixed-width TEXT (tursodb.FormatTime), so the string
// comparison is the time comparison; 0022's created_at index is its access path.
func (s *InquiryStore) PurgeBefore(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("cms turso: inquiry purge limit %d must be positive: %w", limit, sdk.ErrInvalidInput)
	}
	const q = `DELETE FROM inquiries WHERE id IN (
		SELECT id FROM inquiries WHERE created_at <= ? ORDER BY created_at, id LIMIT ?)`
	n, err := tursodb.ExecAffecting(ctx, s.db, q, tursodb.FormatTime(before), limit)
	if err != nil {
		return 0, tursodb.MapError(err)
	}
	return int(n), nil
}
