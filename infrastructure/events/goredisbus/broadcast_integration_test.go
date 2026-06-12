//go:build integration

package goredisbus_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gopernicus/gopernicus/infrastructure/database/kvstore/goredisdb"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/infrastructure/events/goredisbus"
)

// Broadcast must FAN OUT across instances: a subscriber on bus A receives
// an event emitted on bus B — the exact property the consumer-group
// streams path does not have.
func TestBroadcastFansOutAcrossInstances(t *testing.T) {
	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForListeningPort("6379/tcp"),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("redis container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	endpoint, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	rdbA, err := goredisdb.NewTestClient(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	rdbB, err := goredisdb.NewTestClient(endpoint)
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.DiscardHandler)
	busA := goredisbus.New(rdbA, log, goredisbus.Options{StreamPrefix: "bcast-test:"})
	busB := goredisbus.New(rdbB, log, goredisbus.Options{StreamPrefix: "bcast-test:"})
	t.Cleanup(func() {
		_ = busA.Close(context.Background())
		_ = busB.Close(context.Background())
	})

	received := make(chan events.Event, 1)
	if _, err := busA.SubscribeBroadcast("note.created", func(_ context.Context, e events.Event) error {
		received <- e
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Give the SUBSCRIBE a moment to be live before publishing.
	time.Sleep(300 * time.Millisecond)

	event := events.NewBaseEvent("note.created").WithTenant("t1").WithAggregate("note", "n1")
	if err := busB.Emit(ctx, event, events.WithSync()); err != nil {
		t.Fatalf("emit: %v", err)
	}

	select {
	case e := <-received:
		remote, ok := e.(events.RemoteEvent)
		if !ok {
			t.Fatalf("event type = %T, want events.RemoteEvent", e)
		}
		if remote.EventType != "note.created" {
			t.Errorf("type = %q", remote.EventType)
		}
		if remote.Tenant == nil || *remote.Tenant != "t1" {
			t.Errorf("tenant = %v, want t1", remote.Tenant)
		}
		if remote.AggID == nil || *remote.AggID != "n1" {
			t.Errorf("aggregate_id = %v, want n1", remote.AggID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("broadcast event never arrived across instances")
	}

	// Topic filtering: an unmatched type must not arrive.
	if err := busB.Emit(ctx, events.NewBaseEvent("other.event"), events.WithSync()); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-received:
		t.Fatalf("unexpected event %q for filtered topic", e.Type())
	case <-time.After(500 * time.Millisecond):
	}
}
