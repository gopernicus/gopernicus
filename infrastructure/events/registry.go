package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// EventHandler processes an event from an outbox or queue.
// The handler receives the event type and raw JSON payload.
type EventHandler func(ctx context.Context, eventType string, payload json.RawMessage) error

// EventRegistry routes events to their handlers based on event type patterns.
// It supports exact matches, prefix patterns (e.g., "email.*"), and wildcards ("*").
type EventRegistry struct {
	mu       sync.RWMutex
	exact    map[string]EventHandler
	prefixes []prefixHandler
	log      *slog.Logger
}

type prefixHandler struct {
	prefix  string
	handler EventHandler
}

// NewEventRegistry creates a new event handler registry.
func NewEventRegistry(log *slog.Logger) *EventRegistry {
	return &EventRegistry{
		exact:    make(map[string]EventHandler),
		prefixes: make([]prefixHandler, 0),
		log:      log,
	}
}

// Register registers a handler for the given event type pattern.
// Patterns can be:
//   - Exact: "email.verification_code" matches only that type
//   - Prefix: "email.*" matches any type starting with "email."
//   - Wildcard: "*" matches all event types (catch-all)
func (r *EventRegistry) Register(pattern string, handler EventHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, "*")
		r.prefixes = append(r.prefixes, prefixHandler{prefix: prefix, handler: handler})
		r.log.Info("handler registered", "pattern", pattern, "type", "prefix")
	} else if pattern == "*" {
		r.prefixes = append(r.prefixes, prefixHandler{prefix: "", handler: handler})
		r.log.Info("handler registered", "pattern", pattern, "type", "wildcard")
	} else {
		r.exact[pattern] = handler
		r.log.Info("handler registered", "pattern", pattern, "type", "exact")
	}
}

// Handle routes an event to its handler and executes it.
// Returns an error if no handler is found or if the handler fails.
func (r *EventRegistry) Handle(ctx context.Context, eventType string, payload json.RawMessage) error {
	handler := r.findHandler(eventType)
	if handler == nil {
		return fmt.Errorf("no handler for event type: %s", eventType)
	}
	return handler(ctx, eventType, payload)
}

// findHandler finds the appropriate handler for an event type.
// Priority: exact match > prefix match (longest prefix first) > wildcard.
func (r *EventRegistry) findHandler(eventType string) EventHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if h, ok := r.exact[eventType]; ok {
		return h
	}

	var bestMatch EventHandler
	bestLen := -1
	for _, ph := range r.prefixes {
		if strings.HasPrefix(eventType, ph.prefix) && len(ph.prefix) > bestLen {
			bestMatch = ph.handler
			bestLen = len(ph.prefix)
		}
	}

	return bestMatch
}

// HasHandler returns true if there's a handler registered for the event type.
func (r *EventRegistry) HasHandler(eventType string) bool {
	return r.findHandler(eventType) != nil
}
