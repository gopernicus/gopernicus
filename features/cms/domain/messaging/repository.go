package messaging

import "context"

// InquiryRepository is the app port for persisting inquiries. Implemented by
// feature store adapters (features/cms/stores/turso) or any host-provided
// implementation (see examples/minimal's memstore).
type InquiryRepository interface {
	// Create persists a new inquiry.
	Create(ctx context.Context, in Inquiry) (Inquiry, error)
	// List returns all inquiries, newest first.
	List(ctx context.Context) ([]Inquiry, error)
}
