package authorization

import (
	"errors"
	"log/slog"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// Construction and per-kind sentinel errors. A misconfigured host fails at
// New; calling an unwired kind fails closed at the call site.
var (
	// ErrNoKindConfigured is returned by New when neither kind is wired
	// (both Repositories fields nil) — an authorization pocket that does nothing.
	ErrNoKindConfigured = errors.New("authorization: no kind configured (Repositories.Relationships and Repositories.Roles are both nil)")

	// ErrModelRequired is returned by New for a partial relationship-kind
	// wiring: Repositories.Relationships is set without WithRelationshipModel, or
	// WithRelationshipModel is set without the repository. The relationship kind
	// needs both.
	ErrModelRequired = errors.New("authorization: Repositories.Relationships and WithRelationshipModel must be wired together (both or neither)")

	// ErrRoleModelWithoutRoles is returned by New when WithRoleModel is
	// set without Repositories.Roles. The asymmetry with ErrModelRequired is
	// deliberate: a roles repository with NO model is the valid opaque posture,
	// but a model with no repository could never decide anything.
	ErrRoleModelWithoutRoles = errors.New("authorization: WithRoleModel requires Repositories.Roles (a role model with no roles kind decides nothing)")
)

// Repositories is the set of outbound ports the pocket needs. Each kind is
// nil-safe: a nil field turns that kind OFF structurally.
type Repositories struct {
	// Relationships backs the ReBAC kind; nil = the relationship kind is off.
	Relationships relationships.Storer
	// Roles backs the roles kind; nil = the roles kind is off.
	Roles roles.Storer

	// Mutations backs the optional high-integrity guarded write path. A
	// nil field leaves baseline RelationshipWriter operations fully available.
	// It is independent of the read/check ports above.
	Mutations mutations.MutationRepository

	// Audit reads committed change history. The host controls access and retention.
	Audit audit.Reader
}

type config struct {
	Logger                    *slog.Logger
	RelationshipModel         relationships.Schema
	Limits                    authmodel.EvaluationLimits
	RoleModel                 authmodel.RoleModel
	Guard                     mutations.MutationGuard
	RoleRoutesGate            web.Middleware
	RoleRouteAssignmentPolicy authorizationhttp.RoleRouteAssignmentPolicy
	ListStrategy              list.Strategy
}
