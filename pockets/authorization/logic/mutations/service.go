package mutations

import (
	"fmt"
	"log/slog"
	"reflect"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

var ErrMutationsNotConfigured = fmt.Errorf("authorization: actor-facing mutations are not configured (no MutationGuard): %w", sdk.ErrInvalidInput)

type config struct {
	Guard  MutationGuard
	Limits authmodel.EvaluationLimits
	Logger *slog.Logger
}
type Service struct {
	decisions    *decisions.Service
	guard        MutationGuard
	mutations    MutationRepository
	maxBatchSize int
	limits       authmodel.EvaluationLimits
	log          *slog.Logger
}
type Components struct {
	Service       *Service
	SystemMutator *SystemMutator
}

func NewService(repo MutationRepository, engine *decisions.Service, opts ...Option) (Components, error) {
	cfg := config{}
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
	limits, err := cfg.Limits.Resolve()
	if err != nil {
		return Components{}, err
	}
	if engine != nil {
		if cfg.Limits == (authmodel.EvaluationLimits{}) {
			limits = engine.Limits()
		}
		if limits != engine.Limits() {
			return Components{}, authmodel.ErrInvalidLimits
		}
	}
	if repo != nil {
		if err := validateGuardianPolicy(repo.GuardianPolicy()); err != nil {
			return Components{}, err
		}
		if err := validateGuardianModel(repo.GuardianPolicy(), engine); err != nil {
			return Components{}, err
		}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Components{Service: &Service{decisions: engine, guard: cfg.Guard, mutations: repo, maxBatchSize: limits.MaxBatchSize, limits: limits, log: logger}, SystemMutator: &SystemMutator{mutations: repo, log: logger, decisions: engine}}, nil
}
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
