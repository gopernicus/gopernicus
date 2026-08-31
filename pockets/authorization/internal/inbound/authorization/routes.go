// Package authorization is the authorization pocket's JSON transport for the
// BUNDLED role-administration surface: the request/response DTOs, the handlers
// over a narrow role-administration port, and the route table under
// /authorization/*. It is mounted only through pocket.RouteRegistrar, and only
// when the host named an authorization.Config.RoleRoutesGate.
//
// The package is JSON-only (the pocket stays view-free, FS1) and writes every
// response through sdk/foundation/web (FS9). It imports the pocket's domain
// packages and sdk — never the pocket's root package, which imports THIS one to
// mount the routes.
package authorization

import (
	"context"
	"net/http"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
	"github.com/gopernicus/gopernicus/pockets/authorization/domain/role"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
	"github.com/gopernicus/gopernicus/sdk/pocket"
)

// The bundled route paths. /authorization/* is the namespace the pocket
// reserves; /auth/* belongs to the authentication pocket and is never used
// here. The two writes are distinct paths (never one path dispatching on a
// body verb) and each listing is its own path (never one path dispatching on
// which query parameters are present), so a host gate can switch on method and
// path without re-parsing the query.
const (
	pathAssignRole      = "/authorization/roles"
	pathUnassignRole    = "/authorization/roles/unassign"
	pathRolesBySubject  = "/authorization/roles/by-subject"
	pathRolesByResource = "/authorization/roles/by-resource"
	pathRolesEffective  = "/authorization/roles/effective"
)

// AssignRoleRequest is the transport's actor-carrying assign command. The actor
// is a plain (Type, ID) pair the handler read off the request context with
// identity.FromContext; the pocket root's adapter — the single conversion site —
// turns it into an authorization.Actor and the rest of the fields into an
// authorization.AssignRoleCommand.
//
// MutationID empty means "the server mints one" (each request distinct); a
// non-empty value is the client's own idempotency key and is validated by the
// adapter. ExpectedRevision is the optional compare-and-set anchor.
type AssignRoleRequest struct {
	ActorType        string
	ActorID          string
	MutationID       string
	SubjectType      string
	SubjectID        string
	Role             string
	ResourceType     string
	ResourceID       string
	ExpectedRevision *uint64
}

// UnassignRoleRequest is the symmetric unassign command. It carries the same
// shape as AssignRoleRequest on purpose — the two domain commands are
// field-identical — but stays a distinct type so the port cannot confuse them.
type UnassignRoleRequest struct {
	ActorType        string
	ActorID          string
	MutationID       string
	SubjectType      string
	SubjectID        string
	Role             string
	ResourceType     string
	ResourceID       string
	ExpectedRevision *uint64
}

// RoleAdminService is the narrow port the bundled handlers drive. It is declared
// HERE, over types this package can import (domain/mutation, domain/role, sdk
// crud), because the pocket root owns the Actor/command vocabulary and imports
// this package to mount — so the dependency can only point one way. The root
// ships the single adapter that satisfies it.
//
// UnassignRole's bool is same_role_grant_remains: after a SCOPED unassign, true
// iff a global grant of the same exact role still satisfies the scoped HasRole
// fallback. It is false for a global unassign and on replay.
type RoleAdminService interface {
	AssignRole(ctx context.Context, req AssignRoleRequest) (*mutation.Receipt, error)
	UnassignRole(ctx context.Context, req UnassignRoleRequest) (*mutation.Receipt, bool, error)
	ListBySubject(ctx context.Context, subjectType, subjectID string, req crud.ListRequest) (crud.Page[role.Assignment], error)
	ListByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.Assignment], error)
	ListEffectiveByResource(ctx context.Context, resourceType, resourceID string, req crud.ListRequest) (crud.Page[role.EffectiveGrant], error)
}

// Deps is the struct input Mount takes. authorization.Service.Register is its
// only production caller.
type Deps struct {
	// Service is the role-administration port every handler delegates to.
	Service RoleAdminService
	// Gate is the host's complete middleware chain for these routes
	// (authorization.Config.RoleRoutesGate): authentication that stashes the
	// principal, any browser-CSRF defense, and the authorization decision. This
	// package adds NO middleware of its own — the pocket owns no credential — so
	// the gate is the entire stack.
	Gate web.Middleware
	// ListStrategy is the DefaultStrategy the listings pass to crud.ParseListQuery
	// (authorization.Config.ListStrategy). "" resolves to crud.StrategyCursor.
	ListStrategy crud.Strategy
	// RespondError writes a DOMAIN error as JSON. Register fills it with the root
	// package's RespondError, so a bundled body carries the pocket's STABLE machine
	// code (stale_revision, mutation_payload_mismatch, …) rather than the generic
	// sdk-kind mapping — the codes.go seam, reachable from here without this
	// package importing the root. Nil falls back to web.RespondJSONDomainError, so
	// a test mounting the transport alone still gets correct statuses.
	//
	// It carries DOMAIN errors only. Transport refusals the handlers author
	// themselves (401, the named 400s, 415, 413) already name their own codes and
	// are written directly through web.
	RespondError func(http.ResponseWriter, error)
}

// handlers holds what the route handlers delegate to.
type handlers struct {
	svc          RoleAdminService
	listStrategy crud.Strategy
	respondErr   func(http.ResponseWriter, error)
}

// respondError writes a domain error through the host-supplied mapper, falling
// back to the sdk-kind mapping when Deps carried none. It is the ONE place the
// bundled surface answers a domain error, so a body's machine code cannot drift
// per handler.
func (h *handlers) respondError(w http.ResponseWriter, err error) {
	if h.respondErr != nil {
		h.respondErr(w, err)
		return
	}
	web.RespondJSONDomainError(w, err)
}

// Mount registers the five bundled role-administration routes, each carrying
// exactly the host gate. Register calls it only when the gate is non-nil, so
// there is no ungated mount path.
func Mount(r pocket.RouteRegistrar, deps Deps) {
	h := &handlers{svc: deps.Service, listStrategy: deps.ListStrategy, respondErr: deps.RespondError}
	r.Handle("POST", pathAssignRole, h.assignRole, deps.Gate)
	r.Handle("POST", pathUnassignRole, h.unassignRole, deps.Gate)
	r.Handle("GET", pathRolesBySubject, h.listBySubject, deps.Gate)
	r.Handle("GET", pathRolesByResource, h.listByResource, deps.Gate)
	r.Handle("GET", pathRolesEffective, h.listEffectiveByResource, deps.Gate)
}
