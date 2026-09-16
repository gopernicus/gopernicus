package decisions

import (
	"fmt"
	"log/slog"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

type config struct {
	logger     *slog.Logger
	model      Model
	limits     authmodel.EvaluationLimits
	backend    tuplecache.Backend
	source     tuplecache.Source
	policy     tuplecache.Policy
	diagnostic DiagnosticObserver
}

// Service evaluates exact membership and graph policy over one canonical view.
type Service struct {
	logger      *slog.Logger
	store       tuples.Reader
	facts       tuples.Reader
	reader      Reader
	readModel   ReadModel
	compiled    *CompiledModel
	limits      authmodel.EvaluationLimits
	inSnapshot  bool
	tupleCache  *tuplecache.TupleCache
	diagnostic  DiagnosticObserver
	diagnostics *operationDiagnostics
}

func NewService(reader tuples.Reader, opts ...Option) (*Service, error) {
	if isNilReader(reader) {
		return nil, fmt.Errorf("authorization: tuple reader required: %w", sdk.ErrInvalidInput)
	}
	cfg := config{}
	for _, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("authorization: nil option: %w", sdk.ErrInvalidInput)
		}
		opt(&cfg)
	}
	limits, err := cfg.limits.Resolve()
	if err != nil {
		return nil, err
	}
	compiled, err := Compile(cfg.model)
	if err != nil {
		return nil, err
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	s := &Service{logger: cfg.logger, store: reader, facts: reader, compiled: compiled, readModel: compiled.readModel(), limits: limits, diagnostic: cfg.diagnostic}
	s.reader = s.graphReader(reader)
	if cfg.backend != nil {
		if isNilReader(cfg.backend) || isNilReader(cfg.source) {
			return nil, fmt.Errorf("authorization: cache source and backend required: %w", sdk.ErrInvalidInput)
		}
		binding, ok := reader.(interface{ TupleCacheBinding() string })
		if !ok || binding.TupleCacheBinding() != cfg.source.Binding() {
			return nil, tuplecache.ErrBinding
		}
		s.tupleCache, err = tuplecache.New(cfg.source, cfg.backend, tuplecache.WithPolicy(cfg.policy))
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Service) Limits() authmodel.EvaluationLimits {
	if s == nil {
		return authmodel.EvaluationLimits{}
	}
	return s.limits
}
func (s *Service) CompiledModel() *CompiledModel {
	if s == nil {
		return nil
	}
	return s.compiled
}
func (s *Service) DeclaresPermission(rt, p string) bool {
	return s != nil && s.compiled.declaresPermission(rt, p)
}
func (s *Service) graphReader(facts tuples.Reader) Reader {
	if r, ok := facts.(interface {
		ForModel(relationships.ReadModel) relationships.Reader
	}); ok {
		if view := r.ForModel(s.readModel); !isNilReader(view) {
			return view
		}
	}
	fallback := &tupleGraphReader{facts: facts, model: s.readModel}
	if checks, ok := facts.(CheckReadSource); ok {
		if reader := checks.ForChecks(s.readModel); !isNilReader(reader) {
			return &checkGraphReader{tupleGraphReader: fallback, checks: reader}
		}
	}
	return fallback
}
func (s *Service) bound(facts tuples.Reader) *Service {
	v := *s
	v.facts = &memoFacts{Reader: facts, values: map[tuples.Tuple]bool{}}
	v.reader = v.graphReader(facts)
	v.inSnapshot = true
	return &v
}
