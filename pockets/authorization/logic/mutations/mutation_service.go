package mutations

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

var ErrTeardownViaTypedMethod = fmt.Errorf("authorization: OpTeardown requires Service.TeardownResourceAuthorization: %w", sdk.ErrInvalidInput)

const MaxTeardownReasonLen = audit.MaxReasonLen

var ErrTeardownReasonRequired = fmt.Errorf("authorization: TeardownResourceAuthorization requires a non-empty UTF-8 reason without NUL (<= %d bytes): %w", MaxTeardownReasonLen, sdk.ErrInvalidInput)

// TeardownResourceAuthorizationCommand removes all relationships and scoped roles
// for a destroyed resource, including its last protected subject. Reason is required.
type TeardownResourceAuthorizationCommand struct {
	ResourceType string
	ResourceID   string
	Reason       string
}

// TeardownResourceAuthorization is the explicit exception to integrity minimums.
// The host must delete or logically retire the resource first; deletion in another
// store and authorization teardown do not share this transaction. WithAuditSource
// supplies attribution when recording is enabled; this method supplies the reason.
func (m *Service) TeardownResourceAuthorization(ctx context.Context, cmd TeardownResourceAuthorizationCommand) (*Result, error) {
	if m == nil || m.mutations == nil {
		return nil, ErrMutationsNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(cmd.Reason)
	if reason == "" || len(reason) > MaxTeardownReasonLen || !utf8.ValidString(reason) || strings.ContainsRune(reason, 0) {
		return nil, ErrTeardownReasonRequired
	}
	command := Command{Target: resourceTarget(cmd.ResourceType, cmd.ResourceID), Operation: OpTeardown}
	if err := command.Validate(); err != nil {
		return nil, err
	}
	if source, err := audit.SourceFromContext(ctx); err == nil {
		source.Reason = reason
		ctx = audit.WithSource(ctx, source)
	}
	result, err := m.mutations.Apply(ctx, command, semanticValidatorFor(m.model))
	logger := m.log
	if logger == nil {
		logger = slog.Default()
	}
	if err != nil {
		logger.WarnContext(ctx, "authorization resource teardown failed", "resource_type", cmd.ResourceType, "resource_id", cmd.ResourceID, "reason", reason, "error", err)
	} else if result != nil {
		logger.InfoContext(ctx, "authorization resource teardown", "resource_type", cmd.ResourceType, "resource_id", cmd.ResourceID, "reason", reason, "outcome", result.Outcome)
	}
	return result, err
}

// Apply validates and applies one atomic data command. Inbound owns access checks.
func (s *Service) Apply(ctx context.Context, cmd Command) (*Result, error) {
	if s == nil || s.mutations == nil {
		return nil, ErrMutationsNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cmd.Operation == OpTeardown {
		return nil, ErrTeardownViaTypedMethod
	}
	count := len(cmd.Relationships) + len(cmd.Roles) + len(cmd.Tuples.Add) + len(cmd.Tuples.Remove) + len(cmd.Subjects)
	if cmd.OperationID == "" && count > s.maxBatchSize {
		return nil, authmodel.ErrEvaluationLimit
	}
	if cmd.MaxAffectedRows == 0 || cmd.MaxAffectedRows > s.maxBatchSize {
		cmd.MaxAffectedRows = s.maxBatchSize
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	validate := semanticValidatorFor(s.model)
	return s.mutations.Apply(ctx, cmd, func(cmd Command) error {
		if count > s.maxBatchSize {
			return authmodel.ErrEvaluationLimit
		}
		if validate != nil {
			return validate(cmd)
		}
		return nil
	})
}

// Fence durably closes operationID: afterwards no command can commit under it, or
// the result reports the outcome one already committed. Inbound owns access checks.
func (s *Service) Fence(ctx context.Context, operationID string) (*FenceResult, error) {
	if s == nil || s.ledger == nil {
		return nil, ErrOperationLedgerNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := tuples.ValidateRefField("operation id", operationID); err != nil {
		return nil, err
	}
	return s.ledger.Fence(ctx, operationID)
}

// Recorded reports the outcome recorded under operationID. false is never proof
// that the operation will not commit later; only Fence is.
func (s *Service) Recorded(ctx context.Context, operationID string) (Outcome, bool, error) {
	if s == nil || s.ledger == nil {
		return "", false, ErrOperationLedgerNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if err := tuples.ValidateRefField("operation id", operationID); err != nil {
		return "", false, err
	}
	return s.ledger.Recorded(ctx, operationID)
}
func semanticValidatorFor(model *decisions.CompiledModel) SemanticValidator {
	if model == nil {
		return nil
	}
	return func(cmd Command) error {
		for _, t := range cmd.Requested().Add {
			if err := model.ValidateTuple(t); err != nil {
				return err
			}
		}
		return nil
	}
}
