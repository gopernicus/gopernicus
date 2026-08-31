package main

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	authzmem "github.com/gopernicus/gopernicus/pockets/authorization/memstore"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// Host-owned authorization policy vocabulary.
const (
	// platformResourceType / platformResourceID name the `platform` resource whose
	// `admin` relation is the platform-admin DATA tuple (platform:main#admin) — data,
	// never Config. The host composes the platform-admin recipe itself (isPlatformAdmin
	// on the read side, hostMutationGuard on the write side); the engine grants no bypass.
	platformResourceType = "platform"
	platformResourceID   = "main"

	// manageAccessPerm is the schema-declared permission that means "may manage this
	// resource's authorization data" — Direct(owner). The host MutationGuard authorizes
	// actor-facing writes against it (reading its single backing relation, manageRelation,
	// through the DecisionView; see guard.go).
	manageAccessPerm = "manage_access"
)

// authzSchema builds the host's ReBAC schema (AZ3-4.1): the ownable `project` type
// (owner/member relations; `view` = AnyOf(owner, member); the new `manage_access` =
// Direct(owner) permission the host MutationGuard enforces) and the flat `platform`
// admin-list type backing the platform-admin data tuple.
func authzSchema() authorization.Schema {
	return authorization.NewSchema([]authorization.ResourceSchema{
		{Name: demoResourceType, Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"owner":  {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
				"member": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]authorization.PermissionRule{
				demoPermission:   authorization.AnyOf(authorization.Direct("owner"), authorization.Direct("member")),
				manageAccessPerm: authorization.AnyOf(authorization.Direct("owner")),
			},
		}},
		{Name: platformResourceType, Def: authorization.ResourceTypeDef{
			Relations: map[string]authorization.RelationDef{
				"admin": {AllowedSubjects: []authorization.SubjectTypeRef{{Type: "user"}}},
			},
			// The `admin` permission makes platform-admin an ordinary schema-declared
			// check the host runs first in its own Check closure (see requireMembership /
			// demoMyProjects). The engine no longer bypasses on this tuple.
			Permissions: map[string]authorization.PermissionRule{
				"admin": authorization.AnyOf(authorization.Direct("admin")),
			},
		}},
	})
}

// authzRoleModel builds the host's ROLES-kind permission model — the second
// model-bearing kind this host wires. On the SAME `project` resource type the
// relationship schema owns, the opaque `auditor` role grants the `audit`
// permission (the /demo/audit gate).
//
// The two models share the resource TYPE but never a (type, permission) PAIR:
// `project/view` and `project/manage_access` stay relationship-owned, `project/audit`
// is role-owned. That split is exactly what NewService's pair-ownership rule permits
// — a pair declared by both models would fail construction with ErrModelConflict —
// and it is what makes each decision dispatch to exactly one model, so the two
// recipes stay demonstrable side by side without entangling.
func authzRoleModel() authorization.RoleModel {
	return authorization.RoleModel{
		ResourceTypes: map[string]authorization.RoleTypeDef{
			demoResourceType: {
				Roles:       []string{demoRole},
				Permissions: map[string][]string{demoAuditPermission: {demoRole}},
			},
		},
	}
}

// authzGuardianPolicy is the host's guardian invariant: the ratified owner minimum
// (DefaultGuardianPolicy's owner, min-1 direct anchor) applied to the ownable `project`
// resource type. It deliberately does NOT extend to `platform` — the honest documented
// reason for narrowing the default: `platform` is a flat admin-list type with no `owner`
// relation, so an owner-minimum on it is nonsensical and would invariant-block the
// platform-admin data tuple. The last-owner protection that matters — project:demo
// keeping at least one direct owner after every ordinary command — runs at full default
// strength. This is the sanctioned "host narrows it to specific resource types" path,
// not a weakened posture: the empty GuardianPolicy the pre-AZ3-4.1 demo wired (which
// disabled last-owner protection entirely to let member invitations precede an owner) is
// gone, replaced by a boot-time owner seed + this real invariant.
func authzGuardianPolicy() authorization.GuardianPolicy {
	return authorization.GuardianPolicy{
		Rules: []authorization.GuardianRule{{ResourceType: demoResourceType, Relation: "owner", MinAnchors: 1}},
	}
}

// newAuthorization composes the guarded authorization pocket this host runs — the
// testable composition seam run() and the guarded-composition tests share (the
// buildAuthConfig precedent). BOTH kinds ride one shared-state memstore bundle (so the
// trusted SystemMutator writes and the read side observe the same state), and BOTH
// bear a model — the relationship Schema and the RoleModel — so the ONE decision
// surface dispatches each (type, permission) pair to its owning model. It runs under
// the project-scoped guardian minimum, with the host MutationGuard wired into Config.Guard.
// The returned Components hold the actor-facing Service and the separately held trusted
// SystemMutator apart, by construction.
func newAuthorization(roleRoutesGate web.Middleware) (authorization.Components, error) {
	store := authzmem.New(authzmem.WithGuardianPolicy(authzGuardianPolicy()))
	return authorization.NewService(authorization.Repositories{
		Relationships: store.Relationships(),
		Roles:         store.Roles(),
		Mutations:     store.Mutations(),
	}, authorization.Config{
		RelationshipModel: authzSchema(),
		RoleModel:         authzRoleModel(),
		Guard:             hostMutationGuard{},
		// The bundled role-administration routes mount only because this host names
		// a gate; nil (which every composition test passes) is the deny-by-absence
		// posture and registers nothing.
		RoleRoutesGate: roleRoutesGate,
	})
}

// roleAdministrationGate composes the D6 chain the bundled /authorization/* routes
// run behind. The pocket owns no credential and adds NO middleware beneath the
// gate, so this closure is the ENTIRE stack:
//
//  1. authenticate — the auth pocket's live human-session middleware, which stashes
//     the principal with identity.WithPrincipal. Without it the bundled writes
//     answer 401 rather than fabricate a zero Actor.
//  2. authorize — the platform-admin coordinate already declared in authzSchema
//     (platform/admin on platform:main), the same one MachineRoutesGate names.
//
// ⚠ The third layer the pocket's README mandates for a COOKIE-credential host — a
// browser-origin/CSRF defense on these state-changing POSTs — is deliberately NOT
// composed here: the authentication pocket does not export its browser-safe
// middleware, and whether the gate should be a slice so the pocket can help is an
// OPEN QUESTION for the owner (authorization-role-routes, open question 1). A
// production cookie host must add it.
func roleAdministrationGate(authenticate, authorize web.Middleware) web.Middleware {
	return func(next http.Handler) http.Handler {
		return authenticate(authorize(next))
	}
}

// deferredMiddleware carries a middleware that cannot exist yet at the moment it
// must be NAMED. The authorization pocket is constructed and registered before the
// auth pocket exists (the authorizer is itself an input to the auth config), but
// the role-routes gate needs BOTH — so the host passes this indirection at
// construction and assigns the real chain once, after both services are built and
// before the server serves. The pointer is read PER REQUEST, so the ordering costs
// one atomic load rather than a construction reshuffle.
type deferredMiddleware struct {
	chain atomic.Pointer[web.Middleware]
}

// errRoleRoutesGateNotInstalled is the boot failure for a role-routes gate that
// was never assigned. It is deliberately a BOOT error rather than only the
// middleware's request-time 500: an unassigned gate is a wiring fault the
// operator must fix, so it fails construction like the pocket's own
// ErrRoleRoutesGateWithoutGuard rather than surfacing as production 500s.
var errRoleRoutesGateNotInstalled = errors.New("auth-cms: the role-administration gate was never installed; /authorization/roles* would answer 500")

// set installs the real chain. It must be called before the host serves.
func (d *deferredMiddleware) set(m web.Middleware) { d.chain.Store(&m) }

// installed reports whether set has run. run() asserts it right after assignment
// so a reordering refactor fails at boot instead of at the first request.
func (d *deferredMiddleware) installed() bool { return d.chain.Load() != nil }

// middleware is the web.Middleware the host hands to Config.RoleRoutesGate. An
// unassigned chain fails CLOSED with a 500 rather than admitting the request: a
// route that outran its gate is a host bug, never an open door.
func (d *deferredMiddleware) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chain := d.chain.Load()
		if chain == nil {
			web.RespondJSONError(w, web.ErrInternal("role-administration gate is not wired"))
			return
		}
		(*chain)(next).ServeHTTP(w, r)
	})
}

// seedOwnerSubject is the boot-seeded demo owner/platform-admin principal. This proof
// host seeds no real user (registration is part of the proof flow), so the guardian
// minimum is established for a documented synthetic principal at boot rather than by a
// browser-driven "become owner" route. The ROLE-assignment half of that deferral is
// now closed: the pocket's bundled /authorization/roles* surface is mounted behind
// roleAdministrationGate, so a platform admin assigns and unassigns roles over HTTP.
// Establishing the FIRST owner stays trusted and boot-time — it cannot yet prove it
// manages the resource.
var seedOwnerSubject = authorization.SubjectRef{Type: "user", ID: "demo-owner"}

// seedAuthorization establishes the ownable scope through the TRUSTED SystemMutator
// before the host serves: project:demo#owner (the guardian minimum, granted FIRST so a
// later member invitation is not member-first-blocked) and the platform:main#admin data
// tuple. Each MutationID is DERIVED from its tuple, so a restart re-seed dedups against
// the stored receipt — no duplicate revision bump. It is the trusted bootstrap the
// retired POST /demo/admin/bootstrap route used to perform per-request; establishing the
// first owner is inherently trusted (it cannot yet prove it manages the resource).
func seedAuthorization(ctx context.Context, system *authorization.SystemMutator) error {
	grants := []authorization.GrantRelationshipCommand{
		{ResourceType: demoResourceType, ResourceID: demoResourceID, Relation: "owner", Subject: seedOwnerSubject},
		{ResourceType: platformResourceType, ResourceID: platformResourceID, Relation: "admin", Subject: seedOwnerSubject},
	}
	for _, g := range grants {
		g.MutationID = authorization.DeriveMutationID("auth-cms/bootstrap-grant",
			g.ResourceType, g.ResourceID, g.Relation, g.Subject.Type, g.Subject.ID)
		if _, err := system.GrantRelationship(ctx, g); err != nil {
			return err
		}
	}
	// The ROLES-kind seed, on the same trusted seam and the same derived-MutationID
	// replay rule: the demo owner also holds `auditor` on project:demo, so the
	// role-model gate on /demo/audit is answerable at boot. The (project, auditor)
	// pair must be declared by authzRoleModel — with a model wired, an undeclared
	// pair is refused with ErrInvalidRoleModel rather than stored as a silent
	// no-grant.
	if _, err := system.AssignRole(ctx, authorization.AssignRoleCommand{
		MutationID: authorization.DeriveMutationID("auth-cms/bootstrap-role",
			demoResourceType, demoResourceID, demoRole, seedOwnerSubject.Type, seedOwnerSubject.ID),
		Subject:      authorization.PrincipalRef{Type: seedOwnerSubject.Type, ID: seedOwnerSubject.ID},
		Role:         demoRole,
		ResourceType: demoResourceType,
		ResourceID:   demoResourceID,
	}); err != nil {
		return err
	}
	return nil
}
