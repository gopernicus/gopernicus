package model

import (
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// ErrInvalidRequest reports a structurally malformed decision or command.
var ErrInvalidRequest = fmt.Errorf("authorization: invalid request: %w", sdk.ErrInvalidInput)

// ErrUnknownSymbol reports a reference to a symbol the model does not define.
var ErrUnknownSymbol = fmt.Errorf("authorization: unknown model symbol: %w", sdk.ErrInvalidInput)

// Reason classifies an authorization decision or failure for hosts and transports.
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

	// ReasonConcurrentMutation reports an aborted write due to transaction contention.
	ReasonConcurrentMutation Reason = "concurrent_mutation"

	// ReasonInvariantConflict — a protected invariant blocked the write (e.g.
	// last-owner/guardian minimum). Maps to sdk.ErrConflict.
	ReasonInvariantConflict Reason = "invariant_conflict"

	// ReasonSemanticConflict identifies a rejected relationship grant.
	ReasonSemanticConflict Reason = "semantic_conflict"

	// ReasonInfrastructure — an unclassified backing-store or transport failure.
	// Maps to HTTP 500; it wraps no sdk sentinel, so it never leaks as a specific
	// client-actionable code.
	ReasonInfrastructure Reason = "infrastructure_failure"
)

// outcomeReason maps a boolean decision outcome to its stable coarse code. It is
// the single point that keeps CheckResult.ReasonCode and the explain trace in
// lockstep: a granted decision is ReasonGranted, a denied one ReasonDenied.

var ErrNoDecisionKind = errors.New("authorization: no decision-capable kind is configured (set WithRelationshipModel for the relationship kind or WithRoleModel for the roles kind)")
var ErrInfrastructure = errors.New("authorization: infrastructure failure")

func PrincipalFrom(p sdk.Principal) PrincipalRef { return PrincipalRef{Type: p.Type, ID: p.ID} }
