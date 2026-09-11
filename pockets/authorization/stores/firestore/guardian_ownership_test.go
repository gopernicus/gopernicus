package firestore

import (
	"context"
	"errors"
	"testing"

	mutation "github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/sdk"
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

func TestMutationGuardianPolicySnapshotsConstructionAndRead(t *testing.T) {
	policy := mutation.GuardianPolicy{Rules: []mutation.GuardianRule{{ResourceType: "doc", Relation: "owner", MinAnchors: 1}}}
	store := newMutationStore(nil, policy, false)
	policy.Rules[0].Relation = "caller-change"
	read := store.GuardianPolicy()
	read.Rules[0].Relation = "read-change"
	if got := store.GuardianPolicy().MinDirectAnchors("doc", "owner"); got != 1 {
		t.Fatalf("guardian changed through a caller slice: %d", got)
	}
}

func TestGuardHasRoleRejectsMalformedInputBeforeReads(t *testing.T) {
	scope := mutation.Target{Kind: mutation.TargetResource, Type: "doc", ID: "d"}
	for _, tc := range []struct {
		name                         string
		scope                        mutation.Target
		role, subjectType, subjectID string
	}{
		{"unknown scope", mutation.Target{Kind: "unknown", Type: "doc", ID: "d"}, "viewer", "user", "u"},
		{"missing scope ID", mutation.Target{Kind: mutation.TargetSubject, Type: "user"}, "viewer", "user", "u"},
		{"missing role", scope, "", "user", "u"},
		{"missing subject type", scope, "viewer", "", "u"},
		{"missing subject ID", scope, "viewer", "user", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view := newDecisionView(nil, nil)
			if held, err := view.HasRole(context.Background(), tc.scope, tc.role, tc.subjectType, tc.subjectID); held || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("malformed role query: held=%v error=%v", held, err)
			}
		})
	}
}
