// Package storetest is the exported conformance suite for the events feature's
// one outbound port: Run exercises an outbox.EntryRepository. Every store that
// fills it — the test-scoped in-memory reference (reference_test.go), the
// dialect adapters (features/events/stores/turso, .../postgres) — runs the same
// suite, so the port doc comments have one executable definition.
//
// The outbox.EntryRepository doc comments are the spec; this suite is their
// executable form (design §8). It imports stdlib + sdk + the events feature's
// own packages only (guard G2 forbids a driver import here), so features/events's
// own `go test ./...` runs it against the reference on every `make check`.
//
// The dialect-typed transactional appender (AppendTx) is deliberately NOT in
// this suite: it takes a store-specific Tx, so each store module tests it
// against its own integration (design §8).
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/events/logic/outbox"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
	"github.com/gopernicus/gopernicus/sdk/errs"
)

// suiteBase is the fixed OccurredAt every record carries. It is deliberately the
// SAME for every record so the CreatedAt-ascending ordering the port promises is
// proven against the store-assigned append time (Entry.CreatedAt), not against
// the event's own OccurredAt.
var suiteBase = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// Run exercises the outbox.EntryRepository contract against a clean, isolated
// repository obtained from newRepo for each leaf subtest. newRepo MUST return a
// CLEAN, isolated repository per call — a SQL harness truncates its table, the
// memory reference returns a fresh instance.
func Run(t *testing.T, newRepo func(t *testing.T) outbox.EntryRepository) {
	t.Helper()

	t.Run("AppendAndListOrder", func(t *testing.T) { testAppendAndListOrder(t, newRepo(t)) })
	t.Run("UnpublishedOnly", func(t *testing.T) { testUnpublishedOnly(t, newRepo(t)) })
	t.Run("MarkPublishedIdempotence", func(t *testing.T) { testMarkPublishedIdempotence(t, newRepo(t)) })
	t.Run("PurgePublishedRetention", func(t *testing.T) { testPurgePublishedRetention(t, newRepo(t)) })
	t.Run("EventIDUniqueness", func(t *testing.T) { testEventIDUniqueness(t, newRepo(t)) })
}

// testAppendAndListOrder proves the non-transactional append path, the
// empty-append no-op, and CreatedAt-ascending list order (oldest first, so the
// poller drains in append order). A short real sleep separates each append's
// CreatedAt so the tie-break survives a store that truncates timestamps.
func testAppendAndListOrder(t *testing.T, repo outbox.EntryRepository) {
	ctx := context.Background()

	// Appending zero records is a no-op that returns nil.
	if err := repo.Append(ctx); err != nil {
		t.Fatalf("Append() zero records: %v, want nil", err)
	}
	// A clean repo lists nothing.
	if got := mustList(t, repo, 10); len(got) != 0 {
		t.Fatalf("ListUnpublished(clean) = %v, want empty", entryIDs(got))
	}

	order := []string{"evt-1", "evt-2", "evt-3"}
	for _, eid := range order {
		mustAppend(t, repo, rec(eid))
		time.Sleep(3 * time.Millisecond)
	}

	got := mustList(t, repo, 10)
	if len(got) != len(order) {
		t.Fatalf("ListUnpublished returned %d entries, want %d", len(got), len(order))
	}
	for i, want := range order {
		if got[i].EventID != want {
			t.Errorf("entry %d = %q, want %q (CreatedAt ascending, oldest first)", i, got[i].EventID, want)
		}
		if got[i].PublishedAt != nil {
			t.Errorf("entry %q PublishedAt = %v, want nil (freshly appended is unpublished)", got[i].EventID, got[i].PublishedAt)
		}
	}
	// CreatedAt is strictly ascending across the returned page.
	for i := 1; i < len(got); i++ {
		if !got[i-1].CreatedAt.Before(got[i].CreatedAt) {
			t.Errorf("CreatedAt not ascending: entry %d (%v) is not before entry %d (%v)", i-1, got[i-1].CreatedAt, i, got[i].CreatedAt)
		}
	}
	// The durable envelope round-trips: the oldest entry carries what was appended.
	first := got[0]
	if first.Type != "test.event" || !first.OccurredAt.Equal(suiteBase) || string(first.Payload) != `{"eid":"evt-1"}` {
		t.Errorf("entry round-trip mismatch: type=%q occurredAt=%v payload=%s", first.Type, first.OccurredAt, first.Payload)
	}

	// A positive limit caps the page to the oldest N.
	limited := mustList(t, repo, 2)
	if ids := entryIDs(limited); len(ids) != 2 || ids[0] != "evt-1" || ids[1] != "evt-2" {
		t.Errorf("ListUnpublished(limit=2) = %v, want the two oldest [evt-1 evt-2]", ids)
	}
}

// testUnpublishedOnly proves published entries drop out of the unpublished
// listing while the rest stay in append order.
func testUnpublishedOnly(t *testing.T, repo outbox.EntryRepository) {
	mustAppend(t, repo, rec("a"))
	time.Sleep(2 * time.Millisecond)
	mustAppend(t, repo, rec("b"))
	time.Sleep(2 * time.Millisecond)
	mustAppend(t, repo, rec("c"))

	// Publishing b removes it; a and c remain, still oldest-first.
	mustMark(t, repo, "b")
	if ids := entryIDs(mustList(t, repo, 10)); len(ids) != 2 || ids[0] != "a" || ids[1] != "c" {
		t.Fatalf("ListUnpublished after publishing b = %v, want [a c]", ids)
	}

	// Publishing the rest leaves nothing unpublished.
	mustMark(t, repo, "a")
	mustMark(t, repo, "c")
	if got := mustList(t, repo, 10); len(got) != 0 {
		t.Errorf("ListUnpublished after publishing all = %v, want empty", entryIDs(got))
	}
}

// testMarkPublishedIdempotence proves MarkPublished is a nil no-op both for an
// already-published entry and for an unknown eventID (the row may have been
// purged), so the poller can retry a mark without a hard failure.
func testMarkPublishedIdempotence(t *testing.T, repo outbox.EntryRepository) {
	ctx := context.Background()
	mustAppend(t, repo, rec("m"))

	mustMark(t, repo, "m") // first mark publishes it
	if err := repo.MarkPublished(ctx, "m"); err != nil {
		t.Errorf("MarkPublished(already published): %v, want nil (idempotent)", err)
	}
	if err := repo.MarkPublished(ctx, "never-existed"); err != nil {
		t.Errorf("MarkPublished(unknown eventID): %v, want nil", err)
	}
	if got := mustList(t, repo, 10); len(got) != 0 {
		t.Errorf("ListUnpublished after idempotent marks = %v, want empty (m stays published)", entryIDs(got))
	}
}

// testPurgePublishedRetention proves PurgePublished deletes only published
// entries whose CreatedAt is strictly before the cutoff, returns the count
// removed, and never purges an unpublished entry regardless of age.
func testPurgePublishedRetention(t *testing.T, repo outbox.EntryRepository) {
	ctx := context.Background()

	mustAppend(t, repo, rec("keep-unpub")) // stays unpublished for the whole case
	time.Sleep(3 * time.Millisecond)
	mustAppend(t, repo, rec("old-pub"))
	time.Sleep(3 * time.Millisecond)
	mustAppend(t, repo, rec("new-pub"))

	// Read the store-assigned CreatedAts — purge keys on them, not on OccurredAt.
	at := entriesByID(t, repo)
	newAt := at["new-pub"].CreatedAt

	mustMark(t, repo, "old-pub")
	mustMark(t, repo, "new-pub")

	// A cutoff equal to new-pub's CreatedAt purges only old-pub: new-pub's own
	// CreatedAt is not STRICTLY before the cutoff, so it is retained.
	n, err := repo.PurgePublished(ctx, newAt)
	if err != nil {
		t.Fatalf("PurgePublished: %v", err)
	}
	if n != 1 {
		t.Fatalf("PurgePublished(newAt) removed %d, want 1 (only old-pub, strictly before cutoff)", n)
	}
	// keep-unpub is unpublished → never purged, still the only unpublished row.
	if ids := entryIDs(mustList(t, repo, 10)); len(ids) != 1 || ids[0] != "keep-unpub" {
		t.Fatalf("unpublished after purge = %v, want [keep-unpub] (unpublished never purged)", ids)
	}

	// A far-future cutoff removes the remaining published new-pub; the still
	// unpublished keep-unpub survives.
	n2, err := repo.PurgePublished(ctx, newAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("PurgePublished(future): %v", err)
	}
	if n2 != 1 {
		t.Errorf("PurgePublished(future) removed %d, want 1 (new-pub)", n2)
	}
	if ids := entryIDs(mustList(t, repo, 10)); len(ids) != 1 || ids[0] != "keep-unpub" {
		t.Errorf("unpublished after second purge = %v, want [keep-unpub] still present", ids)
	}
}

// testEventIDUniqueness proves the EventID is the primary key and the
// at-least-once de-dupe key: appending a record whose EventID already exists
// returns errs.ErrAlreadyExists and leaves the original row untouched.
func testEventIDUniqueness(t *testing.T, repo outbox.EntryRepository) {
	ctx := context.Background()
	mustAppend(t, repo, rec("dup"))

	if err := repo.Append(ctx, rec("dup")); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("duplicate Append: err=%v, want ErrAlreadyExists", err)
	}
	// Exactly one row remains — the rejected duplicate changed nothing.
	if ids := entryIDs(mustList(t, repo, 10)); len(ids) != 1 || ids[0] != "dup" {
		t.Errorf("after rejected duplicate = %v, want exactly [dup]", ids)
	}
}

// --- helpers ---

// rec builds a durable envelope keyed by eventID. Every record shares suiteBase
// as OccurredAt so ordering assertions turn on the store-assigned CreatedAt.
func rec(eventID string) sdkevents.Record {
	return sdkevents.Record{
		EventID:    eventID,
		Type:       "test.event",
		OccurredAt: suiteBase,
		Payload:    []byte(`{"eid":"` + eventID + `"}`),
	}
}

func mustAppend(t *testing.T, repo outbox.EntryRepository, recs ...sdkevents.Record) {
	t.Helper()
	if err := repo.Append(context.Background(), recs...); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func mustMark(t *testing.T, repo outbox.EntryRepository, eventID string) {
	t.Helper()
	if err := repo.MarkPublished(context.Background(), eventID); err != nil {
		t.Fatalf("MarkPublished(%q): %v", eventID, err)
	}
}

func mustList(t *testing.T, repo outbox.EntryRepository, limit int) []outbox.Entry {
	t.Helper()
	got, err := repo.ListUnpublished(context.Background(), limit)
	if err != nil {
		t.Fatalf("ListUnpublished: %v", err)
	}
	return got
}

func entriesByID(t *testing.T, repo outbox.EntryRepository) map[string]outbox.Entry {
	t.Helper()
	m := map[string]outbox.Entry{}
	for _, e := range mustList(t, repo, 100) {
		m[e.EventID] = e
	}
	return m
}

func entryIDs(entries []outbox.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.EventID
	}
	return out
}
