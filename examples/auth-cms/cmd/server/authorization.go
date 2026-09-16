package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	audit "github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	mutations "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	relationships "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	authzmem "github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
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
func authzSchema() decisions.Model {
	return decisions.NewSchema([]decisions.ResourceSchema{
		{Name: "document", Def: decisions.ResourceTypeDef{
			Relations: map[string]decisions.RelationDef{
				"viewer": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]decisions.Expression{
				"view": decisions.AnyOf(decisions.Direct("viewer")),
			},
		}},
		{Name: demoResourceType, Def: decisions.ResourceTypeDef{
			Relations: map[string]decisions.RelationDef{
				"owner":  {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
				"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]decisions.Expression{
				demoAuditPermission: decisions.RoleIn(demoRole),
				demoPermission:      decisions.AnyOf(decisions.Direct("owner"), decisions.Direct("member")),
				manageAccessPerm:    decisions.AnyOf(decisions.Direct("owner")),
			},
		}},
		{Name: platformResourceType, Def: decisions.ResourceTypeDef{
			Relations: map[string]decisions.RelationDef{
				"admin": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
			},
			// The `admin` permission makes platform-admin an ordinary schema-declared
			// check the host runs first in its own Check closure (see requireMembership /
			// demoMyProjects). The engine no longer bypasses on this tuple.
			Permissions: map[string]decisions.Expression{
				"admin": decisions.AnyOf(decisions.Direct("admin")),
			},
		}},
	})
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
func authzGuardianPolicy() mutations.GuardianPolicy {
	return mutations.GuardianPolicy{
		Rules: []mutations.GuardianRule{{ResourceType: demoResourceType, Relation: "owner", MinAnchors: 1}},
	}
}

// newAuthorization binds one canonical authority and one explicit permission model.
// Exact role predicates and graph predicates share the same decision snapshot.
// The actor-facing service and trusted SystemMutator are separate capabilities.
func newAuthorization(roleRoutesGate web.Middleware, logger *slog.Logger) (authorization.Components, error) {
	store := authzmem.New(authzmem.WithGuardianPolicy(authzGuardianPolicy()))
	return authorization.New(
		authorization.Repositories{
			Tuples:    store.Tuples(),
			Mutations: store.Mutations(),
		},
		authorization.WithLogger(logger),
		authorization.WithModel(authzSchema()),
		authorization.WithGuard(hostMutationGuard{}),
		// The gate enables bundled role administration; a nil gate leaves every
		// role route disabled, including in the headless composition tests.
		authorization.WithRoleRoutes(authorizationhttp.RoleRoutes{Gate: roleRoutesGate}),
	)
}

// roleAdministrationGate composes the D6 chain the bundled /authorization/* routes
// run behind. The pocket owns no credential and adds NO middleware beneath the
// gate, so this closure is the ENTIRE stack:
//
//  1. authenticate — the auth pocket's live human-session middleware, which stashes
//     the principal with sdk.WithPrincipal. Without it the bundled writes
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

// middleware is the web.Middleware the host passes in RoleRoutes.Gate. An
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
var seedOwnerSubject = relationships.SubjectRef{Type: "user", ID: "demo-owner"}

// seedAuthorization establishes the ownable scope through the TRUSTED SystemMutator
// before the host serves: project:demo#owner (the guardian minimum, granted FIRST so a
// later member invitation is not member-first-blocked) and the platform:main#admin data
// tuple. Repeated seeding leaves existing identical grants unchanged. It is the trusted bootstrap the
// retired POST /demo/admin/bootstrap route used to perform per-request; establishing the
// first owner is inherently trusted (it cannot yet prove it manages the resource).
func seedAuthorization(ctx context.Context, system *mutations.SystemMutator) error {
	ctx = audit.WithSource(ctx, audit.Source{System: "bootstrap"})
	grants := []mutations.GrantRelationshipCommand{
		{ResourceType: demoResourceType, ResourceID: demoResourceID, Relation: "owner", Subject: seedOwnerSubject},
		{ResourceType: platformResourceType, ResourceID: platformResourceID, Relation: "admin", Subject: seedOwnerSubject},
	}
	for _, g := range grants {

		if _, err := system.GrantRelationship(ctx, g); err != nil {
			return err
		}
	}
	// The same canonical authority stores this scoped auditor fact. The permission
	// model explicitly checks it; global auditor facts do not satisfy this policy.
	if _, err := system.AssignRole(ctx, mutations.AssignRoleCommand{

		Subject: model.PrincipalRef{Type: seedOwnerSubject.Type, ID: seedOwnerSubject.ID},
		Role:    demoRole,
		Scope:   tuples.On(demoResourceType, demoResourceID),
	}); err != nil {
		return err
	}
	return nil
}
