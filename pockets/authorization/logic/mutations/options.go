package mutations

import (
	"log/slog"
	"time"

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

// WithLedger enables Fence and Recorded. The ledger must read and fence the same
// authority as the service's MutationRepository. Nil leaves both unconfigured.
func WithLedger(ledger OperationLedger) Option { return func(c *config) { c.Ledger = ledger } }

// WithOperationExpiry enables ExpireOperations. The expirer must expire the
// operation records of the same authority as the service's MutationRepository.
// Nil leaves ExpireOperations returning ErrOperationExpiryNotConfigured.
func WithOperationExpiry(expirer OperationExpirer) Option {
	return func(c *config) { c.Expirer = expirer }
}

// WithOperationRetention sets how long ExpireOperations keeps an operation
// record: it expires records committed at or before now minus retention. The
// default is 180 days. NewService refuses a retention below 7 days
// (ErrOperationRetentionTooShort) and one set without WithOperationExpiry
// (ErrOperationRetentionWithoutExpirer). The retention must exceed every
// producer's retry window and the age of the oldest open claim that names an
// operation ID: a command retried after its record expired applies again.
func WithOperationRetention(retention time.Duration) Option {
	return func(c *config) { c.Retention = retention; c.RetentionSet = true }
}
