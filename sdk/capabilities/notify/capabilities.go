package notify

// TransportSecurity classifies the wire protection a Notifier provides. It is
// informational metadata a production host can surface or record; the
// fail-closed rule keys on DevelopmentOnly and on whether the Notifier declares
// metadata at all, not on this value.
type TransportSecurity string

const (
	// TransportSecurityUndeclared is the zero value: the Notifier makes no claim.
	TransportSecurityUndeclared TransportSecurity = ""
	// TransportSecurityNone means the transport does not protect message content
	// on the wire (e.g. a console/log sink).
	TransportSecurityNone TransportSecurity = "none"
	// TransportSecurityTLS means the transport connects over TLS.
	TransportSecurityTLS TransportSecurity = "tls"
)

// Capabilities is the optional security metadata a Notifier declares so a host
// can fail closed in production. A Notifier that does not implement
// CapabilityReporter declares no metadata; a production host treats that as
// unsafe and rejects it.
type Capabilities struct {
	// TransportSecurity classifies the wire protection the transport provides.
	TransportSecurity TransportSecurity
	// DevelopmentOnly marks a transport that must never run in production because
	// it exposes message bodies — e.g. the Console notifier logs OTPs and magic
	// links to stdout. Production wiring rejects it.
	DevelopmentOnly bool
}

// CapabilityReporter is the optional interface a Notifier implements to declare
// its production-safety posture. Consumers detect it with a type assertion
// rather than by concrete type, so any Notifier (bundled or integration) can
// opt in.
type CapabilityReporter interface {
	Capabilities() Capabilities
}
