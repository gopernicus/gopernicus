package pgx

import (
	"context"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/logic/messaging"
	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"
)

// InquiryStore implements messaging.InquiryRepository over a PostgreSQL database.
type InquiryStore struct {
	db *pgxdb.DB
}

var _ messaging.InquiryRepository = (*InquiryStore)(nil)

// NewInquiryStore returns an InquiryStore backed by db.
func NewInquiryStore(db *pgxdb.DB) *InquiryStore {
	return &InquiryStore{db: db}
}

const inquiryColumns = "id, name, email, message, created_at"

// Create persists a new inquiry.
func (s *InquiryStore) Create(ctx context.Context, in messaging.Inquiry) (messaging.Inquiry, error) {
	const q = `INSERT INTO inquiries (` + inquiryColumns + `) VALUES ($1, $2, $3, $4, $5)`
	_, err := s.db.Exec(ctx, q, in.ID, in.Name, in.Email, in.Message, in.CreatedAt.UTC())
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
		var (
			in        messaging.Inquiry
			createdAt time.Time
		)
		if err := rows.Scan(&in.ID, &in.Name, &in.Email, &in.Message, &createdAt); err != nil {
			return nil, pgxdb.MapError(err)
		}
		in.CreatedAt = createdAt.UTC()
		out = append(out, in)
	}
	return out, pgxdb.MapError(rows.Err())
}
