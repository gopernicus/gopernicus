package main

import (
	"context"
	"net/http"
	"sync"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// The demo resource the membership-gated route checks against. An invitation
// created at POST /auth/invitations/project/demo with relation "member" grants
// through the toy Granter into exactly this (resource, relation).
const (
	demoResourceType = "project"
	demoResourceID   = "demo"
	demoRelation     = "member"
)

// membership is the proof host's TOY Granter (design §6, ratified AV4): an
// in-memory resource→relation→subject membership map. It structurally satisfies
// auth.Granter (Grant grants a subject a relation on a resource) and is read back
// by requireMembership to gate a route. It is the DEMONSTRATION of the ruling —
// invitations grant with NO ReBAC anywhere in this host's module graph;
// authorization-v1's proof host swaps authorizer.CreateRelationships in via the
// same seam. Grants are idempotent (a set add), as the Granter contract requires.
type membership struct {
	mu sync.Mutex
	// grants[resourceType/resourceID][relation][subjectType:subjectID].
	grants map[string]map[string]map[string]struct{}
}

var _ auth.Granter = (*membership)(nil)

func newMembership() *membership {
	return &membership{grants: map[string]map[string]map[string]struct{}{}}
}

// Grant records that (subjectType, subjectID) holds relation on the resource.
func (m *membership) Grant(_ context.Context, resourceType, resourceID, relation, subjectType, subjectID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := resourceType + "/" + resourceID
	if m.grants[res] == nil {
		m.grants[res] = map[string]map[string]struct{}{}
	}
	if m.grants[res][relation] == nil {
		m.grants[res][relation] = map[string]struct{}{}
	}
	m.grants[res][relation][subjectType+":"+subjectID] = struct{}{}
	return nil
}

// has reports whether (subjectType, subjectID) holds relation on the resource.
func (m *membership) has(resourceType, resourceID, relation, subjectType, subjectID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	rel := m.grants[resourceType+"/"+resourceID][relation]
	if rel == nil {
		return false
	}
	_, ok := rel[subjectType+":"+subjectID]
	return ok
}

// requireMembership gates a route on the caller — already resolved by
// RequirePrincipal into ctx — holding demoRelation on the demo resource in the
// toy map. A resolved principal WITHOUT the grant → 403; no resolved principal
// (RequirePrincipal should have blocked that already) → 401. It reads the
// principal through the exported auth.Service.CurrentPrincipal port, with zero
// import into the feature internals.
func requireMembership(authSvc *auth.Service, m *membership) web.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := authSvc.CurrentPrincipal(r.Context())
			if !ok {
				writeHostJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
				return
			}
			if !m.has(demoResourceType, demoResourceID, demoRelation, p.Type, p.ID) {
				writeHostJSON(w, http.StatusForbidden, map[string]string{
					"error":    "not a member of " + demoResourceType + "/" + demoResourceID,
					"relation": demoRelation,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
