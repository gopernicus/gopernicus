package mutations

import (
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/sdk"
)

const (
	defaultOperationRetention = 180 * 24 * time.Hour
	minOperationRetention     = 7 * 24 * time.Hour
)

var (
	ErrMutationsNotConfigured       = fmt.Errorf("authorization: atomic tuple writes are not configured: %w", sdk.ErrInvalidInput)
	ErrOperationLedgerNotConfigured = fmt.Errorf("authorization: operation ledger is not configured: %w", sdk.ErrInvalidInput)
	ErrOperationExpiryNotConfigured = fmt.Errorf("authorization: operation expiry is not configured: %w", sdk.ErrInvalidInput)
	// ErrOperationRetentionTooShort is returned when WithOperationRetention is
	// below the 7-day minimum.
	ErrOperationRetentionTooShort = fmt.Errorf("authorization: operation retention must be at least %s (7 days): %w", minOperationRetention, sdk.ErrInvalidInput)
	// ErrOperationRetentionWithoutExpirer is returned when WithOperationRetention
	// is set without an OperationExpirer to enforce it.
	ErrOperationRetentionWithoutExpirer = fmt.Errorf("authorization: operation retention is set but no operation expirer is wired: %w", sdk.ErrInvalidInput)
)

type config struct {
	Model        *decisions.CompiledModel
	Limits       authmodel.EvaluationLimits
	Logger       *slog.Logger
	Ledger       OperationLedger
	Expirer      OperationExpirer
	Retention    time.Duration
	RetentionSet bool
}

// Service applies data commands. Access policy belongs to the calling inbound adapter.
type Service struct {
	model        *decisions.CompiledModel
	mutations    MutationRepository
	ledger       OperationLedger
	expirer      OperationExpirer
	retention    time.Duration
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
	if typedNil(cfg.Ledger) {
		return nil, fmt.Errorf("authorization: typed nil operation ledger: %w", sdk.ErrInvalidInput)
	}
	if typedNil(cfg.Expirer) {
		return nil, fmt.Errorf("authorization: typed nil operation expirer: %w", sdk.ErrInvalidInput)
	}
	retention := defaultOperationRetention
	if cfg.RetentionSet {
		if cfg.Expirer == nil {
			return nil, ErrOperationRetentionWithoutExpirer
		}
		if cfg.Retention < minOperationRetention {
			return nil, ErrOperationRetentionTooShort
		}
		retention = cfg.Retention
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
	return &Service{model: cfg.Model, mutations: repo, ledger: cfg.Ledger, expirer: cfg.Expirer, retention: retention, maxBatchSize: limits.MaxBatchSize, log: logger}, nil
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
