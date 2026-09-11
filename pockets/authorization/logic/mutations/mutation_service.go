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
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
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

func composeGuard(actor Actor, guard MutationGuard, cmd Command, engine *relationships.Service, roleModel *authmodel.CompiledRoleModel, limits authmodel.EvaluationLimits) Guard {
	return func(ctx context.Context, view StoreDecisionView) error {
		attempt := MutationAttempt{
			Actor: actor, Operation: cmd.Operation, Target: cmd.Target,
			Change: ProposedChange{Relationships: slices.Clone(cmd.Relationships), Roles: slices.Clone(cmd.Roles)},
		}
		return guard.AuthorizeMutation(ctx, attempt, permissionView{store: view, engine: engine, roleModel: roleModel, limits: limits})
	}
}

// SystemMutator is a separately held capability for trusted atomic writes.
// It bypasses the host guard, while retaining current-model validation and
// guardian invariants. When recording is enabled, supply WithAuditSource.
type SystemMutator struct {
	mutations     MutationRepository
	log           *slog.Logger
	relationships *relationships.Service
	roleModel     *authmodel.CompiledRoleModel
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
	return m.mutations.Apply(ctx, cmd, semanticValidatorFor(m.relationships, m.roleModel))
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
	result, err := m.mutations.Apply(ctx, command, semanticValidatorFor(m.relationships, m.roleModel))
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
	if cmd.Operation == OpPurge {
		cmd.MaxAffectedRows = s.maxBatchSize
	} else {
		cmd.MaxAffectedRows = 0
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
	guard := composeGuard(actor, s.guard, cmd, s.relationships, s.roleModel, s.limits)
	return s.mutations.ApplyGuarded(ctx, cmd, guard, semanticValidatorFor(s.relationships, s.roleModel))
}

// schemaValidatorFor validates additions against the current relationship model.
// Removed model declarations must not prevent removing their stored facts.
func schemaValidatorFor(eng *relationships.Service) SemanticValidator {
	if eng == nil {
		return nil
	}
	return func(cmd Command) error {
		switch cmd.Operation {
		case OpGrant, OpReplace:
			for _, row := range cmd.Relationships {
				if err := eng.ValidateRelation(cmd.Target.Type, row.Relation, row.Subject.Type, row.Subject.Relation); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

// roleModelValidatorFor checks additions against the current role model.
// Removals remain possible after a role is removed from that model.
func roleModelValidatorFor(model *authmodel.CompiledRoleModel) SemanticValidator {
	if model == nil {
		return nil
	}
	return func(cmd Command) error {
		if cmd.Operation != OpRoleAssign {
			return nil
		}
		resourceType := ""
		if cmd.Target.Kind == TargetResource {
			resourceType = cmd.Target.Type
		}
		for _, row := range cmd.Roles {
			if model.DeclaresRole(resourceType, row.Role) {
				continue
			}
			if resourceType == "" {
				return fmt.Errorf("%w: role %q is declared by no resource type, so it cannot be assigned globally", authmodel.ErrInvalidRoleModel, row.Role)
			}
			return fmt.Errorf("%w: role %q is not declared on resource type %q", authmodel.ErrInvalidRoleModel, row.Role, resourceType)
		}
		return nil
	}
}

// semanticValidatorFor applies the current models to every command.
func semanticValidatorFor(eng *relationships.Service, model *authmodel.CompiledRoleModel) SemanticValidator {
	schema := schemaValidatorFor(eng)
	roles := roleModelValidatorFor(model)
	switch {
	case schema == nil:
		return roles
	case roles == nil:
		return schema
	}
	return func(cmd Command) error {
		if err := schema(cmd); err != nil {
			return err
		}
		return roles(cmd)
	}
}

// logger is fixed at construction, with a fallback for internal test instances.
func (s *Service) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}
