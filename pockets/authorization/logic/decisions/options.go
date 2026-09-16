package decisions

import (
	"log/slog"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
)

type Option func(*config)

func WithModel(model Model) Option {
	snapshot := Model{ResourceTypes: map[string]ResourceTypeDef{}, assemblyErrors: append([]string(nil), model.assemblyErrors...)}
	for name, def := range model.ResourceTypes {
		snapshot.ResourceTypes[name] = copyResourceType(def)
	}
	return func(c *config) { c.model = snapshot }
}
func WithLimits(limits authmodel.EvaluationLimits) Option {
	return func(c *config) { c.limits = limits }
}
func WithTupleCache(backend tuplecache.Backend, source tuplecache.Source, policy tuplecache.Policy) Option {
	return func(c *config) { c.backend = backend; c.source = source; c.policy = policy }
}

// WithLogger supplies the borrowed decision logger. Nil captures slog.Default at
// construction. DEBUG records describe completed evaluations; bound calls do
// not claim that the caller's transaction committed. Hosts own redaction.
func WithLogger(logger *slog.Logger) Option {
	return func(c *config) { c.logger = logger }
}
