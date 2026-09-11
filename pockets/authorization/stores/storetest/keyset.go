package storetest

import (
	"context"
	"errors"
	"slices"
	"sort"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
)

// keysetIDs is the id universe every port-level keyset case pages over. Its
// BYTE order (B, Z, _x, a, ~z, é) is NOT the order a locale collation produces —
// a collation folds case and ignores punctuation, so it would interleave these
// ids differently. Byte order is contractual (the engine k-way merges the
// streams with Go string comparison), so a store that sorts or compares under
// the database's default collation fails these cases instead of passing them by
// accident.
var keysetIDs = []string{"B", "a", "_x", "~z", "Z", "é"}

// maxPagedWalkPages stops a broken continuation from spinning forever: it is far
// above the longest walk any fixture here performs (rolesWalkAssignments ids one
// page at a time).
const maxPagedWalkPages = 1000

// keysetOrder is keysetIDs in the order the ports must return them: exactly what
// Go's sort.Strings produces, which is byte order.
func keysetOrder() []string {
	out := slices.Clone(keysetIDs)
	sort.Strings(out)
	return out
}

// pagedLimits are the page sizes a paged walk is proven at: one id at a time
// (the most continuations a result can need), two, an odd size that divides no
// fixture evenly, and one page large enough to hold the whole result.
func pagedLimits(total int) []int {
	return []int{1, 2, 7, max(total, 1)}
}

// =============================================================================
// Port contracts: the keyset half of the four lookup methods
// =============================================================================

// assertKeysetSweep drives ONE lookup port method over the full byte-order id
// set and asserts the shared keyset contract: the complete read is exactly the
// Go-sorted order with each id once, limit caps the page at the head of that
// order, and after is EXCLUSIVE at every position — an id equal to after is
// never returned, and after the last id the continuation is empty.
func assertKeysetSweep(t *testing.T, name string, lookup func(after string, limit int) ([]string, error)) {
	t.Helper()
	want := keysetOrder()

	all, err := lookup("", 100)
	if err != nil {
		t.Fatalf("%s(after %q, limit 100): %v", name, "", err)
	}
	if !slices.Equal(all, want) {
		t.Fatalf("%s must return the DISTINCT ids in BYTE order: want %v, got %v", name, want, all)
	}

	for _, limit := range []int{1, 2, len(want) - 1} {
		got, err := lookup("", limit)
		if err != nil {
			t.Fatalf("%s(limit %d): %v", name, limit, err)
		}
		if !slices.Equal(got, want[:limit]) {
			t.Fatalf("%s(limit %d) must cap the head of the order: want %v, got %v", name, limit, want[:limit], got)
		}
	}

	for i, after := range want {
		got, err := lookup(after, 100)
		if err != nil {
			t.Fatalf("%s(after %q): %v", name, after, err)
		}
		if !slices.Equal(got, want[i+1:]) {
			t.Fatalf("%s(after %q) must be EXCLUSIVE: want %v, got %v", name, after, want[i+1:], got)
		}
	}

	got, err := lookup(want[0], 2)
	if err != nil {
		t.Fatalf("%s(after %q, limit 2): %v", name, want[0], err)
	}
	if !slices.Equal(got, want[1:3]) {
		t.Fatalf("%s(after %q, limit 2): want %v, got %v", name, want[0], want[1:3], got)
	}
}

// runRelationshipKeyset is the Relationship/LookupKeyset family: the keyset
// contract of the three relationship lookup methods (after exclusive, limit
// capping, distinct ids in byte order) plus the union-relation descendant walk.
func runRelationshipKeyset(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()

	t.Run("Direct", func(t *testing.T) {
		s := newRepos(t).Relationships
		tuples := make([]relationships.CreateRelationship, 0, len(keysetIDs)+2)
		for _, id := range keysetIDs {
			tuples = append(tuples, ct("doc", id, "viewer", "user", "u1"))
		}
		// doc:a is reached a SECOND time by another relation of the queried set,
		// through a group the principal belongs to: it must be returned ONCE.
		tuples = append(tuples,
			ct("group", "g", "member", "user", "u1"),
			ctUserset("doc", "a", "editor", "group", "g", "member"),
		)
		mustCreate(t, s, tuples...)

		assertKeysetSweep(t, "LookupResourceIDs", func(after string, limit int) ([]string, error) {
			return s.LookupResourceIDs(ctx, "doc", []string{"viewer", "editor"}, "user", "u1", after, limit)
		})
	})

	t.Run("ByRelationTarget", func(t *testing.T) {
		s := newRepos(t).Relationships
		tuples := make([]relationships.CreateRelationship, 0, len(keysetIDs)+1)
		for _, id := range keysetIDs {
			tuples = append(tuples, ct("space", id, "parent", "space", "p1"))
		}
		// space:a points at BOTH queried targets: it must be returned ONCE.
		tuples = append(tuples, ct("space", "a", "parent", "space", "p2"))
		mustCreate(t, s, tuples...)

		assertKeysetSweep(t, "LookupResourceIDsByRelationTarget", func(after string, limit int) ([]string, error) {
			return s.LookupResourceIDsByRelationTarget(ctx, "space", "parent", "space", []string{"p1", "p2"}, after, limit)
		})
	})

	t.Run("Descendants", func(t *testing.T) {
		s := newRepos(t).Relationships
		tuples := make([]relationships.CreateRelationship, 0, len(keysetIDs)+1)
		for _, id := range keysetIDs {
			tuples = append(tuples, ct("space", id, "parent", "space", "r1"))
		}
		// space:a descends from BOTH roots, by a different relation each time: the
		// closure is a SET, so it must be returned ONCE.
		tuples = append(tuples, ct("space", "a", "folder", "space", "r2"))
		mustCreate(t, s, tuples...)

		assertKeysetSweep(t, "LookupDescendantResourceIDs", func(after string, limit int) ([]string, error) {
			return s.LookupDescendantResourceIDs(ctx, "space", []string{"parent", "folder"}, "space", []string{"r1", "r2"}, after, limit)
		})
	})

	t.Run("DescendantsFollowTheRelationUnion", func(t *testing.T) {
		s := newRepos(t).Relationships
		// One path that ALTERNATES the two relations: s1 ←parent– s2 ←folder– s3
		// ←parent– s4. The walk follows their UNION, so ONE call returns the whole
		// chain — the engine no longer runs a per-relation fixpoint.
		mustCreate(t, s,
			ct("space", "s2", "parent", "space", "s1"),
			ct("space", "s3", "folder", "space", "s2"),
			ct("space", "s4", "parent", "space", "s3"),
		)
		walk := func(relations []string, after string, limit int) []string {
			t.Helper()
			ids, err := s.LookupDescendantResourceIDs(ctx, "space", relations, "space", []string{"s1"}, after, limit)
			if err != nil {
				t.Fatalf("LookupDescendantResourceIDs(%v, after %q, limit %d): %v", relations, after, limit, err)
			}
			return ids
		}

		both := []string{"parent", "folder"}
		if got, want := walk(both, "", 100), []string{"s2", "s3", "s4"}; !slices.Equal(got, want) {
			t.Fatalf("the alternating path must complete in ONE call: want %v, got %v", want, got)
		}
		// A single relation stops where the path changes relation — proof the union
		// above is what carried the walk past s2.
		if got, want := walk([]string{"parent"}, "", 100), []string{"s2"}; !slices.Equal(got, want) {
			t.Fatalf("a single-relation walk must stop at the relation change: want %v, got %v", want, got)
		}
		// after/limit page the sorted CLOSURE, not the work that computed it.
		if got, want := walk(both, "s2", 100), []string{"s3", "s4"}; !slices.Equal(got, want) {
			t.Fatalf("after on the closure: want %v, got %v", want, got)
		}
		if got, want := walk(both, "", 2), []string{"s2", "s3"}; !slices.Equal(got, want) {
			t.Fatalf("limit on the closure: want %v, got %v", want, got)
		}
		if got, want := walk(both, "s2", 1), []string{"s3"}; !slices.Equal(got, want) {
			t.Fatalf("after+limit on the closure: want %v, got %v", want, got)
		}
	})
}

// runRolesKeyset is the Roles/RolesLookupKeyset family: the keyset contract of
// role.Storer.LookupResourceIDsBySubjectAndRoles, including the two answers no
// other lookup has — a global grant of a QUERIED role is unrestricted with no
// ids at all, and an empty role set is nothing.
func runRolesKeyset(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()
	queried := []string{"auditor", "viewer"}

	t.Run("Scoped", func(t *testing.T) {
		s := newRepos(t).Roles
		for _, id := range keysetIDs {
			assign(t, s, "user", "u1", "auditor", "project", id)
		}
		// project:a is granted by a SECOND queried role: it must be returned ONCE.
		assign(t, s, "user", "u1", "viewer", "project", "a")
		// A role OUTSIDE the queried set grants nothing here.
		assign(t, s, "user", "u1", "editor", "project", "p_unlisted")
		// A queried role on ANOTHER resource type never leaks in.
		assign(t, s, "user", "u1", "auditor", "dataset", "ds1")

		assertKeysetSweep(t, "LookupResourceIDsBySubjectAndRoles", func(after string, limit int) ([]string, error) {
			ids, unrestricted, err := s.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "project", queried, after, limit)
			if unrestricted {
				t.Fatalf("scoped grants must never report unrestricted")
			}
			return ids, err
		})
	})

	t.Run("GlobalQueriedRoleIsUnrestricted", func(t *testing.T) {
		s := newRepos(t).Roles
		assign(t, s, "user", "u1", "viewer", "", "")
		assign(t, s, "user", "u1", "auditor", "project", "p1")

		ids, unrestricted, err := s.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "project", queried, "", 100)
		if err != nil {
			t.Fatalf("LookupResourceIDsBySubjectAndRoles: %v", err)
		}
		if !unrestricted {
			t.Fatalf("a global grant of a queried role must be unrestricted, got ids %v", ids)
		}
		if ids != nil {
			t.Fatalf("unrestricted names no ids to page, got %v", ids)
		}
	})

	t.Run("GlobalUnqueriedRoleIsNotUnrestricted", func(t *testing.T) {
		s := newRepos(t).Roles
		// Held globally, but the query does not name it: it grants nothing.
		assign(t, s, "user", "u1", "editor", "", "")
		assign(t, s, "user", "u1", "auditor", "project", "p1")

		ids, unrestricted, err := s.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "project", queried, "", 100)
		if err != nil {
			t.Fatalf("LookupResourceIDsBySubjectAndRoles: %v", err)
		}
		if unrestricted {
			t.Fatalf("a global grant of an UNQUERIED role must not be unrestricted")
		}
		if want := []string{"p1"}; !slices.Equal(ids, want) {
			t.Fatalf("scoped page = %v, want %v", ids, want)
		}
	})

	t.Run("EmptyRolesIsNothing", func(t *testing.T) {
		s := newRepos(t).Roles
		// Even a global grant: with no roles queried there is nothing to satisfy.
		assign(t, s, "user", "u1", "viewer", "", "")

		ids, unrestricted, err := s.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "project", nil, "", 100)
		if err != nil || unrestricted || ids != nil {
			t.Fatalf("empty roles must be (nil, false, nil), got (%v, %v, %v)", ids, unrestricted, err)
		}
	})
}

// =============================================================================
// Engine: paged enumeration (LookupResourceIDPage)
// =============================================================================

// walkPaged pages ONE enumeration to exhaustion at the given page size and
// returns the concatenation of every page, asserting the per-page shape: IDs is
// non-nil, a page with more to come is FULL and
// carries a continuation, the last page carries none, and no id repeats across
// pages.
func walkPaged(t *testing.T, svc authorization.Components, principal authmodel.PrincipalRef, permission, resourceType string, limit int) []string {
	t.Helper()
	ctx := context.Background()

	var ids []string
	seen := make(map[string]bool)
	cursor := ""
	for page := 0; ; page++ {
		if page > maxPagedWalkPages {
			t.Fatalf("LookupResourceIDPage(%s:%s, %s, %s, limit %d) did not terminate within %d pages",
				principal.Type, principal.ID, permission, resourceType, limit, maxPagedWalkPages)
		}
		res, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
			Principal:    principal,
			Permission:   permission,
			ResourceType: resourceType,
			Limit:        limit,
			After:        cursor,
		})
		if err != nil {
			t.Fatalf("LookupResourceIDPage(%s:%s, %s, %s, limit %d, page %d): %v",
				principal.Type, principal.ID, permission, resourceType, limit, page, err)
		}
		if res.Unrestricted {
			t.Fatalf("page %d is unrestricted; a paged walk fixture must be enumerable", page)
		}
		if res.IDs == nil {
			t.Fatalf("page %d: IDs must be non-nil", page)
		}
		if len(res.IDs) > limit {
			t.Fatalf("page %d returned %d ids, above the requested limit %d", page, len(res.IDs), limit)
		}
		if res.HasMore {
			if len(res.IDs) != limit {
				t.Fatalf("page %d has more to come but is short: %d ids for limit %d", page, len(res.IDs), limit)
			}
			if res.NextCursor == "" {
				t.Fatalf("page %d has more to come but carries no continuation", page)
			}
		} else if res.NextCursor != "" {
			t.Fatalf("the last page must carry no continuation, got %q", res.NextCursor)
		}
		for _, id := range res.IDs {
			if seen[id] {
				t.Fatalf("id %s appeared on more than one page", id)
			}
			seen[id] = true
		}
		ids = append(ids, res.IDs...)
		if !res.HasMore {
			return ids
		}
		cursor = res.NextCursor
	}
}

// assertPagedParityAt is the A5 invariant at the given page sizes: walking
// LookupResourceIDPage to exhaustion concatenates to EXACTLY the plain
// LookupAllResourceIDs result — same ids, same order, no repeats — and every id it
// yields independently passes Check. It returns the plain result.
func assertPagedParityAt(t *testing.T, svc authorization.Components, principal authmodel.PrincipalRef, permission, resourceType string, limits []int) []string {
	t.Helper()
	ctx := context.Background()

	plain, err := svc.Decisions.LookupAllResourceIDs(ctx, principal, permission, resourceType)
	if err != nil {
		t.Fatalf("LookupAllResourceIDs(%s:%s, %s, %s): %v", principal.Type, principal.ID, permission, resourceType, err)
	}
	for _, limit := range limits {
		got := walkPaged(t, svc, principal, permission, resourceType, limit)
		if !slices.Equal(got, plain.IDs) {
			t.Fatalf("paged walk at limit %d (%s:%s, %s, %s) = %v, want the plain result %v",
				limit, principal.Type, principal.ID, permission, resourceType, got, plain.IDs)
		}
	}
	for _, id := range plain.IDs {
		res, err := svc.Decisions.Check(ctx, authmodel.CheckRequest{
			Principal:  principal,
			Permission: permission,
			Resource:   authmodel.Resource{Type: resourceType, ID: id},
		})
		if err != nil {
			t.Fatalf("Check(paged id %s): %v", id, err)
		}
		if !res.Allowed {
			t.Fatalf("the paged walk yielded %s:%s on %s:%s but Check denies it (%s)", principal.Type, principal.ID, resourceType, id, res.Reason)
		}
	}
	return plain.IDs
}

// assertPagedParity is assertPagedParityAt at the standard page sizes.
func assertPagedParity(t *testing.T, svc authorization.Components, principal authmodel.PrincipalRef, permission, resourceType string) []string {
	t.Helper()
	plain, err := svc.Decisions.LookupAllResourceIDs(context.Background(), principal, permission, resourceType)
	if err != nil {
		t.Fatalf("LookupAllResourceIDs(%s:%s, %s, %s): %v", principal.Type, principal.ID, permission, resourceType, err)
	}
	return assertPagedParityAt(t, svc, principal, permission, resourceType, pagedLimits(len(plain.IDs)))
}

// firstPage returns the FIRST page of a paged enumeration and asserts it has a
// continuation — the cursor the refusal cases present elsewhere.
func firstPage(t *testing.T, svc authorization.Components, principal authmodel.PrincipalRef, permission, resourceType string, limit int) decisions.ResourceIDPage {
	t.Helper()
	res, err := svc.Decisions.LookupResourceIDPage(context.Background(), decisions.ResourceIDPageRequest{
		Principal:    principal,
		Permission:   permission,
		ResourceType: resourceType,
		Limit:        limit,
	})
	if err != nil {
		t.Fatalf("LookupResourceIDPage(%s:%s, %s, %s, limit %d): %v", principal.Type, principal.ID, permission, resourceType, limit, err)
	}
	if !res.HasMore || res.NextCursor == "" {
		t.Fatalf("the fixture must have a second page: %+v", res)
	}
	return res
}

// assertRefusedCursor asserts a presented cursor is refused as ErrInvalidCursor
// — always ErrInvalidInput (HTTP 400), never an evaluation outcome, so the
// client learns only that it must restart from page one.
func assertRefusedCursor(t *testing.T, svc authorization.Components, what string, req decisions.ResourceIDPageRequest) {
	t.Helper()
	_, err := svc.Decisions.LookupResourceIDPage(context.Background(), req)
	if !errors.Is(err, authmodel.ErrInvalidCursor) {
		t.Fatalf("cursor presented %s: want ErrInvalidCursor, got %v", what, err)
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("cursor presented %s: ErrInvalidCursor must wrap sdk.ErrInvalidInput, got %v", what, err)
	}
}

// pagingSchema is the paged-enumeration fixture schema. doc.view carries all
// three top-level stream shapes at once — a direct relation, a non-self Through
// (the intermediate node whose overflow is the documented cliff), and TWO
// same-permission self relations so one hierarchy path can ALTERNATE them —
// while doc.edit and org.view give the cursor-binding cases a different
// permission and a different resource type to present a cursor against.
func pagingSchema() relationships.Schema {
	return relationships.NewSchema([]relationships.ResourceSchema{
		{Name: "org", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"admin": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"view": relationships.AnyOf(relationships.Direct("admin")),
			},
		}},
		{Name: "doc", Def: relationships.ResourceTypeDef{
			Relations: map[string]relationships.RelationDef{
				"viewer": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
				"editor": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}},
				"parent": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "doc"}}},
				"folder": {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "doc"}}},
				"org":    {AllowedSubjects: []relationships.SubjectTypeRef{{Type: "org"}}},
			},
			Permissions: map[string]relationships.PermissionRule{
				"view": relationships.AnyOf(
					relationships.Direct("viewer"),
					relationships.Through("org", "view"),
					relationships.Through("parent", "view"),
					relationships.Through("folder", "view"),
				),
				"edit": relationships.AnyOf(relationships.Direct("editor")),
			},
		}},
	})
}

// changedPagingSchema is pagingSchema after a deploy adds one relation. doc.view
// is untouched, so the enumeration is identical — the only thing that changed is
// the schema DIGEST, which is exactly what a cursor is bound to.
func changedPagingSchema() relationships.Schema {
	schema := pagingSchema()
	doc := schema.ResourceTypes["doc"]
	doc.Relations["commenter"] = relationships.RelationDef{AllowedSubjects: []relationships.SubjectTypeRef{{Type: "user"}}}
	doc.Permissions["edit"] = relationships.AnyOf(relationships.Direct("editor"), relationships.Direct("commenter"))
	schema.ResourceTypes["doc"] = doc
	return schema
}

func newPagingService(t *testing.T, repos authorization.Repositories, schema relationships.Schema, limits authmodel.EvaluationLimits) authorization.Components {
	t.Helper()
	comps, err := authorization.New(repos, authorization.WithRelationshipModel(schema), authorization.WithLimits(limits))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return comps
}

// runLookupPagedParity is the Parity/LookupPagedParity family (plan A5): the
// relationship kind's PAGED enumeration proved equal to its plain one over the
// union of pages, plus the cursor-binding and budget boundaries that make the
// continuation safe to hand a client.
func runLookupPagedParity(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()

	t.Run("OracleUniverse", func(t *testing.T) {
		repos := newRepos(t)
		mustCreate(t, repos.Relationships, oracleFixture()...)
		svc := newOracleService(t, repos, generousLimits())

		for _, principal := range oraclePrincipals() {
			for _, resourceType := range []string{"doc", "org"} {
				assertPagedParity(t, svc, principal, "view", resourceType)
			}
		}
	})

	t.Run("HierarchyDescendantBeforeRoot", func(t *testing.T) {
		repos := newRepos(t)
		// z_root is the ONLY granted root and sorts LAST; its descendants sort
		// before it, and one path alternates the two self relations
		// (z_root ←folder– m_mid ←parent– b_deep). A page therefore cannot be
		// filled from the root set alone.
		mustCreate(t, repos.Relationships,
			ct("doc", "z_root", "viewer", "user", "u_page"),
			ct("doc", "a_leaf", "parent", "doc", "z_root"),
			ct("doc", "m_mid", "folder", "doc", "z_root"),
			ct("doc", "b_deep", "parent", "doc", "m_mid"),
		)
		svc := newPagingService(t, repos, pagingSchema(), generousLimits())

		got := assertPagedParity(t, svc, authmodel.PrincipalRef{Type: "user", ID: "u_page"}, "view", "doc")
		if want := []string{"a_leaf", "b_deep", "m_mid", "z_root"}; !slices.Equal(got, want) {
			t.Fatalf("hierarchy enumeration = %v, want %v (descendants before their root)", got, want)
		}
	})

	t.Run("CursorIsBoundToItsQuery", func(t *testing.T) {
		repos := newRepos(t)
		mustCreate(t, repos.Relationships,
			ct("doc", "d1", "viewer", "user", "u_a"),
			ct("doc", "d2", "viewer", "user", "u_a"),
			ct("doc", "d1", "viewer", "user", "u_b"),
			ct("doc", "d2", "viewer", "user", "u_b"),
			ct("doc", "e1", "editor", "user", "u_a"),
			ct("doc", "e2", "editor", "user", "u_a"),
			ct("org", "o1", "admin", "user", "u_a"),
			ct("org", "o2", "admin", "user", "u_a"),
		)
		svc := newPagingService(t, repos, pagingSchema(), generousLimits())
		principal := authmodel.PrincipalRef{Type: "user", ID: "u_a"}
		cursor := firstPage(t, svc, principal, "view", "doc", 1).NextCursor

		// Every one of these enumerations exists and has its own second page; none
		// of them shares an id order with the cursor's query.
		assertRefusedCursor(t, svc, "by another principal", decisions.ResourceIDPageRequest{
			Principal: authmodel.PrincipalRef{Type: "user", ID: "u_b"}, Permission: "view", ResourceType: "doc", Limit: 1, After: cursor,
		})
		assertRefusedCursor(t, svc, "on another permission", decisions.ResourceIDPageRequest{
			Principal: principal, Permission: "edit", ResourceType: "doc", Limit: 1, After: cursor,
		})
		assertRefusedCursor(t, svc, "on another resource type", decisions.ResourceIDPageRequest{
			Principal: principal, Permission: "view", ResourceType: "org", Limit: 1, After: cursor,
		})

		// A deploy that changes the schema invalidates in-flight cursors: same
		// stores, same query, different model digest.
		changed := newPagingService(t, repos, changedPagingSchema(), generousLimits())
		assertRefusedCursor(t, changed, "after a schema change", decisions.ResourceIDPageRequest{
			Principal: principal, Permission: "view", ResourceType: "doc", Limit: 1, After: cursor,
		})

		// The refusals are binding, not breakage: the cursor's OWN query still pages.
		second, err := svc.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
			Principal: principal, Permission: "view", ResourceType: "doc", Limit: 1, After: cursor,
		})
		if err != nil {
			t.Fatalf("the cursor's own query must still page: %v", err)
		}
		if want := []string{"d2"}; !slices.Equal(second.IDs, want) || second.HasMore || second.NextCursor != "" {
			t.Fatalf("second page = %+v, want IDs %v and no continuation", second, want)
		}
	})

	t.Run("IntermediateOverflowIsErrorOnEveryPage", func(t *testing.T) {
		repos := newRepos(t)
		// u_many administers 3 orgs; doc.view reaches docs Through them. The org
		// set is an INTERMEDIATE node: it is complete and budget-bounded on every
		// page, so at MaxLookupResults=2 the enumeration is indeterminate — the
		// documented A3 cliff — no matter how small the page is.
		mustCreate(t, repos.Relationships,
			ct("org", "o1", "admin", "user", "u_many"),
			ct("org", "o2", "admin", "user", "u_many"),
			ct("org", "o3", "admin", "user", "u_many"),
			ct("doc", "d1", "org", "org", "o1"),
			ct("doc", "d2", "org", "org", "o2"),
			ct("doc", "d3", "org", "org", "o3"),
		)
		svc := newPagingService(t, repos, pagingSchema(), authmodel.EvaluationLimits{MaxLookupResults: 2})
		req := decisions.ResourceIDPageRequest{
			Principal: authmodel.PrincipalRef{Type: "user", ID: "u_many"}, Permission: "view", ResourceType: "doc", Limit: 1,
		}
		for attempt := 0; attempt < 2; attempt++ {
			if _, err := svc.Decisions.LookupResourceIDPage(ctx, req); !errors.Is(err, authmodel.ErrEvaluationLimit) {
				t.Fatalf("attempt %d: 3 intermediate orgs over MaxLookupResults=2 must be ErrEvaluationLimit, got %v", attempt, err)
			}
		}

		// The page size is bounded by the same budget, and above it is invalid
		// INPUT — never an evaluation outcome.
		req.Limit = 3
		if _, err := svc.Decisions.LookupResourceIDPage(ctx, req); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("limit 3 over MaxLookupResults=2: want sdk.ErrInvalidInput, got %v", err)
		}
	})
}

// runRolesPagedParity is the Parity/RolesPagedParity family: the roles kind's
// paged enumeration over its own indexed resource-id lookup, proved equal to the
// plain assignment walk, plus the answers only this kind has (a global grant) and
// its own model-digest cursor binding.
func runRolesPagedParity(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()

	t.Run("ScopedWalk", func(t *testing.T) {
		repos := rolesOnly(newRepos(t))
		comps := newRoleModelService(t, repos, authorization.WithRoleModel(decisionRoleModel()))

		// The same multi-page fixture RolesMultiPageWalk seeds, now paged by the
		// DECISION surface rather than internally: more assignments than one
		// enumeration page, seeded through the role.Storer port (see that case for
		// why the mutation seam is not paid per row).
		want := make([]string, 0, rolesWalkAssignments)
		for i := 0; i < rolesWalkAssignments; i++ {
			id := walkProjectID(i)
			want = append(want, id)
			assign(t, repos.Roles, "user", "u_walk", "auditor", "project", id)
		}
		// A non-granting role and another resource type inside the same subject's
		// assignments: neither may enter any page.
		assign(t, repos.Roles, "user", "u_walk", "viewer", "project", "p_view_only")
		assign(t, repos.Roles, "user", "u_walk", "auditor", "dataset", "ds1")

		got := assertPagedParityAt(t, comps, authmodel.PrincipalRef{Type: "user", ID: "u_walk"}, "audit", "project", []int{1, 7, 50})
		if !slices.Equal(got, want) {
			t.Fatalf("scoped walk returned %d ids, want the %d seeded assignments", len(got), len(want))
		}
	})

	t.Run("DuplicateGrantingRolesAppearOnce", func(t *testing.T) {
		repos := rolesOnly(newRepos(t))
		comps := newRoleModelService(t, repos, authorization.WithRoleModel(decisionRoleModel()))
		// project/view is granted by BOTH auditor and viewer; u1 holds both on p1.
		assign(t, repos.Roles, "user", "u1", "auditor", "project", "p1")
		assign(t, repos.Roles, "user", "u1", "viewer", "project", "p1")
		assign(t, repos.Roles, "user", "u1", "auditor", "project", "p2")

		got := assertPagedParityAt(t, comps, authmodel.PrincipalRef{Type: "user", ID: "u1"}, "view", "project", []int{1, 2})
		if want := []string{"p1", "p2"}; !slices.Equal(got, want) {
			t.Fatalf("two granting roles on one resource must page it ONCE: got %v, want %v", got, want)
		}
	})

	t.Run("GlobalGrantHasNoPage", func(t *testing.T) {
		repos := rolesOnly(newRepos(t))
		comps := newRoleModelService(t, repos, authorization.WithRoleModel(decisionRoleModel()))
		grantRole(t, repos, comps.SystemMutator, "user", "u_global", "viewer", "", "")

		res, err := comps.Decisions.LookupResourceIDPage(ctx, decisions.ResourceIDPageRequest{
			Principal: authmodel.PrincipalRef{Type: "user", ID: "u_global"}, Permission: "view", ResourceType: "project", Limit: 1,
		})
		if err != nil {
			t.Fatalf("LookupResourceIDPage: %v", err)
		}
		if !res.Unrestricted || len(res.IDs) != 0 || res.HasMore || res.NextCursor != "" {
			t.Fatalf("a global grant names no ids to page: %+v, want unrestricted with no ids and no continuation", res)
		}
	})

	t.Run("CursorRefusedAfterRoleModelChange", func(t *testing.T) {
		repos := rolesOnly(newRepos(t))
		comps := newRoleModelService(t, repos, authorization.WithRoleModel(decisionRoleModel()))
		grantRole(t, repos, comps.SystemMutator, "user", "u1", "auditor", "project", "p1")
		grantRole(t, repos, comps.SystemMutator, "user", "u1", "auditor", "project", "p2")

		principal := authmodel.PrincipalRef{Type: "user", ID: "u1"}
		cursor := firstPage(t, comps, principal, "audit", "project", 1).NextCursor

		// The roles kind binds its cursors to a deterministic digest of its own
		// compiled model, symmetrically with the relationship schema digest.
		changed := newRoleModelService(t, repos, authorization.WithRoleModel(changedDecisionRoleModel()))
		assertRefusedCursor(t, changed, "after a role-model change", decisions.ResourceIDPageRequest{
			Principal: principal, Permission: "audit", ResourceType: "project", Limit: 1, After: cursor,
		})
	})
}

// changedDecisionRoleModel is decisionRoleModel after a deploy adds one role to
// the same resource type — a different compiled model, and therefore a different
// digest and a different cursor fingerprint.
func changedDecisionRoleModel() authmodel.RoleModel {
	return authmodel.RoleModel{ResourceTypes: map[string]authmodel.RoleTypeDef{
		"project": {
			Roles: []string{"auditor", "steward", "viewer"},
			Permissions: map[string][]string{
				"audit": {"auditor", "steward"},
				"view":  {"auditor", "viewer"},
			},
		},
	}}
}
