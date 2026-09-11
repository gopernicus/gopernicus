package outbox

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"

	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/gopernicus/gopernicus/sdk/pkg/workers"
)

func TestPollerStorageFailureIsVisibleThroughWorkerPool(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := newFakeRepo()
		repo.listErr = errors.New("outbox storage unavailable")
		poller := newTestPoller(t, repo, func(context.Context, sdkevents.Event) error {
			t.Error("failed reads must not reach delivery")
			return nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var logs bytes.Buffer
		pool := workers.NewPool(func(ctx context.Context) error {
			err := poller.Poll(ctx)
			cancel()
			return err
		}, workers.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
		if err := pool.Run(ctx); err != nil {
			t.Fatal(err)
		}
		if repo.listCalled != 1 || pool.Stats().Errors != 1 {
			t.Fatalf("reads=%d stats=%+v", repo.listCalled, pool.Stats())
		}
		if !strings.Contains(logs.String(), "outbox storage unavailable") {
			t.Fatal("the poller's storage failure disappeared at the pool boundary")
		}
	})
}
