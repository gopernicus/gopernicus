package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync/atomic"

	access "github.com/gopernicus/gopernicus/examples/auth-cms/pockets/access/inbound"
	"github.com/gopernicus/gopernicus/examples/auth-cms/pockets/audit/outbound/securityevents"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"

	authorization "github.com/gopernicus/gopernicus/pockets/authorization"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	audit "github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	model "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	mutations "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	relationships "github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	roles "github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	authzmem "github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Host-owned authorization policy vocabulary.
const (
	platformResourceType = "platform"
	platformResourceID   = "main"

	manageAccessPerm = "manage_access"
)

// The erase cleanup's audit reason and page size. The reason names the
// request, not an outcome the erase may not reach.
const (
	eraseAuditReason = "user_erase"
	erasePageSize    = 100
)

func authorizationModel() decisions.Model {
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

// authorizationIntegrityPolicy is the host's integrity invariant: the ratified owner minimum
// (DefaultIntegrityPolicy's owner, min-1 direct anchor) applied to the ownable `project`
// resource type. It deliberately does NOT extend to `platform` — the honest documented
// reason for narrowing the default: `platform` is a flat admin-list type with no `owner`
// relation, so an owner-minimum on it is nonsensical and would invariant-block the
// platform-admin data tuple. The last-owner protection that matters — project:demo
// keeping at least one direct owner after every ordinary command — runs at full default
// strength. This is the sanctioned "host narrows it to specific resource types" path,
// not a weakened posture: the empty IntegrityPolicy the pre-AZ3-4.1 demo wired (which
// disabled last-owner protection entirely to let member invitations precede an owner) is
// gone, replaced by a boot-time owner seed + this real invariant.
// mutations.DefaultIntegrityPolicyFor(model) derives the same per-type owner rules from a model.
func authorizationIntegrityPolicy() mutations.IntegrityPolicy {
	return mutations.IntegrityPolicy{
		Rules: []mutations.IntegrityRule{{ResourceType: demoResourceType, Relation: "owner", MinSubjects: 1}},
	}
}

// newAuthorization builds the authorization pocket over the in-memory store,
// recording audit and exposing its reader, so production mode's audit
// requirement is met. A recording store needs an audit.Source on every change:
// the bundled role routes stamp the principal, and this host's seeds and
// granters stamp themselves as systems. With securityEvents set, the decision
// bridge records denials, refused role writes and audit-listing reads into the
// authentication pocket's security-event rail through that recorder; nil
// records none. The role routes and GET /authorization/audit share the one
// gate; a role write naming the caller itself is refused before host policy.
func newAuthorization(roleRoutesGate web.Middleware, securityEvents securityevents.Recorder, mode environment.Mode, logger *slog.Logger) (authorization.Components, error) {
	store := authzmem.New(authzmem.WithIntegrityPolicy(authorizationIntegrityPolicy()), authzmem.WithAudit())
	var observer decisions.DecisionObserver
	var auditReads authorizationhttp.AuditReadObserver
	if securityEvents != nil {
		bridge := securityevents.NewBridge(securityEvents)
		observer, auditReads = bridge.Observer(), bridge.AuditReads()
	}
	components, err := authorization.New(
		authorization.Repositories{
			Tuples:          store.Tuples(),
			Mutations:       store.Mutations(),
			Operations:      store.Operations(),
			OperationExpiry: store.OperationExpiry(),
			Audit:           store.Audit(),
		},
		authorization.WithLogger(logger),
		authorization.WithModel(authorizationModel()),
		authorization.WithRuntimeMode(mode),
		authorization.WithDecisionObserver(observer),
	)
	if err != nil {
		return authorization.Components{}, err
	}
	if roleRoutesGate != nil {
		components.HTTP, err = authorizationhttp.New(
			authorizationhttp.Services{Decisions: components.Decisions, Roles: components.Roles, Mutations: components.Mutations, Audit: components.Audit},
			authorizationhttp.WithRoleRoutes(authorizationhttp.RoleRoutes{
				Gate:        roleRoutesGate,
				WritePolicy: authorizationhttp.AllOf(authorizationhttp.RefuseSelfTarget, access.New(components.Decisions).RoleWrite),
				AuditGate:   roleRoutesGate,
			}),
			authorizationhttp.WithDecisionObserver(observer), authorizationhttp.WithAuditReadObserver(auditReads), authorizationhttp.WithLogger(logger))
	}
	return components, err
}

// beforeErase is the authentication pocket's BeforeErase hook: it revokes every
// role assignment and relationship of user:<id>, in every scope, through the
// audited unassign command with the administrator as actor, before the erase
// transaction runs. It re-reads the first page until it is empty, attempts the
// integrity-protected relations of each page first (so a last-owner refusal
// fires before lesser roles go), stops at the first error and returns it
// unwrapped so its kind maps (the last owner is a 409, nothing erased). Revoking
// nothing is success, so a re-run completes a half-finished erase.
func beforeErase(assignments *roles.Service, unassign *mutations.Service, policy mutations.IntegrityPolicy) func(context.Context, sdk.Principal, string) error {
	return func(ctx context.Context, actor sdk.Principal, userID string) error {
		ctx = audit.WithSource(ctx, audit.Source{ActorType: actor.Type, ActorID: actor.ID, Reason: eraseAuditReason})
		subject := model.PrincipalRef{Type: sdk.PrincipalTypeUser, ID: userID}
		for {
			page, err := assignments.ListRoleAssignmentsBySubject(ctx, subject, list.Request{Limit: erasePageSize})
			if err != nil {
				return err
			}
			if len(page.Items) == 0 {
				return nil
			}
			slices.SortStableFunc(page.Items, func(a, b roles.Assignment) int {
				return guardRank(policy, a) - guardRank(policy, b)
			})
			removed := 0
			for _, a := range page.Items {
				result, err := unassign.UnassignRole(ctx, mutations.UnassignRoleCommand{Subject: subject, Role: a.Role, Scope: a.Scope})
				if err != nil {
					return err
				}
				if result.Outcome == mutations.OutcomeApplied {
					removed++
				}
			}
			if removed == 0 {
				return fmt.Errorf("erase cleanup: a page of role assignments did not shrink: %w", sdk.ErrUnavailable)
			}
		}
	}
}

// guardRank orders an assignment an integrity rule protects before the rest.
func guardRank(policy mutations.IntegrityPolicy, a roles.Assignment) int {
	if a.Scope.Kind != tuples.ResourceScope {
		return 1
	}
	for _, rule := range policy.Rules {
		if rule.Relation == a.Role && (rule.ResourceType == "" || rule.ResourceType == a.Scope.Type) {
			return 0
		}
	}
	return 1
}

// roleAdministrationGate composes the D6 chain the bundled /authorization/* routes
// run behind. The pocket owns no credential and adds NO middleware beneath the
// gate, so this closure is the ENTIRE stack:
//
//  1. authenticate — the auth pocket's live human-session middleware, which stashes
//     the principal with sdk.WithPrincipal. Without it the bundled writes
//     answer 401 rather than fabricate a zero Actor.
//  2. browserSafe — the auth pocket's RequireBrowserSafeMutation: a cookie POST
//     needs an allowlisted Origin and the X-CSRF-Token double-submit; GETs and
//     bearer-only calls pass.
//  3. authorize — the platform-admin coordinate already declared in authorizationModel
//     (platform/admin on platform:main), the same one MachineRoutesGate names.
func roleAdministrationGate(authenticate, browserSafe, authorize web.Middleware) web.Middleware {
	return func(next http.Handler) http.Handler {
		return authenticate(browserSafe(authorize(next)))
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

// deferredRecorder is the recorder the decision bridge is built over before the
// authentication pocket exists — the same ordering seam as deferredMiddleware.
// run sets it to the pocket's ONE recorder, Components.SecurityEvents, right
// after auth.New and before the host serves; until then Record does nothing (a
// nil *securityevent.Recorder records nothing).
type deferredRecorder struct {
	recorder atomic.Pointer[securityevent.Recorder]
}

// set installs the authentication pocket's recorder.
func (d *deferredRecorder) set(r *securityevent.Recorder) { d.recorder.Store(r) }

// Record forwards to the installed recorder.
func (d *deferredRecorder) Record(ctx context.Context, in securityevent.Input) {
	d.recorder.Load().Record(ctx, in)
}

// seedOwnerSubject is the boot-seeded demo owner/platform-admin principal. This proof
// host seeds no real user (registration is part of the proof flow), so the integrity
// minimum is established for a documented synthetic principal at boot rather than by a
// browser-driven "become owner" route. The ROLE-assignment half of that deferral is
// now closed: the pocket's bundled /authorization/roles* surface is mounted behind
// roleAdministrationGate, so a platform admin assigns and unassigns roles over HTTP.
// Establishing the FIRST owner stays trusted and boot-time — it cannot yet prove it
// manages the resource.
var seedOwnerSubject = relationships.SubjectRef{Type: "user", ID: "demo-owner"}

func seedAuthorization(ctx context.Context, system *mutations.Service) error {
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
