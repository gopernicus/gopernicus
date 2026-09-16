package main

import (
	"context"
	"fmt"
	"sync"

	access "github.com/gopernicus/gopernicus/examples/auth-cms/pockets/access/inbound"

	authenticationhttp "github.com/gopernicus/gopernicus/pockets/authentication/inbound/http"

	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	audit "github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	decisions "github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	mutations "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	relationships "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

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
	writer *relationships.RelationshipWriter
	reader *relationships.Service
	exists resourceExistsFn
}

var _ invitations.Granter = relationshipGranter{}

// Grant records that (in.SubjectType, in.SubjectID) holds in.Relation on the resource as
// a relationship tuple. Two host duties frame the write:
//
// Resource existence (D2). The host — not authentication — owns resource lifecycle, so it
// validates the target still exists BEFORE writing a tuple. Accepting an invitation against
// a since-deleted resource fails loudly (sdk.ErrNotFound) and writes nothing; nil could
// otherwise grant access to a resource that no longer exists.
//
// The writer validates explicit subject-shape constraints and adds the exact fact
// idempotently. Accepting member preserves any existing owner fact on this resource.
func (g relationshipGranter) Grant(ctx context.Context, in invitations.GrantInput) error {
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
	if err := g.writer.CreateRelationships(ctx, []relationships.CreateRelationship{{
		ResourceType: in.ResourceType,
		ResourceID:   in.ResourceID,
		Relation:     in.Relation,
		SubjectType:  in.SubjectType,
		SubjectID:    in.SubjectID,
	}}); err != nil {
		return err
	}
	return nil
}

type integrityRelationshipGranter struct {
	system *mutations.Service
	exists resourceExistsFn
}

var _ invitations.Granter = integrityRelationshipGranter{}

func (g integrityRelationshipGranter) Grant(ctx context.Context, in invitations.GrantInput) error {
	if g.exists == nil {
		return fmt.Errorf("auth-cms: integrityRelationshipGranter resource-existence seam is not wired")
	}
	exists, err := g.exists(ctx, in.ResourceType, in.ResourceID)
	if err != nil {
		return fmt.Errorf("auth-cms: resource existence probe for %s:%s failed: %w", in.ResourceType, in.ResourceID, err)
	}
	if !exists {
		return fmt.Errorf("auth-cms: host resource %s:%s no longer exists; not granting %q to %s:%s: %w",
			in.ResourceType, in.ResourceID, in.Relation, in.SubjectType, in.SubjectID, sdk.ErrNotFound)
	}

	ctx = audit.WithSource(ctx, audit.Source{System: "invitation-acceptance"})
	_, err = g.system.GrantRelationship(ctx, mutations.GrantRelationshipCommand{
		ResourceType: in.ResourceType, ResourceID: in.ResourceID, Relation: in.Relation,
		Subject: relationships.SubjectRef{Type: in.SubjectType, ID: in.SubjectID},
	})
	return err
}

// hostInviteCheck is the relation-aware host authorization policy the authentication
// pocket calls from its parsed create/list invitation handlers (authenticationConfig.InviteCheck,
// design D3). It is REQUIRED whenever a Granter enables invitations, and it runs AFTER the
// pocket has resolved the caller principal and parsed the exact requested relation — data a
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
func hostInviteCheck(authorizer *decisions.Service) authenticationhttp.InviteCheck {
	return access.New(authorizer).Invite
}

func isPlatformAdmin(ctx context.Context, authorizer *decisions.Service, subjectType, subjectID string) bool {
	ok, err := access.New(authorizer).PlatformAdmin(ctx, sdk.Principal{Type: subjectType, ID: subjectID})
	return err == nil && ok
}

// requireMembership accepts a platform administrator or a demo project member
// within one authorization snapshot and shared evaluation budget.
func requireMembership(gates *authorizationhttp.Adapter) web.Middleware {
	return gates.Require(authorizationhttp.Any(
		authorizationhttp.Can("admin", authorizationhttp.Fixed(platformResourceType, platformResourceID)),
		authorizationhttp.Can(demoPermission, authorizationhttp.Fixed(demoResourceType, demoResourceID)),
	))
}
