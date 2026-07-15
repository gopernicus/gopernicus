package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	authorization "github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/sdk"
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

// resourceExistsFn reports whether a host resource (resourceType:resourceID) still
// exists. Only the host — never authentication — owns resource lifecycle, so the host
// supplies this seam and the Granter consults it before writing a tuple. A production
// host reads its own datastore here (does this post/page/project row still exist?);
// this proof host reads a small in-memory registry.
type resourceExistsFn func(ctx context.Context, resourceType, resourceID string) (bool, error)

// hostResourceRegistry is the host's authoritative record of which resources exist.
// Authorization tuples merely DESCRIBE resources the host owns; only the host knows
// whether a project/post/page row still exists. A production host consults its own
// datastore here. This proof host keeps a tiny in-memory set (seeded with the demo
// project) so the reference composition can demonstrate the Granter's deleted-resource
// duty: accepting an invitation against a since-deleted resource must fail loudly, never
// grant access to a resource that no longer exists.
type hostResourceRegistry struct {
	mu   sync.RWMutex
	live map[string]struct{}
}

// newHostResourceRegistry builds the registry pre-populated with the given
// resourceKey(...) values.
func newHostResourceRegistry(keys ...string) *hostResourceRegistry {
	r := &hostResourceRegistry{live: make(map[string]struct{}, len(keys))}
	for _, k := range keys {
		r.live[k] = struct{}{}
	}
	return r
}

// resourceKey is the host's stable identity for a resource across the registry.
func resourceKey(resourceType, resourceID string) string {
	return resourceType + ":" + resourceID
}

// Exists is the resourceExistsFn seam the Granter consults before mutating.
func (r *hostResourceRegistry) Exists(_ context.Context, resourceType, resourceID string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.live[resourceKey(resourceType, resourceID)]
	return ok, nil
}

// remove deletes a resource from the registry — a host lifecycle event (the project/post
// was destroyed). After this, a grant against the resource fails the existence preflight.
func (r *hostResourceRegistry) remove(resourceType, resourceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, resourceKey(resourceType, resourceID))
}

// relationshipGranter adapts the authorization write path to auth.Granter — design
// §6's promised completion (authorization-v1 Z4 commit 2). Invitation-accept is a
// TRUSTED operation (a listed SystemMutator holder), so it writes through the
// separately held SystemMutator, not the actor-facing Service (which now requires a
// host MutationGuard). There is STILL no import edge between features: the host is
// the only party that holds both auth and authorization, wiring them through this
// host-local adapter over the sdk-shaped Granter seam. It also holds the host's
// resource-existence seam, because only the host knows whether the target still exists.
type relationshipGranter struct {
	system *authorization.SystemMutator
	exists resourceExistsFn
}

var _ auth.Granter = relationshipGranter{}

// Grant records that (in.SubjectType, in.SubjectID) holds in.Relation on the resource as
// a relationship tuple. Two host duties frame the write:
//
// Resource existence (D2). The host — not authentication — owns resource lifecycle, so it
// validates the target still exists BEFORE writing a tuple. Accepting an invitation against
// a since-deleted resource fails loudly (sdk.ErrNotFound) and writes nothing; nil could
// otherwise grant access to a resource that no longer exists.
//
// Operation-scoped identity (D1). The MutationID is derived from a fixed host purpose, the
// authentication OPERATION ID, and the exact tuple — NOT from the tuple alone. Authentication
// guarantees a distinct operation id per logical invitation grant (the persisted invitation
// row id; a fresh high-entropy value for direct-add). So a retry of the SAME invitation
// reuses the SAME MutationID (an idempotent replay), while a LATER invitation that re-grants
// the same tuple after a revoke carries a DISTINCT operation id → a DISTINCT MutationID → a
// fresh grant that restores the tuple. Deriving from the tuple alone (the retired bug)
// collapsed those into one mutation, so the re-grant silently replayed and never restored the
// revoked tuple while authentication recorded the grant as successful.
//
// Receipt outcome mapping (D2). A non-error receipt is NOT automatically success. Only
// OutcomeApplied and OutcomeNoChange mean the EXACT requested relation is now effective →
// nil. OutcomeSemanticConflict (a DIFFERENT relation already exists for the subject under the
// one-relation rule) and OutcomeInvariantBlocked (a guardian minimum refused the write)
// changed nothing and persisted no receipt, so authentication must not record them as a
// successful grant → a loud error wrapping sdk.ErrConflict. We deliberately do NOT
// ReplaceRelationship: authentication cannot decide an invitation may upgrade or downgrade an
// existing membership. Any other outcome (e.g. not_found) is likewise not a provable success
// and fails closed.
func (g relationshipGranter) Grant(ctx context.Context, in auth.GrantInput) error {
	if g.exists == nil {
		return fmt.Errorf("auth-cms: relationshipGranter resource-existence seam is not wired")
	}
	exists, err := g.exists(ctx, in.ResourceType, in.ResourceID)
	if err != nil {
		return fmt.Errorf("auth-cms: resource existence probe for %s:%s failed: %w", in.ResourceType, in.ResourceID, err)
	}
	if !exists {
		return fmt.Errorf("auth-cms: host resource %s:%s no longer exists; not granting %q to %s:%s: %w",
			in.ResourceType, in.ResourceID, in.Relation, in.SubjectType, in.SubjectID, sdk.ErrNotFound)
	}

	mid := authorization.DeriveMutationID("auth-cms/invitation-grant",
		in.OperationID, in.ResourceType, in.ResourceID, in.Relation, in.SubjectType, in.SubjectID)
	receipt, err := g.system.GrantRelationship(ctx, authorization.GrantRelationshipCommand{
		MutationID:   mid,
		ResourceType: in.ResourceType,
		ResourceID:   in.ResourceID,
		Relation:     in.Relation,
		Subject:      authorization.SubjectRef{Type: in.SubjectType, ID: in.SubjectID},
	})
	if err != nil {
		return err
	}

	switch receipt.Outcome {
	case authorization.OutcomeApplied, authorization.OutcomeNoChange:
		return nil
	case authorization.OutcomeSemanticConflict:
		return fmt.Errorf("auth-cms: %s:%s already holds a different relation on %s:%s; grant of %q refused "+
			"(one-relation conflict, no implicit replace): %w",
			in.SubjectType, in.SubjectID, in.ResourceType, in.ResourceID, in.Relation, sdk.ErrConflict)
	case authorization.OutcomeInvariantBlocked:
		return fmt.Errorf("auth-cms: grant of %q to %s:%s on %s:%s was blocked by a protected invariant: %w",
			in.Relation, in.SubjectType, in.SubjectID, in.ResourceType, in.ResourceID, sdk.ErrConflict)
	default:
		return fmt.Errorf("auth-cms: grant of %q to %s:%s on %s:%s returned non-success outcome %q: %w",
			in.Relation, in.SubjectType, in.SubjectID, in.ResourceType, in.ResourceID, receipt.Outcome, sdk.ErrConflict)
	}
}

// hostInviteCheck is the relation-aware host authorization policy the authentication
// feature calls from its parsed create/list invitation handlers (auth.Config.InviteCheck,
// design D3). It is REQUIRED whenever a Granter enables invitations, and it runs AFTER the
// feature has resolved the caller principal and parsed the exact requested relation — data a
// route-wrapping middleware could never see. The mapping expresses "may this caller grant
// relation R on this resource":
//
//   - a platform admin manages membership on every resource (the host recipe, run first,
//     fails closed) — including granting owner;
//   - granting the OWNER relation is otherwise reserved: an ordinary membership manager may
//     add members but not mint a co-owner. This is the editor→owner escalation guard;
//   - every other create, and every list, requires the caller to hold manage_access
//     (Direct(owner), the membership-management permission) on the target resource.
//
// A denial wraps sdk.ErrForbidden (→403); an authorizer infrastructure error fails CLOSED
// (returned as-is, →500), never an allow.
func hostInviteCheck(authorizer *authorization.Service) auth.InviteCheck {
	return func(ctx context.Context, req auth.InviteCheckRequest) error {
		// Platform-admin recipe: host runs it first (engine grants no bypass). isPlatformAdmin
		// already fails closed to false on any probe error.
		if isPlatformAdmin(ctx, authorizer, req.Principal.Type, req.Principal.ID) {
			return nil
		}
		// Owner-granting is elevated: a member-capable manager cannot invite an owner.
		if req.Action == auth.InviteCreate && req.Relation == "owner" {
			return fmt.Errorf("auth-cms: %s:%s may not invite an owner on %s:%s (owner grants are reserved to platform admins): %w",
				req.Principal.Type, req.Principal.ID, req.ResourceType, req.ResourceID, sdk.ErrForbidden)
		}
		// Otherwise the caller must hold manage_access on the target resource — for both
		// creating a non-owner invitation and listing a resource's invitations.
		res, err := authorizer.Check(ctx, authorization.CheckRequest{
			Principal:  authorization.PrincipalRef{Type: req.Principal.Type, ID: req.Principal.ID},
			Permission: manageAccessPerm,
			Resource:   authorization.Resource{Type: req.ResourceType, ID: req.ResourceID},
		})
		if err != nil {
			// Fail closed: an authorizer failure denies, never allows.
			return fmt.Errorf("auth-cms: invite authorization probe failed for %s:%s on %s:%s: %w",
				req.Principal.Type, req.Principal.ID, req.ResourceType, req.ResourceID, err)
		}
		if !res.Allowed {
			return fmt.Errorf("auth-cms: %s:%s lacks manage_access to %s invitations on %s:%s: %w",
				req.Principal.Type, req.Principal.ID, req.Action, req.ResourceType, req.ResourceID, sdk.ErrForbidden)
		}
		return nil
	}
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
