package authorization

import (
	"errors"
	"log/slog"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

var ErrNoKindConfigured = errors.New("authorization: Repositories.Tuples is required")

// Repositories supplies one required tuple authority and optional capabilities.
type Repositories struct {
	// TupleSource supplies authoritative snapshots and committed tuple changes.
	TupleSource tuplecache.Source
	// Tuples is the single canonical authority used by roles, relationships and decisions.
	Tuples tuples.Storer

	// Mutations backs the optional high-integrity guarded write path. A
	// nil field leaves baseline RelationshipWriter operations fully available.
	// It is independent of the read/check ports above.
	Mutations mutations.MutationRepository

	// Audit reads committed change history. The host controls access and retention.
	Audit audit.Reader
}

type config struct {
	TupleBackend              tuplecache.Backend
	TuplePolicy               tuplecache.Policy
	Logger                    *slog.Logger
	ModelOption               decisions.Option
	DiagnosticObserver        decisions.DiagnosticObserver
	Limits                    authmodel.EvaluationLimits
	Guard                     mutations.MutationGuard
	RoleRoutesGate            web.Middleware
	RoleRouteAssignmentPolicy authorizationhttp.RoleRouteAssignmentPolicy
	ListStrategy              list.Strategy
}
