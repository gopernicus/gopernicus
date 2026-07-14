package delivery

import (
	deliverycmd "github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery/command"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
)

// This file bridges the bespoke producer vocabulary (delivery.Command /
// delivery.Envelope) to the transport-neutral, versioned deliverycmd.Envelope the
// jobs-mode processor (deliverycmd.Engine) opens (AV3D-3.1). It is the codec swap the
// AV3D-2.3 dispatcher log promised for phase 3 — a drop-in implementation change
// behind the same Dispatcher seam, keeping producers and the Dispatcher unchanged.
// The delivery package already owns the command subpackage (sdk-only), so this adds
// no new dependency direction.

// sealCommand converts a producer Command into the versioned deliverycmd.Envelope and
// seals it, so a jobs-mode submit persists exactly what deliverycmd.Open reads back. An
// opaque start (no rendered content) becomes StageOpaque carrying only the resolution
// input; a pre-rendered command becomes StageRendered.
func sealCommand(enc cryptids.Encrypter, cmd Command) ([]byte, error) {
	env, err := toCommandEnvelope(cmd)
	if err != nil {
		return nil, err
	}
	return deliverycmd.Seal(enc, env)
}

// toCommandEnvelope maps a producer Command onto the versioned deliverycmd.Envelope.
func toCommandEnvelope(cmd Command) (deliverycmd.Envelope, error) {
	e := cmd.Envelope
	if commandOpaque(e) {
		return deliverycmd.NewOpaque(cmd.Kind, cmd.Purpose, cmd.IdempotencyKey, e.ResolutionInput)
	}
	return deliverycmd.NewRendered(cmd.Kind, cmd.Purpose, cmd.IdempotencyKey, e.Destination, e.Subject, e.Body, e.HTML, e.Secret)
}

// commandOpaque mirrors the worker's needsInit: a rendered message always carries a
// Body (text/SMS) or HTML; an opaque enumeration-safe start carries only the
// normalized resolution input.
func commandOpaque(e Envelope) bool { return e.Body == "" && e.HTML == "" }

// commandToEnvelope converts a rendered deliverycmd.Envelope back into the bespoke
// delivery.Envelope the router/initializer collaborators speak.
func commandToEnvelope(e deliverycmd.Envelope) Envelope {
	return Envelope{
		Destination:     e.Destination,
		ResolutionInput: e.ResolutionInput,
		Subject:         e.Subject,
		Body:            e.Body,
		HTML:            e.HTML,
		Secret:          e.Secret,
	}
}
