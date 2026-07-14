// Package command is the transport-neutral core of the authentication feature's
// outbound-delivery runtime (plan authv3-delivery-refactor phase 2). It freezes
// ONE versioned, encrypted delivery command envelope and the reusable processor
// contract that opens, initializes, checkpoints, delivers, and classifies that
// command — independent of whether the executor is the durable generic-jobs
// runtime (phase 3) or the bounded in-process runtime (phase 4).
//
// Placement rationale (AV3D-2.1): this is delivery POLICY and its encrypted wire
// format, not a driving/driven adapter, so it lives under internal/logic/delivery
// alongside the router and the (soon-superseded) worker — sdk-only, never
// importing inbound/outbound/integrations or any sibling feature. It is a focused
// subpackage rather than new symbols in the delivery package so the versioned
// Envelope and the Processor contract carry their own, non-colliding names
// (delivery.Envelope / delivery.Command are the bespoke-worker types this
// supersedes in phase 5). Per 00-overview's public-boundary direction, a
// composition adapter never reaches this internal package directly: authentication
// exposes a public delivery seam (AV3D-2.3 / phase 3) that wraps the processor, so
// both the jobs-mode adapter and the bounded runtime consume it through that
// exported boundary while the feature core still imports no sibling feature.
//
// The Envelope is the plaintext a delivery command must carry but must NEVER
// persist, log, or surface in the clear: the resolved destination, the rendered
// secret/message, and — for enumeration-safe opaque starts — the normalized
// account-resolution input. Seal encrypts the whole validated envelope into the
// opaque bytes a queue stores; Open reverses it and re-validates. Because no field
// is a durable column, a leaked row exposes ciphertext only, and Open rejects an
// unsealed, unknown-version, or malformed payload without ever echoing its bytes.
package command

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// Version numbers the envelope schema so a payload sealed by one build is rejected
// (never silently misread) by a build that does not recognize its version. Open
// rejects any version other than the current one — including the zero value, which
// is what an uninitialized struct or an unsealed/foreign payload decodes to, so an
// "unsealed durable payload" fails closed as an unknown version.
type Version int

const (
	// Version1 is the current (and only recognized) envelope schema version.
	Version1 Version = 1
)

// Stage discriminates the two forms one logical delivery command takes over its
// lifetime. An enumeration-safe start is admitted OPAQUE — carrying only the
// normalized resolution input, no rendered content — and is resolved, issued, and
// rendered off the request path; the processor then checkpoints the RENDERED form
// before any provider send so every retry resends the identical secret.
type Stage string

const (
	// StageOpaque is an admitted-but-not-yet-initialized command: it carries the
	// normalized ResolutionInput and no rendered destination, content, or secret.
	StageOpaque Stage = "opaque"
	// StageRendered is an initialized command ready to send: it carries the resolved
	// Destination and rendered content (Body and/or HTML), plus any Secret.
	StageRendered Stage = "rendered"
)

// Stable, kind-taggable envelope errors. Each wraps an sdk kind so callers match
// with errors.Is (never string parsing), and each message is STATIC — it never
// interpolates an envelope field — so an error surfaced from a malformed or
// unsealed payload can never carry a destination, identifier, or secret.
var (
	// ErrEncrypterRequired is returned by Seal/Open when no encrypter is supplied:
	// the durable payload is always sealed, so the codec cannot run without it.
	ErrEncrypterRequired = fmt.Errorf("delivery command: encrypter is required: %w", sdk.ErrInvalidInput)
	// ErrDelivererRequired is returned by NewProcessor when no Deliverer is supplied:
	// the processor cannot perform the bounded provider send without it.
	ErrDelivererRequired = fmt.Errorf("delivery command: deliverer is required: %w", sdk.ErrInvalidInput)
	// ErrUnknownVersion is returned when an opened envelope's Version is not a
	// recognized schema version (including the zero value of an unsealed payload).
	ErrUnknownVersion = fmt.Errorf("delivery command: unknown envelope version: %w", sdk.ErrInvalidInput)
	// ErrMissingKind is returned when Kind (the address rail) is empty.
	ErrMissingKind = fmt.Errorf("delivery command: kind is required: %w", sdk.ErrInvalidInput)
	// ErrMissingPurpose is returned when Purpose (the template/routing selector) is
	// empty.
	ErrMissingPurpose = fmt.Errorf("delivery command: purpose is required: %w", sdk.ErrInvalidInput)
	// ErrMissingKey is returned when Key (the PII-free logical receipt key) is empty.
	ErrMissingKey = fmt.Errorf("delivery command: logical key is required: %w", sdk.ErrInvalidInput)
	// ErrInvalidStage is returned when Stage is empty or not a recognized stage.
	ErrInvalidStage = fmt.Errorf("delivery command: unknown stage: %w", sdk.ErrInvalidInput)
	// ErrStageMismatch is returned when the envelope's content does not match its
	// stage: an opaque command carrying rendered content or lacking resolution input,
	// or a rendered command lacking a destination or any content.
	ErrStageMismatch = fmt.Errorf("delivery command: stage and content do not match: %w", sdk.ErrInvalidInput)
	// ErrUnsealedPayload is returned by Open when the payload is not valid sealed
	// ciphertext (a durable payload must always be sealed). The underlying decrypt
	// error is deliberately NOT wrapped, so no payload bytes reach the caller.
	ErrUnsealedPayload = fmt.Errorf("delivery command: payload is not sealed: %w", sdk.ErrInvalidInput)
	// ErrMalformedPayload is returned by Open when the decrypted bytes are not a
	// well-formed envelope. The underlying unmarshal error is deliberately NOT
	// wrapped, because it would echo the decrypted plaintext (which carries the
	// secret) into the error string.
	ErrMalformedPayload = fmt.Errorf("delivery command: payload is malformed: %w", sdk.ErrInvalidInput)
)

// Envelope is the versioned plaintext delivery command sealed into a queue's opaque
// payload. It is never stored, logged, or surfaced in the clear.
//
// Kind is the address rail (identity.KindEmail selects the email rail; any other
// kind selects the body-only rail). Purpose selects the rendered template and
// routes observation. Key is the PII-free logical receipt key that makes a
// duplicate admission idempotent, lets a resend supersede exactly the prior active
// work, and is the value a session-gated status lookup keys on — the stable
// metadata safe to observe alongside Kind, Purpose, and Stage.
//
// For StageOpaque, ResolutionInput is the normalized identifier the processor
// resolves off the request path; the rendered fields are empty. For StageRendered,
// Destination is the resolved address and Subject/Body/HTML are the rendered
// message (Subject empty for SMS; HTML present only on the email rail so a retry
// resends the identical message). Secret is the rendered OTP/token, kept separate
// so diagnostics can scrub it. None of these fields is safe to observe; Validate
// guarantees they are consistent with Stage, and the STATIC error messages
// guarantee they never leak through an error.
type Envelope struct {
	Version         Version `json:"version"`
	Kind            string  `json:"kind"`
	Purpose         string  `json:"purpose"`
	Key             string  `json:"key"`
	Stage           Stage   `json:"stage"`
	ResolutionInput string  `json:"resolution_input,omitempty"`
	Destination     string  `json:"destination,omitempty"`
	Subject         string  `json:"subject,omitempty"`
	Body            string  `json:"body,omitempty"`
	HTML            string  `json:"html,omitempty"`
	Secret          string  `json:"secret,omitempty"`
}

// NewOpaque builds a validated opaque start command: the enumeration-safe admission
// form that carries only the normalized resolution input and is resolved/rendered
// off the request path. It sets the current Version so a struct built through it can
// never decode as an unknown version.
func NewOpaque(kind, purpose, key, resolutionInput string) (Envelope, error) {
	env := Envelope{
		Version:         Version1,
		Kind:            kind,
		Purpose:         purpose,
		Key:             key,
		Stage:           StageOpaque,
		ResolutionInput: resolutionInput,
	}
	if err := env.Validate(); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

// NewRendered builds a validated rendered command ready to send. secret may be
// empty for a notice with no code; body or html must be present. It sets the
// current Version.
func NewRendered(kind, purpose, key, destination, subject, body, html, secret string) (Envelope, error) {
	env := Envelope{
		Version:     Version1,
		Kind:        kind,
		Purpose:     purpose,
		Key:         key,
		Stage:       StageRendered,
		Destination: destination,
		Subject:     subject,
		Body:        body,
		HTML:        html,
		Secret:      secret,
	}
	if err := env.Validate(); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

// Opaque reports whether the command is an admitted-but-uninitialized start that the
// processor must resolve and render before delivery.
func (e Envelope) Opaque() bool { return e.Stage == StageOpaque }

// Validate enforces the structural invariants the processor and codec rely on:
// a recognized version; a non-empty kind, purpose, and logical key; a recognized
// stage; and stage/content consistency. It rejects an opaque command that carries
// rendered content (or lacks resolution input) and a rendered command that lacks a
// destination or any content. Every returned error is a STATIC sentinel, so a
// validation failure on attacker-influenced bytes never echoes a field value.
func (e Envelope) Validate() error {
	if e.Version != Version1 {
		return ErrUnknownVersion
	}
	if e.Kind == "" {
		return ErrMissingKind
	}
	if e.Purpose == "" {
		return ErrMissingPurpose
	}
	if e.Key == "" {
		return ErrMissingKey
	}
	switch e.Stage {
	case StageOpaque:
		if e.ResolutionInput == "" {
			return ErrStageMismatch
		}
		if e.Destination != "" || e.Subject != "" || e.Body != "" || e.HTML != "" || e.Secret != "" {
			return ErrStageMismatch
		}
	case StageRendered:
		if e.Destination == "" {
			return ErrStageMismatch
		}
		if e.Body == "" && e.HTML == "" {
			return ErrStageMismatch
		}
	default:
		return ErrInvalidStage
	}
	return nil
}
