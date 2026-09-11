package goredis

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/redis/go-redis/v9"
)

func TestBusLiveBroadcastSetupFailureCanRetry(t *testing.T) {
	rdb, ctx, opts := liveBusClient(t)
	var reachable atomic.Bool
	failure := errors.New("deliberate test connection failure")
	client := redis.NewClient(&redis.Options{Addr: rdb.Options().Addr, MaxRetries: -1,
		Dialer: func(ctx context.Context, network, address string) (net.Conn, error) {
			if !reachable.Load() {
				return nil, failure
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}})
	t.Cleanup(func() { _ = client.Close() })
	b := newLiveBus(t, client, opts)
	delivered := make(chan events.Record, 1)
	handler := func(_ context.Context, event events.Event) error {
		delivered <- event.(events.RemoteEvent).Record
		return nil
	}
	if sub, err := b.Subscribe("topic", handler); err == nil || sub != nil {
		t.Fatalf("failed subscription setup = %v, %v", sub, err)
	}
	b.mu.Lock()
	inert := b.broadcastPubsub != nil || b.broadcastSetup != nil || len(b.broadcastSubs) != 0
	b.mu.Unlock()
	if inert {
		t.Fatal("failed setup retained an inert reader or registration")
	}
	reachable.Store(true)
	if _, err := b.Subscribe("topic", handler); err != nil {
		t.Fatalf("retry after connection recovery: %v", err)
	}
	if err := b.Publish(ctx, (events.Record{EventID: "after-recovery", Type: "topic"}).Event()); err != nil {
		t.Fatal(err)
	}
	if got := receiveBusRecord(t, ctx, delivered); got.EventID != "after-recovery" {
		t.Fatalf("recovered notification = %+v", got)
	}
}

func TestBusLiveBroadcastSetupCloseRaceCleansConnection(t *testing.T) {
	rdb, ctx, opts := liveBusClient(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	releaseSetup := func() { releaseOnce.Do(func() { close(release) }) }
	client := redis.NewClient(&redis.Options{Addr: rdb.Options().Addr, MaxRetries: -1,
		Dialer: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			enteredOnce.Do(func() { close(entered) })
			<-release
			return conn, nil
		}})
	t.Cleanup(func() { _ = client.Close() })
	b := newLiveBus(t, client, opts)
	t.Cleanup(releaseSetup)
	done := make(chan error, 1)
	go func() {
		_, err := b.Subscribe("topic", func(context.Context, events.Event) error { return nil })
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	closeCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	if err := b.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close did not wait for admitted setup: %v", err)
	}
	cancel()
	releaseSetup()
	select {
	case err := <-done:
		if !errors.Is(err, events.ErrClosed) {
			t.Fatalf("registration raced through Close: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	counts, err := rdb.PubSubNumSub(ctx, b.broadcastChannel()).Result()
	if err != nil || counts[b.broadcastChannel()] != 0 {
		t.Fatalf("provisional subscription retained: %v, %v", counts, err)
	}
	if b.broadcastPubsub != nil || len(b.broadcastSubs) != 0 {
		t.Fatal("closed setup retained local registration/connection")
	}
}

type busAfterReadHook struct {
	after func(redis.Cmder)
}

func (h busAfterReadHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h busAfterReadHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h busAfterReadHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if err == nil && cmd.Name() == "xreadgroup" {
			h.after(cmd)
		}
		return err
	}
}

func TestBusLiveUnsubscribeLeavesClaimedWorkForPeer(t *testing.T) {
	rdb, ctx, opts := liveBusClient(t)
	claimed, release := make(chan struct{}), make(chan struct{})
	var claimedOnce, releaseOnce sync.Once
	releaseRead := func() { releaseOnce.Do(func() { close(release) }) }
	client := redis.NewClient(&redis.Options{Addr: rdb.Options().Addr})
	t.Cleanup(func() { _ = client.Close() })
	client.AddHook(busAfterReadHook{after: func(cmd redis.Cmder) {
		if len(cmd.(*redis.XStreamSliceCmd).Val()) != 0 {
			claimedOnce.Do(func() { close(claimed) })
			<-release
		}
	}})
	first := newLiveBus(t, client, opts)
	t.Cleanup(releaseRead)
	var removedCalls atomic.Int64
	sub, err := first.SubscribeWork(ctx, "topic", func(context.Context, events.Event) error { removedCalls.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Publish(ctx, (events.Record{EventID: "claimed", Type: "topic"}).Event()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-claimed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	releaseRead()
	if err := first.Publish(ctx, (events.Record{EventID: "fresh", Type: "topic"}).Event()); err != nil {
		t.Fatal(err)
	}
	peer := newLiveBus(t, rdb, opts)
	delivered := make(chan events.Record, 4)
	if _, err := peer.SubscribeWork(ctx, "topic", func(_ context.Context, event events.Event) error {
		delivered <- event.(events.RemoteEvent).Record
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for len(seen) < 2 {
		seen[receiveBusRecord(t, ctx, delivered).EventID] = true
	}
	if !seen["claimed"] || !seen["fresh"] || removedCalls.Load() != 0 || len(first.activeTopics()) != 0 {
		t.Fatalf("unsubscribe lost pending/fresh work or kept its callback: seen=%v removedCalls=%d topics=%v", seen, removedCalls.Load(), first.activeTopics())
	}
}

func TestBusLiveCloseWaitsForActiveNotificationCallback(t *testing.T) {
	rdb, ctx, opts := liveBusClient(t)
	b := newLiveBus(t, rdb, opts)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	releaseCallback := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseCallback)
	sub, err := b.Subscribe("topic", func(context.Context, events.Event) error {
		close(entered)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Publish(ctx, events.NewBaseEvent("topic")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for range 2 {
		closeCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		if err := b.Close(closeCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close returned before callback completion: %v", err)
		}
		cancel()
	}
	releaseCallback()
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if sub.(*broadcastSub).handler != nil || len(b.broadcastSubs) != 0 {
		t.Fatal("Close retained callback graph")
	}
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("Close affected caller-owned client: %v", err)
	}
}
