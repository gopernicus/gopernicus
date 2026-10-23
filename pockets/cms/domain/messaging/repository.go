package messaging

import (
	"context"
	"time"
)

// InquiryRepository is the app port for persisting inquiries. Implemented by
// pocket store adapters (pockets/cms/stores/turso) or any host-provided
// implementation (see examples/minimal's memstore).
type InquiryRepository interface {
	// Create persists a new inquiry.
	Create(ctx context.Context, in Inquiry) (Inquiry, error)
	// List returns all inquiries, newest first.
	List(ctx context.Context) ([]Inquiry, error)
}

// InquiryEraser is the OPTIONAL disposal capability over the same inquiries an
// InquiryRepository persists, filled as cms.Repositories.InquiryMaintenance.
// The bundled stores implement it; a host store that does not keeps today's
// behavior (nothing is ever deleted) and production Register WARNs. The host
// calls the eraser it wired directly — a retention schedule runs PurgeBefore,
// an erasure request runs DeleteByEmail; the pocket mounts no route for it.
type InquiryEraser interface {
	// Delete removes the inquiry with id. An absent id is sdk.ErrNotFound.
	Delete(ctx context.Context, id string) error
	// DeleteByEmail removes every inquiry whose email equals email under ASCII
	// case folding (A–Z matches a–z) and returns how many it removed; none is
	// 0 with a nil error. Emails are stored as submitted, so the fold is what
	// matches "Bob@Example.com" to "bob@example.com". Only the ASCII fold is the
	// contract: how letters outside ASCII compare is store-defined (SQLite's
	// lower() folds ASCII only; PostgreSQL's follows the database's ctype).
	DeleteByEmail(ctx context.Context, email string) (int, error)
	// PurgeBefore removes at most limit inquiries whose created_at is at or
	// before before (<=: a row exactly AT before is removed), oldest first by
	// (created_at, id), and returns how many it removed. A backlog larger than
	// limit drains over successive calls. limit <= 0 is refused with
	// sdk.ErrInvalidInput and removes nothing.
	PurgeBefore(ctx context.Context, before time.Time, limit int) (int, error)
}
