package outbox_test

import (
	"context"
	"testing"
	"time"

	sdkevents "github.com/gopernicus/gopernicus/sdk/events"

	"github.com/gopernicus/gopernicus/features/events/logic/outbox"
)

// stubRepo is a doc-contract compile check: it proves the EntryRepository
// signatures are implementable and pins them to the exact shapes the storetest
// suite (task-6) will drive. It is not a behavioral fake.
type stubRepo struct{}

var _ outbox.EntryRepository = stubRepo{}

func (stubRepo) Append(ctx context.Context, recs ...sdkevents.Record) error { return nil }

func (stubRepo) ListUnpublished(ctx context.Context, limit int) ([]outbox.Entry, error) {
	return nil, nil
}

func (stubRepo) MarkPublished(ctx context.Context, eventID string) error { return nil }

func (stubRepo) PurgePublished(ctx context.Context, before time.Time) (int, error) { return 0, nil }

// TestEntryZeroValueUnpublished pins the nil-PublishedAt = unpublished
// semantics the poller and stores rely on.
func TestEntryZeroValueUnpublished(t *testing.T) {
	var e outbox.Entry
	if e.PublishedAt != nil {
		t.Fatalf("zero-value Entry.PublishedAt = %v, want nil (unpublished)", e.PublishedAt)
	}
	if !e.CreatedAt.IsZero() {
		t.Fatalf("zero-value Entry.CreatedAt = %v, want zero time", e.CreatedAt)
	}
}

// TestEntryEmbedsRecord confirms the embedded Record fields (EventID as the
// de-dupe key foremost) are promoted onto Entry.
func TestEntryEmbedsRecord(t *testing.T) {
	now := time.Now()
	e := outbox.Entry{
		Record: sdkevents.Record{
			EventID: "evt-1",
			Type:    "content.published",
		},
		CreatedAt:   now,
		PublishedAt: &now,
	}
	if e.EventID != "evt-1" {
		t.Fatalf("Entry.EventID = %q, want %q", e.EventID, "evt-1")
	}
	if e.Type != "content.published" {
		t.Fatalf("Entry.Type = %q, want %q", e.Type, "content.published")
	}
	if e.PublishedAt == nil {
		t.Fatal("Entry.PublishedAt = nil, want non-nil (published)")
	}
}
