package email

import (
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk/foundation/environment"
)

// ErrInsecureTransport is returned by CheckSender in environment.ModeProduction
// when a Sender is development-only or declares no capability metadata at all. A
// development-only transport exposes message bodies — the bundled Console sender
// logs verification codes and magic links to stdout — and a Sender that declares
// nothing cannot be proven safe, so production rejects both. The returned error
// wraps this sentinel with the specific reason.
var ErrInsecureTransport = errors.New("email: production rejects a development-only or metadata-less Sender")

// TransportPosture is the classification CheckSender returns: what the Sender
// declared about itself, separate from whether that is acceptable for a given
// mode. A caller in development uses it to phrase its own warning — this package
// deliberately takes no logger, because message text and log routing are
// composition concerns.
type TransportPosture struct {
	// Declared reports whether the Sender implements CapabilityReporter. False
	// means Capabilities is the zero value because nothing was declared, not
	// because the transport declared zero values.
	Declared bool
	// Capabilities is what the Sender declared. It is the zero value when
	// Declared is false.
	Capabilities Capabilities
}

// ProductionCapable reports whether the Sender is acceptable in
// environment.ModeProduction: it declared metadata and is not development-only.
// In development a false result is exactly the condition worth warning about.
func (p TransportPosture) ProductionCapable() bool {
	return p.Declared && !p.Capabilities.DevelopmentOnly
}

// InspectSender reports what s declares about itself without applying any
// policy. Detection is structural — any Sender implementing CapabilityReporter
// qualifies, bundled or third-party — so a host's own transport can opt in
// without this package knowing its type. A nil Sender declares nothing.
func InspectSender(s Sender) TransportPosture {
	r, ok := s.(CapabilityReporter)
	if !ok {
		return TransportPosture{}
	}
	return TransportPosture{Declared: true, Capabilities: r.Capabilities()}
}

// CheckSender validates s against mode and returns the Sender's declared
// posture either way.
//
// In environment.ModeProduction a Sender that declares no metadata, or declares
// itself development-only, is rejected with an error wrapping
// ErrInsecureTransport. In environment.ModeDevelopment both are accepted and the
// returned posture tells the caller whether to warn:
//
//	posture, err := email.CheckSender(mode, sender)
//	if err != nil {
//		return fmt.Errorf("mail transport: %w", err)
//	}
//	if !posture.ProductionCapable() {
//		log.Warn("development-only mail transport wired; never use in production")
//	}
//
// An invalid or empty mode is rejected with the environment package's own
// validation error rather than defaulting to a posture the host did not choose.
func CheckSender(mode environment.Mode, s Sender) (TransportPosture, error) {
	posture := InspectSender(s)

	if err := environment.ValidateMode(mode); err != nil {
		return posture, err
	}
	if !mode.IsProduction() {
		return posture, nil
	}
	if !posture.Declared {
		return posture, fmt.Errorf("%w: the Sender declares no capability metadata", ErrInsecureTransport)
	}
	if posture.Capabilities.DevelopmentOnly {
		return posture, fmt.Errorf("%w: the Sender is development-only", ErrInsecureTransport)
	}
	return posture, nil
}
