package notify

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// ErrInsecureTransport is returned by CheckTransport in
// environment.ModeProduction when a transport is development-only or declares no
// capability metadata at all. A development-only transport exposes message
// bodies — the bundled Console sender logs them — and a transport that declares
// nothing cannot be proven safe, so production rejects both. The returned error
// wraps this sentinel with the specific reason.
var ErrInsecureTransport = errors.New("notify: production rejects a development-only or metadata-less transport")

// TransportPosture is the classification CheckTransport returns: what the
// transport declared about itself, separate from whether that is acceptable for a
// given mode. A caller in development uses it to phrase its own warning — this
// package deliberately takes no logger, because message text and log routing are
// composition concerns.
type TransportPosture struct {
	// Declared reports whether the transport implements CapabilityReporter. False
	// means Capabilities is the zero value because nothing was declared, not
	// because the transport declared zero values.
	Declared bool
	// Capabilities is what the transport declared. It is the zero value when
	// Declared is false.
	Capabilities Capabilities
}

// ProductionCapable reports whether the transport is acceptable in
// environment.ModeProduction: it declared metadata and is not development-only.
// In development a false result is exactly the condition worth warning about.
func (p TransportPosture) ProductionCapable() bool {
	return p.Declared && !p.Capabilities.DevelopmentOnly
}

// InspectTransport reports what n declares about itself without applying any
// policy. Detection is structural — any transport implementing CapabilityReporter
// qualifies, bundled or third-party — so a host's own transport can opt in
// without this package knowing its type. A nil transport declares nothing.
func InspectTransport(n any) TransportPosture {
	if n == nil {
		return TransportPosture{}
	}
	v := reflect.ValueOf(n)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if v.IsNil() {
			return TransportPosture{}
		}
	}
	r, ok := n.(CapabilityReporter)
	if !ok {
		return TransportPosture{}
	}
	return TransportPosture{Declared: true, Capabilities: r.Capabilities()}
}

// CheckTransport validates n against mode and returns the transport's declared
// posture either way.
//
// In environment.ModeProduction a transport that declares no metadata, or
// declares itself development-only, is rejected with an error wrapping
// ErrInsecureTransport. In environment.ModeDevelopment both are accepted and the
// returned posture tells the caller whether to warn.
//
// An invalid or empty mode is rejected with the environment package's own
// validation error rather than defaulting to a posture the host did not choose.
func CheckTransport(mode environment.Mode, n any) (TransportPosture, error) {
	posture := InspectTransport(n)

	if err := environment.ValidateMode(mode); err != nil {
		return posture, err
	}
	if !mode.IsProduction() {
		return posture, nil
	}
	if !posture.Declared {
		return posture, fmt.Errorf("%w: the transport declares no capability metadata", ErrInsecureTransport)
	}
	if posture.Capabilities.DevelopmentOnly {
		return posture, fmt.Errorf("%w: the transport is development-only", ErrInsecureTransport)
	}
	return posture, nil
}
