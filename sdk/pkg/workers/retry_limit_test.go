package workers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type retryLimitedJob struct{ limit int }

func (retryLimitedJob) ID() string        { return "limited" }
func (j retryLimitedJob) RetryLimit() int { return j.limit }

type retryLimitStore struct {
	job      retryLimitedJob
	failWith int
}

func (s *retryLimitStore) Claim(context.Context, string, time.Time) (retryLimitedJob, error) {
	return s.job, nil
}
func (*retryLimitStore) Complete(context.Context, string, time.Time) error { return nil }
func (s *retryLimitStore) Fail(_ context.Context, _ string, _ time.Time, _ string, maxAttempts int) error {
	s.failWith = maxAttempts
	return nil
}

func TestRunnerPerJobRetryLimit(t *testing.T) {
	store := &retryLimitStore{}
	processErr := errors.New("temporary")
	runner := NewRunner(store, func(context.Context, retryLimitedJob) error { return processErr },
		WithRunnerLogger(slog.New(slog.NewTextHandler(io.Discard, nil))), WithMaxAttempts(3))
	for _, tc := range []struct {
		name  string
		limit int
		want  int
		err   error
	}{
		{"lower ceiling", 1, 1, processErr},
		{"higher ceiling", 5, 5, processErr},
		{"zero uses default", 0, 3, processErr},
		{"negative uses default", -1, 3, processErr},
		{"permanent overrides ceiling", 5, 1, Reject("permanent")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.job.limit = tc.limit
			processErr = tc.err
			if err := runner.WorkFunc()(context.Background()); err != nil {
				t.Fatal(err)
			}
			if store.failWith != tc.want {
				t.Fatalf("Fail maxAttempts = %d, want %d", store.failWith, tc.want)
			}
		})
	}
}
