package delivery

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// BodySender delivers a rendered authentication body to a resolved non-email
// destination. The host chooses its provider. Production transports declare
// notify.CapabilityReporter; eligibility and identifier kinds stay in the pocket.
type BodySender interface {
	Send(ctx context.Context, destination, body string) error
}

// CopyBodySenders validates and snapshots host wiring. An email entry is invalid
// because all email must retain its typed HTML/text representation through Mailer.
func CopyBodySenders(senders map[string]BodySender) (map[string]BodySender, error) {
	result := make(map[string]BodySender, len(senders))
	for kind, sender := range senders {
		if strings.TrimSpace(kind) == "" || kind != strings.TrimSpace(kind) || kind == sdk.AddressKindEmail {
			return nil, fmt.Errorf("auth: BodySenders requires a non-email identifier kind: %w", sdk.ErrInvalidInput)
		}
		if sender == nil {
			return nil, fmt.Errorf("auth: BodySenders contains a nil sender: %w", sdk.ErrInvalidInput)
		}
		v := reflect.ValueOf(sender)
		switch v.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
			if v.IsNil() {
				return nil, fmt.Errorf("auth: BodySenders contains a typed nil sender: %w", sdk.ErrInvalidInput)
			}
		}
		result[kind] = sender
	}
	return result, nil
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
