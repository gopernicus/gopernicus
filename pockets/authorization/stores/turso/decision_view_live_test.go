//go:build integration

package turso

import (
	"context"
	"errors"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
)

func TestDecisionViewBoundedCheckMatchesReadSide(t *testing.T) {
	ctx := context.Background()
	db, repos := liveRepos(t)
	for _, cmd := range []mutations.Command{edge(mutations.OpGrant, scopeOf("group", "g1"), "member", relationships.SubjectRef{Type: "user", ID: "alice"}), edge(mutations.OpGrant, scopeOf("group", "g2"), "member", relationships.SubjectRef{Type: "group", ID: "g1", Relation: "member"}), edge(mutations.OpGrant, scopeOf("group", "g3"), "member", relationships.SubjectRef{Type: "group", ID: "g2", Relation: "member"}), edge(mutations.OpGrant, scopeOf("doc", "x"), "editor", relationships.SubjectRef{Type: "group", ID: "g3", Relation: "member"})} {
		mustApplyLive(t, repos.Mutations, cmd)
	}
	for _, bound := range []int{0, 3, 4, 5, 6} {
		want, wantErr := repos.Relationships.CheckRelationWithGroupExpansion(ctx, "doc", "x", "editor", "user", "alice", bound)
		var got bool
		var gotErr error
		if err := db.InTx(ctx, func(tx *tursodb.Tx) error {
			got, gotErr = newDecisionView(tx).CheckRelationBounded(ctx, scopeOf("doc", "x"), "editor", "user", "alice", bound)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if got != want || !errors.Is(gotErr, wantErr) || (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("bound=%d got=(%v,%v) want=(%v,%v)", bound, got, gotErr, want, wantErr)
		}
		if (bound == 3 || bound == 4) && !errors.Is(gotErr, relationships.ErrExpansionBudgetExceeded) {
			t.Fatalf("expected overflow: %v", gotErr)
		}
	}
}
func TestDecisionViewGlobalRoleUsesCheckedPrincipal(t *testing.T) {
	ctx := context.Background()
	db, repos := liveRepos(t)
	if err := repos.Roles.Assign(ctx, roles.Assignment{SubjectType: "user", SubjectID: "alice", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []mutations.Target{scopeOf("doc", "x"), {Kind: mutations.TargetSubject, Type: "user", ID: "bob"}} {
		if err := db.InTx(ctx, func(tx *tursodb.Tx) error {
			view := newDecisionView(tx)
			yes, err := view.HasRole(ctx, target, "admin", "user", "alice")
			if err != nil || !yes {
				t.Fatalf("alice global grant lost: %v %v", yes, err)
			}
			no, err := view.HasRole(ctx, target, "admin", "user", "bob")
			if err != nil || no {
				t.Fatalf("bob inherited alice grant: %v %v", no, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
