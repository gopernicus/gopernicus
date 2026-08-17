package notify

import (
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
)

// ErrInsecureTransport is returned by CheckNotifier in
// environment.ModeProduction when a Notifier is development-only or declares no
// capability metadata at all. A development-only transport exposes message
// bodies — the bundled Console notifier logs them — and a Notifier that declares
// nothing cannot be proven safe, so production rejects both. The returned error
// wraps this sentinel with the specific reason.
var ErrInsecureTransport = errors.New("notify: production rejects a development-only or metadata-less Notifier")

// TransportPosture is the classification CheckNotifier returns: what the
// Notifier declared about itself, separate from whether that is acceptable for a
// given mode. A caller in development uses it to phrase its own warning — this
// package deliberately takes no logger, because message text and log routing are
// composition concerns.
type TransportPosture struct {
	// Declared reports whether the Notifier implements CapabilityReporter. False
	// means Capabilities is the zero value because nothing was declared, not
	// because the transport declared zero values.
	Declared bool
	// Capabilities is what the Notifier declared. It is the zero value when
	// Declared is false.
	Capabilities Capabilities
}

// ProductionCapable reports whether the Notifier is acceptable in
// environment.ModeProduction: it declared metadata and is not development-only.
// In development a false result is exactly the condition worth warning about.
func (p TransportPosture) ProductionCapable() bool {
	return p.Declared && !p.Capabilities.DevelopmentOnly
}

// InspectNotifier reports what n declares about itself without applying any
// policy. Detection is structural — any Notifier implementing CapabilityReporter
// qualifies, bundled or third-party — so a host's own transport can opt in
// without this package knowing its type. A nil Notifier declares nothing.
func InspectNotifier(n Notifier) TransportPosture {
	r, ok := n.(CapabilityReporter)
	if !ok {
		return TransportPosture{}
	}
	return TransportPosture{Declared: true, Capabilities: r.Capabilities()}
}

// CheckNotifier validates n against mode and returns the Notifier's declared
// posture either way.
//
// In environment.ModeProduction a Notifier that declares no metadata, or
// declares itself development-only, is rejected with an error wrapping
// ErrInsecureTransport. In environment.ModeDevelopment both are accepted and the
// returned posture tells the caller whether to warn.
//
// An invalid or empty mode is rejected with the environment package's own
// validation error rather than defaulting to a posture the host did not choose.
func CheckNotifier(mode environment.Mode, n Notifier) (TransportPosture, error) {
	posture := InspectNotifier(n)

	if err := environment.ValidateMode(mode); err != nil {
		return posture, err
	}
	if !mode.IsProduction() {
		return posture, nil
	}
	if !posture.Declared {
		return posture, fmt.Errorf("%w: the Notifier declares no capability metadata", ErrInsecureTransport)
	}
	if posture.Capabilities.DevelopmentOnly {
		return posture, fmt.Errorf("%w: the Notifier is development-only", ErrInsecureTransport)
	}
	return posture, nil
}
