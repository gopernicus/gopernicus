package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

// InquiryStore implements messaging.InquiryRepository over a libSQL database.
type InquiryStore struct {
	db *tursodb.DB
}

var _ messaging.InquiryRepository = (*InquiryStore)(nil)

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
	// Empty ID → the cryptids.Database strategy (amended D10): omit the id
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
