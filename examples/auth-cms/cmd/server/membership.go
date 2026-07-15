package main

import (
	"context"
	"net/http"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	authorization "github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// The demo resource the flagship checks against. An invitation created at
// POST /auth/invitations/project/demo with relation "member" grants — through the
// relationshipGranter → the trusted SystemMutator — the tuple
// project:demo#member@user:<id>. The demo `view` permission is AnyOf(owner,
// member), so both an owner and a member pass the gate.
const (
	demoResourceType = "project"
	demoResourceID   = "demo"
	demoRelation     = "member" // the relation the invitation grants (owner also satisfies view)
	demoPermission   = "view"
)

// relationshipGranter adapts the authorization write path to auth.Granter — design
// §6's promised completion (authorization-v1 Z4 commit 2). Invitation-accept is a
// TRUSTED operation (a listed SystemMutator holder), so it writes through the
// separately held SystemMutator, not the actor-facing Service (which now requires a
// host MutationGuard). There is STILL no import edge between features: the host is
// the only party that holds both auth and authorization, wiring them through this
// host-local adapter over the sdk-shaped Granter seam.
type relationshipGranter struct {
	system *authorization.SystemMutator
}

var _ auth.Granter = relationshipGranter{}

// Grant records that (subjectType, subjectID) holds relation on the resource as a
// relationship tuple. The MutationID is DERIVED deterministically from the grant's
// operation identity (its resulting tuple), so a retried accept dedups against the
// stored mutation — no duplicate stored mutation and no duplicate revision bump,
// satisfying the Granter contract's idempotence requirement (AZ3-3.4). A persisted
// outcome (applied / no_change / a one-relation conflict) returns nil; only a genuine
// command/infrastructure error propagates.
func (g relationshipGranter) Grant(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	mid := authorization.DeriveMutationID("auth-cms/invitation-grant", resourceType, resourceID, relation, subjectType, subjectID)
	_, err := g.system.GrantRelationship(ctx, authorization.GrantRelationshipCommand{
		MutationID:   mid,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Relation:     relation,
		Subject:      authorization.SubjectRef{Type: subjectType, ID: subjectID},
	})
	return err
}

// isPlatformAdmin is the HOST-side platform-admin recipe (host composition —
// the engine no longer bypasses on the platform:main#admin tuple). It runs an
// ordinary schema-declared `admin` permission Check on platform/main, which the
// host declares in its model (see main.go). It fails CLOSED: any error or a
// missing tuple yields false. A host that wants admin-sees-everything runs this
// FIRST in its own closure, before the resource-specific check.
func isPlatformAdmin(ctx context.Context, authorizer *authorization.Service, subjectType, subjectID string) bool {
	res, err := authorizer.Check(ctx, authorization.CheckRequest{
		Principal:  authorization.PrincipalRef{Type: subjectType, ID: subjectID},
		Permission: "admin",
		Resource:   authorization.Resource{Type: "platform", ID: "main"},
	})
	return err == nil && res.Allowed
}

// requireMembership gates a route on the caller — already resolved by
// RequirePrincipal into ctx — holding the demo `view` permission on the demo
// resource. The Check/401/403/500 leg is now the FEATURE's exported builder
// (authorizer.RequirePermission, whose responses carry the FS9 web.Error shape);
// platform-admin stays HOST composition, run FIRST in this closure via the
// isPlatformAdmin recipe (the engine grants no bypass). The gate is built once
// at registration — a roles-only wiring would panic here at boot, not per
// request. Per request it reads the principal ONCE through the exported
// auth.Service.CurrentPrincipal port (zero import into feature internals): a
// present admin passes straight to next; every other case — non-admin principal
// or none at all — falls through to the builder-gated handler (its 403/500 or
// 401 legs respectively).
func requireMembership(authSvc *auth.Service, authorizer *authorization.Service) web.Middleware {
	gate := authorizer.RequirePermission(demoPermission, authorization.FixedResource(demoResourceType, demoResourceID))
	return func(next http.Handler) http.Handler {
		gated := gate(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Platform-admin recipe: host runs it first (engine grants no bypass).
			if p, ok := authSvc.CurrentPrincipal(r.Context()); ok && isPlatformAdmin(r.Context(), authorizer, p.Type, p.ID) {
				next.ServeHTTP(w, r)
				return
			}
			gated.ServeHTTP(w, r)
		})
	}
}
