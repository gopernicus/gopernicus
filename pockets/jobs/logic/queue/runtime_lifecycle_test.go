package queue

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

func TestRuntimeFatalCancelsSibling(t *testing.T) {
	for _, fatalQueue := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			started := make(chan struct{})
			cause := errors.New("fatal runtime dependency")
			fatal := func(context.Context) error { <-started; return errors.Join(workers.ErrPoolShutdown, cause) }
			sibling := func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }
			queue, scheduler := sibling, fatal
			if fatalQueue {
				queue, scheduler = fatal, sibling
			}
			r := &Runtime{queuePool: workers.NewPool(queue), schedulerPool: workers.NewPool(scheduler)}
			if err := r.Run(context.Background()); !errors.Is(err, cause) {
				t.Fatalf("Run=%v", err)
			}
			if r.queuePool.Stats().ActiveWorkers != 0 || r.schedulerPool.Stats().ActiveWorkers != 0 {
				t.Fatal("Run returned before sibling drained")
			}
		})
	}
}
