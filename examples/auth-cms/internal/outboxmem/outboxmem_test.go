package outboxmem

import (
	"testing"

	"github.com/gopernicus/gopernicus/features/events/logic/outbox"
	"github.com/gopernicus/gopernicus/features/events/storetest"
)

// TestConformance runs the shared events storetest suite against outboxmem — the
// cheapest honesty proof (the auth-cms memstore precedent). The suite's
// EventIDUniqueness case only passes because Append genuinely enforces the
// primary-key invariant a SQL store gets for free; a store that silently
// overwrote or blindly appended a duplicate would fail it. A fresh Store per
// call is the clean-isolation contract Run documents.
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) outbox.EntryRepository {
		return New()
	})
}
