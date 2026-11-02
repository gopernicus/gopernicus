package storetest

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// auditExpiryAge places the fixture's boundary safely below every bundled
// store's 365-day retention floor.
const auditExpiryAge = 400 * 24 * time.Hour

// runAuditExpiry proves the audit.Expirer contract: records at or before the
// cutoff expire oldest first (occurred_at, then ID) and bounded by limit, newer
// records survive, a cutoff younger than the floor is refused and deletes
// nothing, and limit <= 0 is refused.
func runAuditExpiry(t *testing.T, newRepos func(*testing.T) Repositories) {
	expiryRepos := func(t *testing.T) (Repositories, []string) {
		t.Helper()
		r := newRepos(t)
		requireAudit(t, r)
		if r.AuditExpiry == nil {
			t.Skip("Repositories.AuditExpiry not wired — audit expiry NOT verified")
		}
		if r.SetAuditOccurredAt == nil {
			t.Fatal("Repositories.AuditExpiry is wired but SetAuditOccurredAt is nil: records cannot be aged below the retention floor")
		}
		ctx := audit.WithSource(t.Context(), audit.Source{System: "audit-expiry"})
		var rows []relationships.CreateRelationship
		for _, subject := range []string{"a", "b", "c", "d", "e"} {
			rows = append(rows, ct("doc", "expiry", "viewer", "user", subject))
		}
		if err := r.Relationships.CreateRelationships(ctx, rows); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, record := range readAudit(t, r.Audit) {
			ids = append(ids, record.ID)
		}
		if len(ids) != len(rows) {
			t.Fatalf("fixture recorded %d rows, want %d", len(ids), len(rows))
		}
		slices.Sort(ids)
		return r, ids
	}
	remaining := func(t *testing.T, r Repositories) []string {
		t.Helper()
		var ids []string
		for _, record := range readAudit(t, r.Audit) {
			ids = append(ids, record.ID)
		}
		slices.Sort(ids)
		return ids
	}
	mustExpire := func(t *testing.T, r Repositories, before time.Time, limit, want int) {
		t.Helper()
		got, err := r.AuditExpiry.Expire(t.Context(), before, limit)
		if err != nil || got != want {
			t.Fatalf("Expire(%s, %d) = %d %v, want %d", before.Format(time.RFC3339Nano), limit, got, err, want)
		}
	}

	t.Run("OldestFirstBoundedAndBoundary", func(t *testing.T) {
		r, ids := expiryRepos(t)
		// Microsecond-aligned, so every store keeps the fixture times exactly.
		boundary := time.Now().Add(-auditExpiryAge).UTC().Truncate(time.Microsecond)
		r.SetAuditOccurredAt(t, ids[4], boundary.Add(-2*time.Microsecond))
		r.SetAuditOccurredAt(t, ids[2], boundary.Add(-time.Microsecond))
		r.SetAuditOccurredAt(t, ids[3], boundary.Add(-time.Microsecond))
		r.SetAuditOccurredAt(t, ids[0], boundary)
		// ids[1] keeps its write time and must survive every pass.
		mustExpire(t, r, boundary, 2, 2)
		if got, want := remaining(t, r), []string{ids[0], ids[1], ids[3]}; !slices.Equal(got, want) {
			t.Fatalf("after the first pass: %q, want %q (oldest first, ties by ID)", got, want)
		}
		// A record exactly at the cutoff is eligible.
		mustExpire(t, r, boundary, 2, 2)
		mustExpire(t, r, boundary, 2, 0)
		if got := remaining(t, r); !slices.Equal(got, []string{ids[1]}) {
			t.Fatalf("after expiry: %q, want only %q", got, ids[1])
		}
	})

	t.Run("BelowFloorRefused", func(t *testing.T) {
		r, ids := expiryRepos(t)
		for _, id := range ids {
			r.SetAuditOccurredAt(t, id, time.Now().Add(-200*24*time.Hour).UTC().Truncate(time.Microsecond))
		}
		for _, before := range []time.Time{time.Now(), time.Now().Add(-100 * 24 * time.Hour), time.Now().Add(-364 * 24 * time.Hour)} {
			if got, err := r.AuditExpiry.Expire(t.Context(), before, 10); got != 0 || !errors.Is(err, audit.ErrBelowRetentionFloor) || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("Expire(%s) = %d %v, want audit.ErrBelowRetentionFloor", before.Format(time.RFC3339), got, err)
			}
		}
		if got := remaining(t, r); !slices.Equal(got, ids) {
			t.Fatalf("a refused purge deleted history: %q", got)
		}
		// Nothing is old enough at a floor-clearing cutoff: a zero count, no error.
		mustExpire(t, r, time.Now().Add(-auditExpiryAge), 10, 0)
	})

	t.Run("LimitZeroRefused", func(t *testing.T) {
		r, ids := expiryRepos(t)
		for _, id := range ids {
			r.SetAuditOccurredAt(t, id, time.Now().Add(-2*auditExpiryAge).UTC().Truncate(time.Microsecond))
		}
		for _, limit := range []int{0, -1} {
			if got, err := r.AuditExpiry.Expire(t.Context(), time.Now().Add(-auditExpiryAge), limit); got != 0 || !errors.Is(err, sdk.ErrInvalidInput) || errors.Is(err, audit.ErrBelowRetentionFloor) {
				t.Fatalf("Expire limit %d = %d %v, want sdk.ErrInvalidInput", limit, got, err)
			}
		}
		if got := remaining(t, r); len(got) != len(ids) {
			t.Fatalf("a refused purge deleted history: %s", strings.Join(got, ","))
		}
		mustExpire(t, r, time.Now().Add(-auditExpiryAge), audit.MaxExpireLimit+1, len(ids))
	})
}
