package authorization

import (
	"fmt"
	"log/slog"
	"reflect"

	"github.com/gopernicus/gopernicus/pockets"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/sdk"
)

// Components contains independently usable decision, read and data-write services.
// Inbound access points authorize calls before invoking data writers.
type Components struct {
	TupleCache         *tuplecache.TupleCache
	Decisions          *decisions.Service
	Relationships      *relationships.Service
	Roles              *roles.Service
	Mutations          *mutations.Service
	HTTP               *authorizationhttp.Adapter
	RelationshipWriter *relationships.RelationshipWriter
	RoleWriter         *roles.Writer
	log                *slog.Logger
}

// New validates host wiring and assembles the public services.
func New(repos Repositories, opts ...Option) (Components, error) {
	var cfg config
	for _, opt := range opts {
		if opt == nil {
			return Components{}, fmt.Errorf("authorization: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	for _, dep := range []struct {
		name  string
		value any
	}{{"Repositories.Tuples", repos.Tuples}, {"Repositories.Mutations", repos.Mutations}, {"Repositories.Audit", repos.Audit}, {"Repositories.TupleSource", repos.TupleSource}, {"WithTupleCache", cfg.TupleBackend}} {
		if isTypedNil(dep.value) {
			return Components{}, fmt.Errorf("authorization: %s is typed nil: %w", dep.name, sdk.ErrInvalidInput)
		}
	}
	if repos.Tuples == nil {
		return Components{}, ErrNoKindConfigured
	}
	if cfg.RoleRoutesGate != nil && repos.Mutations == nil {
		return Components{}, authorizationhttp.ErrRoleRoutesWithoutMutations
	}
	if err := authorizationhttp.ValidateListStrategy(cfg.ListStrategy); err != nil {
		return Components{}, err
	}
	comps := Components{log: cfg.Logger}
	if comps.log == nil {
		comps.log = slog.Default()
	}
	decisionOptions := []decisions.Option{decisions.WithLogger(comps.log), decisions.WithLimits(cfg.Limits), decisions.WithDiagnosticObserver(cfg.DiagnosticObserver), decisions.WithTupleCache(cfg.TupleBackend, repos.TupleSource, cfg.TuplePolicy)}
	if cfg.ModelOption != nil {
		decisionOptions = append(decisionOptions, cfg.ModelOption)
	}
	var err error
	comps.Decisions, err = decisions.NewService(repos.Tuples, decisionOptions...)
	if err != nil {
		return Components{}, err
	}
	comps.TupleCache = comps.Decisions.TupleCache()
	roleOptions := []roles.Option{}
	if comps.TupleCache != nil {
		roleOptions = append(roleOptions, roles.WithSnapshots(comps.TupleCache))
	}
	comps.Roles, err = roles.NewService(repos.Tuples, roleOptions...)
	if err != nil {
		return Components{}, err
	}
	comps.RoleWriter, err = roles.NewWriter(repos.Tuples, roles.WithValidator(comps.Decisions.CompiledModel()))
	if err != nil {
		return Components{}, err
	}
	parts, err := relationships.NewService(repos.Tuples, relationships.WithValidator(comps.Decisions.CompiledModel()))
	if err != nil {
		return Components{}, err
	}
	comps.Relationships, comps.RelationshipWriter = parts.Service, parts.RelationshipWriter
	if repos.Mutations != nil {
		comps.Mutations, err = mutations.NewService(repos.Mutations, mutations.WithModel(comps.Decisions.CompiledModel()), mutations.WithLimits(cfg.Limits), mutations.WithLogger(comps.log))
		if err != nil {
			return Components{}, err
		}
	}
	var roleWriter authorizationhttp.RoleWriter
	if comps.Mutations != nil {
		roleWriter = comps.Mutations
	}

	var decision authorizationhttp.DecisionService
	if comps.Decisions != nil {
		decision = comps.Decisions
	}
	var roleReader authorizationhttp.RoleReader
	if comps.Roles != nil {
		roleReader = comps.Roles
	}
	comps.HTTP, err = authorizationhttp.New(
		authorizationhttp.Services{Decisions: decision, Roles: roleReader, Mutations: roleWriter},
		authorizationhttp.WithRoleRoutes(authorizationhttp.RoleRoutes{
			Gate:         cfg.RoleRoutesGate,
			WritePolicy:  cfg.RoleWritePolicy,
			ListStrategy: cfg.ListStrategy,
		}),
	)
	if err != nil {
		return Components{}, err
	}
	return comps, nil
}

// Register mounts only the configured bundled HTTP routes.
func (c Components) Register(m pockets.Mount) error {
	if c.HTTP == nil {
		return fmt.Errorf("authorization: components are not initialized: %w", sdk.ErrInvalidInput)
	}
	c.log.Info("registered authorization pocket", "relationships", c.Relationships != nil, "roles", c.Roles != nil, "model", c.Decisions.CompiledModel() != nil, "baseline_relationship_writes", c.RelationshipWriter != nil, "role_routes", c.HTTP.RoutesEnabled())
	return c.HTTP.Register(m.Router)
}
func isTypedNil(value any) bool {
	if value == nil {
		return false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
