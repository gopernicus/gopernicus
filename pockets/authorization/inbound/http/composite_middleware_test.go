package authorizationhttp

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

func TestRequireNamedGraphAndRolePermissions(t *testing.T) {
	model := decisions.Model{ResourceTypes: map[string]decisions.ResourceTypeDef{
		"organization": {
			Relations:   map[string]decisions.RelationDef{"member": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]decisions.Expression{"enter": decisions.Direct("member")},
		},
		"project": {
			Relations: map[string]decisions.RelationDef{
				"viewer":       {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}},
				"organization": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "organization"}}},
			},
			Permissions: map[string]decisions.Expression{
				"view":  decisions.Any(decisions.Direct("viewer"), decisions.Through("organization", "enter")),
				"audit": decisions.Any(decisions.RoleIn("auditor"), decisions.Role("auditor")),
			},
		},
	}}
	store := memory.NewTuples()
	seedGuard(t, store,
		guardFact(tuples.On("project", "direct"), "viewer", "user", "alice", ""),
		guardFact(tuples.On("project", "through"), "organization", "organization", "acme", ""),
		guardFact(tuples.On("organization", "acme"), "member", "user", "alice", ""),
		guardFact(tuples.On("project", "scoped-role"), "auditor", "user", "alice", ""),
		guardFact(tuples.Global(), "auditor", "user", "global-auditor", ""),
	)
	adapter := guardAdapter(t, store, decisions.WithModel(model))
	project := Path("project", "projectID")
	gate := adapter.Require(Any(Can("view", project), Can("audit", project)))
	for _, tc := range []struct {
		principal, id string
		status        int
	}{
		{"", "direct", 401}, {"alice", "direct", 204}, {"alice", "through", 204},
		{"alice", "scoped-role", 204}, {"global-auditor", "ungranted", 204},
		{"outsider", "direct", 403}, {"alice", "ungranted", 403}, {"alice", "", 500},
	} {
		t.Run(tc.principal+"/"+tc.id, func(t *testing.T) {
			req := guardRequest(tc.principal)
			req.SetPathValue("projectID", tc.id)
			rec, next := serveGuard(gate, req)
			if rec.Code != tc.status || next != boolInt(tc.status == 204) {
				t.Fatalf("status=%d next=%d body=%s", rec.Code, next, rec.Body)
			}
		})
	}
}
