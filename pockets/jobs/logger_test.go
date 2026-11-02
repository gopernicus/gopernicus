package jobs_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	pocketjobs "github.com/gopernicus/gopernicus/pockets/jobs"
	"github.com/gopernicus/gopernicus/pockets/jobs/stores/memory"
)

// TestWithLoggerRoutesTheConstructedLine pins #230 part 2: the queue service's
// construction line goes to the host's logger, not slog.Default.
func TestWithLoggerRoutesTheConstructedLine(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	if _, err := pocketjobs.New(pocketjobs.Repositories{FencedQueue: memory.NewFencedQueue()}, pocketjobs.WithLogger(log)); err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.Contains(buf.String(), `"msg":"jobs queue service constructed"`) {
		t.Fatalf("host logger did not receive the constructed line; got %q", buf.String())
	}
}
