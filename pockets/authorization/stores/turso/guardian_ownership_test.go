package turso

import (
	"testing"

	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
)

func TestGuardianOptionSnapshotsPolicy(t *testing.T) {
	policy := mutation.GuardianPolicy{Rules: []mutation.GuardianRule{{ResourceType: "doc", Relation: "owner", MinAnchors: -1}}}
	option := WithGuardianPolicy(policy)
	policy.Rules[0].ResourceType = "changed-before-construction"
	var first, second config
	option(&first)
	option(&second)
	if got := first.guardian.MinDirectAnchors("doc", "owner"); got != 1 {
		t.Fatalf("caller changed captured policy or minimum normalization: %d", got)
	}
	first.guardian.Rules[0].Relation = "first-store-only"
	if got := second.guardian.MinDirectAnchors("doc", "owner"); got != 1 {
		t.Fatalf("reused option shares mutable policy: %d", got)
	}
}
