package turso

import (
	"context"

	"github.com/gopernicus/gopernicus/features/cms/logic/messaging"
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

// Create persists a new inquiry.
func (s *InquiryStore) Create(ctx context.Context, in messaging.Inquiry) (messaging.Inquiry, error) {
	const q = `INSERT INTO inquiries (` + inquiryColumns + `) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.Exec(ctx, q, in.ID, in.Name, in.Email, in.Message, in.CreatedAt.UTC().Format(tsLayout))
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
			createdAt string
		)
		if err := rows.Scan(&in.ID, &in.Name, &in.Email, &in.Message, &createdAt); err != nil {
			return nil, tursodb.MapError(err)
		}
		if in.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, tursodb.MapError(rows.Err())
}
