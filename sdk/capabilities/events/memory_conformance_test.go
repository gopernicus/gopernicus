package events_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/capabilities/events/eventstest"
)

// TestMemory_Conformance runs the shared events.Bus conformance suite against a
// default (async) Memory bus. The suite's own WithSync subtest exercises the
// synchronous path, so one default construction covers both dispatch modes.
func TestMemory_Conformance(t *testing.T) {
	eventstest.Run(t, func(t *testing.T) events.Bus {
		return events.NewMemory(events.WithLogger(silentLogger()))
	})
}
