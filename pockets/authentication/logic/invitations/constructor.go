package invitations

import (
	"fmt"
	"reflect"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// New constructs invitation use cases over host-owned storage and grant policy.
// Authorized operations additionally require InviteCheck; absence fails closed.
func New(repo InvitationRepository, granter Granter, opts ...Option) (*Service, error) {
	d := constructorConfig{}
	d.Invitations = repo
	d.Granter = granter
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authentication New: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&d)
	}

	for _, v := range []struct {
		name  string
		value any
	}{{"Invitations", d.Invitations}, {"Granter", d.Granter}} {
		if nilDependency(v.value) {
			return nil, fmt.Errorf("invitations: %s is required: %w", v.name, sdk.ErrInvalidInput)
		}
	}
	if d.Queue != nil && d.Deliver == nil {
		return nil, fmt.Errorf("invitations: delivery queue requires a renderer: %w", sdk.ErrInvalidInput)
	}
	if d.TTL < 0 {
		return nil, fmt.Errorf("invitations: TTL must not be negative: %w", sdk.ErrInvalidInput)
	}
	for _, v := range []any{d.Queue, d.Normalizer, d.Mailer, d.SecurityEvents} {
		if v != nil && nilDependency(v) {
			return nil, fmt.Errorf("invitations: dependency contains a typed nil: %w", sdk.ErrInvalidInput)
		}
	}
	var err error
	d.BodySenders, err = delivery.CopyBodySenders(d.BodySenders)
	if err != nil {
		return nil, err
	}
	d.RedirectAllowlist = append([]string(nil), d.RedirectAllowlist...)
	return newService(d), nil
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
