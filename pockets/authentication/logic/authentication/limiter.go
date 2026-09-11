package authentication

// LimiterDurability is the optional metadata a rate-limiter backend may declare
// through RateLimiterDurabilityReporter (design §4.4/§8). InProcessOnly marks a
// limiter whose window state lives in a single process, so its budget is enforced
// N× across N instances; production rejects it and development warns. The zero
// value (InProcessOnly false) declares a shared/durable limiter safe for
// multi-instance use.
type LimiterDurability struct {
	InProcessOnly bool
}

// RateLimiterDurabilityReporter is the optional interface a ratelimiter.Limiter
// may implement to declare whether it is shared/durable across instances (design
// §8). The bundled in-process ratelimiter.Memory is detected structurally — it is
// sdk-only and cannot import this pocket to declare metadata — while a host's
// custom in-process limiter implements this to be rejected in production, and a
// durable host limiter may implement it to positively declare safety. It is defined
// pocket-side because the Limiter port lives in sdk.
type RateLimiterDurabilityReporter interface {
	RateLimiterDurability() LimiterDurability
}
