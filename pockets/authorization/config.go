package authorization

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

var (
	ErrNoKindConfigured           = errors.New("authorization: Repositories.Tuples is required")
	ErrOperationsWithoutMutations = fmt.Errorf("authorization: Repositories.Operations requires Repositories.Mutations: %w", sdk.ErrInvalidInput)
	// ErrAuditRequired is returned by New in production when Repositories.Tuples,
	// or Repositories.Mutations when wired, does not report AuditRecording() true,
	// or when Repositories.Audit is nil. The wrapped text names each failing port.
	ErrAuditRequired = errors.New("authorization: production requires an audit-recording authority and Repositories.Audit")
	// ErrAuditAcknowledgedWithRecording is returned by New in every mode when
	// WithAuditDisabledAcknowledged is passed beside an authority that records.
	ErrAuditAcknowledgedWithRecording = fmt.Errorf("authorization: WithAuditDisabledAcknowledged set but the authority records audit (remove the acknowledgement): %w", sdk.ErrInvalidInput)
)

// Repositories supplies one required tuple authority and optional capabilities.
type Repositories struct {
	// TupleSource supplies authoritative snapshots and committed tuple changes.
	TupleSource tuplecache.Source
	// Tuples is the single canonical authority used by roles, relationships and decisions.
	Tuples tuples.Storer

	// Mutations backs atomic data commands and IntegrityPolicy. A
	// nil field leaves baseline RelationshipWriter operations fully available.
	// It is independent of the read/check ports above.
	Mutations mutations.MutationRepository
	// Operations enables Mutations.Fence and Mutations.Recorded. It must fence and
	// read the same authority as Mutations, and requires it. Nil leaves both
	// returning ErrOperationLedgerNotConfigured.
	Operations mutations.OperationLedger
	// OperationExpiry enables Mutations.ExpireOperations over the same authority
	// as Mutations, and requires it. Nil leaves ExpireOperations returning
	// ErrOperationExpiryNotConfigured and keeps every operation record; production
	// then logs one WARN.
	OperationExpiry mutations.OperationExpirer

	// Audit reads committed change history. The host controls access and retention.
	// Production (WithRuntimeMode) requires it.
	Audit audit.Reader
}

type config struct {
	TupleBackend              tuplecache.Backend
	TuplePolicy               tuplecache.Policy
	Logger                    *slog.Logger
	ModelOption               decisions.Option
	DiagnosticObserver        decisions.DiagnosticObserver
	DecisionObserver          decisions.DecisionObserver
	Limits                    authmodel.EvaluationLimits
	RoleRoutesGate            web.Middleware
	RoleWritePolicy           authorizationhttp.RoleWritePolicy
	ListStrategy              list.Strategy
	RuntimeMode               environment.Mode
	AuditDisabledAcknowledged bool
	OperationRetention        time.Duration
	OperationRetentionSet     bool
}
