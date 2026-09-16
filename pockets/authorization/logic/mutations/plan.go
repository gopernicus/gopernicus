package mutations

import (
	"slices"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

// Plan computes the one canonical delta over all facts in a target. Adapters
// invoke it after semantic validation, inside their serialized
// write boundary. It does no I/O and never changes before or cmd.
func Plan(cmd Command, before []tuples.Tuple, policy IntegrityPolicy) (tuples.Changes, Outcome, error) {
	if err := cmd.Validate(); err != nil {
		return tuples.Changes{}, "", err
	}
	original := make(map[tuples.Tuple]bool, len(before))
	for _, t := range before {
		if err := t.Validate(); err != nil {
			return tuples.Changes{}, "", err
		}
		if !cmd.Target.Contains(t) {
			return tuples.Changes{}, "", ErrInvalidCommand
		}
		original[t] = true
	}
	next := make(map[tuples.Tuple]bool, len(original))
	for t := range original {
		next[t] = true
	}
	requested := cmd.Requested()
	for _, t := range requested.Remove {
		delete(next, t)
	}
	if cmd.Operation == OpPurge || cmd.Operation == OpTeardown {
		clear(next)
	}
	if cmd.Operation == OpReconcile {
		for t := range next {
			if t.Relation == cmd.Relation {
				delete(next, t)
			}
		}
	}
	for _, t := range requested.Add {
		next[t] = true
	}
	if cmd.Operation != OpTeardown {
		facts := make([]tuples.Tuple, 0, len(next))
		for t := range next {
			facts = append(facts, t)
		}
		if err := policy.ValidateState(cmd.Target.Scope(), facts); err != nil {
			return tuples.Changes{}, "", err
		}
	}
	var delta tuples.Changes
	for t := range original {
		if !next[t] {
			delta.Remove = append(delta.Remove, t)
		}
	}
	for t := range next {
		if !original[t] {
			delta.Add = append(delta.Add, t)
		}
	}
	limit := cmd.MaxAffectedRows
	if limit == 0 {
		limit = MaxCommandTuples
	}
	if cmd.Operation != OpTeardown && len(delta.Add)+len(delta.Remove) > limit {
		return tuples.Changes{}, "", ErrInvariantBlocked
	}
	slices.SortFunc(delta.Add, tuples.Compare)
	slices.SortFunc(delta.Remove, tuples.Compare)
	outcome := OutcomeApplied
	if len(delta.Add)+len(delta.Remove) == 0 {
		outcome = OutcomeNoChange
		if cmd.Operation == OpRevoke || cmd.Operation == OpRoleUnassign {
			outcome = OutcomeNotFound
		}
	}
	return delta, outcome, nil
}
