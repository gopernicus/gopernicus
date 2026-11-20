package events

import (
	"context"
	"fmt"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// StartEarliest and StartLatest select where a new consumer group begins.
const (
	// StartEarliest delivers every retained entry, including those published
	// before the group existed. It is the zero value and the safe default: a
	// worker that boots after its publisher loses nothing.
	StartEarliest DeliveryStart = iota
	// StartLatest skips entries published before the group is first created.
	// It never applies when a backend recovers a lost group; recovery always
	// starts at the earliest retained entry.
	StartLatest
)

// ErrDeadLetterNotFound reports a dead-letter ID that is absent from a topic.
var ErrDeadLetterNotFound = fmt.Errorf("events: dead letter not found: %w", sdk.ErrNotFound)

// TaskSubscriber registers reliable competing-consumer tasks for one exact
// topic. Delivery is at least once: every entry is handled by one member of
// the consumer group per attempt, failed attempts are retried, and handlers
// must be idempotent on the record's EventID. A handler error, panic or
// deadline is a failed delivery. How the group is named and how long an
// abandoned delivery waits before reclaim are backend configuration.
type TaskSubscriber interface {
	SubscribeTask(ctx context.Context, topic string, handler Handler) (Subscription, error)
}

// TaskAdmin is the operational surface of a task backend: backlog
// health, dead-letter inspection and disposition, and retention. Every method
// takes an exact topic and acts for the backend's configured consumer group.
type TaskAdmin interface {
	// BacklogStats reports the group's backlog for topic. Before the group
	// exists, only DeadLetters is meaningful.
	BacklogStats(ctx context.Context, topic string) (BacklogStats, error)
	// DeadLetters lists at most limit dead letters, oldest first. A
	// nonpositive limit is invalid input.
	DeadLetters(ctx context.Context, topic string, limit int) ([]DeadLetter, error)
	// Redeliver atomically moves a dead letter back onto topic as a new entry
	// with a fresh delivery count. The EventID is kept.
	Redeliver(ctx context.Context, topic, deadLetterID string) error
	// DiscardDeadLetter permanently removes a dead letter, recording that its
	// loss is accepted.
	DiscardDeadLetter(ctx context.Context, topic, deadLetterID string) error
	// Trim removes entries older than olderThan that every consumer group has
	// acknowledged. It never removes pending or unread entries, and it removes
	// nothing while topic has no consumer group. It returns the count removed.
	Trim(ctx context.Context, topic string, olderThan time.Duration) (int64, error)
}

// DeliveryStart selects where a new consumer group begins reading.
type DeliveryStart int

// DeliveryPolicy bounds task delivery. The zero value retries
// forever with no backoff beyond the backend's own reclaim floor, matching
// unbounded at-least-once delivery.
type DeliveryPolicy struct {
	// MaxDeliveries dead-letters an entry when the delivery numbered
	// MaxDeliveries fails. Zero is unbounded.
	MaxDeliveries int
	// BackoffBase and BackoffMax set the minimum delay after the nth failed
	// delivery to min(BackoffBase·2^(n−1), BackoffMax). A zero base disables
	// backoff; a nonzero base needs BackoffMax ≥ BackoffBase. Backends may
	// retry later than this, never sooner.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// HandlerTimeout bounds one whole delivery attempt. Zero selects the
	// backend default.
	HandlerTimeout time.Duration
	// Start applies only when a consumer group is first created.
	Start DeliveryStart
	// OnDeadLetter, when set, is called once per dead-lettered entry after
	// the move commits. It runs outside backend locks; a panic is recovered.
	OnDeadLetter func(context.Context, DeadLetter)
}

// BacklogStats describes one consumer group's backlog for a topic.
type BacklogStats struct {
	// Pending counts entries delivered but not yet acknowledged.
	Pending int64
	// OldestAge is the age of the oldest entry the group has not yet
	// acknowledged, whether pending or still unread. Zero means none. It keeps
	// growing while no consumer runs, so it detects a stalled group.
	OldestAge time.Duration
	// DeadLetters counts dead letters retained for the topic.
	DeadLetters int64
}

// DeadLetter is a task that exhausted its deliveries.
type DeadLetter struct {
	ID         string
	Topic      string
	SourceID   string // the entry's backend ID before it was dead-lettered
	Consumer   string
	Error      string
	Deliveries int
	FailedAt   time.Time
	// Record is nil when the entry's envelope could not be decoded.
	Record *Record
	// Raw is the original envelope bytes, always retained.
	Raw []byte
}

type deliveryAttemptKey struct{}

// Validate reports an unusable policy as sdk.ErrInvalidInput.
func (p DeliveryPolicy) Validate() error {
	switch {
	case p.MaxDeliveries < 0:
		return fmt.Errorf("events: MaxDeliveries must not be negative: %w", sdk.ErrInvalidInput)
	case p.HandlerTimeout < 0:
		return fmt.Errorf("events: HandlerTimeout must not be negative: %w", sdk.ErrInvalidInput)
	case p.BackoffBase < 0 || p.BackoffMax < 0:
		return fmt.Errorf("events: backoff durations must not be negative: %w", sdk.ErrInvalidInput)
	case p.BackoffBase == 0 && p.BackoffMax != 0:
		return fmt.Errorf("events: BackoffMax needs a BackoffBase: %w", sdk.ErrInvalidInput)
	case p.BackoffBase > 0 && p.BackoffMax < p.BackoffBase:
		return fmt.Errorf("events: BackoffMax must be at least BackoffBase: %w", sdk.ErrInvalidInput)
	case p.Start != StartEarliest && p.Start != StartLatest:
		return fmt.Errorf("events: unknown DeliveryStart %d: %w", p.Start, sdk.ErrInvalidInput)
	}
	return nil
}

// RetryDelay is the minimum delay after the given number of failed
// deliveries, before the next one. It is zero when backoff is disabled.
func (p DeliveryPolicy) RetryDelay(failedDeliveries int) time.Duration {
	if p.BackoffBase <= 0 || failedDeliveries < 1 {
		return 0
	}
	delay := p.BackoffBase
	for i := 1; i < failedDeliveries && delay < p.BackoffMax; i++ {
		if delay > p.BackoffMax/2 {
			return p.BackoffMax
		}
		delay *= 2
	}
	return min(delay, p.BackoffMax)
}

// Exhausted reports whether a failure of the given delivery dead-letters it.
func (p DeliveryPolicy) Exhausted(delivery int) bool {
	return p.MaxDeliveries > 0 && delivery >= p.MaxDeliveries
}

// DeliveryAttempt returns the 1-based delivery attempt a task backend stored
// in ctx, or 0 when ctx is not a task delivery.
func DeliveryAttempt(ctx context.Context) int {
	attempt, _ := ctx.Value(deliveryAttemptKey{}).(int)
	return attempt
}

// WithDeliveryAttempt records the delivery attempt for a handler. Backends
// call it; handlers read it with DeliveryAttempt.
func WithDeliveryAttempt(ctx context.Context, attempt int) context.Context {
	return context.WithValue(ctx, deliveryAttemptKey{}, attempt)
}

// ValidateTaskTopic checks that topic names exactly one task topic.
func ValidateTaskTopic(topic string) error {
	if topic == "" || topic == "*" {
		return fmt.Errorf("events: tasks require an exact topic: %w", sdk.ErrInvalidInput)
	}
	return nil
}
