package decisions

import (
	"fmt"
	"reflect"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// RoleReader is the role fact port consumed by permission evaluation.
type RoleReader interface{ roleProbe }

// Readers supplies the model-bearing services for permission evaluation. A
// relationship service or a roles reader paired with WithRoleModel is required.
// Services are borrowed; construction never changes their models or limits.
type Readers struct {
	Relationships *relationships.Service
	Roles         RoleReader
}

type config struct {
	Readers
	RoleModel authmodel.RoleModel
	Limits    authmodel.EvaluationLimits
}

// NewService compiles the role model and validates all decision dependencies.
func NewService(readers Readers, opts ...Option) (*Service, error) {
	cfg := config{Readers: readers}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authorization decisions: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	if typedNil(cfg.Roles) {
		return nil, fmt.Errorf("authorization: role reader is typed nil: %w", sdk.ErrInvalidInput)
	}
	if cfg.RoleModel.IsSet() && cfg.Roles == nil {
		return nil, fmt.Errorf("authorization: role model requires role reader: %w", sdk.ErrInvalidInput)
	}
	if cfg.Relationships == nil && !cfg.RoleModel.IsSet() {
		return nil, authmodel.ErrNoDecisionKind
	}
	limits, err := cfg.Limits.Resolve()
	if err != nil {
		return nil, err
	}
	if cfg.Relationships != nil {
		if cfg.Limits == (authmodel.EvaluationLimits{}) {
			limits = cfg.Relationships.Limits()
		}
		if limits != cfg.Relationships.Limits() {
			return nil, fmt.Errorf("%w: decision and relationship services must share limits", authmodel.ErrInvalidLimits)
		}
	}
	var declared authmodel.Declarer
	if cfg.Relationships != nil {
		declared = cfg.Relationships
	}
	var compiled *authmodel.CompiledRoleModel
	if cfg.RoleModel.IsSet() {
		compiled, err = authmodel.CompileRoleModel(cfg.RoleModel, declared)
		if err != nil {
			return nil, err
		}
	}
	return newComposite(cfg.Relationships, cfg.Roles, compiled, limits), nil
}

// Limits returns the immutable resolved budget for host adapters.
func (s *Service) Limits() authmodel.EvaluationLimits {
	if s == nil {
		return authmodel.EvaluationLimits{}
	}
	return s.limits
}

// CompiledRoleModel returns the immutable role model used by decisions and guarded writes.
func (s *Service) CompiledRoleModel() *authmodel.CompiledRoleModel {
	if s == nil || s.roles == nil {
		return nil
	}
	return s.roles.model
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
