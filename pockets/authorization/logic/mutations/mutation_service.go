package mutations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

var (
	// ErrGuardWithoutMutations reports a guard with no atomic write repository.
	ErrGuardWithoutMutations = errors.New("authorization: WithGuard requires Repositories.Mutations")
	// ErrTrustedOperationRequired refuses teardown through an actor-facing method.
	ErrTrustedOperationRequired = fmt.Errorf("authorization: operation requires the separately held SystemMutator: %w", sdk.ErrInvalidInput)
	// ErrTeardownViaTypedMethod requires the teardown method and its reason.
	ErrTeardownViaTypedMethod = fmt.Errorf("authorization: OpTeardown requires SystemMutator.TeardownResourceAuthorization: %w", sdk.ErrInvalidInput)
)

// ProposedChange contains the command's requested relationship or role rows.
// Audit history records the actual changes committed by the repository.
type ProposedChange struct {
	Relationships []RelationshipRow
	Roles         []RoleRow
	Tuples        tuples.Changes
	Relation      string
	Subjects      []tuples.SubjectRef
}

// MutationAttempt is the request the host guard must authorize.
type MutationAttempt struct {
	Actor     Actor
	Operation Operation
	Target    Target
	Change    ProposedChange
}

// MutationGuard authorizes a write inside the repository's transaction.
// Read authorization state only through view: an outer Service check would
// introduce a check-then-write race. Return nil to allow, or an error to refuse.
// Callbacks must honor cancellation, return promptly, and avoid external I/O.
// Stores may retry aborted transactions, so callbacks must have no side effects.
type MutationGuard interface {
	AuthorizeMutation(context.Context, MutationAttempt, DecisionView) error
}

func composeGuard(actor Actor, guard MutationGuard, cmd Command, engine *decisions.Service, limits authmodel.EvaluationLimits) Guard {
	return func(ctx context.Context, view StoreDecisionView) error {
		attempt := MutationAttempt{
			Actor: actor, Operation: cmd.Operation, Target: cmd.Target,
			Change: ProposedChange{Relationships: slices.Clone(cmd.Relationships), Roles: slices.Clone(cmd.Roles), Tuples: tuples.Changes{Add: slices.Clone(cmd.Tuples.Add), Remove: slices.Clone(cmd.Tuples.Remove)}, Relation: cmd.Relation, Subjects: slices.Clone(cmd.Subjects)},
		}
		return guard.AuthorizeMutation(ctx, attempt, permissionView{store: view, engine: engine, limits: limits})
	}
}

// SystemMutator is a separately held capability for trusted atomic writes.
// It bypasses the host guard, while retaining current-model validation and
// guardian invariants. When recording is enabled, supply WithAuditSource.
type SystemMutator struct {
	mutations MutationRepository
	log       *slog.Logger
	decisions *decisions.Service
}

// Apply validates and applies a trusted command. Resource teardown must use its
// typed method so the caller supplies an explicit reason.
func (m *SystemMutator) Apply(ctx context.Context, cmd Command) (*Result, error) {
	if m == nil || m.mutations == nil {
		return nil, ErrMutationsNotConfigured
	}
	if cmd.Operation == OpTeardown {
		return nil, ErrTeardownViaTypedMethod
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	return m.mutations.Apply(ctx, cmd, semanticValidatorFor(m.decisions))
}

func (m *SystemMutator) GrantRelationship(ctx context.Context, cmd GrantRelationshipCommand) (*Result, error) {
	return m.Apply(ctx, grantRelationshipCommand(cmd))
}

func (m *SystemMutator) RevokeRelationship(ctx context.Context, cmd RevokeRelationshipCommand) (*Result, error) {
	return m.Apply(ctx, revokeRelationshipCommand(cmd))
}

func (m *SystemMutator) AssignRole(ctx context.Context, cmd AssignRoleCommand) (*Result, error) {
	command, err := assignRoleCommand(cmd)
	if err != nil {
		return nil, err
	}
	return m.Apply(ctx, command)
}

func (m *SystemMutator) UnassignRole(ctx context.Context, cmd UnassignRoleCommand) (*Result, error) {
	command, err := unassignRoleCommand(cmd)
	if err != nil {
		return nil, err
	}
	return m.Apply(ctx, command)
}

const MaxTeardownReasonLen = audit.MaxReasonLen

var ErrTeardownReasonRequired = fmt.Errorf("authorization: TeardownResourceAuthorization requires a non-empty UTF-8 reason without NUL (<= %d bytes): %w", MaxTeardownReasonLen, sdk.ErrInvalidInput)

// TeardownResourceAuthorizationCommand removes all relationships and scoped roles
// for a destroyed resource, including its last guardian. Reason is required.
type TeardownResourceAuthorizationCommand struct {
	ResourceType string
	ResourceID   string
	Reason       string
}

// TeardownResourceAuthorization is the explicit exception to guardian minimums.
// The host must delete or logically retire the resource first; deletion in another
// store and authorization teardown do not share this transaction. WithAuditSource
// supplies attribution when recording is enabled; this method supplies the reason.
func (m *SystemMutator) TeardownResourceAuthorization(ctx context.Context, cmd TeardownResourceAuthorizationCommand) (*Result, error) {
	if m == nil || m.mutations == nil {
		return nil, ErrMutationsNotConfigured
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
	result, err := m.mutations.Apply(ctx, command, semanticValidatorFor(m.decisions))
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

func (s *Service) applyMutation(ctx context.Context, actor Actor, cmd Command) (*Result, error) {
	if s.guard == nil || s.mutations == nil {
		return nil, ErrMutationsNotConfigured
	}
	if cmd.Operation == OpTeardown {
		return nil, ErrTrustedOperationRequired
	}
	if err := actor.Validate(); err != nil {
		return nil, err
	}
	cmd.MaxAffectedRows = s.maxBatchSize
	if len(cmd.Relationships)+len(cmd.Roles)+len(cmd.Tuples.Add)+len(cmd.Tuples.Remove)+len(cmd.Subjects) > s.maxBatchSize {
		return nil, authmodel.ErrEvaluationLimit
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	// Caller-supplied audit metadata cannot impersonate the authenticated actor.
	source := audit.Source{ActorType: actor.Type, ActorID: actor.ID}
	if supplied, err := audit.SourceFromContext(ctx); err == nil {
		source.Reason = supplied.Reason
	}
	ctx = audit.WithSource(ctx, source)
	guard := composeGuard(actor, s.guard, cmd, s.decisions, s.limits)
	return s.mutations.ApplyGuarded(ctx, cmd, guard, semanticValidatorFor(s.decisions))
}

// semanticValidatorFor applies explicit canonical shape constraints through every facade.
func semanticValidatorFor(engine *decisions.Service) SemanticValidator {
	if engine == nil || engine.CompiledModel() == nil {
		return nil
	}
	return func(cmd Command) error {
		for _, t := range cmd.Requested().Add {
			if err := engine.CompiledModel().ValidateTuple(t); err != nil {
				return err
			}
		}
		return nil
	}
}

// Apply performs a guarded exact batch or reconciliation with the ordinary policy.
func (s *Service) Apply(ctx context.Context, actor Actor, cmd Command) (*Result, error) {
	return s.applyMutation(ctx, actor, cmd)
}

// logger is fixed at construction, with a fallback for internal test instances.
func (s *Service) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}
