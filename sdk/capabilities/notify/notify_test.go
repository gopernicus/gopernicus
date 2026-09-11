package notify

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestSendExplicitSelectionAndPartialFailure(t *testing.T) {
	var calls []string
	failed := errors.New("provider unavailable")
	email := DeliveryFunc(func(context.Context) error { calls = append(calls, "email"); return nil })
	chat := DeliveryFunc(func(context.Context) error { calls = append(calls, "chat"); return failed })
	sms := DeliveryFunc(func(context.Context) error { calls = append(calls, "sms"); return nil })
	err := Send(context.Background(), email, chat, sms)
	var sendErr *SendError
	if !errors.As(err, &sendErr) || !errors.Is(err, failed) {
		t.Fatalf("failure causes lost: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"email", "chat", "sms"}) || len(sendErr.Failures) != 1 || sendErr.Failures[0].Index != 1 || !sendErr.Failures[0].Attempted {
		t.Fatalf("calls=%v failures=%+v", calls, sendErr.Failures)
	}
	calls = nil
	if err := Send(context.Background(), email); err != nil || !reflect.DeepEqual(calls, []string{"email"}) {
		t.Fatalf("email-only send: calls=%v err=%v", calls, err)
	}
}

func TestSendCancellationRecordsSkippedPositions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := DeliveryFunc(func(context.Context) error { cancel(); return nil })
	later := DeliveryFunc(func(context.Context) error { t.Fatal("canceled delivery started"); return nil })
	err := Send(ctx, first, later, later)
	var sendErr *SendError
	if !errors.As(err, &sendErr) || !errors.Is(err, context.Canceled) || len(sendErr.Failures) != 2 {
		t.Fatalf("cancellation report: %v", err)
	}
	for i, failure := range sendErr.Failures {
		if failure.Index != i+1 || failure.Attempted {
			t.Fatalf("wrong skipped position: %+v", failure)
		}
	}
}

func TestSendProviderCancellationDoesNotCancelOtherDeliveries(t *testing.T) {
	called := false
	err := Send(context.Background(), DeliveryFunc(func(context.Context) error { return context.Canceled }), DeliveryFunc(func(context.Context) error { called = true; return nil }))
	if !called || !errors.Is(err, context.Canceled) {
		t.Fatalf("provider-local failure suppressed next delivery: called=%v err=%v", called, err)
	}
}

func TestSendPreservesFinalAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := Send(ctx, DeliveryFunc(func(context.Context) error { cancel(); return nil })); err != nil {
		t.Fatalf("acknowledgement replaced with cancellation: %v", err)
	}
}

func TestSendInvalidSelections(t *testing.T) {
	if err := Send(context.Background()); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("empty selection: %v", err)
	}
	var nilFunc DeliveryFunc
	err := Send(context.Background(), nil, nilFunc)
	var sendErr *SendError
	if !errors.As(err, &sendErr) || !errors.Is(err, sdk.ErrInvalidInput) || len(sendErr.Failures) != 2 {
		t.Fatalf("nil selections: %v", err)
	}
}
