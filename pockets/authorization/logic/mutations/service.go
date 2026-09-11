package mutations

import (
	"fmt"
	"log/slog"
	"reflect"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
)

var ErrMutationsNotConfigured = fmt.Errorf("authorization: actor-facing mutations are not configured (no MutationGuard): %w", sdk.ErrInvalidInput)

// Services binds mutations to the host's model-bearing relationship and role
// services. Each kind may be absent. Services are borrowed and remain immutable.
type Services struct {
	Relationships *relationships.Service
	Roles         *roles.Service
}

type config struct {
	Services
	RoleModel *authmodel.CompiledRoleModel
	Guard     MutationGuard
	Limits    authmodel.EvaluationLimits
	Logger    *slog.Logger
}

// Service owns actor-facing role and relationship writes inside one atomic guard.
// It exposes no raw repository or trusted writer.
type Service struct {
	relationships *relationships.Service
	roles         *roles.Service
	roleModel     *authmodel.CompiledRoleModel
	guard         MutationGuard
	mutations     MutationRepository
	maxBatchSize  int
	limits        authmodel.EvaluationLimits
	log           *slog.Logger
}
type Components struct {
	Service       *Service
	SystemMutator *SystemMutator
}

// NewService validates dependencies and returns separately held actor/trusted capabilities.
func NewService(repo MutationRepository, services Services, opts ...Option) (Components, error) {
	cfg := config{Services: services}
	for _, opt := range opts {
		if opt == nil {
			return Components{}, fmt.Errorf("authorization mutations: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if typedNil(repo) || typedNil(cfg.Guard) {
		return Components{}, fmt.Errorf("authorization: typed nil mutation dependency: %w", sdk.ErrInvalidInput)
	}
	if cfg.Guard != nil && repo == nil {
		return Components{}, ErrGuardWithoutMutations
	}
	if cfg.RoleModel != nil && cfg.Roles == nil {
		return Components{}, fmt.Errorf("authorization: role model requires roles service: %w", sdk.ErrInvalidInput)
	}
	var limits authmodel.EvaluationLimits
	if cfg.Relationships != nil || cfg.RoleModel != nil || cfg.Guard != nil {
		var err error
		limits, err = cfg.Limits.Resolve()
		if err != nil {
			return Components{}, err
		}
	}
	if cfg.Relationships != nil {
		if cfg.Limits == (authmodel.EvaluationLimits{}) {
			limits = cfg.Relationships.Limits()
		}
		if limits != cfg.Relationships.Limits() {
			return Components{}, fmt.Errorf("%w: mutation and relationship services must share limits", authmodel.ErrInvalidLimits)
		}
		if err := cfg.RoleModel.ValidateDisjoint(cfg.Relationships); err != nil {
			return Components{}, err
		}
	}
	if cfg.Relationships != nil && repo != nil {
		if err := validateGuardianPolicy(cfg.Relationships.GetSchema(), repo.GuardianPolicy()); err != nil {
			return Components{}, err
		}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Components{Service: &Service{relationships: cfg.Relationships, roles: cfg.Roles, roleModel: cfg.RoleModel, guard: cfg.Guard, mutations: repo, maxBatchSize: limits.MaxBatchSize, limits: limits, log: logger}, SystemMutator: &SystemMutator{mutations: repo, log: logger, relationships: cfg.Relationships, roleModel: cfg.RoleModel}}, nil
}

// Guarded reports whether actor writes have both a host guard and atomic repository.
func (s *Service) Guarded() bool { return s != nil && s.guard != nil && s.mutations != nil }
func typedNil(v any) bool {
	if v == nil {
		return false
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}
