// Package notify sends the deliveries a caller explicitly selects. A delivery
// binds a channel-specific sender, destination and message. Email, SMS and chat
// share dispatch without flattening their different content types.
// Hosts own recipient policy, provider configuration, queues and retries.
package notify

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// Delivery is one prepared transport attempt. Preparing a delivery must not send
// it. Send returns nil when the provider accepts the attempt, not when a person
// receives or reads the message. An error cannot guarantee non-delivery.
type Delivery interface {
	Send(context.Context) error
}

// DeliveryFunc adapts a host's channel-specific send to a prepared Delivery.
type DeliveryFunc func(context.Context) error

func (f DeliveryFunc) Send(ctx context.Context) error {
	if f == nil {
		return fmt.Errorf("notify: nil delivery function: %w", sdk.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return f(ctx)
}

// Failure identifies a failed or skipped delivery by its zero-based position in
// Send's arguments. Attempted means its Send method was invoked; it does not
// prove whether a remote provider accepted the message.
type Failure struct {
	Index     int
	Attempted bool
	Err       error
}

// SendError reports all failed or skipped deliveries. Positions absent from
// Failures returned nil. Callers may inspect these positions when deciding what
// to retry; Send never retries or rolls back another delivery.
type SendError struct {
	Failures []Failure
}

func (e *SendError) Error() string {
	indices := make([]int, len(e.Failures))
	for i, failure := range e.Failures {
		indices[i] = failure.Index
	}
	return fmt.Sprintf("notify: deliveries %v failed or were skipped", indices)
}

// Unwrap preserves each failure's cause for errors.Is and errors.As. Error's
// summary intentionally excludes provider messages and notification content.
func (e *SendError) Unwrap() []error {
	causes := make([]error, len(e.Failures))
	for i, failure := range e.Failures {
		causes[i] = failure.Err
	}
	return causes
}

// Send attempts exactly the selected deliveries, sequentially in argument order.
// An ordinary error does not suppress later deliveries. Once ctx is canceled,
// remaining deliveries are recorded as skipped. A successful acknowledgement is
// not replaced with a cancellation that arrives afterward.
//
// Empty selections are invalid. Nil deliveries are reported at their positions;
// callers must not pass typed nil implementations. No retries, panic recovery,
// background work or implicit provider selection occurs.
func Send(ctx context.Context, deliveries ...Delivery) error {
	if len(deliveries) == 0 {
		return fmt.Errorf("notify: select at least one delivery: %w", sdk.ErrInvalidInput)
	}
	var failures []Failure
	for i, delivery := range deliveries {
		if err := ctx.Err(); err != nil {
			failures = append(failures, Failure{Index: i, Err: err})
			continue
		}
		if delivery == nil {
			failures = append(failures, Failure{Index: i, Err: fmt.Errorf("notify: nil delivery: %w", sdk.ErrInvalidInput)})
			continue
		}
		if err := delivery.Send(ctx); err != nil {
			failures = append(failures, Failure{Index: i, Attempted: true, Err: err})
		}
	}
	if len(failures) > 0 {
		return &SendError{Failures: failures}
	}
	return nil
}
