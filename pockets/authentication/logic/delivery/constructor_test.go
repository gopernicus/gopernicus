package delivery

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestPublicQueueConstructorValidatesBounds(t *testing.T) {
	for _, cfg := range []inProcessQueueConfig{{Capacity: -1}, {AdmissionDeadline: -1}, {StatusMaxEntries: -1}, {StatusTTL: -1}, {Capacity: 8, StatusMaxEntries: 1}} {
		if queue, err := NewInProcessQueue(WithQueueAdmission(QueueAdmissionConfig{Capacity: cfg.Capacity, AdmissionDeadline: cfg.AdmissionDeadline}),
			WithQueueRetention(QueueRetentionConfig{StatusMaxEntries: cfg.StatusMaxEntries, StatusTTL: cfg.StatusTTL}),
			WithQueueClock(cfg.Now)); queue != nil || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("NewInProcessQueue(%+v) = %v, %v", cfg, queue, err)
		}
	}
	if _, err := NewInProcessQueue(); err != nil {
		t.Fatal(err)
	}
}
