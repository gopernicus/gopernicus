package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

func TestDeliveryPolicyValidate(t *testing.T) {
	ms := time.Millisecond
	cases := []struct {
		name   string
		policy events.DeliveryPolicy
		ok     bool
	}{
		{"zero", events.DeliveryPolicy{}, true},
		{"bounded with backoff", events.DeliveryPolicy{MaxDeliveries: 5, BackoffBase: ms, BackoffMax: 8 * ms, Start: events.StartLatest}, true},
		{"constant backoff", events.DeliveryPolicy{BackoffBase: ms, BackoffMax: ms}, true},
		{"negative max deliveries", events.DeliveryPolicy{MaxDeliveries: -1}, false},
		{"negative timeout", events.DeliveryPolicy{HandlerTimeout: -ms}, false},
		{"negative base", events.DeliveryPolicy{BackoffBase: -ms}, false},
		{"max without base", events.DeliveryPolicy{BackoffMax: ms}, false},
		{"max below base", events.DeliveryPolicy{BackoffBase: 2 * ms, BackoffMax: ms}, false},
		{"unknown start", events.DeliveryPolicy{Start: 7}, false},
	}
	for _, c := range cases {
		err := c.policy.Validate()
		if c.ok && err != nil || !c.ok && !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("%s: Validate() = %v", c.name, err)
		}
	}
}

func TestDeliveryPolicyRetryDelay(t *testing.T) {
	p := events.DeliveryPolicy{BackoffBase: 100 * time.Millisecond, BackoffMax: 350 * time.Millisecond}
	want := []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond, 350 * time.Millisecond, 350 * time.Millisecond}
	for n, w := range want {
		if got := p.RetryDelay(n); got != w {
			t.Errorf("RetryDelay(%d) = %v, want %v", n, got, w)
		}
	}
	if got := (events.DeliveryPolicy{}).RetryDelay(3); got != 0 {
		t.Errorf("disabled RetryDelay = %v, want 0", got)
	}
	if got := p.RetryDelay(1 << 20); got != 350*time.Millisecond {
		t.Errorf("huge RetryDelay = %v, want capped", got)
	}
}

func TestDeliveryPolicyExhausted(t *testing.T) {
	if (events.DeliveryPolicy{}).Exhausted(1000) {
		t.Error("unbounded policy reported exhaustion")
	}
	p := events.DeliveryPolicy{MaxDeliveries: 3}
	if p.Exhausted(2) || !p.Exhausted(3) {
		t.Error("MaxDeliveries 3 must exhaust on the third delivery only")
	}
}

func TestDeliveryAttempt(t *testing.T) {
	if got := events.DeliveryAttempt(context.Background()); got != 0 {
		t.Fatalf("DeliveryAttempt(background) = %d, want 0", got)
	}
	if got := events.DeliveryAttempt(events.WithDeliveryAttempt(context.Background(), 4)); got != 4 {
		t.Fatalf("DeliveryAttempt = %d, want 4", got)
	}
}

func TestValidateTaskTopic(t *testing.T) {
	for _, topic := range []string{"", "*"} {
		if err := events.ValidateTaskTopic(topic); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("ValidateTaskTopic(%q) = %v", topic, err)
		}
	}
	if err := events.ValidateTaskTopic("orders.created"); err != nil {
		t.Errorf("exact topic rejected: %v", err)
	}
}
