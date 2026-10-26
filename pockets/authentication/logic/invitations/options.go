package invitations

import (
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/protection"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

// Option configures New before construction. Nil options are invalid.
type Option func(*constructorConfig)

// AccessConfig supplies invitation membership and verified identity collaborators.
type AccessConfig struct {
	MemberCheck MemberCheck
	UserLookup  UserLookup
	// VerifiedIdentifiers lists a subject's active verified identifiers holding
	// the authentication claim (login or recovery) for the accept-time account
	// match and identifier resolution (design §7/V11): an email or phone
	// invitation matches any of them, primary or not. Wired by
	// package auth from authlogic.VerifiedIdentifiers; nil disables the email/phone
	// match (fail closed).
	VerifiedIdentifiers IdentifierLister
}

// WithAccess replaces the complete AccessConfig group, including zero values.
func WithAccess(value AccessConfig) Option {
	return func(c *constructorConfig) {
		c.MemberCheck = value.MemberCheck
		c.UserLookup = value.UserLookup
		c.VerifiedIdentifiers = value.VerifiedIdentifiers
	}
}

// WithInvitationURL sets the absolute landing URL an invitation or member-added
// link points at when no allowlisted redirect was requested. It must carry no
// fragment (the token owns it). Empty keeps the same-origin default "/", a
// relative link most mail clients cannot open.
func WithInvitationURL(value string) Option {
	return func(c *constructorConfig) {
		c.InvitationURL = value
	}
}

// WithLinkTokenInQuery restores the "?token=" invitation link the fragment
// replaced. It is a one-release migration hatch: the next minor removes it.
func WithLinkTokenInQuery(enabled bool) Option {
	return func(c *constructorConfig) {
		c.LinkTokenInQuery = enabled
	}
}

// DeliveryConfig selects invitation rendering and outbound delivery.
type DeliveryConfig struct {
	Mailer   email.Sender
	MailFrom string
	// Deliver is the shared kind-aware delivery renderer/router (design §6.1),
	// constructor-injected by package auth and shared with authentication service so the two
	// services route outbound through one kind policy instead of two drifting copies.
	// It renders an encrypted-job-ready Envelope and routes a send through the
	// email/body-sender kind fork; the durable worker (phase 4) consumes it. The
	// invitation/member-added send sites enqueue rendered commands through Queue
	// (AV3-4.3).
	Deliver *delivery.Router
	// Queue is the delivery dispatch seam the send sites enqueue through. Wired
	// whenever a delivery dispatcher is (package auth builds it); nil → outbound
	// disabled.
	Queue deliveryQueue
	// BodySenders is the host's wired delivery set keyed by kind, built by package
	// auth from BodySenders. It defines supported non-email kinds
	// (deny-by-absence, ruling 6); all email uses the Mailer.
	BodySenders map[string]delivery.BodySender
}

// WithDelivery replaces the complete DeliveryConfig group, including zero values.
func WithDelivery(value DeliveryConfig) Option {
	value = cloneDeliveryConfig(value)
	return func(c *constructorConfig) {
		value := cloneDeliveryConfig(value)
		c.Mailer = value.Mailer
		c.MailFrom = value.MailFrom
		c.Deliver = value.Deliver
		c.Queue = value.Queue
		c.BodySenders = value.BodySenders
	}
}

func cloneDeliveryConfig(value DeliveryConfig) DeliveryConfig {
	value.BodySenders = maps.Clone(value.BodySenders)
	return value
}

// WithFencer supplies the optional terminal-settlement seam. With it, Cancel
// settles an accepting claim by fencing its grant operation; without it, Cancel
// on an accepting claim returns ErrNotPending.
func WithFencer(value Fencer) Option {
	return func(c *constructorConfig) {
		c.Fencer = value
	}
}

// WithNormalizer supplies identifier normalization; nil selects identifier.DefaultNormalizer.
func WithNormalizer(value identifier.Normalizer) Option {
	return func(c *constructorConfig) {
		c.Normalizer = value
	}
}

// WithIdentifierKeyer supplies the keyer invitation audit rows digest the invitee
// identifier with (AUDIT-054): identifier_digest is IdentifierKey(kind,
// normalized), the keyed HMAC the authentication service's limiter and outbox
// keys derive from. Nil selects an unkeyed SHA-256 digest, a development fallback
// only; the pocket root requires a keyer in production.
func WithIdentifierKeyer(value protection.IdentifierKeyer) Option {
	return func(c *constructorConfig) {
		c.IdentifierKeyer = value
	}
}

// WithRedirectAllowlist replaces RedirectAllowlist for this constructor.
func WithRedirectAllowlist(value []string) Option {
	value = slices.Clone(value)
	return func(c *constructorConfig) {
		value := slices.Clone(value)
		c.RedirectAllowlist = value
	}
}

// WithSecurityEvents replaces SecurityEvents for this constructor. Without
// WithSecurityEventRecorder the service records through a recorder of its own
// over this repository, attributing rows through the client-info carrier; its
// write failures are counted apart from every other recording site.
func WithSecurityEvents(value securityevent.SecurityEventRepository) Option {
	return func(c *constructorConfig) {
		c.SecurityEvents = value
	}
}

// WithSecurityEventRecorder supplies the recorder invitation events are recorded
// through, so they share one recorder — ids, client attribution, correlation and
// failure count — with the host's other recording sites. It wins over
// WithSecurityEvents; passing both with different repositories is
// ErrSecurityEventsConflict.
func WithSecurityEventRecorder(value *securityevent.Recorder) Option {
	return func(c *constructorConfig) {
		c.SecurityRecorder = value
	}
}

// WithClock supplies the clock; nil selects time.Now.
func WithClock(value func() time.Time) Option {
	return func(c *constructorConfig) {
		c.Clock = value
	}
}

// WithLogger supplies the logger; nil selects slog.Default().
func WithLogger(value *slog.Logger) Option {
	return func(c *constructorConfig) {
		c.Logger = value
	}
}

// WithTTL sets invitation lifetime; zero selects seven days.
func WithTTL(value time.Duration) Option {
	return func(c *constructorConfig) {
		c.TTL = value
	}
}

// WithIDs replaces IDs for this constructor.
func WithIDs(value sdk.IDGenerator) Option {
	return func(c *constructorConfig) {
		c.IDs = value
	}
}
