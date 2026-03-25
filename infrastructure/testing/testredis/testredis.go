// Package testredis provides test Redis setup utilities using testcontainers.
// This package centralizes all test Redis infrastructure for integration tests.
package testredis

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/infrastructure/database/kvstore/goredisdb"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// noopLogger silences testcontainers output.
type noopLogger struct{}

func (n noopLogger) Printf(_ string, _ ...any) {}

func init() {
	// Silence testcontainers logging by default.
	// Set TESTCONTAINERS_VERBOSE=true to see container logs.
	if os.Getenv("TESTCONTAINERS_VERBOSE") != "true" {
		tclog.SetDefault(&noopLogger{})
	}
}

// TestRedis represents a test Redis setup with cleanup.
type TestRedis struct {
	Client    *goredisdb.Client
	Container testcontainers.Container
	Addr      string
}

// SetupTestRedis creates a Redis container and returns a connected client.
// It automatically registers cleanup with t.Cleanup().
func SetupTestRedis(t *testing.T, ctx context.Context) *TestRedis {
	t.Helper()

	// Start Redis container.
	redisContainer, err := tcredis.Run(ctx,
		"redis:7-alpine",
	)
	require.NoError(t, err, "failed to start Redis container")

	// Get connection address.
	addr, err := redisContainer.Endpoint(ctx, "")
	require.NoError(t, err, "failed to get Redis address")

	// Connect to Redis.
	client, err := goredisdb.NewTestClient(addr)
	require.NoError(t, err, "failed to connect to Redis")

	tr := &TestRedis{
		Client:    client,
		Container: redisContainer,
		Addr:      addr,
	}

	t.Cleanup(func() {
		tr.Cleanup(t)
	})

	return tr
}

// Cleanup closes the client and terminates the container.
func (tr *TestRedis) Cleanup(t *testing.T) {
	t.Helper()

	if tr.Client != nil {
		tr.Client.Close()
	}

	if tr.Container != nil {
		terminateCtx, terminateCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer terminateCancel()

		if err := tr.Container.Terminate(terminateCtx); err != nil {
			t.Logf("warning: failed to terminate Redis container: %s", err)
		}
	}
}
