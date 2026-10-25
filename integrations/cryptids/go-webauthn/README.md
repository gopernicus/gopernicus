# integrations/cryptids/go-webauthn

WebAuthn (passkey) relying-party ceremonies using
`github.com/go-webauthn/webauthn` v0.18.2. `RelyingParty` implements
`sdk/capabilities/authentication/webauthn.RelyingParty` and passes that
package's `webauthntest` conformance suite. Hosts construct one in `cmd` and
hand it to the consumer that stores credentials:

```go
origins, err := web.NewOriginPolicy(web.OriginPolicyConfig{Exact: []string{"https://example.com"}})
rp, err := gowebauthn.New(webauthn.Config{
	RPID:    "example.com",
	RPName:  "Example",
	Origins: origins,
})
```

The package name is `gowebauthn`. It owns the ceremonies only: storing
credentials and their owners' handles, keeping `Ceremony.State` server-side
and single-use, assurance, and every passkey policy belong to the consumer.

The RP ID is permanent for a deployment: changing it orphans every registered
credential.

## Surface

| Member | Behavior |
|---|---|
| `New(cfg webauthn.Config) (*RelyingParty, error)` | Runs `cfg.Validate()` first, so an RP ID outside the origin policy fails boot in every mode. A zero `Timeout` selects `webauthn.MaxTimeout` (5 min); above it, or under 1 ms, is refused. Every error matches `sdk.ErrInvalidInput`. |
| `Config()` | The validated configuration, `Timeout` resolved. |
| `BeginRegistration(ctx, RegistrationStart)` | `residentKey: "required"`, `attestation: "none"`, ES256 / EdDSA / RS256, `excludeCredentials` from `Exclude`. A zero `UserVerification` is `preferred`. |
| `FinishRegistration(ctx, RegistrationFinish)` | Verifies through go-webauthn's `CreateCredential`; returns the credential with flags, AAGUID and count from this ceremony's authenticator data. |
| `BeginAssertion(ctx, AssertionStart)` | Discoverable: no `allowCredentials`. |
| `ReadAssertion(response)` | `protocol.ParseCredentialRequestResponseBytes`; the unverified credential id and user handle, for the consumer's lookup. |
| `FinishAssertion(ctx, AssertionFinish)` | Verifies through `ValidatePasskeyLogin` against the stored record and its owner's handle; flags and count from this ceremony; `CounterRegressed` when either count is non-zero and the presented one is not above the stored one. |

Every failure of a response or its state matches `webauthn.ErrCeremonyRejected`
(`sdk.ErrUnauthorized`); an otherwise valid response lacking user verification
under `required` returns `webauthn.ErrUserVerificationRequired`. go-webauthn's
message is kept for logs; none of its error types is reachable through
`errors.As`.

## Verification

1. **State.** `Ceremony.State` is the go-webauthn session data in an envelope
   naming the ceremony kind. A registration state never finishes an assertion
   and an assertion state never finishes a registration.
2. **Origin.** `Config.Origins.Allows` must admit the `clientDataJSON` origin.
   go-webauthn then verifies with that origin as its only `RPOrigins` entry,
   so origin namespaces work although go-webauthn matches origins exactly.
3. **go-webauthn.** Challenge, `clientDataJSON` type, `rpIdHash`, user
   presence, signature, algorithm, backup-flag consistency, and expiry
   (`Timeouts.Login.Enforce` and `Timeouts.Registration.Enforce` are on).
4. **User verification** under `required`, last, from this ceremony's flags.

## Extension outputs

The options request no extension, so every output is unsolicited.

| Ceremony | Authenticator outputs (ED flag) | Client outputs (`clientExtensionResults`) |
|---|---|---|
| Registration | rejected | rejected |
| Assertion | ignored | ignored |

Registration rejects them because the credential the consumer stores is being
created, and a refused enrollment is recoverable. Assertions ignore them
because password managers and some platforms attach outputs nobody requested,
an assertion acts only on the signature, flags and counter, and rejecting them
would lock those users out of sign-in.

## Testing

Run `go test ./...`. `TestConformance` runs `webauthntest.Run` against the
adapter: options for both ceremonies under each requirement, registration and
assertion with and without user verification, ceremony truth, two credentials
of one user, origin admission (exact and namespace), every rejection class,
extension outputs, counter regression, expiry and each `Config.Validate`
refusal. The package's own tests pin the ceremony kind in the state, client
extension outputs per ceremony, the sub-millisecond `Timeout` refusal, and that
no go-webauthn error type crosses the port.
