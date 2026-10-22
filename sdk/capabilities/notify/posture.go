package notify

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// ErrInsecureTransport is returned by CheckTransport in
// environment.ModeProduction when a transport is development-only or declares no
// capability metadata at all. A development-only transport exposes message
// bodies — the bundled Console sender logs them — and a transport that declares
// nothing cannot be proven safe, so production rejects both. The returned error
// wraps this sentinel with the specific reason.
var ErrInsecureTransport = errors.New("notify: production rejects a development-only or metadata-less transport")

// ErrOpportunisticTLS is returned by CheckTransport in
// environment.ModeProduction, under RequireMandatoryTLS, for a transport that
// declares TransportSecurityStartTLS: a relay that does not offer STARTTLS, or
// an attacker who strips the offer, leaves the message in plaintext. It wraps
// ErrInsecureTransport, so errors.Is checks for that sentinel keep matching.
var ErrOpportunisticTLS error = &refinedError{
	msg:    "notify: production requires mandatory TLS; the transport declares opportunistic STARTTLS",
	parent: ErrInsecureTransport,
}

// CheckOption tightens CheckTransport's production verdict. Options never
// change the development verdict.
type CheckOption func(*checkConfig)

// checkConfig is the policy CheckOptions assemble for one CheckTransport call.
type checkConfig struct {
	requireMandatoryTLS bool
}

// RequireMandatoryTLS makes CheckTransport reject, in
// environment.ModeProduction, a transport that declares
// TransportSecurityStartTLS, with ErrOpportunisticTLS. Without it the verdict
// is unchanged and opportunistic STARTTLS is accepted, which is how a caller
// admits a relay it trusts on a private network.
func RequireMandatoryTLS() CheckOption {
	return func(c *checkConfig) { c.requireMandatoryTLS = true }
}

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
// ErrInsecureTransport; with RequireMandatoryTLS, so is one declaring
// TransportSecurityStartTLS (ErrOpportunisticTLS). In
// environment.ModeDevelopment all are accepted and the returned posture tells
// the caller whether to warn.
//
// An invalid or empty mode is rejected with the environment package's own
// validation error rather than defaulting to a posture the host did not choose.
// A nil option is rejected with an error wrapping sdk.ErrInvalidInput.
func CheckTransport(mode environment.Mode, n any, opts ...CheckOption) (TransportPosture, error) {
	posture := InspectTransport(n)

	var cfg checkConfig
	for _, opt := range opts {
		if opt == nil {
			return posture, fmt.Errorf("notify: nil CheckOption: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
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
	if cfg.requireMandatoryTLS && posture.Capabilities.TransportSecurity == TransportSecurityStartTLS {
		return posture, ErrOpportunisticTLS
	}
	return posture, nil
}

// refinedError is a sentinel with its own message that also matches a broader
// sentinel through errors.Is.
type refinedError struct {
	msg    string
	parent error
}

func (e *refinedError) Error() string { return e.msg }

func (e *refinedError) Unwrap() error { return e.parent }
