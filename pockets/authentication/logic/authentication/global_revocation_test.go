package authentication

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

// scriptedRevoker answers RevokeAllBatch from a script of batches, failing at
// failAt (1-based; zero never fails), and records each cursor it was called with.
type scriptedRevoker struct {
	batches []session.GlobalRevocationBatch
	failAt  int
	sweep   [2]int
	afters  []string
	limits  []int
}

func (r *scriptedRevoker) RevokeAllBatch(_ context.Context, after string, limit int, _ time.Time) (session.GlobalRevocationBatch, error) {
	r.afters = append(r.afters, after)
	r.limits = append(r.limits, limit)
	n := len(r.afters)
	if n == r.failAt {
		return session.GlobalRevocationBatch{}, sdk.ErrUnavailable
	}
	return r.batches[n-1], nil
}

func (r *scriptedRevoker) SweepOrphans(context.Context, time.Time) (int, int, error) {
	return r.sweep[0], r.sweep[1], nil
}

func globalRows(events *spySecurityEvents) []securityevent.SecurityEvent {
	var out []securityevent.SecurityEvent
	for _, e := range events.recorded() {
		if e.EventType == securityevent.TypeAllSessionsRevokedGlobal {
			out = append(out, e)
		}
	}
	return out
}

func TestRevokeAllSessionsUnwired(t *testing.T) {
	h := newHarness(t, nil)
	if h.svc.GlobalSessionRevocationEnabled() {
		t.Fatal("enabled without a GlobalRevoker")
	}
	if _, err := h.svc.RevokeAllSessions(context.Background(), Principal{Type: PrincipalUser, ID: "op"}); !errors.Is(err, ErrGlobalRevocationUnsupported) || !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("unwired = %v, want ErrGlobalRevocationUnsupported", err)
	}
	if rows := globalRows(h.events); len(rows) != 0 {
		t.Fatalf("an unwired call recorded %+v", rows)
	}
}

// TestRevokeAllSessionsBatchesAndRecords pins the run: batches of 500 keyed on
// the last user id until Done, the sweep added in, and the started/success pair
// attributed to the operator with the counts.
func TestRevokeAllSessionsBatchesAndRecords(t *testing.T) {
	h := newHarness(t, nil)
	rev := &scriptedRevoker{
		batches: []session.GlobalRevocationBatch{
			{Users: 500, Sessions: 700, Grants: 3, LastUserID: "u500"},
			{Users: 12, Sessions: 20, Grants: 1, LastUserID: "u512", Done: true},
		},
		sweep: [2]int{4, 2},
	}
	h.svc.globalSessions = rev
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	h.svc.now = func() time.Time { return at }
	actor := Principal{Type: PrincipalUser, ID: "op-1"}

	got, err := h.svc.RevokeAllSessions(context.Background(), actor)
	if err != nil {
		t.Fatalf("RevokeAllSessions: %v", err)
	}
	want := GlobalRevocation{Users: 512, Sessions: 724, Grants: 6, Batches: 2, RevokedAt: at}
	if got != want {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
	if strings.Join(rev.afters, ",") != ",u500" || rev.limits[0] != 500 {
		t.Fatalf("cursors = %v limits = %v, want '' then u500 at 500", rev.afters, rev.limits)
	}
	rows := globalRows(h.events)
	if len(rows) != 2 || rows[0].EventStatus != securityevent.StatusStarted || rows[1].EventStatus != securityevent.StatusSuccess {
		t.Fatalf("rows = %+v, want started then success", rows)
	}
	for _, r := range rows {
		if r.Actor.Type != PrincipalUser || r.Actor.ID != "op-1" || r.UserID != "" {
			t.Errorf("row attribution = %+v / user %q, want the operator as actor", r.Actor, r.UserID)
		}
	}
	if rows[0].Details["batch_size"] != 500 {
		t.Errorf("started details = %v, want batch_size 500", rows[0].Details)
	}
	for k, v := range map[string]any{"users": 512, "sessions": 724, "grants": 6, "batches": 2} {
		if rows[1].Details[k] != v {
			t.Errorf("success details = %v, want %s=%v", rows[1].Details, k, v)
		}
	}
	if _, ok := rows[1].Details["duration_ms"]; !ok {
		t.Error("success row has no duration_ms")
	}
}

// TestRevokeAllSessionsFailureKeepsPartialCounts proves a batch failure returns
// the partial counts beside the error and records failure with error_kind.
func TestRevokeAllSessionsFailureKeepsPartialCounts(t *testing.T) {
	h := newHarness(t, nil)
	h.svc.globalSessions = &scriptedRevoker{
		batches: []session.GlobalRevocationBatch{{Users: 500, Sessions: 500, LastUserID: "u500"}},
		failAt:  2,
	}
	got, err := h.svc.RevokeAllSessions(context.Background(), Principal{Type: PrincipalUser, ID: "op"})
	if !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("err = %v, want the store's error", err)
	}
	if got.Users != 500 || got.Batches != 1 {
		t.Fatalf("partial = %+v, want the committed batch", got)
	}
	rows := globalRows(h.events)
	if len(rows) != 2 || rows[1].EventStatus != securityevent.StatusFailure || rows[1].Details["users"] != 500 || rows[1].Details["error_kind"] == "" {
		t.Fatalf("rows = %+v, want started then failure with partial counts and error_kind", rows)
	}
}

// TestRevokeAllSessionsEvidenceFailureLogsError proves a lost evidence row is
// an ERROR line, never swallowed.
func TestRevokeAllSessionsEvidenceFailureLogsError(t *testing.T) {
	h := newHarness(t, nil)
	h.svc.globalSessions = &scriptedRevoker{batches: []session.GlobalRevocationBatch{{Done: true}}}
	var buf bytes.Buffer
	h.svc.logger = slog.New(slog.NewTextHandler(&buf, nil))
	h.events.createErr = errors.New("rail down")
	if _, err := h.svc.RevokeAllSessions(context.Background(), Principal{Type: PrincipalUser, ID: "op"}); err != nil {
		t.Fatalf("a rail failure failed the revocation: %v", err)
	}
	if strings.Count(buf.String(), "level=ERROR") != 2 || !strings.Contains(buf.String(), "error_kind=") || !strings.Contains(buf.String(), "event_status=started") {
		t.Fatalf("log = %s, want two ERROR lines with error_kind", buf.String())
	}
}

// TestRevokeAllSessionsEvidenceFailureIsCounted proves the evidence rows run
// the recorder's failure accounting: a lost row counts in WriteFailures.
func TestRevokeAllSessionsEvidenceFailureIsCounted(t *testing.T) {
	h := newHarness(t, nil)
	h.svc.globalSessions = &scriptedRevoker{batches: []session.GlobalRevocationBatch{{Done: true}}}
	h.events.createErr = errors.New("rail down")
	before := h.svc.securityEvents.WriteFailures()
	if _, err := h.svc.RevokeAllSessions(context.Background(), Principal{Type: PrincipalUser, ID: "op"}); err != nil {
		t.Fatal(err)
	}
	if got := h.svc.securityEvents.WriteFailures() - before; got != 2 {
		t.Fatalf("write failures counted = %d, want 2", got)
	}
}
