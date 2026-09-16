package pgx

import (
	"testing"

	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
)

func TestIntegrityOptionSnapshotsPolicy(t *testing.T) {
	policy := mutation.IntegrityPolicy{Rules: []mutation.IntegrityRule{{ResourceType: "doc", Relation: "owner", MinSubjects: -1}}}
	option := WithIntegrityPolicy(policy)
	policy.Rules[0].ResourceType = "changed-before-construction"
	var first, second config
	option(&first)
	option(&second)
	if got := first.integrity.MinDirectSubjects("doc", "owner"); got != 1 {
		t.Fatalf("caller changed captured policy or minimum normalization: %d", got)
	}
	first.integrity.Rules[0].Relation = "first-store-only"
	if got := second.integrity.MinDirectSubjects("doc", "owner"); got != 1 {
		t.Fatalf("reused option shares mutable policy: %d", got)
	}
}
