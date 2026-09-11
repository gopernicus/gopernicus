package delivery

import (
	"time"
)

// QueueOption configures NewInProcessQueue before construction. Nil options are invalid.
type QueueOption func(*inProcessQueueConfig)

// QueueAdmissionConfig bounds queued work and the wait for an available slot.
type QueueAdmissionConfig struct {
	// Capacity is the finite queue depth; 0 → defaultInProcessCapacity.
	Capacity int
	// AdmissionDeadline bounds how long an enqueue waits for a free slot; 0 →
	// defaultInProcessAdmissionDeadline.
	AdmissionDeadline time.Duration
}

// WithQueueAdmission replaces the complete QueueAdmissionConfig group, including zero values.
func WithQueueAdmission(value QueueAdmissionConfig) QueueOption {
	return func(c *inProcessQueueConfig) {
		c.Capacity = value.Capacity
		c.AdmissionDeadline = value.AdmissionDeadline
	}
}

// QueueRetentionConfig bounds delivery-status history by age and entry count.
type QueueRetentionConfig struct {
	// StatusMaxEntries is the finite maximum number of latest-by-key status records the
	// arbiter retains; 0 → defaultInProcessStatusMaxEntries. Beyond it, a fresh
	// admission evicts the oldest terminal record.
	StatusMaxEntries int
	// StatusTTL bounds how long a terminal latest-by-key status is retained; 0 →
	// defaultInProcessStatusTTL.
	StatusTTL time.Duration
}

// WithQueueRetention replaces the complete QueueRetentionConfig group, including zero values.
func WithQueueRetention(value QueueRetentionConfig) QueueOption {
	return func(c *inProcessQueueConfig) {
		c.StatusMaxEntries = value.StatusMaxEntries
		c.StatusTTL = value.StatusTTL
	}
}

// WithQueueClock supplies the queue clock; nil selects time.Now.
func WithQueueClock(value func() time.Time) QueueOption {
	return func(c *inProcessQueueConfig) {
		c.Now = value
	}
}
