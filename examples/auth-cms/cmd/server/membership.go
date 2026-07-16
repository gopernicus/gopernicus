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

// relationshipGranter is the ordinary collaboration posture: it adapts the
// trusted application-side RelationshipWriter to auth.Granter. The invitation's
// OperationID is intentionally unused because this state-convergent path needs no
// mutation identity or receipt. InviteCheck still decides who may invite; that
// detached host authorization decision is simply not transactionally coupled to
// the tuple write. The host also checks resource existence because authentication
// does not own resource lifecycle.
type relationshipGranter struct {
	writer *authorization.RelationshipWriter
	reader *authorization.Service
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
// The writer validates the tuple against the compiled schema. Its raw create is
// idempotent; afterward this adapter performs the Granter contract's detached
// exact-state check so a pre-existing different relation is a loud conflict, never
// an implicit upgrade/downgrade. A concurrent later write may of course win after
// that check: this is the ordinary application race posture chosen for project
// member invitations.
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

	if g.writer == nil || g.reader == nil {
		return fmt.Errorf("auth-cms: relationshipGranter authorization capabilities are not wired")
	}
	if err := g.writer.CreateRelationships(ctx, []authorization.CreateRelationship{{
		ResourceType: in.ResourceType,
		ResourceID:   in.ResourceID,
		Relation:     in.Relation,
		SubjectType:  in.SubjectType,
		SubjectID:    in.SubjectID,
	}}); err != nil {
		return err
	}
	targets, err := g.reader.GetRelationTargets(ctx, in.ResourceType, in.ResourceID, in.Relation)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if target.Type == in.SubjectType && target.ID == in.SubjectID && target.Relation == "" {
			return nil
		}
	}
	return fmt.Errorf("auth-cms: %s:%s already holds a different relation on %s:%s; grant of %q refused (no implicit replace): %w",
		in.SubjectType, in.SubjectID, in.ResourceType, in.ResourceID, in.Relation, sdk.ErrConflict)
}

// guardedRelationshipGranter is the opt-in high-integrity invitation posture.
// It consumes OperationID to derive durable mutation idempotency and maps guarded
// mutation receipts. A host can select this adapter for tenant/account owner or
// administrator invitations while ordinary resource sharing uses relationshipGranter.
type guardedRelationshipGranter struct {
	system *authorization.SystemMutator
	exists resourceExistsFn
}

var _ auth.Granter = guardedRelationshipGranter{}

func (g guardedRelationshipGranter) Grant(ctx context.Context, in auth.GrantInput) error {
	if g.exists == nil {
		return fmt.Errorf("auth-cms: guardedRelationshipGranter resource-existence seam is not wired")
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
		MutationID: mid, ResourceType: in.ResourceType, ResourceID: in.ResourceID, Relation: in.Relation,
		Subject: authorization.SubjectRef{Type: in.SubjectType, ID: in.SubjectID},
	})
	if err != nil {
		return err
	}
	switch receipt.Outcome {
	case authorization.OutcomeApplied, authorization.OutcomeNoChange:
		return nil
	case authorization.OutcomeSemanticConflict, authorization.OutcomeInvariantBlocked:
		return fmt.Errorf("auth-cms: guarded invitation grant returned %q: %w", receipt.Outcome, sdk.ErrConflict)
	default:
		return fmt.Errorf("auth-cms: guarded invitation grant returned non-success outcome %q: %w", receipt.Outcome, sdk.ErrConflict)
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
