package events_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gopernicus/gopernicus/infrastructure/events"
)

func newTestRegistry() *events.EventRegistry {
	return events.NewEventRegistry()
}

// =============================================================================
// Exact Match
// =============================================================================

func TestRegistry_ExactMatch(t *testing.T) {
	r := newTestRegistry()
	var called bool

	r.Register("user.created", func(_ context.Context, _ string, _ json.RawMessage) error {
		called = true
		return nil
	})

	err := r.Handle(context.Background(), "user.created", nil)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !called {
		t.Error("exact match handler should be called")
	}
}

func TestRegistry_ExactMatch_NoMatch(t *testing.T) {
	r := newTestRegistry()
	r.Register("user.created", func(_ context.Context, _ string, _ json.RawMessage) error {
		return nil
	})

	err := r.Handle(context.Background(), "user.deleted", nil)
	if err == nil {
		t.Error("Handle() should return error for unregistered type")
	}
}

// =============================================================================
// Prefix Match
// =============================================================================

func TestRegistry_PrefixMatch(t *testing.T) {
	r := newTestRegistry()
	var receivedType string

	r.Register("email.*", func(_ context.Context, eventType string, _ json.RawMessage) error {
		receivedType = eventType
		return nil
	})

	err := r.Handle(context.Background(), "email.verification", nil)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if receivedType != "email.verification" {
		t.Errorf("received type = %q, want %q", receivedType, "email.verification")
	}
}

func TestRegistry_PrefixMatch_LongestWins(t *testing.T) {
	r := newTestRegistry()
	var handler string

	r.Register("email.*", func(_ context.Context, _ string, _ json.RawMessage) error {
		handler = "email"
		return nil
	})
	r.Register("email.verification.*", func(_ context.Context, _ string, _ json.RawMessage) error {
		handler = "email.verification"
		return nil
	})

	r.Handle(context.Background(), "email.verification.code", nil)
	if handler != "email.verification" {
		t.Errorf("handler = %q, want %q (longest prefix)", handler, "email.verification")
	}
}

// =============================================================================
// Wildcard Match
// =============================================================================

func TestRegistry_Wildcard(t *testing.T) {
	r := newTestRegistry()
	var called bool

	r.Register("*", func(_ context.Context, _ string, _ json.RawMessage) error {
		called = true
		return nil
	})

	err := r.Handle(context.Background(), "anything.at.all", nil)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !called {
		t.Error("wildcard handler should be called")
	}
}

func TestRegistry_ExactTakesPriority(t *testing.T) {
	r := newTestRegistry()
	var handler string

	r.Register("*", func(_ context.Context, _ string, _ json.RawMessage) error {
		handler = "wildcard"
		return nil
	})
	r.Register("user.created", func(_ context.Context, _ string, _ json.RawMessage) error {
		handler = "exact"
		return nil
	})

	r.Handle(context.Background(), "user.created", nil)
	if handler != "exact" {
		t.Errorf("handler = %q, want %q (exact takes priority)", handler, "exact")
	}
}

// =============================================================================
// HasHandler
// =============================================================================

func TestHasHandler_True(t *testing.T) {
	r := newTestRegistry()
	r.Register("user.created", func(_ context.Context, _ string, _ json.RawMessage) error {
		return nil
	})

	if !r.HasHandler("user.created") {
		t.Error("HasHandler() = false, want true")
	}
}

func TestHasHandler_False(t *testing.T) {
	r := newTestRegistry()

	if r.HasHandler("user.created") {
		t.Error("HasHandler() = true, want false")
	}
}

func TestHasHandler_ViaWildcard(t *testing.T) {
	r := newTestRegistry()
	r.Register("*", func(_ context.Context, _ string, _ json.RawMessage) error {
		return nil
	})

	if !r.HasHandler("anything") {
		t.Error("HasHandler() with wildcard should return true")
	}
}
