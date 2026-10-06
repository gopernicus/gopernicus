package mutations

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
)

const OperationEncoding = "operation/v1"

// Fingerprint binds a structurally valid command's normalized meaning using the
// frozen operation/v1 encoding. OperationID and execution limits are excluded.
func Fingerprint(cmd Command) string {
	h := sha256.New()
	field := func(s string) { h.Write([]byte(s)); h.Write([]byte{0}) }
	field(OperationEncoding)
	field(string(cmd.Target.Kind))
	field(cmd.Target.Type)
	field(cmd.Target.ID)
	field(string(cmd.Operation))
	requested := cmd.Requested()
	for _, set := range []struct {
		tag   string
		facts []tuples.Tuple
	}{{"add", requested.Add}, {"remove", requested.Remove}} {
		facts := slices.Clone(set.facts)
		slices.SortFunc(facts, tuples.Compare)
		facts = slices.Compact(facts)
		field(set.tag)
		field(strconv.Itoa(len(facts)))
		for _, t := range facts {
			kind := "global"
			if t.Scope.Kind == tuples.ResourceScope {
				kind = "resource"
			}
			field(kind)
			field(t.Scope.Type)
			field(t.Scope.ID)
			field(t.Relation)
			field(t.Subject.Type)
			field(t.Subject.ID)
			field(t.Subject.Relation)
		}
	}
	field("reconcile")
	relation := ""
	var subjects []tuples.SubjectRef
	if cmd.Operation == OpReconcile {
		relation = cmd.Relation
		subjects = slices.Clone(cmd.Subjects)
	}
	slices.SortFunc(subjects, func(a, b tuples.SubjectRef) int {
		return cmp.Or(cmp.Compare(a.Type, b.Type), cmp.Compare(a.ID, b.ID), cmp.Compare(a.Relation, b.Relation))
	})
	subjects = slices.Compact(subjects)
	field(relation)
	field(strconv.Itoa(len(subjects)))
	for _, s := range subjects {
		field(s.Type)
		field(s.ID)
		field(s.Relation)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Superseded checks whether requested facts are still in effect under the write
// lock, without applying current model, integrity or execution-limit policy.
func Superseded(cmd Command, before []tuples.Tuple) bool {
	facts := make(map[tuples.Tuple]bool, len(before))
	for _, t := range before {
		facts[t] = true
	}
	requested := cmd.Requested()
	for _, t := range requested.Add {
		if !facts[t] {
			return true
		}
	}
	for _, t := range requested.Remove {
		if facts[t] {
			return true
		}
	}
	if cmd.Operation == OpPurge {
		return len(facts) != 0
	}
	if cmd.Operation == OpReconcile {
		desired := make(map[tuples.Tuple]bool, len(requested.Add))
		for _, t := range requested.Add {
			desired[t] = true
		}
		for t := range facts {
			if t.Relation == cmd.Relation && !desired[t] {
				return true
			}
		}
	}
	return false
}
