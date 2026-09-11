//go:build integration && !live

package firestore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/integrations/datastores/firestore/firestoretest"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	authroles "github.com/gopernicus/gopernicus/pockets/authorization/logic/roles"
	"github.com/gopernicus/gopernicus/sdk"
	listing "github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The A3a/A3b fixtures and the cases the shared conformance suite does not
// reach: the document shape a role grant writes, natural role_key ordering, the
// unrestricted probe's read budget, and the effective listing's page-boundary
// and reverse/offset/count behaviour.

// newRoles opens this train's emulator database, clears it, and returns a fresh
// role store over it.
func newRoles(t *testing.T) (*firestoredb.DB, *roleStore) {
	t.Helper()
	db := firestoretest.OpenDatabase(t, emulatorDatabase)
	firestoretest.Reset(t, db)
	return db, newRoleStore(db, false)
}

// ra is the local assignment constructor.
func ra(subjectID, roleName, resourceType, resourceID string) authroles.Assignment {
	return authroles.Assignment{SubjectType: "user", SubjectID: subjectID, Role: roleName, ResourceType: resourceType, ResourceID: resourceID}
}

// seedRoles writes grants through putRole — the SAME helper the write path uses,
// so a fixture cannot disagree with runtime writes about derived keys.
func seedRoles(t *testing.T, db *firestoredb.DB, in ...authroles.Assignment) {
	t.Helper()
	ctx := context.Background()
	w := db.WriterFrom(ctx)
	for _, a := range in {
		row := newRoleDoc(a)
		if err := putRole(ctx, db, w, row); err != nil {
			t.Fatalf("seed %s:%s %s on %s/%s: %v", a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID, err)
		}
	}
}

// TestAssignWritesTheDocumentShape pins what a grant stores: the two equality
// keys, both contractual sort keys, and no creation timestamp. A
// derived key that disagreed with its own row would make every list and lookup
// silently wrong, and no port-level assertion could see it.
func TestAssignWritesTheDocumentShape(t *testing.T) {
	ctx := context.Background()
	db, s := newRoles(t)

	a := ra("u/1", "editor/lead", "doc", "d1")
	if err := s.Assign(ctx, a); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	snap, err := db.ReaderFrom(ctx).Get(ctx, roleRef(db, a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	row, err := decodeRole(snap)
	if err != nil {
		t.Fatalf("decodeRole: %v", err)
	}
	if want := roleSubjectKey(a.SubjectType, a.SubjectID); row.SubjectKey != want {
		t.Errorf("subject_key = %q, want %q", row.SubjectKey, want)
	}
	if want := resourceKey(a.ResourceType, a.ResourceID); row.ResourceKey != want {
		t.Errorf("resource_key = %q, want %q", row.ResourceKey, want)
	}
	if want := roleKey(a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID); row.RoleKey != want {
		t.Errorf("role_key = %q, want %q", row.RoleKey, want)
	}
	if want := grantKey(a.SubjectType, a.SubjectID, a.Role); row.GrantKey != want {
		t.Errorf("grant_key = %q, want %q", row.GrantKey, want)
	}
	if _, exists := snap.Data()["created_at"]; exists {
		t.Error("role document stores removed created_at metadata")
	}
	if row.ResourceType != "doc" || row.ResourceID != "d1" || row.Role != a.Role || row.SubjectID != a.SubjectID {
		t.Errorf("stored row lost an original field: %+v", row)
	}
}

// TestAssignIsIdempotentAndUnassignAbsentIsNil covers the two idempotency rules
// at the document level: a duplicate Assign leaves the ORIGINAL document
// untouched, and an Unassign of an absent grant is nil
// with no precondition turning absence into an error.
func TestAssignIsIdempotentAndUnassignAbsentIsNil(t *testing.T) {
	ctx := context.Background()
	db, s := newRoles(t)
	a := ra("u1", "editor", "", "")

	if err := s.Unassign(ctx, a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID); err != nil {
		t.Fatalf("Unassign of an absent grant must be nil, got %v", err)
	}

	if err := s.Assign(ctx, a); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	ref := roleRef(db, a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID)
	first, err := db.ReaderFrom(ctx).Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	if err := s.Assign(ctx, a); err != nil {
		t.Fatalf("duplicate Assign: %v", err)
	}
	second, err := db.ReaderFrom(ctx).Get(ctx, ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !second.UpdateTime.Equal(first.UpdateTime) {
		t.Fatalf("duplicate Assign rewrote the document (update time moved %v → %v)", first.UpdateTime, second.UpdateTime)
	}

	if err := s.Unassign(ctx, a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID); err != nil {
		t.Fatalf("Unassign: %v", err)
	}
	if err := s.Unassign(ctx, a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID); err != nil {
		t.Fatalf("repeat Unassign: %v", err)
	}
	if ok, err := s.HasExactRole(ctx, a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID); err != nil || ok {
		t.Fatalf("HasExactRole after Unassign = %v, %v", ok, err)
	}
}

// TestRoleListingsBreakTiesOnRoleKey checks raw byte ordering of natural role keys.
func TestRoleListingsBreakTiesOnRoleKey(t *testing.T) {
	ctx := context.Background()
	db, s := newRoles(t)

	roles := []string{"Zed", "_x", "admin/lead", "admin_lead", "éditeur", "~last"}
	seeds := make([]authroles.Assignment, 0, len(roles))
	for _, name := range roles {
		seeds = append(seeds, ra("u1", name, "doc", "d1"))
	}
	seedRoles(t, db, seeds...)

	// The default order is ascending natural role_key.
	keys := make([]string, 0, len(roles))
	for _, name := range roles {
		keys = append(keys, roleKey("user", "u1", name, "doc", "d1"))
	}
	slices.Sort(keys)

	for _, tc := range []struct {
		name string
		list func(listing.Request) (listing.Page[authroles.Assignment], error)
	}{
		{"ListBySubject", func(req listing.Request) (listing.Page[authroles.Assignment], error) {
			return s.ListBySubject(ctx, "user", "u1", req)
		}},
		{"ListByResource", func(req listing.Request) (listing.Page[authroles.Assignment], error) {
			return s.ListByResource(ctx, "doc", "d1", req)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// One page: the whole tied batch in role_key DESC order.
			page, err := tc.list(listing.Request{Limit: 100})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if got := assignmentKeys(page.Items); !slices.Equal(got, keys) {
				t.Fatalf("tied batch order = %q, want %q", got, keys)
			}

			// Paged two at a time: the cursor must carry the TIEBREAK, or a tied
			// page repeats or skips rows.
			var walked []string
			cursor := ""
			for i := 0; i < len(keys); i++ {
				page, err := tc.list(listing.Request{Limit: 2, Cursor: cursor})
				if err != nil {
					t.Fatalf("page %d: %v", i, err)
				}
				walked = append(walked, assignmentKeys(page.Items)...)
				if !page.HasMore {
					break
				}
				cursor = page.NextCursor
			}
			if !slices.Equal(walked, keys) {
				t.Fatalf("paged walk = %q, want %q", walked, keys)
			}
		})
	}
}

// assignmentKeys projects a page to its role_key values, recomputed from the
// returned rows so the assertion reads the PORT's answer rather than the store's
// internal field.
func assignmentKeys(items []authroles.Assignment) []string {
	out := make([]string, 0, len(items))
	for _, a := range items {
		out = append(out, roleKey(a.SubjectType, a.SubjectID, a.Role, a.ResourceType, a.ResourceID))
	}
	return out
}

// TestGlobalRoleProbeIssuesNoQuery pins the short-circuit's read budget: the
// unrestricted probe is a GetAll on computed document ids, so it issues ZERO
// queries and exactly one document read per queried role — no index, no chunk
// arithmetic, and nothing that could scan the subject's whole grant set. The
// count is taken at the connector Reader seam every read of this store goes
// through.
func TestGlobalRoleProbeIssuesNoQuery(t *testing.T) {
	ctx := context.Background()
	db, s := newRoles(t)

	seedRoles(t, db,
		ra("u1", "viewer", "", ""),
		ra("u1", "auditor", "project", "p1"),
	)

	counter := &countingReader{reader: db.ReaderFrom(ctx)}
	roles := []string{"auditor", "viewer"}
	global, err := anyGlobalRole(ctx, db, counter, "user", "u1", roles)
	if err != nil {
		t.Fatalf("anyGlobalRole: %v", err)
	}
	if !global {
		t.Fatal("a global grant of a queried role must be reported")
	}
	if counter.queries != 0 {
		t.Fatalf("the unrestricted probe issued %d queries, want 0", counter.queries)
	}
	if counter.gets != len(roles) {
		t.Fatalf("the unrestricted probe read %d documents, want %d (one per queried role)", counter.gets, len(roles))
	}

	// Through the port: unrestricted names no ids at all.
	ids, unrestricted, err := s.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "project", roles, "", 100)
	if err != nil || !unrestricted || ids != nil {
		t.Fatalf("lookup = (%v, %v, %v), want (nil, true, nil)", ids, unrestricted, err)
	}

	// A global grant of an UNQUERIED role is not a short-circuit, and the scoped
	// answer is the ordinary one.
	ids, unrestricted, err = s.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "project", []string{"auditor"}, "", 100)
	if err != nil || unrestricted || !slices.Equal(ids, []string{"p1"}) {
		t.Fatalf("lookup on the unqueried global role = (%v, %v, %v), want ([p1], false, nil)", ids, unrestricted, err)
	}
}

// TestRolesLookupFoldsDuplicatesAcrossChunks drives the roles lookup past its
// chunk budget: more queried roles than one query may carry, with resources
// granted by roles that land in DIFFERENT chunks. A duplicate resource id must
// be folded BEFORE the limit applies, or a page returns fewer ids than asked for
// while more remain.
func TestRolesLookupFoldsDuplicatesAcrossChunks(t *testing.T) {
	ctx := context.Background()
	db, s := newRoles(t)

	// One role per chunk-crossing position: with lookupChunkBudget roles per
	// query, roles[0] and roles[budget] are read by two different streams.
	roles := make([]string, 0, lookupChunkBudget+4)
	for i := 0; i < cap(roles); i++ {
		roles = append(roles, fmt.Sprintf("r%03d", i))
	}
	ids := []string{"p1", "p2", "p3", "p4"}
	var seeds []authroles.Assignment
	for _, id := range ids {
		// Every project is granted by BOTH a first-chunk role and a later-chunk one.
		seeds = append(seeds,
			ra("u1", roles[0], "project", id),
			ra("u1", roles[lookupChunkBudget], "project", id),
		)
	}
	// A role outside the queried set and a foreign resource type never leak in.
	seeds = append(seeds, ra("u1", "unqueried", "project", "p_hidden"), ra("u1", roles[0], "dataset", "ds1"))
	seedRoles(t, db, seeds...)

	got, unrestricted, err := s.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "project", roles, "", 0)
	if err != nil || unrestricted {
		t.Fatalf("lookup = (%v, %v)", unrestricted, err)
	}
	if !slices.Equal(got, ids) {
		t.Fatalf("distinct ids = %v, want %v", got, ids)
	}

	// The limit counts DISTINCT ids, not documents: two ids means two ids, even
	// though four documents carry them.
	got, _, err = s.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "project", roles, "", 2)
	if err != nil {
		t.Fatalf("limited lookup: %v", err)
	}
	if want := ids[:2]; !slices.Equal(got, want) {
		t.Fatalf("limited lookup = %v, want %v", got, want)
	}

	// after is exclusive and spans the chunk boundary the same way.
	got, _, err = s.LookupResourceIDsBySubjectAndRoles(ctx, "user", "u1", "project", roles, "p2", 100)
	if err != nil {
		t.Fatalf("after lookup: %v", err)
	}
	if want := ids[2:]; !slices.Equal(got, want) {
		t.Fatalf("after lookup = %v, want %v", got, want)
	}
}

// TestEffectiveDualProvenanceAtAPageBoundary is A3b's sharp case: the grant held
// BOTH directly and globally sits exactly at the end of page one. It must appear
// ONCE, carry both provenance flags, never repeat on page two, and the page-two
// cursor must round-trip.
func TestEffectiveDualProvenanceAtAPageBoundary(t *testing.T) {
	ctx := context.Background()
	db, s := newRoles(t)

	seedRoles(t, db,
		ra("u1", "auditor", "doc", "d1"), // direct only
		ra("u2", "auditor", "doc", "d1"), // both:
		ra("u2", "auditor", "", ""),      //   direct + global
		ra("u3", "auditor", "", ""),      // global only
		ra("u4", "auditor", "doc", "d1"), // direct only
		ra("u5", "auditor", "doc", "d2"), // another scope — must not leak
	)

	first, err := s.ListEffectiveByResource(ctx, "doc", "d1", listing.Request{Limit: 2})
	if err != nil {
		t.Fatalf("page one: %v", err)
	}
	if got, want := grantLabels(first.Items), []string{"u1:direct", "u2:both"}; !slices.Equal(got, want) {
		t.Fatalf("page one = %v, want %v", got, want)
	}
	if !first.HasMore || first.NextCursor == "" {
		t.Fatalf("page one must carry a continuation: %+v", first)
	}

	second, err := s.ListEffectiveByResource(ctx, "doc", "d1", listing.Request{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("page two: %v", err)
	}
	if got, want := grantLabels(second.Items), []string{"u3:global", "u4:direct"}; !slices.Equal(got, want) {
		t.Fatalf("page two = %v, want %v", got, want)
	}
	// Page two is addressed by a cursor, so it reports a previous page; its
	// window is one row short of the limit (page one is the first page and no
	// cursor addresses it), which is listing.MarkPrevPage's empty-PreviousCursor
	// case and the SQL siblings' behaviour.
	if !second.HasPrev {
		t.Fatalf("page two must report HasPrev: %+v", second)
	}
	if second.HasMore {
		t.Fatalf("page two is the last page: %+v", second)
	}

	// The count is DISTINCT logical grants: four, not the five documents that
	// carry them.
	counted, err := s.ListEffectiveByResource(ctx, "doc", "d1", listing.Request{Limit: 2, WithCount: true})
	if err != nil {
		t.Fatalf("counted page: %v", err)
	}
	if counted.Total == nil || *counted.Total != 4 {
		t.Fatalf("total = %v, want 4 distinct grants", counted.Total)
	}
}

// TestEffectivePageThreeRoundTripsToPageTwo walks the effective listing one
// grant at a time in BOTH directions and returns from page three to page two by
// its PreviousCursor — the full logical reverse window, which an existence-probe
// HasPrev could report but never address.
func TestEffectivePageThreeRoundTripsToPageTwo(t *testing.T) {
	ctx := context.Background()
	db, s := newRoles(t)

	seedRoles(t, db,
		ra("u1", "auditor", "doc", "d1"),
		ra("u2", "auditor", "", ""),
		ra("u3", "auditor", "doc", "d1"),
		ra("u3", "auditor", "", ""),
		ra("u4", "auditor", "doc", "d1"),
	)
	want := []string{"u1:direct", "u2:global", "u3:both", "u4:direct"}

	for _, tc := range []struct {
		name  string
		order listing.Order
		want  []string
	}{
		{"Ascending", listing.Order{}, want},
		{"Descending", listing.NewOrder("grant_key", listing.DESC), reversed(want)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := func(cursor string) listing.Page[roles.EffectiveGrant] {
				t.Helper()
				p, err := s.ListEffectiveByResource(ctx, "doc", "d1", listing.Request{Limit: 1, Cursor: cursor, Order: tc.order})
				if err != nil {
					t.Fatalf("page(%q): %v", cursor, err)
				}
				return p
			}

			one := page("")
			two := page(one.NextCursor)
			three := page(two.NextCursor)
			if got := []string{label(one), label(two), label(three)}; !slices.Equal(got, tc.want[:3]) {
				t.Fatalf("pages one..three = %v, want %v", got, tc.want[:3])
			}
			if !three.HasPrev || three.PreviousCursor == "" {
				t.Fatalf("page three must address its previous page: %+v", three)
			}
			back := page(three.PreviousCursor)
			if label(back) != label(two) {
				t.Fatalf("page three's PreviousCursor landed on %q, want page two %q", label(back), label(two))
			}
			if !back.HasMore || back.NextCursor == "" {
				t.Fatalf("the returned page must continue forward again: %+v", back)
			}
			if label(page(back.NextCursor)) != label(three) {
				t.Fatal("the round trip must return to page three")
			}
		})
	}
}

// TestEffectiveOffsetAndCountCountGroups pins the two remaining strategies over
// GROUPS rather than documents: an offset skips logical grants, and a count is
// the number of them.
func TestEffectiveOffsetAndCountCountGroups(t *testing.T) {
	ctx := context.Background()
	db, s := newRoles(t)

	seedRoles(t, db,
		ra("u1", "auditor", "doc", "d1"),
		ra("u2", "auditor", "doc", "d1"),
		ra("u2", "auditor", "", ""),
		ra("u3", "auditor", "", ""),
	)

	page, err := s.ListEffectiveByResource(ctx, "doc", "d1", listing.Request{Strategy: listing.StrategyOffset, Offset: 1, Limit: 1, WithCount: true})
	if err != nil {
		t.Fatalf("offset page: %v", err)
	}
	if got, want := grantLabels(page.Items), []string{"u2:both"}; !slices.Equal(got, want) {
		t.Fatalf("offset 1 = %v, want %v", got, want)
	}
	if !page.HasMore || !page.HasPrev {
		t.Fatalf("a middle offset page has both neighbours: %+v", page)
	}
	if page.NextCursor != "" || page.PreviousCursor != "" {
		t.Fatalf("the offset strategy emits no cursors: %+v", page)
	}
	if page.Total == nil || *page.Total != 3 {
		t.Fatalf("total = %v, want 3 distinct grants (four documents)", page.Total)
	}

	// Past the end: an empty page, not an error.
	page, err = s.ListEffectiveByResource(ctx, "doc", "d1", listing.Request{Strategy: listing.StrategyOffset, Offset: 9, Limit: 2})
	if err != nil {
		t.Fatalf("offset past the end: %v", err)
	}
	if len(page.Items) != 0 || page.HasMore {
		t.Fatalf("offset past the end = %+v, want an empty page", page)
	}
}

// TestEffectiveGlobalScopeRequestIsAllDirect is the special case: a request for
// the GLOBAL scope has nothing to fall back to, so it reads ONE stream and every
// grant is Direct — HasRole's no-fallback path for an unscoped query, and
// turso's collapsed union.
func TestEffectiveGlobalScopeRequestIsAllDirect(t *testing.T) {
	ctx := context.Background()
	db, s := newRoles(t)

	seedRoles(t, db,
		ra("u1", "auditor", "", ""),
		ra("u2", "auditor", "", ""),
		ra("u3", "auditor", "doc", "d1"), // scoped: never in a global listing
	)

	page, err := s.ListEffectiveByResource(ctx, "", "", listing.Request{Limit: 100, WithCount: true})
	if err != nil {
		t.Fatalf("global listing: %v", err)
	}
	if got, want := grantLabels(page.Items), []string{"u1:direct", "u2:direct"}; !slices.Equal(got, want) {
		t.Fatalf("global listing = %v, want %v", got, want)
	}
	if page.Total == nil || *page.Total != 2 {
		t.Fatalf("total = %v, want 2", page.Total)
	}
	for _, g := range page.Items {
		if g.Global {
			t.Fatalf("a global-scope request must not mark a grant Global: %+v", g)
		}
	}
}

// TestEffectiveRejectsUnknownOrderAndSearch keeps the store-owned listing on the
// same refusals the connector's List applies to every other list here: an order
// field outside role.EffectiveOrderFields, and a search against a list that
// declares nothing searchable (ruling R4).
func TestEffectiveRejectsUnknownOrderAndSearch(t *testing.T) {
	ctx := context.Background()
	_, s := newRoles(t)

	if _, err := s.ListEffectiveByResource(ctx, "doc", "d1", listing.Request{Order: listing.NewOrder("created_at", listing.ASC)}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("unknown order field must reject with ErrInvalidInput, got %v", err)
	}
	if _, err := s.ListEffectiveByResource(ctx, "doc", "d1", listing.Request{Search: "aud"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("search must reject with ErrInvalidInput, got %v", err)
	}
	if _, err := s.ListEffectiveByResource(ctx, "doc", "d1", listing.Request{Cursor: "x", Strategy: listing.StrategyOffset}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("a contradictory strategy must reject with ErrInvalidInput, got %v", err)
	}
}

// grantLabels renders a page as subject:provenance pairs, the shape every
// effective assertion above reads.
func grantLabels(items []roles.EffectiveGrant) []string {
	out := make([]string, 0, len(items))
	for _, g := range items {
		out = append(out, g.SubjectID+":"+g.Provenance())
	}
	return out
}

// label renders a one-item page.
func label(p listing.Page[roles.EffectiveGrant]) string {
	if len(p.Items) != 1 {
		return fmt.Sprintf("<%d items>", len(p.Items))
	}
	return grantLabels(p.Items)[0]
}

func reversed(in []string) []string {
	out := slices.Clone(in)
	slices.Reverse(out)
	return out
}
