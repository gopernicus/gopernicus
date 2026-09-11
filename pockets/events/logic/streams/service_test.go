package streams

import (
	"context"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

func TestVisibilityCancellationSkipsProjection(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())
	projected := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	h, err := New(bus, WithVisibility(func(context.Context, sdk.Principal, sdkevents.Event) (bool, error) {
		calls++
		cancel()
		return true, nil
	}), WithProjector(func(sdkevents.Event) any { projected = true; return "private" }))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	event := sdkevents.NewBaseEvent("test.cancel")
	if _, allowed := h.visibleFrame(ctx, sdk.Principal{ID: "u"}, event); allowed || projected {
		t.Fatal("canceled visibility projected an event")
	}
	if _, allowed := h.visibleFrame(ctx, sdk.Principal{ID: "u"}, event); allowed || calls != 1 {
		t.Fatal("already canceled stream called visibility again")
	}
}
