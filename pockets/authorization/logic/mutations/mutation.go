// Package mutation defines atomic authorization commands. Commands describe the
// desired state of one resource or one global-role subject. Every application
// validates current policy; there is no request replay ledger or revision token.
package mutations

import (
	"fmt"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

var (
	ErrInvalidCommand     = fmt.Errorf("authorization mutation: invalid command: %w", sdk.ErrInvalidInput)
	ErrConcurrentMutation = fmt.Errorf("authorization mutation: concurrent change: %w", sdk.ErrConflict)
	ErrSemanticConflict   = fmt.Errorf("authorization mutation: relationship conflict: %w", sdk.ErrConflict)
	ErrInvariantBlocked   = fmt.Errorf("authorization mutation: invariant blocked: %w", sdk.ErrConflict)
	// ErrGuardedInsideTransaction refuses a guarded command in an ambient host
	// transaction. The command owns its serialization and commit boundary.
	ErrGuardedInsideTransaction = fmt.Errorf("authorization mutation: guarded mutation inside an ambient transaction (move the call outside Transact): %w", sdk.ErrInvalidInput)
)

// TargetKind distinguishes a resource from a global-role subject.
type TargetKind string

const (
	TargetResource TargetKind = "resource"
	TargetSubject  TargetKind = "subject"
)

// Target addresses the one resource or global-role subject a command changes.
// It is not a persisted revision anchor.
type Target struct {
	Kind TargetKind
	Type string
	ID   string
}

func (t Target) Validate() error {
	if t.Kind != TargetResource && t.Kind != TargetSubject {
		return fmt.Errorf("target kind %q is not resource or subject: %w", t.Kind, ErrInvalidCommand)
	}
	if err := authmodel.ValidateRefField("target type", t.Type); err != nil {
		return err
	}
	return authmodel.ValidateRefField("target id", t.ID)
}
func (t Target) String() string { return string(t.Kind) + ":" + t.Type + ":" + t.ID }

// Operation names the state transition a [Command] requests. The set is closed:
// grant/revoke/replace/purge/teardown mutate relationships (resource target);
// role_assign/role_unassign mutate roles (resource or subject target).
type Operation string

const (
	// OpGrant adds the command's relationship rows. Under the one-relation rule a
	// second, DIFFERENT relation for a subject already related to the resource is
	// NOT a silent overwrite: it is [ErrSemanticConflict]. Use
	// [OpReplace] to change a subject's relation atomically.
	OpGrant Operation = "grant"
	// OpRevoke removes the command's exact relationship rows. Revoking an absent
	// row is [OutcomeNotFound] (a no-op), never an error.
	OpRevoke Operation = "revoke"
	// OpReplace atomically replaces whatever relation each row's subject currently
	// holds on the resource with the row's relation — the sanctioned answer to the
	// one-relation conflict, with no delete/create visibility gap (default #1).
	OpReplace Operation = "replace"
	// OpPurge removes every relationship on the resource (carries no rows). It
	// still honors guardian invariants: a purge that would orphan a protected
	// resource is [ErrInvariantBlocked]. It is not resource teardown.
	OpPurge Operation = "resource_purge"
	// OpTeardown removes all relationships (and scoped roles) for a resource being
	// destroyed. It is a DISTINCT command from [OpPurge] because it may reduce
	// protected guardian counts to zero; it is a trusted operation (SystemMutator,
	// AZ3-0.5) and carries no rows. There is no subject-purge command in v3.
	OpTeardown Operation = "resource_teardown"
	// OpRoleAssign assigns the command's role rows. Target [TargetResource] is a
	// scoped assignment; target [TargetSubject] is a global assignment whose rows'
	// subject must equal the target subject.
	OpRoleAssign Operation = "role_assign"
	// OpRoleUnassign removes the command's exact role rows. Unassigning an absent
	// role is [OutcomeNotFound].
	OpRoleUnassign Operation = "role_unassign"
)

// isRelationshipOp reports whether op mutates relationships (resource target).
func (o Operation) isRelationshipOp() bool {
	switch o {
	case OpGrant, OpRevoke, OpReplace, OpPurge, OpTeardown:
		return true
	}
	return false
}

// isRoleOp reports whether op mutates roles.
func (o Operation) isRoleOp() bool {
	return o == OpRoleAssign || o == OpRoleUnassign
}

// carriesRows reports whether op takes explicit rows; purge and teardown operate
// on the whole resource and carry none.
func (o Operation) carriesRows() bool {
	return o != OpPurge && o != OpTeardown
}

// RelationshipRow is one relationship change within a resource target. The
// resource is the command's target, so a row names only the relation and subject
// — a row cannot escape the command's single target by construction.
type RelationshipRow struct {
	Relation string
	Subject  relationships.SubjectRef
}

// Validate reports whether the row is structurally well formed.
func (r RelationshipRow) Validate() error {
	if err := authmodel.ValidateRefField("relation", r.Relation); err != nil {
		return err
	}
	return r.Subject.Validate()
}

// RoleRow is one role change. Role and subject are always explicit; for a subject-targeted (global) command the subject
// must equal the target subject (enforced by [Command.Validate]).
type RoleRow struct {
	SubjectType string
	SubjectID   string
	Role        string
}

// Validate reports whether the row is structurally well formed.
func (r RoleRow) Validate() error {
	if err := authmodel.ValidateRefField("role subject type", r.SubjectType); err != nil {
		return err
	}
	if err := authmodel.ValidateRefField("role subject id", r.SubjectID); err != nil {
		return err
	}
	return authmodel.ValidateRefField("role", r.Role)
}

// Command is one atomic desired-state change. Rows belong to Target by
// construction. Current semantic validation and a supplied guard run on every
// application, including natural no-ops.
type Command struct {
	Target        Target
	Operation     Operation
	Relationships []RelationshipRow
	Roles         []RoleRow
	// MaxAffectedRows bounds ordinary resource purge. Zero is unbounded; trusted
	// teardown is exempt. Exceeding the bound returns ErrInvariantBlocked.
	MaxAffectedRows int
}

// Validate checks shape, exact references and duplicate rows without I/O.
func (c Command) Validate() error {
	if err := c.Target.Validate(); err != nil {
		return err
	}
	switch {
	case c.Operation.isRelationshipOp():
		if c.Target.Kind != TargetResource {
			return fmt.Errorf("operation %q requires a resource target, got %q: %w", c.Operation, c.Target.Kind, ErrInvalidCommand)
		}
		if len(c.Roles) != 0 {
			return fmt.Errorf("relationship operation %q must not carry role rows: %w", c.Operation, ErrInvalidCommand)
		}
		if c.Operation.carriesRows() {
			if len(c.Relationships) == 0 {
				return fmt.Errorf("operation %q requires at least one relationship row: %w", c.Operation, ErrInvalidCommand)
			}
		} else if len(c.Relationships) != 0 {
			return fmt.Errorf("operation %q operates on the whole resource and must carry no rows: %w", c.Operation, ErrInvalidCommand)
		}
		seen := make(map[relationships.SubjectRef]struct{}, len(c.Relationships))
		for _, row := range c.Relationships {
			if err := row.Validate(); err != nil {
				return err
			}
			// The one-relation invariant makes two rows for the SAME subject
			// reference in one command intrinsically contradictory (differing
			// relations) or non-canonical (an exact-duplicate row), so all three
			// backends must reject it before any evaluator runs — a dialect-agnostic
			// domain rule, not a store-specific unique-index side effect.
			if _, dup := seen[row.Subject]; dup {
				return fmt.Errorf("subject %s:%s#%s appears in more than one relationship row of one command: %w",
					row.Subject.Type, row.Subject.ID, row.Subject.Relation, ErrInvalidCommand)
			}
			seen[row.Subject] = struct{}{}
		}
	case c.Operation.isRoleOp():
		if len(c.Relationships) != 0 {
			return fmt.Errorf("role operation %q must not carry relationship rows: %w", c.Operation, ErrInvalidCommand)
		}
		if len(c.Roles) == 0 {
			return fmt.Errorf("operation %q requires at least one role row: %w", c.Operation, ErrInvalidCommand)
		}
		seen := make(map[RoleRow]struct{}, len(c.Roles))
		for _, row := range c.Roles {
			if err := row.Validate(); err != nil {
				return err
			}
			if c.Target.Kind == TargetSubject && (row.SubjectType != c.Target.Type || row.SubjectID != c.Target.ID) {
				return fmt.Errorf("global (subject-targeted) role row subject %s:%s must equal the target subject %s:%s: %w",
					row.SubjectType, row.SubjectID, c.Target.Type, c.Target.ID, ErrInvalidCommand)
			}
			// A subject may hold multiple DISTINCT roles in one command, but an
			// exact-duplicate (subject, role) row is non-canonical; reject it in the
			// dialect-agnostic domain so every backend agrees before evaluation.
			if _, dup := seen[row]; dup {
				return fmt.Errorf("role row %s:%s/%s is duplicated in one command: %w",
					row.SubjectType, row.SubjectID, row.Role, ErrInvalidCommand)
			}
			seen[row] = struct{}{}
		}
	default:
		return fmt.Errorf("unknown operation %q: %w", c.Operation, ErrInvalidCommand)
	}
	return nil
}
