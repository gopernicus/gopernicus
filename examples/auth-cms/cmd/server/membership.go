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
// relationshipGranter → authorizer.CreateRelationships — the tuple
// project:demo#member@user:<id>. The demo `view` permission is AnyOf(owner,
// member), so both an owner and a member pass the gate.
const (
	demoResourceType = "project"
	demoResourceID   = "demo"
	demoRelation     = "member" // the relation the invitation grants (owner also satisfies view)
	demoPermission   = "view"
)

// relationshipGranter adapts the authorization engine to auth.Granter — design
// §6's promised completion (authorization-v1 Z4 commit 2). Invitation-accept now
// writes a real ReBAC tuple via authorizer.CreateRelationships, retiring the A9
// toy in-memory membership map. There is STILL no import edge between features:
// the host is the only party that holds both auth and authorization, wiring them
// through this host-local adapter over the sdk-shaped Granter seam.
type relationshipGranter struct {
	authorizer *authorization.Service
}

var _ auth.Granter = relationshipGranter{}

// Grant records that (subjectType, subjectID) holds relation on the resource as a
// relationship tuple. CreateRelationships is idempotent (a duplicate is a silent
// no-op), satisfying the Granter contract's idempotence requirement.
func (g relationshipGranter) Grant(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	return g.authorizer.CreateRelationships(ctx, []authorization.CreateRelationship{{
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Relation:     relation,
		SubjectType:  subjectType,
		SubjectID:    subjectID,
	}})
}

// isPlatformAdmin is the HOST-side platform-admin recipe (host composition —
// the engine no longer bypasses on the platform:main#admin tuple). It runs an
// ordinary schema-declared `admin` permission Check on platform/main, which the
// host declares in its model (see main.go). It fails CLOSED: any error or a
// missing tuple yields false. A host that wants admin-sees-everything runs this
// FIRST in its own closure, before the resource-specific check.
func isPlatformAdmin(ctx context.Context, authorizer *authorization.Service, subjectType, subjectID string) bool {
	res, err := authorizer.Check(ctx, authorization.CheckRequest{
		Subject:    authorization.Subject{Type: subjectType, ID: subjectID},
		Permission: "admin",
		Resource:   authorization.Resource{Type: "platform", ID: "main"},
	})
	return err == nil && res.Allowed
}

// requireMembership gates a route on the caller — already resolved by
// RequirePrincipal into ctx — holding the demo `view` permission on the demo
// resource, checked THROUGH the authorization engine (authorizer.Check, the
// flagship posture; the A9 toy-map read is retired). A platform admin passes via
// the host-composed isPlatformAdmin recipe run FIRST. A resolved principal
// WITHOUT access → 403; no resolved principal (RequirePrincipal should have
// blocked that already) → 401. It reads the principal through the exported
// auth.Service.CurrentPrincipal port, with zero import into the feature internals.
func requireMembership(authSvc *auth.Service, authorizer *authorization.Service) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := authSvc.CurrentPrincipal(r.Context())
			if !ok {
				writeHostJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
				return
			}
			// Platform-admin recipe: host runs it first (engine grants no bypass).
			if isPlatformAdmin(r.Context(), authorizer, p.Type, p.ID) {
				next.ServeHTTP(w, r)
				return
			}
			res, err := authorizer.Check(r.Context(), authorization.CheckRequest{
				Subject:    authorization.Subject{Type: p.Type, ID: p.ID},
				Permission: demoPermission,
				Resource:   authorization.Resource{Type: demoResourceType, ID: demoResourceID},
			})
			if err != nil {
				writeHostJSON(w, http.StatusInternalServerError, map[string]string{"error": "authorization check failed"})
				return
			}
			if !res.Allowed {
				writeHostJSON(w, http.StatusForbidden, map[string]string{
					"error":      "not authorized on " + demoResourceType + "/" + demoResourceID,
					"permission": demoPermission,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
