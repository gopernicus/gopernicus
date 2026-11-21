package oauth2

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

func TestExchangeAuditAdmit(t *testing.T) {
	start := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	a := newExchangeAudit(time.Hour)
	step := func(id string, at time.Duration, wantRecord bool, wantCoalesced int) {
		t.Helper()
		record, coalesced := a.admit(id, start.Add(at))
		if record != wantRecord || coalesced != wantCoalesced {
			t.Fatalf("admit(%s, +%s) = %v, %d; want %v, %d", id, at, record, coalesced, wantRecord, wantCoalesced)
		}
	}
	step("a", 0, true, 0)
	for i := 1; i <= 5; i++ {
		step("a", time.Duration(i)*10*time.Minute, false, 0)
	}
	step("b", 30*time.Minute, true, 0) // sessions coalesce independently
	// An active connection keeps its count across the sweep at the window edge.
	step("a", 61*time.Minute, true, 5)
	step("a", 62*time.Minute, false, 0)
	// b went quiet for a whole window: its mark is swept, so it writes again
	// with no count.
	step("b", 3*time.Hour, true, 0)
	if _, ok := a.marks["a"]; ok {
		t.Fatal("a quiet connection's mark was not swept")
	}
	if newExchangeAudit(0) != nil {
		t.Fatal("a zero window built a coalescer")
	}
}

func TestExchangeAuditRows(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	build := func(t *testing.T, window time.Duration) (*Service, *auditEvents) {
		t.Helper()
		events := &auditEvents{}
		cfg := protocolConfig()
		cfg.ExchangeAuditWindow = window
		s, err := New(Repositories{OAuth2: &constructorRepository{}, Users: &constructorUsers{}, Sessions: &constructorSessions{}, SecurityEvents: events},
			&constructorSigner{}, clientResolverFunc(func(context.Context, string) (Client, error) { return Client{}, ErrInvalidClient }),
			&constructorVerifier{}, environment.ModeDevelopment, cfg, WithClock(func() time.Time { return now }))
		if err != nil {
			t.Fatal(err)
		}
		return s, events
	}
	t.Run("EveryExchangeByDefault", func(t *testing.T) {
		s, events := build(t, 0)
		for range 3 {
			s.auditExchange(t.Context(), "u-1", "sess-1", "mcp")
		}
		if len(events.created) != 3 {
			t.Fatalf("rows = %d, want 3", len(events.created))
		}
		if _, ok := events.created[0].Details["coalesced"]; ok {
			t.Fatalf("an uncoalesced row carries coalesced: %+v", events.created[0].Details)
		}
	})
	t.Run("OncePerWindow", func(t *testing.T) {
		s, events := build(t, time.Hour)
		for range 4 { // at +0, +10m, +20m, +30m
			s.auditExchange(t.Context(), "u-1", "sess-1", "mcp")
			now = now.Add(10 * time.Minute)
		}
		now = now.Add(21 * time.Minute) // +61m
		s.auditExchange(t.Context(), "u-1", "sess-1", "mcp")
		if len(events.created) != 2 {
			t.Fatalf("rows = %d, want 2", len(events.created))
		}
		first, second := events.created[0], events.created[1]
		if first.EventType != TypeTokenExchanged || first.Details["coalesced"] != 0 || first.Details["session_id"] != "sess-1" || first.Details["client_id"] != "mcp" {
			t.Fatalf("first row = %+v", first)
		}
		if second.Details["coalesced"] != 3 {
			t.Fatalf("second row coalesced = %v, want 3", second.Details["coalesced"])
		}
	})
}

func TestExchangeAuditWindowBounds(t *testing.T) {
	for _, window := range []time.Duration{-time.Second, time.Millisecond, MaxExchangeAuditWindow + time.Second} {
		cfg := protocolConfig()
		cfg.ExchangeAuditWindow = window
		if _, err := constructorService(t, cfg, environment.ModeProduction); !errors.Is(err, ErrExchangeAuditWindowInvalid) {
			t.Errorf("window %s: err = %v, want ErrExchangeAuditWindowInvalid", window, err)
		}
	}
	for _, window := range []time.Duration{0, time.Second, MaxExchangeAuditWindow} {
		cfg := protocolConfig()
		cfg.ExchangeAuditWindow = window
		if _, err := constructorService(t, cfg, environment.ModeProduction); err != nil {
			t.Errorf("window %s: err = %v", window, err)
		}
	}
}
