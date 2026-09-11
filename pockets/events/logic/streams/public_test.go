package streams_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/events/logic/streams"
	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

func TestDirectStreamDeliversOnlyVisibleProjection(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())
	projected := 0
	service, err := streams.New(bus, streams.WithVisibility(func(_ context.Context, p sdk.Principal, e sdkevents.Event) (bool, error) {
		return p.ID == "u" && e.Type() == "visible", nil
	}), streams.WithProjector(func(e sdkevents.Event) any { projected++; return e.Type() }))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	principal := sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "u"}
	stream, err := service.Open(principal, streams.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for _, kind := range []string{"denied", "visible"} {
		if err := bus.Dispatch(context.Background(), sdkevents.NewBaseEvent(kind)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	frame, err := stream.Next(ctx)
	if err != nil || frame.Type != "visible" || frame.Data != "visible" || projected != 1 {
		t.Fatalf("frame=%+v projected=%d err=%v", frame, projected, err)
	}
	if _, err := service.Open(sdk.Principal{}, streams.Filter{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing principal: %v", err)
	}
	if _, err := service.Open(principal, streams.Filter{ResourceType: "entry"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("incomplete resource: %v", err)
	}
	if err := bus.Dispatch(context.Background(), sdkevents.NewBaseEvent("visible")); err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if _, err := stream.Next(ctx); !errors.Is(err, sdkevents.ErrClosed) {
		t.Fatalf("closed stream: %v", err)
	}
	if projected != 1 {
		t.Fatal("closed stream projected a buffered event")
	}
	_ = service.Close()
	if _, err := service.Open(principal, streams.Filter{}); !errors.Is(err, sdkevents.ErrClosed) {
		t.Fatalf("closed service: %v", err)
	}
}

func TestMissingVisibilityPreventsDirectOpen(t *testing.T) {
	bus := sdkevents.NewMemory()
	defer bus.Close(context.Background())
	service, err := streams.New(bus)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.Open(sdk.Principal{Type: sdk.PrincipalTypeUser, ID: "u"}, streams.Filter{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("missing visibility: %v", err)
	}
}
