package events

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

const (
	defaultMemoryHandlerTimeout = 30 * time.Second
	memoryTaskMaxWait           = time.Second
	// memoryRetryFloor keeps a zero-backoff failure from spinning a worker.
	memoryRetryFloor = 10 * time.Millisecond
)

var (
	_ TaskSubscriber = (*MemoryTaskConsumer)(nil)
	_ TaskAdmin      = (*MemoryTaskConsumer)(nil)
)

// MemoryTaskQueue is a nonpersistent, in-process task backend: one
// consumer group shared by every consumer it hands out. It exists so host
// tests can exercise retries, backoff and dead letters without Redis, and it
// is the reference implementation for eventstest.RunTask.
type MemoryTaskQueue struct {
	policy         DeliveryPolicy
	handlerTimeout time.Duration

	mu            sync.Mutex
	topics        map[string]*memoryTopic
	changed       chan struct{}
	nextConsumer  uint64
	nextDeadEntry uint64
}

// MemoryTaskConsumer is one competing member of a MemoryTaskQueue's group. It
// runs one delivery at a time. Close it when done.
type MemoryTaskConsumer struct {
	queue *MemoryTaskQueue
	id    uint64
	name  string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	done   chan struct{}

	mu       sync.Mutex
	closed   bool
	started  bool
	nextID   uint64
	handlers map[string][]handlerRegistration
}

type handlerRegistration struct {
	id      uint64
	handler Handler
}

type memoryTopic struct {
	entries     []*memoryEntry // ascending seq; retained until trimmed
	nextSeq     uint64
	group       bool
	cursor      uint64 // the group's next unread seq
	deadLetters []DeadLetter
}

type memoryEntry struct {
	seq         uint64
	record      Record
	raw         []byte
	published   time.Time
	acked       bool
	deliveries  int
	owner       uint64 // consumer holding the delivery; 0 when none
	leaseUntil  time.Time
	availableAt time.Time
}

type memoryClaim struct {
	topic   string
	entry   *memoryEntry
	attempt int
}

type memoryTaskSubscription struct {
	consumer *MemoryTaskConsumer
	topic    string
	id       uint64
	once     sync.Once
}

// NewMemoryTaskQueue validates policy and returns an empty queue. A zero
// HandlerTimeout selects 30s.
func NewMemoryTaskQueue(policy DeliveryPolicy) (*MemoryTaskQueue, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	timeout := policy.HandlerTimeout
	if timeout == 0 {
		timeout = defaultMemoryHandlerTimeout
	}
	return &MemoryTaskQueue{policy: policy, handlerTimeout: timeout,
		topics: make(map[string]*memoryTopic), changed: make(chan struct{})}, nil
}

// Consumer returns a new competing consumer of the queue's group.
func (q *MemoryTaskQueue) Consumer() *MemoryTaskConsumer {
	q.mu.Lock()
	q.nextConsumer++
	id := q.nextConsumer
	q.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	return &MemoryTaskConsumer{queue: q, id: id, name: "memory-" + strconv.FormatUint(id, 10),
		ctx: ctx, cancel: cancel, done: make(chan struct{}), handlers: make(map[string][]handlerRegistration)}
}

// Publish appends event to its topic. It does not wait for a handler.
func (c *MemoryTaskConsumer) Publish(ctx context.Context, event Event) error {
	if err := c.admit(ctx); err != nil {
		return err
	}
	if err := ValidateEvent(event); err != nil {
		return err
	}
	record, err := NewRecord(event)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("events: encoding task record: %w", err)
	}
	c.queue.mu.Lock()
	c.queue.append(record.Type, record, raw)
	c.queue.mu.Unlock()
	return nil
}

// SubscribeTask registers handler for topic and starts this consumer's worker.
// Several handlers on one topic run together as a single delivery attempt.
func (c *MemoryTaskConsumer) SubscribeTask(ctx context.Context, topic string, handler Handler) (Subscription, error) {
	if err := c.admit(ctx); err != nil {
		return nil, err
	}
	if err := ValidateSubscription(topic, handler); err != nil {
		return nil, err
	}
	if err := ValidateTaskTopic(topic); err != nil {
		return nil, err
	}
	c.queue.mu.Lock()
	c.queue.ensureGroup(topic)
	c.queue.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	c.nextID++
	c.handlers[topic] = append(c.handlers[topic], handlerRegistration{id: c.nextID, handler: handler})
	if !c.started {
		c.started = true
		c.wg.Add(1)
		go c.work()
	}
	c.queue.mu.Lock()
	c.queue.signal()
	c.queue.mu.Unlock()
	return &memoryTaskSubscription{consumer: c, topic: topic, id: c.nextID}, nil
}

// Unsubscribe stops this consumer from claiming the topic after its last
// handler leaves. Entries stay available to other consumers.
func (s *memoryTaskSubscription) Unsubscribe() error {
	s.once.Do(func() {
		c := s.consumer
		c.mu.Lock()
		defer c.mu.Unlock()
		c.handlers[s.topic] = slices.DeleteFunc(c.handlers[s.topic], func(r handlerRegistration) bool { return r.id == s.id })
		if len(c.handlers[s.topic]) == 0 {
			delete(c.handlers, s.topic)
		}
	})
	return nil
}

// Close stops claiming and waits for an active delivery. A delivery cut short
// by Close is released without counting as a failure.
func (c *MemoryTaskConsumer) Close(ctx context.Context) error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.cancel()
		go func() {
			c.wg.Wait()
			close(c.done)
		}()
	}
	c.mu.Unlock()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BacklogStats implements TaskAdmin.
func (c *MemoryTaskConsumer) BacklogStats(ctx context.Context, topic string) (BacklogStats, error) {
	if err := c.admitTopic(ctx, topic); err != nil {
		return BacklogStats{}, err
	}
	q := c.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.topics[topic]
	if t == nil {
		return BacklogStats{}, nil
	}
	stats := BacklogStats{DeadLetters: int64(len(t.deadLetters))}
	if !t.group {
		return stats, nil
	}
	now := time.Now()
	for _, e := range t.entries {
		if e.acked {
			continue
		}
		if stats.OldestAge == 0 {
			stats.OldestAge = max(now.Sub(e.published), time.Nanosecond)
		}
		if e.seq < t.cursor {
			stats.Pending++
		}
	}
	return stats, nil
}

// DeadLetters implements TaskAdmin.
func (c *MemoryTaskConsumer) DeadLetters(ctx context.Context, topic string, limit int) ([]DeadLetter, error) {
	if err := c.admitTopic(ctx, topic); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("events: dead-letter limit must be positive: %w", sdk.ErrInvalidInput)
	}
	q := c.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.topics[topic]
	if t == nil {
		return nil, nil
	}
	out := make([]DeadLetter, 0, min(limit, len(t.deadLetters)))
	for _, dl := range t.deadLetters[:min(limit, len(t.deadLetters))] {
		out = append(out, cloneDeadLetter(dl))
	}
	return out, nil
}

// Redeliver implements TaskAdmin.
func (c *MemoryTaskConsumer) Redeliver(ctx context.Context, topic, deadLetterID string) error {
	if err := c.admitTopic(ctx, topic); err != nil {
		return err
	}
	q := c.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	dl, ok := q.takeDeadLetter(topic, deadLetterID)
	if !ok {
		return ErrDeadLetterNotFound
	}
	q.append(topic, *dl.Record, dl.Raw)
	return nil
}

// DiscardDeadLetter implements TaskAdmin.
func (c *MemoryTaskConsumer) DiscardDeadLetter(ctx context.Context, topic, deadLetterID string) error {
	if err := c.admitTopic(ctx, topic); err != nil {
		return err
	}
	q := c.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.takeDeadLetter(topic, deadLetterID); !ok {
		return ErrDeadLetterNotFound
	}
	return nil
}

// Trim implements TaskAdmin.
func (c *MemoryTaskConsumer) Trim(ctx context.Context, topic string, olderThan time.Duration) (int64, error) {
	if err := c.admitTopic(ctx, topic); err != nil {
		return 0, err
	}
	if olderThan < 0 {
		return 0, fmt.Errorf("events: trim age must not be negative: %w", sdk.ErrInvalidInput)
	}
	q := c.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.topics[topic]
	if t == nil || !t.group {
		return 0, nil
	}
	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, e := range t.entries {
		if !e.acked || e.published.After(cutoff) {
			break
		}
		removed++
	}
	t.entries = slices.Delete(t.entries, 0, removed)
	return int64(removed), nil
}

func (c *MemoryTaskConsumer) admit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	return nil
}

func (c *MemoryTaskConsumer) admitTopic(ctx context.Context, topic string) error {
	if err := c.admit(ctx); err != nil {
		return err
	}
	return ValidateTaskTopic(topic)
}

func (c *MemoryTaskConsumer) activeTopics() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	topics := make([]string, 0, len(c.handlers))
	for topic := range c.handlers {
		topics = append(topics, topic)
	}
	slices.Sort(topics)
	return topics
}

func (c *MemoryTaskConsumer) topicHandlers(topic string) []handlerRegistration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.handlers[topic])
}

func (c *MemoryTaskConsumer) work() {
	defer c.wg.Done()
	for c.ctx.Err() == nil {
		claim, changed, wait := c.queue.claim(c.id, c.activeTopics())
		if claim == nil {
			timer := time.NewTimer(wait)
			select {
			case <-c.ctx.Done():
			case <-changed:
			case <-timer.C:
			}
			timer.Stop()
			continue
		}
		c.deliver(claim)
	}
}

func (c *MemoryTaskConsumer) deliver(claim *memoryClaim) {
	q := c.queue
	handlers := c.topicHandlers(claim.topic)
	if len(handlers) == 0 {
		q.release(c.id, claim.entry, true)
		return
	}
	ctx, cancel := context.WithTimeout(WithDeliveryAttempt(c.ctx, claim.attempt), q.handlerTimeout)
	defer cancel()
	var failure error
	for _, reg := range handlers {
		if err := ctx.Err(); err != nil {
			failure = err
			break
		}
		if err := invoke(ctx, claim.entry.record.Event(), reg.handler); err != nil && failure == nil {
			failure = err
		}
	}
	if failure == nil {
		failure = ctx.Err()
	}
	if c.ctx.Err() != nil {
		q.release(c.id, claim.entry, false)
		return
	}
	if dl, ok := q.finish(c.id, c.name, claim, failure); ok {
		q.notifyDeadLetter(dl)
	}
}

// The methods below require q.mu unless they say otherwise.

func (q *MemoryTaskQueue) signal() {
	close(q.changed)
	q.changed = make(chan struct{})
}

func (q *MemoryTaskQueue) topic(name string) *memoryTopic {
	t := q.topics[name]
	if t == nil {
		t = &memoryTopic{nextSeq: 1, cursor: 1}
		q.topics[name] = t
	}
	return t
}

func (q *MemoryTaskQueue) ensureGroup(name string) {
	t := q.topic(name)
	if t.group {
		return
	}
	t.group = true
	if q.policy.Start == StartLatest {
		for _, e := range t.entries {
			e.acked = true
		}
		t.cursor = t.nextSeq
	}
}

func (q *MemoryTaskQueue) append(name string, record Record, raw []byte) {
	t := q.topic(name)
	t.entries = append(t.entries, &memoryEntry{seq: t.nextSeq, record: record, raw: raw, published: time.Now()})
	t.nextSeq++
	q.signal()
}

func (q *MemoryTaskQueue) takeDeadLetter(name, id string) (DeadLetter, bool) {
	t := q.topics[name]
	if t == nil {
		return DeadLetter{}, false
	}
	i := slices.IndexFunc(t.deadLetters, func(dl DeadLetter) bool { return dl.ID == id })
	if i < 0 {
		return DeadLetter{}, false
	}
	dl := t.deadLetters[i]
	t.deadLetters = slices.Delete(t.deadLetters, i, i+1)
	return dl, true
}

// claim locks q.mu itself. It prefers a due retry over fresh work, and
// otherwise reports when to look again.
func (q *MemoryTaskQueue) claim(consumer uint64, topics []string) (*memoryClaim, <-chan struct{}, time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	wait := memoryTaskMaxWait
	for _, name := range topics {
		t := q.topics[name]
		if t == nil || !t.group {
			continue
		}
		for _, e := range t.entries {
			if e.acked {
				continue
			}
			if e.seq >= t.cursor {
				t.cursor = e.seq + 1
				return q.take(consumer, name, e, now), q.changed, 0
			}
			due := e.availableAt
			if e.owner != 0 {
				due = e.leaseUntil
			}
			if !now.Before(due) {
				return q.take(consumer, name, e, now), q.changed, 0
			}
			wait = min(wait, due.Sub(now))
		}
	}
	return nil, q.changed, wait
}

func (q *MemoryTaskQueue) take(consumer uint64, topic string, e *memoryEntry, now time.Time) *memoryClaim {
	e.deliveries++
	e.owner = consumer
	e.leaseUntil = now.Add(2 * q.handlerTimeout)
	return &memoryClaim{topic: topic, entry: e, attempt: e.deliveries}
}

// release locks q.mu itself and returns an owned delivery unfinished.
// uncount reverses the claim's delivery count.
func (q *MemoryTaskQueue) release(consumer uint64, e *memoryEntry, uncount bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if e.owner != consumer {
		return
	}
	e.owner = 0
	e.availableAt = time.Time{}
	if uncount {
		e.deliveries--
	}
	q.signal()
}

// finish locks q.mu itself. Only the current owner settles a delivery, so a
// peer that reclaimed an overrunning delivery decides its fate instead.
func (q *MemoryTaskQueue) finish(consumer uint64, consumerName string, claim *memoryClaim, failure error) (DeadLetter, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e := claim.entry
	if e.owner != consumer || e.acked {
		return DeadLetter{}, false
	}
	e.owner = 0
	defer q.signal()
	if failure == nil {
		e.acked = true
		return DeadLetter{}, false
	}
	if !q.policy.Exhausted(e.deliveries) {
		e.availableAt = time.Now().Add(max(memoryRetryFloor, q.policy.RetryDelay(e.deliveries)))
		return DeadLetter{}, false
	}
	t := q.topics[claim.topic]
	t.entries = slices.DeleteFunc(t.entries, func(x *memoryEntry) bool { return x == e })
	q.nextDeadEntry++
	record := e.record
	dl := DeadLetter{ID: strconv.FormatUint(q.nextDeadEntry, 10), Topic: claim.topic,
		SourceID: strconv.FormatUint(e.seq, 10), Consumer: consumerName, Error: failure.Error(),
		Deliveries: e.deliveries, FailedAt: time.Now(), Record: &record, Raw: e.raw}
	t.deadLetters = append(t.deadLetters, dl)
	return cloneDeadLetter(dl), true
}

// notifyDeadLetter runs without q.mu.
func (q *MemoryTaskQueue) notifyDeadLetter(dl DeadLetter) {
	if q.policy.OnDeadLetter == nil {
		return
	}
	defer func() { _ = recover() }()
	q.policy.OnDeadLetter(context.Background(), dl)
}

func cloneDeadLetter(dl DeadLetter) DeadLetter {
	if dl.Record != nil {
		record := dl.Record.Event().Record
		dl.Record = &record
	}
	dl.Raw = slices.Clone(dl.Raw)
	return dl
}
