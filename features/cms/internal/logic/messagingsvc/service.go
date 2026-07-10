// Package messagingsvc holds the contact-inquiry use-case service, kept internal
// so it is not part of the feature's public SemVer surface (plan §5/B3). The
// public domain types and InquiryRepository interface stay in package messaging.
package messagingsvc

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/domain/messaging"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// Clock returns the current time. Injected so tests can pin timestamps.
type Clock func() time.Time

// Service implements the contact-inquiry use cases: persist a submission and
// notify the operator by email.
type Service struct {
	inquiries messaging.InquiryRepository
	sender    email.Sender
	from      string // From address for notification emails
	notifyTo  string // operator address that receives inquiries
	// ids is the app-chosen entity-ID strategy (cms.Config.IDs); zero value →
	// default nanoids.
	ids   cryptids.IDGenerator
	clock Clock
}

// NewService constructs a Service. A nil clock defaults to time.Now. ids is the
// app's entity-ID strategy (cms.Config.IDs).
func NewService(inquiries messaging.InquiryRepository, sender email.Sender, from, notifyTo string, ids cryptids.IDGenerator, clock Clock) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{inquiries: inquiries, sender: sender, from: from, notifyTo: notifyTo, ids: ids, clock: clock}
}

// Submit validates and persists the inquiry, then emails the operator. The
// inquiry is persisted before the notification is attempted, so a send failure
// still leaves the submission captured (and is returned to the caller).
func (s *Service) Submit(ctx context.Context, name, fromEmail, message string) (messaging.Inquiry, error) {
	inq, err := messaging.NewInquiry(s.ids, name, fromEmail, message, s.clock())
	if err != nil {
		return messaging.Inquiry{}, err
	}
	saved, err := s.inquiries.Create(ctx, inq)
	if err != nil {
		return messaging.Inquiry{}, err
	}

	msg := email.Message{
		From:    s.from,
		To:      []string{s.notifyTo},
		Subject: "New contact inquiry from " + saved.Name,
		Text:    fmt.Sprintf("Name: %s\nEmail: %s\n\n%s", saved.Name, saved.Email, saved.Message),
	}
	if err := s.sender.Send(ctx, msg); err != nil {
		return saved, fmt.Errorf("notify operator: %w", err)
	}
	return saved, nil
}

// ListInquiries returns all inquiries, newest first.
func (s *Service) ListInquiries(ctx context.Context) ([]messaging.Inquiry, error) {
	return s.inquiries.List(ctx)
}
