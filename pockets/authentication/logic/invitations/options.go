package invitations

import (
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/gopernicus/gopernicus/sdk"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify/email"
)

// Option configures New before construction. Nil options are invalid.
type Option func(*constructorConfig)

// AccessConfig supplies invitation authorization and membership checks.
type AccessConfig struct {
	MemberCheck MemberCheck
	UserLookup  UserLookup
	// InviteCheck is the host authorization policy the AUTHORIZED operations pose
	// their question to (design §6/D3). It is used ONLY by CreateAuthorized and
	// ListByResourceAuthorized; the trusted Create/ListByResource composition methods
	// stay check-free. Package auth requires it whenever a Granter enables
	// invitations, so an authorized operation reached without one fails closed.
	InviteCheck InviteCheck
	// CallerIdentifiers resolves the accepting caller's active verified identifier
	// value of a kind for the accept-time account match (design §7/V11). Wired by
	// package auth from authlogic.ActiveVerifiedIdentifier; nil disables email/phone
	// accept-time match (fail closed).
	CallerIdentifiers IdentifierLookup
}

// WithAccess replaces the complete AccessConfig group, including zero values.
func WithAccess(value AccessConfig) Option {
	return func(c *constructorConfig) {
		c.MemberCheck = value.MemberCheck
		c.UserLookup = value.UserLookup
		c.InviteCheck = value.InviteCheck
		c.CallerIdentifiers = value.CallerIdentifiers
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

// WithNormalizer supplies identifier normalization; nil selects identifier.DefaultNormalizer.
func WithNormalizer(value identifier.Normalizer) Option {
	return func(c *constructorConfig) {
		c.Normalizer = value
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

// WithSecurityEvents replaces SecurityEvents for this constructor.
func WithSecurityEvents(value securityevent.SecurityEventRepository) Option {
	return func(c *constructorConfig) {
		c.SecurityEvents = value
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
