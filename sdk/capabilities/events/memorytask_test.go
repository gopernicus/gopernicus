package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/capabilities/events/eventstest"
)

// TestMemoryTask_Conformance runs the task suite against the
// in-process reference backend.
func TestMemoryTask_Conformance(t *testing.T) {
	eventstest.RunTask(t, func(t *testing.T, policy events.DeliveryPolicy) func() eventstest.TaskPeer {
		q, err := events.NewMemoryTaskQueue(policy)
		if err != nil {
			t.Fatal(err)
		}
		return func() eventstest.TaskPeer {
			c := q.Consumer()
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := c.Close(ctx); err != nil {
					t.Errorf("Close: %v", err)
				}
			})
			return c
		}
	})
}

func TestNewMemoryTaskQueueRejectsInvalidPolicy(t *testing.T) {
	if _, err := events.NewMemoryTaskQueue(events.DeliveryPolicy{MaxDeliveries: -1}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}
