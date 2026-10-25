// Package gowebauthn is a WebAuthn relying-party connector wrapping exactly one
// third-party library, github.com/go-webauthn/webauthn. Its RelyingParty
// implements the sdk-owned webauthn.RelyingParty port
// (sdk/capabilities/authentication/webauthn) and passes that package's
// webauthntest conformance suite. It owns the ceremonies only: storing
// credentials, single-use ceremony state, assurance and every passkey policy
// belong to the consumer.
//
// # Construction
//
// New runs Config.Validate before anything else, so an RP ID outside the
// origin policy, an empty policy or a Timeout above webauthn.MaxTimeout fails
// boot in every runtime mode. A zero Timeout selects webauthn.MaxTimeout; one
// under a millisecond is refused, since WebAuthn timeouts are whole
// milliseconds and go-webauthn would substitute its own default. go-webauthn
// enforces the Timeout: Ceremony.ExpiresAt is Begin's time plus Timeout and
// Finish refuses an expired State.
//
// # Ceremonies
//
// Registration asks for a discoverable credential (residentKey "required"),
// "none" attestation and ES256, EdDSA or RS256 keys. Every assertion is
// discoverable: the options carry no allowCredentials.
//
// State is the go-webauthn session data inside an envelope that names the
// ceremony kind. FinishRegistration refuses an assertion State and
// FinishAssertion a registration State before go-webauthn sees either:
// CreateCredential never checks which ceremony a session was begun for, and a
// discoverable assertion session has no user id to mismatch, so it fails only
// incidentally (it lists no credential parameters).
//
// Finish admits the clientDataJSON origin through Config.Origins first, then
// verifies with a go-webauthn configuration whose only origin is that one, so
// origin namespaces (one label under a suffix) work although go-webauthn
// matches origins exactly. Registration finishes through CreateCredential and
// assertion through ValidatePasskeyLogin with a handler that admits only the
// caller's stored record and owner handle.
//
// # Ceremony truth
//
// UserVerified, BackupEligible, BackupState and SignCount come from THIS
// ceremony's authenticator data, never from the record go-webauthn returns: a
// stored user-verified flag would stay latched and a stored count is kept when
// go-webauthn sees a clone. User verification under webauthn.Required is checked
// after every other check, so only an otherwise valid response returns
// webauthn.ErrUserVerificationRequired.
//
// # Extension outputs
//
// The options request no extension, so every output is unsolicited.
// Registration rejects them, authenticator outputs (the authenticator-data ED
// flag) and client outputs alike: they mean the client did something the
// options never asked for while creating the credential the consumer will
// store, and a refused enrollment is recoverable by retrying or choosing
// another authenticator. Assertions ignore them: password managers and some
// platforms attach outputs to a get() nobody asked for, an assertion acts on
// nothing but the signature, flags and counter, and rejecting them would lock
// users of those clients out of sign-in.
//
// # Errors
//
// Every failure of a response or its State returns an error matching
// webauthn.ErrCeremonyRejected (and so sdk.ErrUnauthorized). go-webauthn's
// message is kept for logs, but no go-webauthn error type is reachable through
// errors.As, so consumers depend on the port alone.
package gowebauthn
