// Package storetest is the exported conformance suite for the authorization
// feature's two kinds. Run drives BOTH a relationship.Storer and a role.Storer
// (bundled in an authorization.Repositories) so every backend — the in-core
// memstore, the dialect adapters (features/authorization/stores/{turso,pgx}) —
// runs the SAME suite and authorizes identically.
//
// Two layers: (a) the port contracts directly against each Storer, and (b) the
// engine/service constructed over the stores under test, asserting authorization
// OUTCOMES — this is what proves the memstore and the SQL stores authorize
// identically. A nil kind in the Repositories skips that kind's families with a
// loud t.Skip (deny-by-absence extended to conformance), so a single-kind host
// store can still prove conformance.
//
// The suite imports stdlib + sdk + this feature only (guards G2/FS1 keep drivers
// out), so features/authorization's own `go test ./...` runs it against the
// memstore reference hermetically.
package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization"
	"github.com/gopernicus/gopernicus/features/authorization/domain/relationship"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
)

// Run executes the full conformance suite. newRepos returns a FRESH, empty
// Repositories per call so each subtest is isolated.
func Run(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	t.Run("Relationship", func(t *testing.T) {
		if newRepos(t).Relationships == nil {
			t.Skip("relationship kind not wired")
		}
		runRelationshipContracts(t, newRepos)
	})
	t.Run("Adversarial", func(t *testing.T) {
		if newRepos(t).Relationships == nil {
			t.Skip("relationship kind not wired")
		}
		runAdversarial(t, newRepos)
	})
	t.Run("Budget", func(t *testing.T) {
		if newRepos(t).Relationships == nil {
			t.Skip("relationship kind not wired")
		}
		runBudget(t, newRepos)
	})
	t.Run("Parity", func(t *testing.T) {
		if newRepos(t).Relationships == nil {
			t.Skip("relationship kind not wired")
		}
		runParity(t, newRepos)
	})
	t.Run("Roles", func(t *testing.T) {
		if newRepos(t).Roles == nil {
			t.Skip("roles kind not wired")
		}
		runRoles(t, newRepos)
	})
	t.Run("Mutations", func(t *testing.T) {
		if newRepos(t).Mutations == nil {
			t.Skip("mutation repository not wired")
		}
		runMutations(t, newRepos)
	})
}

func ct(rt, rid, relation, st, sid string) relationship.CreateRelationship {
	return relationship.CreateRelationship{ResourceType: rt, ResourceID: rid, Relation: relation, SubjectType: st, SubjectID: sid}
}

func mustCreate(t *testing.T, s relationship.Storer, tuples ...relationship.CreateRelationship) {
	t.Helper()
	if err := s.CreateRelationships(context.Background(), tuples); err != nil {
		t.Fatalf("CreateRelationships: %v", err)
	}
}

// runRelationshipContracts is layer (a): the relationship.Storer port contract.
func runRelationshipContracts(t *testing.T, newRepos func(t *testing.T) authorization.Repositories) {
	ctx := context.Background()

	t.Run("CRUDRoundTrip", func(t *testing.T) {
		s := newRepos(t).Relationships
		mustCreate(t, s, ct("doc", "d1", "owner", "user", "u1"))

		if ok, err := s.CheckRelationExists(ctx, "doc", "d1", "owner", "user", "u1"); err != nil || !ok {
			t.Fatalf("created tuple should exist: ok=%v err=%v", ok, err)
		}
		targets, err := s.GetRelationTargets(ctx, "doc", "d1", "owner")
		if err != nil || len(targets) != 1 || targets[0].ID != "u1" {
			t.Fatalf("GetRelationTargets: %+v err=%v", targets, err)
		}
		if err := s.DeleteRelationship(ctx, "doc", "d1", "owner", "user", "u1"); err != nil {
			t.Fatalf("DeleteRelationship: %v", err)
		}
		if ok, _ := s.CheckRelationExists(ctx, "doc", "d1", "owner", "user", "u1"); ok {
			t.Fatalf("tuple should be gone after delete")
		}
		// Deleting an absent tuple is idempotent.
		if err := s.DeleteRelationship(ctx, "doc", "d1", "owner", "user", "u1"); err != nil {
			t.Fatalf("idempotent delete: %v", err)
		}
	})

	t.Run("DuplicateTupleNoOp", func(t *testing.T) {
		s := newRepos(t).Relationships
		mustCreate(t, s, ct("doc", "d1", "owner", "user", "u1"))
		// Exact-duplicate (same six columns) is an idempotent no-op — nil, count 1.
		mustCreate(t, s, ct("doc", "d1", "owner", "user", "u1"))
		if n, _ := s.CountByResourceAndRelation(ctx, "doc", "d1", "owner"); n != 1 {
			t.Fatalf("duplicate tuple must not add a row, count=%d", n)
		}
	})

	t.Run("SecondRelationSilentNoOp", func(t *testing.T) {
		s := newRepos(t).Relationships
		mustCreate(t, s, ct("doc", "d1", "owner", "user", "u1"))
		// A SECOND, different relation for the same subject on the same resource is
		// a SILENT NO-OP (nil error), NOT ErrAlreadyExists — the existing relation
		// is unchanged on re-read.
		if err := s.CreateRelationships(ctx, []relationship.CreateRelationship{ct("doc", "d1", "member", "user", "u1")}); err != nil {
			t.Fatalf("second relation must be a nil no-op, got %v", err)
		}
		if ok, _ := s.CheckRelationExists(ctx, "doc", "d1", "owner", "user", "u1"); !ok {
			t.Fatalf("existing owner relation must be unchanged")
		}
		if ok, _ := s.CheckRelationExists(ctx, "doc", "d1", "member", "user", "u1"); ok {
			t.Fatalf("second relation must have been skipped")
		}
	})

	t.Run("UsersetSubjectRelationsCoexistOnResource", func(t *testing.T) {
		s := newRepos(t).Relationships
		// group:eng#member and group:eng#admin are DISTINCT subject references
		// (same type+id, different subject_relation). The unique-SUBJECT key
		// includes subject_relation (idx_iam_relationships_unique_subject), so a RAW
		// create of BOTH usersets on ONE resource persists BOTH and they are
		// independently observable — never a silent collision that drops the second.
		mustCreate(t, s,
			ctUserset("doc", "d1", "viewer", "group", "eng", "member"),
			ctUserset("doc", "d1", "viewer", "group", "eng", "admin"),
		)
		targets, err := s.GetRelationTargets(ctx, "doc", "d1", "viewer")
		if err != nil {
			t.Fatalf("GetRelationTargets: %v", err)
		}
		relations := map[string]bool{}
		for _, tgt := range targets {
			if tgt.Type != "group" || tgt.ID != "eng" {
				t.Fatalf("unexpected target %+v", tgt)
			}
			relations[tgt.Relation] = true
		}
		if len(relations) != 2 || !relations["member"] || !relations["admin"] {
			t.Fatalf("group:eng#member and group:eng#admin must both persist, got %+v", targets)
		}
		// The one-relation rule still holds PER subject reference: re-creating the
		// SAME reference (group:eng#member) with a DIFFERENT relation is the silent
		// no-op — it must not gain the new relation nor drop the existing one.
		if err := s.CreateRelationships(ctx, []relationship.CreateRelationship{
			ctUserset("doc", "d1", "owner", "group", "eng", "member"),
		}); err != nil {
			t.Fatalf("second relation for an existing subject reference must be a nil no-op, got %v", err)
		}
		if n, _ := s.CountByResourceAndRelation(ctx, "doc", "d1", "owner"); n != 0 {
			t.Fatalf("the group:eng#member reference must stay viewer, not gain owner, got %d owner rows", n)
		}
	})

	t.Run("DeleteVariants", func(t *testing.T) {
		s := newRepos(t).Relationships
		mustCreate(t, s,
			ct("doc", "d1", "owner", "user", "u1"),
			ct("doc", "d1", "viewer", "user", "u2"),
			ct("doc", "d2", "owner", "user", "u1"),
		)
		// DeleteByResourceAndSubject removes all of u1's relations on d1 only.
		if err := s.DeleteByResourceAndSubject(ctx, "doc", "d1", "user", "u1"); err != nil {
			t.Fatalf("DeleteByResourceAndSubject: %v", err)
		}
		if ok, _ := s.CheckRelationExists(ctx, "doc", "d1", "owner", "user", "u1"); ok {
			t.Fatalf("u1's d1 relation should be gone")
		}
		if ok, _ := s.CheckRelationExists(ctx, "doc", "d2", "owner", "user", "u1"); !ok {
			t.Fatalf("u1's d2 relation must survive")
		}
		// DeleteResourceRelationships wipes everything on d1.
		if err := s.DeleteResourceRelationships(ctx, "doc", "d1"); err != nil {
			t.Fatalf("DeleteResourceRelationships: %v", err)
		}
		if ok, _ := s.CheckRelationExists(ctx, "doc", "d1", "viewer", "user", "u2"); ok {
			t.Fatalf("d1 relations should all be gone")
		}
	})

	t.Run("CheckBatchDirect", func(t *testing.T) {
		s := newRepos(t).Relationships
		mustCreate(t, s,
			ct("doc", "d1", "viewer", "user", "u1"),
			ct("doc", "d3", "viewer", "user", "u1"),
		)
		got, err := s.CheckBatchDirect(ctx, "doc", []string{"d1", "d2", "d3"}, "viewer", "user", "u1", 0)
		if err != nil {
			t.Fatalf("CheckBatchDirect: %v", err)
		}
		if !got["d1"] || got["d2"] || !got["d3"] {
			t.Fatalf("batch map wrong: %v", got)
		}
	})

	t.Run("CountDirectOnly", func(t *testing.T) {
		s := newRepos(t).Relationships
		// Two direct owners + a group-expanded owner; the direct count is 2.
		mustCreate(t, s,
			ct("doc", "d1", "owner", "user", "u1"),
			ct("doc", "d1", "owner", "group", "eng"),
			ct("group", "eng", "member", "user", "u2"),
		)
		if n, _ := s.CountByResourceAndRelation(ctx, "doc", "d1", "owner"); n != 2 {
			t.Fatalf("direct count must be 2 (u1 + group:eng), got %d", n)
		}
	})

	t.Run("Lookups", func(t *testing.T) {
		s := newRepos(t).Relationships
		mustCreate(t, s,
			ct("doc", "d1", "viewer", "user", "u1"),
			ct("doc", "d2", "viewer", "user", "u1"),
			ct("space", "s2", "parent", "space", "s1"),
			ct("space", "s3", "parent", "space", "s2"),
		)
		ids, _ := s.LookupResourceIDs(ctx, "doc", []string{"viewer"}, "user", "u1", 100)
		if len(ids) != 2 {
			t.Fatalf("LookupResourceIDs want 2, got %v", ids)
		}
		byTarget, _ := s.LookupResourceIDsByRelationTarget(ctx, "space", "parent", "space", []string{"s1"}, 100)
		if len(byTarget) != 1 || byTarget[0] != "s2" {
			t.Fatalf("LookupResourceIDsByRelationTarget want [s2], got %v", byTarget)
		}
		desc, _ := s.LookupDescendantResourceIDs(ctx, "space", "parent", "space", []string{"s1"}, 100)
		if len(desc) != 2 {
			t.Fatalf("descendants want [s2 s3], got %v", desc)
		}
		// The result cap is a distinguishable overflow signal: a cap of 1 returns
		// exactly 1 of the 2 viewer docs (never a silent "complete" 2).
		capped, _ := s.LookupResourceIDs(ctx, "doc", []string{"viewer"}, "user", "u1", 1)
		if len(capped) != 1 {
			t.Fatalf("LookupResourceIDs cap=1 must return exactly 1 (overflow signal), got %v", capped)
		}
	})

	t.Run("ListingPagination", func(t *testing.T) {
		s := newRepos(t).Relationships
		mustCreate(t, s,
			ct("doc", "d1", "viewer", "user", "u1"),
			ct("doc", "d2", "viewer", "user", "u1"),
			ct("doc", "d3", "viewer", "user", "u1"),
		)
		assertFullCoverage(t, s, "user", "u1", 3)

		// Empty-page shape.
		empty, err := s.ListRelationshipsByResource(ctx, "doc", "absent", relationship.ResourceRelationshipFilter{}, crud.ListRequest{})
		if err != nil {
			t.Fatalf("empty list: %v", err)
		}
		if len(empty.Items) != 0 || empty.HasMore || empty.NextCursor != "" {
			t.Fatalf("empty page shape wrong: %+v", empty)
		}
	})

	t.Run("RejectsUnknownOrderField", func(t *testing.T) {
		s := newRepos(t).Relationships
		mustCreate(t, s, ct("doc", "d1", "viewer", "user", "u1"))
		// An order field outside relationship.OrderFields (created_at only) is
		// rejected with ErrInvalidInput identically across every backend.
		if _, err := s.ListRelationshipsBySubject(ctx, "user", "u1", relationship.SubjectRelationshipFilter{}, crud.ListRequest{Order: crud.NewOrder("subject_id", crud.ASC)}); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("unknown order field must reject with ErrInvalidInput, got %v", err)
		}
	})

	t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) {
		s := newRepos(t).Relationships
		// A cryptids.Database-wired batch: every RelationshipID is empty. Each row
		// must come back with a non-empty id, readable via the listing (the create
		// returns no rows). Asserted per-backend, never comparing id ordering across
		// backends.
		mustCreate(t, s,
			ct("doc", "d1", "viewer", "user", "u1"),
			ct("doc", "d2", "viewer", "user", "u1"),
		)
		page, err := s.ListRelationshipsBySubject(ctx, "user", "u1", relationship.SubjectRelationshipFilter{}, crud.ListRequest{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(page.Items) != 2 {
			t.Fatalf("want 2 rows, got %d", len(page.Items))
		}
		for _, it := range page.Items {
			if it.ID == "" {
				t.Fatalf("DB-generated id must be non-empty: %+v", it)
			}
		}

		// Partial batch [new, duplicate-tuple, new]: both new rows present, the
		// duplicate skipped, nil error.
		if err := s.CreateRelationships(ctx, []relationship.CreateRelationship{
			ct("doc", "d3", "viewer", "user", "u1"),
			ct("doc", "d1", "viewer", "user", "u1"), // duplicate of an existing tuple
			ct("doc", "d4", "viewer", "user", "u1"),
		}); err != nil {
			t.Fatalf("partial batch must be nil, got %v", err)
		}
		assertFullCoverage(t, s, "user", "u1", 4) // d1..d4, each exactly once
	})
}

// assertFullCoverage pages through a subject's relationships two at a time and
// asserts every resource appears exactly once across page boundaries (the
// RETURNING/DO-NOTHING row-count trap), with a total of want.
func assertFullCoverage(t *testing.T, s relationship.Storer, subjectType, subjectID string, want int) {
	t.Helper()
	ctx := context.Background()
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < want+2; pages++ {
		page, err := s.ListRelationshipsBySubject(ctx, subjectType, subjectID, relationship.SubjectRelationshipFilter{}, crud.ListRequest{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("list page: %v", err)
		}
		for _, it := range page.Items {
			if seen[it.ResourceID] {
				t.Fatalf("resource %s appeared twice across pages", it.ResourceID)
			}
			seen[it.ResourceID] = true
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != want {
		t.Fatalf("want %d distinct resources across pages, got %d", want, len(seen))
	}
}
