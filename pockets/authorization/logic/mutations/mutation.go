// Package mutations owns atomic canonical fact commands and data integrity.
package mutations

import (
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
)

var (
	ErrInvalidCommand            = fmt.Errorf("authorization mutation: invalid command: %w", sdk.ErrInvalidInput)
	ErrConcurrentMutation        = fmt.Errorf("authorization mutation: concurrent change: %w", sdk.ErrConflict)
	ErrInvariantBlocked          = fmt.Errorf("authorization mutation: invariant blocked: %w", sdk.ErrConflict)
	ErrMutationInsideTransaction = fmt.Errorf("authorization mutation: atomic tuple command inside an ambient transaction: %w", sdk.ErrInvalidInput)
)

const MaxCommandTuples = 4096

type TargetKind string

const (
	TargetResource TargetKind = "resource"
	TargetSubject  TargetKind = "subject"
)

// Target bounds an atomic command to one resource or one global subject.
type Target struct {
	Kind     TargetKind
	Type, ID string
}

func (t Target) Validate() error {
	if t.Kind != TargetResource && t.Kind != TargetSubject {
		return ErrInvalidCommand
	}
	if err := tuples.ValidateRefField("target type", t.Type); err != nil {
		return err
	}
	return tuples.ValidateRefField("target id", t.ID)
}
func (t Target) String() string { return string(t.Kind) + ":" + t.Type + ":" + t.ID }
func (t Target) Scope() tuples.Scope {
	if t.Kind == TargetResource {
		return tuples.On(t.Type, t.ID)
	}
	return tuples.Global()
}
func (t Target) Contains(f tuples.Tuple) bool {
	if t.Kind == TargetResource {
		return f.Scope == t.Scope()
	}
	return f.Scope == tuples.Global() && f.Subject.Type == t.Type && f.Subject.ID == t.ID
}

type Operation string

const (
	OpGrant        Operation = "grant"
	OpRevoke       Operation = "revoke"
	OpBatch        Operation = "tuple_batch"
	OpReconcile    Operation = "relation_reconcile"
	OpPurge        Operation = "resource_purge"
	OpTeardown     Operation = "resource_teardown"
	OpRoleAssign   Operation = "role_assign"
	OpRoleUnassign Operation = "role_unassign"
)

type RelationshipRow struct {
	Relation string
	Subject  relationships.SubjectRef
}

func (r RelationshipRow) Validate() error {
	if err := tuples.ValidateRefField("relation", r.Relation); err != nil {
		return err
	}
	return r.Subject.Validate()
}

type RoleRow struct{ SubjectType, SubjectID, Role string }

func (r RoleRow) Validate() error {
	return (tuples.Tuple{Scope: tuples.Global(), Relation: r.Role, Subject: tuples.SubjectRef{Type: r.SubjectType, ID: r.SubjectID}}).Validate()
}

type Command struct {
	Target        Target
	Operation     Operation
	Relationships []RelationshipRow
	Roles         []RoleRow
	Tuples        tuples.Changes
	Relation      string
	Subjects      []tuples.SubjectRef
	// MaxAffectedRows bounds the canonical delta; zero defaults to MaxCommandTuples.
	MaxAffectedRows int
}

func (c Command) Validate() error {
	if err := c.Target.Validate(); err != nil {
		return err
	}
	if c.MaxAffectedRows < 0 {
		return ErrInvalidCommand
	}
	if len(c.Relationships)+len(c.Roles)+len(c.Tuples.Add)+len(c.Tuples.Remove)+len(c.Subjects) > MaxCommandTuples {
		return fmt.Errorf("command exceeds %d facts: %w", MaxCommandTuples, ErrInvalidCommand)
	}
	noRows := len(c.Relationships) == 0 && len(c.Roles) == 0 && len(c.Tuples.Add) == 0 && len(c.Tuples.Remove) == 0
	switch c.Operation {
	case OpGrant, OpRevoke:
		if c.Target.Kind != TargetResource || len(c.Relationships) == 0 || len(c.Roles) > 0 || len(c.Tuples.Add)+len(c.Tuples.Remove) > 0 {
			return ErrInvalidCommand
		}
		for _, r := range c.Relationships {
			if err := r.Validate(); err != nil {
				return err
			}
		}
	case OpRoleAssign, OpRoleUnassign:
		if len(c.Roles) == 0 || len(c.Relationships) > 0 || len(c.Tuples.Add)+len(c.Tuples.Remove) > 0 {
			return ErrInvalidCommand
		}
		for _, r := range c.Roles {
			if err := r.Validate(); err != nil {
				return err
			}
			if c.Target.Kind == TargetSubject && (r.SubjectType != c.Target.Type || r.SubjectID != c.Target.ID) {
				return ErrInvalidCommand
			}
		}
	case OpBatch:
		if len(c.Relationships)+len(c.Roles) > 0 || len(c.Tuples.Add)+len(c.Tuples.Remove) == 0 {
			return ErrInvalidCommand
		}
		if err := c.Tuples.Validate(); err != nil {
			return err
		}
		for _, set := range [][]tuples.Tuple{c.Tuples.Add, c.Tuples.Remove} {
			for _, f := range set {
				if !c.Target.Contains(f) {
					return fmt.Errorf("tuple escapes command target: %w", ErrInvalidCommand)
				}
			}
		}
	case OpReconcile:
		if c.Target.Kind != TargetResource || !noRows {
			return ErrInvalidCommand
		}
		if err := tuples.ValidateRefField("relation", c.Relation); err != nil {
			return err
		}
		for _, s := range c.Subjects {
			if err := s.Validate(); err != nil {
				return err
			}
		}
	case OpPurge, OpTeardown:
		if c.Target.Kind != TargetResource || !noRows {
			return ErrInvalidCommand
		}
	default:
		return ErrInvalidCommand
	}
	if c.Operation != OpReconcile && (c.Relation != "" || len(c.Subjects) != 0) {
		return ErrInvalidCommand
	}
	return nil
}

// Requested normalizes role/relationship convenience commands to canonical facts.
// Reconciliation and scope removal require the serialized current facts in Plan.
func (c Command) Requested() tuples.Changes {
	out := c.Tuples
	for _, r := range c.Relationships {
		f := tuples.Tuple{Scope: c.Target.Scope(), Relation: r.Relation, Subject: r.Subject}
		if c.Operation == OpRevoke {
			out.Remove = append(out.Remove, f)
		} else {
			out.Add = append(out.Add, f)
		}
	}
	for _, r := range c.Roles {
		f := tuples.Tuple{Scope: c.Target.Scope(), Relation: r.Role, Subject: tuples.SubjectRef{Type: r.SubjectType, ID: r.SubjectID}}
		if c.Operation == OpRoleUnassign {
			out.Remove = append(out.Remove, f)
		} else {
			out.Add = append(out.Add, f)
		}
	}
	if c.Operation == OpReconcile {
		for _, s := range c.Subjects {
			out.Add = append(out.Add, tuples.Tuple{Scope: c.Target.Scope(), Relation: c.Relation, Subject: s})
		}
	}
	return out
}
