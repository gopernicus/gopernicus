package authorization

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gopernicus/gopernicus/features/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/features/authorization/internal/logic/authorizersvc"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Reason is a stable, machine-readable classification of an authorization
// decision outcome or failure. The string values are frozen wire codes: a host,
// an audit sink, an explain trace, or the deferred admin surface may switch on
// them. Replay is NOT a Reason — it is independent receipt metadata (per default
// #8), and an effect outcome is not a Reason either (deferred to the effects
// packet).
//
// The type and its values are defined once in the sealed engine
// (internal/logic/authorizersvc) so the decision path — CheckResult.ReasonCode
// and the explain trace — can name a stable code without importing this root
// package; the root re-exports them as aliases (the ErrEvaluationLimit
// precedent, AZ3-0.3).
type Reason = authorizersvc.Reason

const (
	// ReasonGranted — a well-formed decision evaluated to allow (CheckResult).
	ReasonGranted = authorizersvc.ReasonGranted

	// ReasonInvalidRequest — the request is structurally malformed. Maps to
	// sdk.ErrInvalidInput / HTTP 400.
	ReasonInvalidRequest = authorizersvc.ReasonInvalidRequest

	// ReasonUnknownSymbol — the request or command names a symbol the compiled
	// model does not define. Maps to sdk.ErrInvalidInput / HTTP 400.
	ReasonUnknownSymbol = authorizersvc.ReasonUnknownSymbol

	// ReasonDenied — a well-formed decision evaluated to deny. It is an ordinary
	// decision outcome, NOT an error.
	ReasonDenied = authorizersvc.ReasonDenied

	// ReasonEvaluationLimit — evaluation exhausted its work budget. Indeterminate,
	// wrapping sdk.ErrUnavailable / HTTP 503 (default #9).
	ReasonEvaluationLimit = authorizersvc.ReasonEvaluationLimit

	// ReasonStaleRevision — an expected scope revision no longer matched at
	// commit. A command error. Maps to sdk.ErrConflict.
	ReasonStaleRevision = authorizersvc.ReasonStaleRevision

	// ReasonInvariantConflict — a protected invariant blocked the write. Maps to
	// sdk.ErrConflict.
	ReasonInvariantConflict = authorizersvc.ReasonInvariantConflict

	// ReasonMutationMismatch — a MutationID was replayed with a different payload.
	// A command error. Maps to sdk.ErrConflict.
	ReasonMutationMismatch = authorizersvc.ReasonMutationMismatch

	// ReasonInfrastructure — an unclassified backing-store/transport failure. Maps
	// to HTTP 500; it wraps no sdk sentinel.
	ReasonInfrastructure = authorizersvc.ReasonInfrastructure
)

// Stable feature sentinels. Each wraps the sdk taxonomy kind that fixes its
// transport mapping (post-AV3-9.8): saturation/limit exhaustion wraps
// sdk.ErrUnavailable — never a new kind, never sdk.ErrConflict (default #9). The
// mutation/revision sentinels are the frozen vocabulary the later mutation
// phases (AZ3-0.4+) attach behavior to; this task freezes the codes only.
var (
	// ErrInvalidRequest reports a structurally malformed decision or command.
	ErrInvalidRequest = fmt.Errorf("authorization: invalid request: %w", sdk.ErrInvalidInput)

	// ErrUnknownSymbol reports a reference to a symbol the model does not define.
	ErrUnknownSymbol = fmt.Errorf("authorization: unknown model symbol: %w", sdk.ErrInvalidInput)

	// ErrEvaluationLimit reports evaluation-budget exhaustion (indeterminate). It
	// is the same sentinel the engine returns, re-exported so a host can classify
	// a decision error with errors.Is; it wraps sdk.ErrUnavailable (never a new
	// kind, never sdk.ErrConflict — default #9).
	ErrEvaluationLimit = authorizersvc.ErrEvaluationLimit

	// ErrInvalidLimits reports a negative Config.Limits field at NewService. It is
	// a CONSTRUCTION error wrapping sdk.ErrInvalidInput — distinct from the
	// runtime ErrEvaluationLimit above. Zero fields are valid: they select the
	// safe defaults.
	ErrInvalidLimits = authorizersvc.ErrInvalidLimits

	// ErrStaleRevision reports an expected scope revision that no longer matched.
	// It is the shared contract sentinel (mutation.ErrStaleRevision) re-exported so
	// a host classifying a mutation error, the store adapters that RETURN it, and
	// this package's mapper all share ONE identity (the ErrEvaluationLimit precedent).
	ErrStaleRevision = mutation.ErrStaleRevision

	// ErrInvariantConflict reports a protected-invariant block.
	ErrInvariantConflict = fmt.Errorf("authorization: invariant conflict: %w", sdk.ErrConflict)

	// ErrMutationMismatch reports a MutationID replayed with a different payload. It
	// is the shared contract sentinel (mutation.ErrPayloadMismatch) re-exported so
	// every store returns one identity classifiable by errors.Is at the host.
	ErrMutationMismatch = mutation.ErrPayloadMismatch

	// ErrInfrastructure reports an unclassified backing-store/transport failure.
	// It wraps no sdk sentinel, so it maps to HTTP 500.
	ErrInfrastructure = errors.New("authorization: infrastructure failure")

	// ErrMutationsNotConfigured reports that an actor-facing (guarded) mutation was
	// attempted on a deployment that wired no MutationGuard — the read-only posture
	// (AZ3-0.5) — or with no atomic MutationRepository behind it. It is a stable,
	// deterministic PRECONDITION refusal: the request cannot be accepted as-is by
	// this deployment, and retrying it unchanged will not help until the host wires
	// a guard/repository. It therefore wraps sdk.ErrInvalidInput (precondition),
	// NOT sdk.ErrUnavailable — this is not transient saturation (default #9) — and
	// NOT sdk.ErrForbidden, which would falsely imply some other principal could
	// succeed against the same deployment. There is deliberately no default allow
	// guard: the absence of a guard closes the actor-facing write path, it does not
	// open it.
	ErrMutationsNotConfigured = fmt.Errorf("authorization: actor-facing mutations are not configured (no MutationGuard): %w", sdk.ErrInvalidInput)
)

// ReasonFor classifies err into its stable [Reason], returning ok=false for an
// error the feature does not own (the caller then treats it as infrastructure).
// It recognizes the feature sentinels above and the last-owner invariant; it is
// the shared classifier for logging, audit, and the HTTP seam.
func ReasonFor(err error) (Reason, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, ErrEvaluationLimit):
		return ReasonEvaluationLimit, true
	case errors.Is(err, ErrStaleRevision):
		return ReasonStaleRevision, true
	case errors.Is(err, ErrMutationMismatch):
		return ReasonMutationMismatch, true
	case errors.Is(err, ErrInvariantConflict):
		return ReasonInvariantConflict, true
	case errors.Is(err, ErrUnknownSymbol):
		return ReasonUnknownSymbol, true
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, sdk.ErrInvalidInput):
		return ReasonInvalidRequest, true
	case errors.Is(err, sdk.ErrUnavailable):
		return ReasonEvaluationLimit, true
	default:
		return "", false
	}
}

// errorResponse maps a feature sentinel to a *web.Error carrying its named
// machine [Reason] code, following auth v3's feature-local mapper precedent. It
// returns ok=false for anything else so the caller falls back to the generic
// sdk-kind mapping. The sdk mapper (web.ErrFromDomain) is untouched.
func errorResponse(err error) (*web.Error, bool) {
	switch {
	case errors.Is(err, ErrEvaluationLimit), errors.Is(err, sdk.ErrUnavailable):
		return web.NewError(http.StatusServiceUnavailable, "authorization temporarily unavailable").WithCode(string(ReasonEvaluationLimit)), true
	case errors.Is(err, ErrStaleRevision):
		return web.NewError(http.StatusConflict, "stale revision").WithCode(string(ReasonStaleRevision)), true
	case errors.Is(err, ErrMutationMismatch):
		return web.NewError(http.StatusConflict, "mutation payload mismatch").WithCode(string(ReasonMutationMismatch)), true
	case errors.Is(err, ErrInvariantConflict):
		return web.NewError(http.StatusConflict, "invariant conflict").WithCode(string(ReasonInvariantConflict)), true
	case errors.Is(err, ErrUnknownSymbol):
		return web.NewError(http.StatusBadRequest, "unknown model symbol").WithCode(string(ReasonUnknownSymbol)), true
	case errors.Is(err, ErrInvalidRequest):
		return web.NewError(http.StatusBadRequest, "invalid request").WithCode(string(ReasonInvalidRequest)), true
	default:
		return nil, false
	}
}

// RespondError writes err as a JSON error, emitting the feature's stable machine
// code when err is an authorization sentinel and otherwise falling back to the
// generic sdk-kind mapping (web.RespondJSONDomainError). It is the one seam a
// future authorization inbound surface writes decision/mutation errors through,
// so the JSON body code derives from one mapping and the sdk mapper stays
// untouched. Denial is not an error and never reaches here.
func RespondError(w http.ResponseWriter, err error) {
	if mapped, ok := errorResponse(err); ok {
		web.RespondJSONError(w, mapped)
		return
	}
	web.RespondJSONDomainError(w, err)
}
