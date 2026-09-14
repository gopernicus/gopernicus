package firestore

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/domain/mutation"
)

// The dependency guard-rail, hermetically. Firestore validates the read set at
// commit — an anchor the guard read is in it, so a concurrent bump aborts the
// commit and the whole attempt re-runs — and the repository's explicit
// re-validation is the parity mirror of the SQL siblings' locked re-read. A
// mirror nothing exercises is a mirror that can rot, so these two cases pin its
// arithmetic without a database: the ordering of the anchor reads, and the
// refusal when a re-read disagrees with what the guard observed.

func testScope(kind mutation.ScopeKind, typ, id string) mutation.ScopeKey {
	return mutation.ScopeKey{Kind: kind, Type: typ, ID: id}
}

// viewWith builds a decisionView carrying exactly the given dependencies, with
// no database behind it — the state a guard would have left after its reads.
func viewWith(deps ...mutation.Dependency) *decisionView {
	v := &decisionView{deps: map[string]mutation.Dependency{}}
	for _, dep := range deps {
		key := dep.Scope.Canonical()
		v.deps[key] = dep
		v.order = append(v.order, key)
	}
	return v
}

// TestValidateDepsRefusesAChangedDependency is the stale-dependency rule: a
// scope the guard observed at one revision and the transaction re-reads at
// another is mutation.ErrStaleRevision, never a committed write. An unchanged
// set passes, and the trusted path (no view) has nothing to validate.
func TestValidateDepsRefusesAChangedDependency(t *testing.T) {
	group := testScope(mutation.ScopeResource, "group", "g")
	subject := testScope(mutation.ScopeSubject, "user", "alice")
	view := viewWith(
		mutation.Dependency{Scope: group, Revision: 7},
		mutation.Dependency{Scope: subject, Revision: 0},
	)

	unchanged := map[string]mutation.Revision{
		group.Canonical():   7,
		subject.Canonical(): 0,
	}
	if err := validateDeps(view, unchanged); err != nil {
		t.Fatalf("an unchanged dependency set must validate: %v", err)
	}
	if err := validateDeps(nil, unchanged); err != nil {
		t.Fatalf("the trusted path has no dependencies to validate: %v", err)
	}

	for name, locked := range map[string]map[string]mutation.Revision{
		"bumped": {group.Canonical(): 8, subject.Canonical(): 0},
		// An anchor that vanished from the re-read reads as 0, which is a CHANGE
		// from 7 — the absent-anchor rule cuts both ways.
		"missing": {subject.Canonical(): 0},
		// A first writer on a scope observed absent is the 0→1 change the
		// revision-0 record exists to make detectable.
		"first-writer": {group.Canonical(): 7, subject.Canonical(): 1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateDeps(view, locked); !errors.Is(err, mutation.ErrStaleRevision) {
				t.Fatalf("a changed dependency must be ErrStaleRevision, got %v", err)
			}
		})
	}
}

// TestLockSetIsCanonicalAndDeduped pins the anchor read order: the mutation
// scope plus every dependency, deduplicated (the mutation scope is commonly a
// dependency too) and sorted by ScopeKey.Canonical() — the canonical order the
// port names, so the three families take their anchors in one sequence.
func TestLockSetIsCanonicalAndDeduped(t *testing.T) {
	mutScope := testScope(mutation.ScopeResource, "doc", "d1")
	group := testScope(mutation.ScopeResource, "group", "g")
	subject := testScope(mutation.ScopeSubject, "user", "alice")

	got := lockSet(mutScope, viewWith(
		mutation.Dependency{Scope: group, Revision: 1},
		mutation.Dependency{Scope: mutScope, Revision: 2}, // the duplicate
		mutation.Dependency{Scope: subject, Revision: 0},
	))
	if len(got) != 3 {
		t.Fatalf("the lock set must dedupe the mutation scope: %+v", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Canonical() >= got[i].Canonical() {
			t.Fatalf("the lock set must be sorted by ScopeKey.Canonical(): %+v", got)
		}
	}
	if only := lockSet(mutScope, nil); len(only) != 1 || only[0] != mutScope {
		t.Fatalf("the trusted path locks the mutation scope alone, got %+v", only)
	}
}
