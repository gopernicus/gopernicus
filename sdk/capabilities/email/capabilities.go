package email

// TransportSecurity classifies the wire protection an email Sender provides. It
// is informational metadata a production host can surface or record; the
// fail-closed rule keys on DevelopmentOnly and on whether the Sender declares
// metadata at all, not on this value.
type TransportSecurity string

const (
	// TransportSecurityUndeclared is the zero value: the Sender makes no claim.
	TransportSecurityUndeclared TransportSecurity = ""
	// TransportSecurityNone means the transport does not protect message content
	// on the wire (e.g. a console/log sink).
	TransportSecurityNone TransportSecurity = "none"
	// TransportSecurityStartTLS means the transport negotiates STARTTLS when the
	// server advertises it (best effort).
	TransportSecurityStartTLS TransportSecurity = "starttls"
	// TransportSecurityTLS means the transport connects over TLS.
	TransportSecurityTLS TransportSecurity = "tls"
)

// Capabilities is the optional security metadata a Sender declares so a host can
// fail closed in production. A Sender that does not implement CapabilityReporter
// declares no metadata; a production host treats that as unsafe and rejects it.
type Capabilities struct {
	// TransportSecurity classifies the wire protection the transport provides.
	TransportSecurity TransportSecurity
	// DevelopmentOnly marks a transport that must never run in production because
	// it exposes message bodies — e.g. the Console sender logs verification codes
	// and magic links to stdout. Production wiring rejects it.
	DevelopmentOnly bool
}

// CapabilityReporter is the optional interface a Sender implements to declare its
// production-safety posture. Consumers detect it with a type assertion rather
// than by concrete type, so any Sender (bundled or integration) can opt in.
type CapabilityReporter interface {
	Capabilities() Capabilities
}
