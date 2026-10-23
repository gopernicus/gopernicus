package invitations

import (
	"fmt"
	"log/slog"
	"reflect"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

// ErrSecurityEventsConflict is returned by New when WithSecurityEvents names a
// repository other than the one the WithSecurityEventRecorder recorder writes
// to: the recorder wins, so the other repository would silently receive nothing.
var ErrSecurityEventsConflict = fmt.Errorf("invitations: WithSecurityEvents and WithSecurityEventRecorder name different repositories: %w", sdk.ErrInvalidInput)

// New constructs invitation use cases over host-owned storage and grant policy.
// Its caller authorizes operations before invoking them.
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
	for _, v := range []any{d.Queue, d.Normalizer, d.Mailer, d.SecurityEvents, d.Fencer} {
		if v != nil && nilDependency(v) {
			return nil, fmt.Errorf("invitations: dependency contains a typed nil: %w", sdk.ErrInvalidInput)
		}
	}
	if d.SecurityRecorder != nil && d.SecurityEvents != nil && !sameRepository(d.SecurityEvents, d.SecurityRecorder.Repository()) {
		return nil, ErrSecurityEventsConflict
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

// securityEventRecorder returns the recorder the service records through: the
// WithSecurityEventRecorder one, else a recorder of its own over SecurityEvents
// that attributes rows through the authentication pocket's client-info carrier.
// The service's own recorder counts its failures apart from every other
// recording site.
func (d constructorConfig) securityEventRecorder(clock func() time.Time, logger *slog.Logger) *securityevent.Recorder {
	if d.SecurityRecorder != nil {
		return d.SecurityRecorder
	}
	return securityevent.NewRecorder(d.SecurityEvents, d.IDs, clock, logger, securityevent.WithClientInfoReader(authlogic.ClientInfoFromContext))
}

// sameRepository reports whether a and b are the same repository. Values of
// one dynamic type that cannot be compared are never the same.
func sameRepository(a, b securityevent.SecurityEventRepository) (same bool) {
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}
