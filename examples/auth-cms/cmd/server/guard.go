package main

import (
	"context"
	"fmt"

	authorization "github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// manageRelation is the concrete relationship that backs the schema's
// `manage_access` permission (manageAccessPerm = Direct("owner")). The host
// MutationGuard authorizes an actor-facing write on a resource scope by reading
// this relation through the dependency-tracking DecisionView. It reads the RELATION
// rather than evaluating the permission because a DecisionView exposes only relation
// and role reads (CheckRelation / HasRole) — never permission evaluation — so a
// guard's authorization data is always a concrete, revision-trackable anchor. The
// schema still DECLARES manage_access = Direct(owner) so the meaning of "may manage
// access" is a single, visible schema fact and this guard reads its one backing
// relation.
const manageRelation = "owner"

// platformAdminScope is the resource scope the platform-admin data tuple lives on
// (platform:main#admin). The host guard reads it through the DecisionView so the
// platform-admin short-circuit is composed IN the guard and its authorization data
// is a revision-tracked dependency of the mutation — a concurrent revoke of an
// actor's platform-admin grant is detectable at commit, never a detached allow.
var platformAdminScope = authorization.ScopeKey{
	Kind: authorization.ScopeResource,
	Type: platformResourceType,
	ID:   platformResourceID,
}

// hostMutationGuard is the host's actor-facing authorization policy (the
// middleware-consolidation "host recipe" row: platform-admin short-circuits are
// host composition, run first, fail closed). It composes two host-owned rules over
// the SUPPLIED dependency-tracking DecisionView only — it never calls the outer
// authorization Service, which would open a detached check-then-write race:
//
//   - platform-admin short-circuit: an actor holding platform:main#admin may drive
//     any actor-facing mutation. Read through the view, so the grant's revision is a
//     mutation dependency the repository re-validates under lock.
//   - manage_access: otherwise, an actor may mutate a RESOURCE scope only if it
//     holds the manage_access-backing relation (owner) on THAT resource. A global
//     (subject-scoped) mutation has no per-resource manage_access anchor, so only a
//     platform admin may drive one; every other global attempt is refused (its blast
//     radius belongs to a trusted SystemMutator holder).
//
// The guard is the single sanctioned authority for actor-facing writes on this host:
// the trusted, actor-free path is the separately held SystemMutator (bootstrap seed,
// invitation acceptance), never an Actor flagged privileged.
type hostMutationGuard struct{}

var _ authorization.MutationGuard = hostMutationGuard{}

// AuthorizeMutation returns nil to ALLOW the attempt or a stable denial wrapping
// sdk.ErrForbidden to reject it. It performs only DecisionView reads and honors ctx
// cancellation — no outer-Service call, no network or unrelated-store I/O.
func (hostMutationGuard) AuthorizeMutation(ctx context.Context, attempt authorization.MutationAttempt, view authorization.DecisionView) error {
	// Platform-admin recipe, composed in the host guard and read ONLY through the
	// view (the engine grants no bypass). Fail closed: a view error propagates.
	admin, err := view.CheckRelation(ctx, platformAdminScope, "admin", attempt.Actor.Type, attempt.Actor.ID)
	if err != nil {
		return err
	}
	if admin {
		return nil
	}

	// Global mutations are trusted-only unless the actor is a platform admin (above).
	if attempt.Scope.Kind != authorization.ScopeResource {
		return fmt.Errorf("global authorization mutation requires platform admin: %w", sdk.ErrForbidden)
	}

	// manage_access: the actor must hold the manage-backing relation on the mutated
	// resource scope, read through the view so the allow decision is itself a
	// revision-tracked dependency.
	ok, err := view.CheckRelation(ctx, attempt.Scope, manageRelation, attempt.Actor.Type, attempt.Actor.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s:%s lacks manage_access on %s:%s: %w",
			attempt.Actor.Type, attempt.Actor.ID, attempt.Scope.Type, attempt.Scope.ID, sdk.ErrForbidden)
	}
	return nil
}

// hostActor adapts a resolved platform identity.Principal directly onto an
// authorization.Actor (its concrete PrincipalRef) at the host boundary — the untrusted
// principal an actor-facing guarded mutation is attempted on behalf of. There is no
// system synonym here by construction: Actor carries only (Type, ID), so ordinary host
// code cannot manufacture a trusted actor; trusted writes go through the separately
// held SystemMutator. Actor-facing HTTP mutation routes are deferred until
// authentication exports a public sensitive-operation protector (the AZADM packet), so
// on this proof host the adapter is exercised by the guarded-composition tests rather
// than a browser flow.
func hostActor(p identity.Principal) authorization.Actor {
	return authorization.Actor{PrincipalRef: authorization.PrincipalFrom(p)}
}
