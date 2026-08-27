package authorizersvc

// Reason is a stable, machine-readable classification of an authorization
// decision outcome or failure. The string values are frozen wire codes: a host,
// an audit sink, an explain trace, or the deferred admin surface may switch on
// them. Replay is NOT a Reason — it is independent receipt metadata (per default
// #8) — and an effect outcome is not a Reason either (deferred to the effects
// packet).
//
// The type HOME is this engine package, not the root, because the sealed
// decision engine (CheckResult.ReasonCode, the explain trace) must name a stable
// code and cannot import the root package (the root imports it). The root
// re-exports the type and every value as aliases, exactly as it re-exports
// ErrEvaluationLimit (AZ3-0.3) — so hosts write authorization.Reason /
// authorization.ReasonDenied with one shared definition beneath both.
type Reason string

const (
	// ReasonGranted — a well-formed decision evaluated to ALLOW. It is the stable
	// coarse code carried by an allowed CheckResult; the accompanying free-text
	// Reason ("direct:owner", "through:org->...") is non-contract debug only.
	ReasonGranted Reason = "granted"

	// ReasonDenied — a well-formed decision evaluated to DENY (no rule granted, a
	// path-local cycle, or no rules defined). It is an ordinary decision outcome,
	// NOT an error; it never masquerades as a failure and a failure never
	// masquerades as it.
	ReasonDenied Reason = "denied"

	// ReasonInvalidRequest — the request is structurally malformed (empty,
	// over-long, non-UTF-8, or control-character-bearing type/id/relation/
	// permission). Maps to sdk.ErrInvalidInput / HTTP 400.
	ReasonInvalidRequest Reason = "invalid_request"

	// ReasonUnknownSymbol — the request or command names a resource type,
	// relation, or permission the compiled model does not define. Maps to
	// sdk.ErrInvalidInput / HTTP 400.
	ReasonUnknownSymbol Reason = "unknown_model_symbol"

	// ReasonEvaluationLimit — evaluation exhausted its work budget (depth, graph
	// states, fan-out, batch, or result bound). It is indeterminate, wrapping
	// sdk.ErrUnavailable / HTTP 503 (default #9): callers fail closed and retry
	// unchanged; it is never a deny and never a complete partial list.
	ReasonEvaluationLimit Reason = "evaluation_limit"

	// ReasonStaleRevision — an expected scope revision no longer matched at
	// commit. A command error, not a successful outcome. Maps to sdk.ErrConflict.
	ReasonStaleRevision Reason = "stale_revision"

	// ReasonInvariantConflict — a protected invariant blocked the write (e.g.
	// last-owner/guardian minimum). Maps to sdk.ErrConflict.
	ReasonInvariantConflict Reason = "invariant_conflict"

	// ReasonMutationMismatch — a MutationID was replayed with a different payload
	// than the one first committed under it. A command error. Maps to
	// sdk.ErrConflict.
	ReasonMutationMismatch Reason = "mutation_payload_mismatch"

	// ReasonInfrastructure — an unclassified backing-store or transport failure.
	// Maps to HTTP 500; it wraps no sdk sentinel, so it never leaks as a specific
	// client-actionable code.
	ReasonInfrastructure Reason = "infrastructure_failure"
)

// outcomeReason maps a boolean decision outcome to its stable coarse code. It is
// the single point that keeps CheckResult.ReasonCode and the explain trace in
// lockstep: a granted decision is ReasonGranted, a denied one ReasonDenied.
func outcomeReason(allowed bool) Reason {
	if allowed {
		return ReasonGranted
	}
	return ReasonDenied
}
