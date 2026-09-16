package mutations

import (
	"log/slog"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

// Option configures tuple writes. Options replace whole values in order; nil is invalid.
type Option func(*config)

// WithModel enforces declared tuple shapes; it makes no principal permission decision.
func WithModel(model *decisions.CompiledModel) Option { return func(c *config) { c.Model = model } }

// WithLimits bounds requested and affected facts using MaxBatchSize.
func WithLimits(limits authmodel.EvaluationLimits) Option {
	return func(c *config) { c.Limits = limits }
}

// WithLogger supplies the borrowed operational logger. Nil uses slog.Default.
func WithLogger(logger *slog.Logger) Option { return func(c *config) { c.Logger = logger } }
