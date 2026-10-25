// Package webauthn is the WebAuthn relying-party port: passkey registration
// and assertion ceremonies behind a RelyingParty, so a consumer never imports a
// WebAuthn library. It sits inside the authentication subsystem but imports
// nothing from its parent package: root sdk and sdk/pkg/web only, stdlib
// beneath them. A host that wires passkeys does not link the external-provider
// port, and the reverse.
//
// There is no default implementation. The implementation of record is an
// integration over a WebAuthn library (the oauth and work precedent);
// webauthntest ships a software authenticator and the conformance suite every
// implementation passes.
//
// # Ceremonies
//
// Begin returns a Ceremony: Options is the inner publicKey options JSON the
// browser's PublicKeyCredential.parseCreationOptionsFromJSON or
// parseRequestOptionsFromJSON takes, safe to send to the client. State is what
// Finish needs to verify the response. It holds the challenge and the
// ceremony's parameters and no secret, but it is not integrity-protected: the
// caller keeps it server-side, presents it to Finish at most once, and never
// accepts it from the client. ExpiresAt is Begin's time plus Config.Timeout;
// Finish refuses an expired State.
//
// Every assertion is discoverable: the options carry an empty
// allowCredentials, so no credential id leaves the server before the user is
// authenticated. ReadAssertion returns the response's unverified credential id
// and user handle, which the caller uses only to look up the stored record and
// its owner; FinishAssertion then verifies the response against that record.
//
// # Port rules
//
// Attestation is "none" and residentKey is "required"; the accepted algorithms
// are ES256, EdDSA and RS256. Credential.PublicKey is a COSE_Key.
//
// Ceremony truth: UserVerified, BackupEligible, BackupState and SignCount in a
// Credential or an Assertion come from THIS ceremony's authenticator data,
// never from the stored record. FinishAssertion reads the stored record's ID,
// PublicKey, SignCount and BackupEligible only; a BackupEligible flag that
// differs from the stored one is rejected (it is fixed when the credential is
// created), as is BackupState without BackupEligible.
//
// ErrCeremonyRejected covers every failure of a response or its State: the
// signature, the challenge, the origin (Config.Origins must admit the
// clientDataJSON origin), the rpIdHash, the clientDataJSON type, user
// presence, an expired or foreign State, a credential id or user handle that
// does not match the stored record, and malformed input. A response that
// passes every check but lacks user verification under Required returns
// ErrUserVerificationRequired instead. The port requests no extension, so any
// authenticator extension output (the authenticator-data ED flag) is
// unsolicited: registration rejects it, assertions ignore it.
//
// Assertion.CounterRegressed reports a possible cloned authenticator: either
// count is non-zero and the presented count is not above the stored one. An
// authenticator that keeps no counter (0 stored, 0 presented) never regresses.
// It comes with a nil error; the caller decides, and keeps the stored count.
//
// Begin refuses an empty or oversize User.Handle and an unknown Requirement
// with sdk.ErrInvalidInput; a zero Requirement selects Preferred.
package webauthn
