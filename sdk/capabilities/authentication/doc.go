// Package authentication is the port for external identity providers: a
// Provider turns a presented Credential into a verified Identity — who the
// issuer says presented it and how well (email/phone with their own verified
// flags, tenant, authentication time, methods, MFA).
//
// Identity carries no sdk.Principal and no local user id. Mapping an external
// subject to the host's principal belongs to the identity owner (in this
// framework, pockets/authentication), which CONSUMES this port; the
// implementations of record live here (NewOIDC) and in integrations. Imported
// as sdkauth beside the pocket's own authentication packages.
//
// NewOIDC is the one generic, stdlib-only provider: it maps the claims of a
// cryptids.JWTVerifier through a vendor-neutral Profile (dotted claim paths
// with OIDC defaults). Vendor presets supply Profile values from integration
// modules; no field or default here names a vendor. Provider claims are
// identity data only: the mapping has no field for roles or permissions, and
// Identity.Attributes is host-mapped data, never an authorization grant.
//
// RevocationChecker is optional: a provider that can report a subject's
// standing returns RevocationFacts, which the consumer compares to each
// token's AuthenticatedAt, so a cached answer never locks out a subject who
// just re-authenticated.
//
// CapabilityReporter is optional too: a provider that must never run in
// production declares Capabilities{DevelopmentOnly: true}, and CheckProvider
// refuses it there with ErrDevelopmentOnlyProvider. A provider that declares
// nothing is accepted.
//
// Errors are stable kinds: ErrInvalidCredential (wraps sdk.ErrUnauthorized),
// ErrIssuerMismatch and ErrSignInMethodDenied (both wrap ErrInvalidCredential),
// and ErrProviderUnavailable (wraps sdk.ErrUnavailable; an *UnavailableError
// names the Kind for logs). An outage is never reported as an invalid
// credential. authenticationtest pins the contract for every implementation.
package authentication
