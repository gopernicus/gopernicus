package mutations

import (
	"fmt"
	"log/slog"
	"reflect"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

var ErrMutationsNotConfigured = fmt.Errorf("authorization: atomic tuple writes are not configured: %w", sdk.ErrInvalidInput)

type config struct {
	Model  *decisions.CompiledModel
	Limits authmodel.EvaluationLimits
	Logger *slog.Logger
}

// Service applies data commands. Access policy belongs to the calling inbound adapter.
type Service struct {
	model        *decisions.CompiledModel
	mutations    MutationRepository
	maxBatchSize int
	log          *slog.Logger
}

func NewService(repo MutationRepository, opts ...Option) (*Service, error) {
	var cfg config
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authorization mutations: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if typedNil(repo) {
		return nil, fmt.Errorf("authorization: typed nil mutation repository: %w", sdk.ErrInvalidInput)
	}
	if repo == nil {
		return nil, ErrMutationsNotConfigured
	}
	limits, err := cfg.Limits.Resolve()
	if err != nil {
		return nil, err
	}
	if err := validateIntegrityPolicy(repo.IntegrityPolicy()); err != nil {
		return nil, err
	}
	if err := validateIntegrityModel(repo.IntegrityPolicy(), cfg.Model); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{model: cfg.Model, mutations: repo, maxBatchSize: limits.MaxBatchSize, log: logger}, nil
}
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
