package storetest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

type rawRelationshipReads interface {
	CheckRelationExists(context.Context, string, string, string, string, string) (bool, error)
	CountByResourceAndRelation(context.Context, string, string, string) (int, error)
	ListRelationshipsBySubject(context.Context, string, string, relationships.SubjectRelationshipFilter, list.Request) (list.Page[relationships.SubjectRelationship], error)
	ListRelationshipsByResource(context.Context, string, string, relationships.ResourceRelationshipFilter, list.Request) (list.Page[relationships.ResourceRelationship], error)
}

// Raw adapters and the public service project the same canonical facts. A raw
// adapter must not silently accept filters/search or ignore cancellation merely
// because the host did not construct the pocket's root components.
func runRawRelationshipFacadeParity(t *testing.T, factory func(*testing.T) Repositories) {
	repos := factory(t)
	if repos.Relationships == nil {
		t.Skip("raw relationship facade not supplied")
	}
	parts, err := relationships.NewService(repos.Tuples)
	if err != nil {
		t.Fatal(err)
	}
	facts := []tuples.Tuple{
		{Scope: tuples.Global(), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g"}},
		{Scope: tuples.On("doc", "a"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g"}},
		{Scope: tuples.On("doc", "a"), Relation: "viewer", Subject: tuples.SubjectRef{Type: "group", ID: "g", Relation: "member"}},
		{Scope: tuples.On("doc", "b"), Relation: "owner", Subject: tuples.SubjectRef{Type: "group", ID: "g"}},
	}
	if err := repos.Tuples.ApplyTuples(t.Context(), tuples.Changes{Add: facts}); err != nil {
		t.Fatal(err)
	}
	views := map[string]rawRelationshipReads{"raw": repos.Relationships, "service": parts.Service}
	// A model-scoped graph view must still expose raw inspection methods as raw
	// facts when it also implements the aggregate store port.
	graph := repos.Relationships.ForModel(relationships.NewReadModel(nil))
	if raw, ok := graph.(rawRelationshipReads); ok {
		views["model-view-raw-methods"] = raw
	}
	if targets, err := graph.GetRelationTargets(t.Context(), "doc", "a", "viewer"); err != nil || len(targets) != 0 {
		t.Fatalf("raw facade bypassed graph model: %v/%v", targets, err)
	}

	for name, view := range views {
		t.Run(name, func(t *testing.T) {
			if held, err := view.CheckRelationExists(t.Context(), "doc", "a", "viewer", "group", "g"); err != nil || !held {
				t.Fatalf("exact fact: %v/%v", held, err)
			}
			if n, err := view.CountByResourceAndRelation(t.Context(), "doc", "a", "viewer"); err != nil || n != 2 {
				t.Fatalf("count must include userset but exclude global: %d/%v", n, err)
			}
			request := list.Request{Limit: 1, WithCount: true}
			first, err := view.ListRelationshipsBySubject(t.Context(), "group", "g", relationships.SubjectRelationshipFilter{}, request)
			want, wantErr := parts.Service.ListRelationshipsBySubject(t.Context(), "group", "g", relationships.SubjectRelationshipFilter{}, request)
			if err != nil || wantErr != nil || !reflect.DeepEqual(first, want) || first.Total == nil || *first.Total != 3 || !first.HasMore {
				t.Fatalf("subject first page: %+v/%v; want %+v/%v", first, err, want, wantErr)
			}
			second, err := view.ListRelationshipsBySubject(t.Context(), "group", "g", relationships.SubjectRelationshipFilter{}, list.Request{Limit: 1, Cursor: first.NextCursor})
			if err != nil || len(second.Items) != 1 || second.Items[0].SubjectRelation != "member" || !second.HasPrev || !second.HasMore {
				t.Fatalf("subject second page: %+v/%v", second, err)
			}
			previous, err := view.ListRelationshipsBySubject(t.Context(), "group", "g", relationships.SubjectRelationshipFilter{}, list.Request{Limit: 1, Cursor: second.PreviousCursor})
			if err != nil || !reflect.DeepEqual(previous.Items, first.Items) {
				t.Fatalf("subject previous page: %+v/%v", previous, err)
			}
			for _, id := range []string{"a", "absent"} {
				got, err := view.ListRelationshipsByResource(t.Context(), "doc", id, relationships.ResourceRelationshipFilter{}, list.Request{WithCount: true})
				want, wantErr := parts.Service.ListRelationshipsByResource(t.Context(), "doc", id, relationships.ResourceRelationshipFilter{}, list.Request{WithCount: true})
				if err != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("resource %s: %+v/%v; want %+v/%v", id, got, err, want, wantErr)
				}
			}

			empty := ""
			invalid := []func() error{
				func() error {
					_, err := view.CheckRelationExists(t.Context(), "doc", "a", "", "group", "g")
					return err
				},
				func() error { _, err := view.CountByResourceAndRelation(t.Context(), "doc", "a", ""); return err },
				func() error {
					_, err := view.ListRelationshipsBySubject(t.Context(), "", "g", relationships.SubjectRelationshipFilter{}, list.Request{})
					return err
				},
				func() error {
					_, err := view.ListRelationshipsByResource(t.Context(), "doc", "", relationships.ResourceRelationshipFilter{}, list.Request{})
					return err
				},
				func() error {
					_, err := view.ListRelationshipsBySubject(t.Context(), "group", "g", relationships.SubjectRelationshipFilter{Relation: &empty}, list.Request{})
					return err
				},
				func() error {
					_, err := view.ListRelationshipsByResource(t.Context(), "doc", "a", relationships.ResourceRelationshipFilter{Relation: &empty}, list.Request{})
					return err
				},
				func() error {
					_, err := view.ListRelationshipsBySubject(t.Context(), "group", "g", relationships.SubjectRelationshipFilter{}, list.Request{Search: "absent"})
					return err
				},
				func() error {
					_, err := view.ListRelationshipsByResource(t.Context(), "doc", "a", relationships.ResourceRelationshipFilter{}, list.Request{Search: "absent"})
					return err
				},
			}
			for i, call := range invalid {
				if err := call(); !errors.Is(err, sdk.ErrInvalidInput) {
					t.Errorf("invalid read %d: %v", i, err)
				}
			}

			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if held, err := view.CheckRelationExists(ctx, "doc", "a", "viewer", "group", "g"); held || !errors.Is(err, context.Canceled) {
				t.Errorf("canceled exact read: %v/%v", held, err)
			}
			if count, err := view.CountByResourceAndRelation(ctx, "doc", "a", "viewer"); count != 0 || !errors.Is(err, context.Canceled) {
				t.Errorf("canceled count: %d/%v", count, err)
			}
			if page, err := view.ListRelationshipsBySubject(ctx, "group", "g", relationships.SubjectRelationshipFilter{}, list.Request{}); len(page.Items) != 0 || !errors.Is(err, context.Canceled) {
				t.Errorf("canceled subject list: %+v/%v", page, err)
			}
			if page, err := view.ListRelationshipsByResource(ctx, "doc", "a", relationships.ResourceRelationshipFilter{}, list.Request{}); len(page.Items) != 0 || !errors.Is(err, context.Canceled) {
				t.Errorf("canceled resource list: %+v/%v", page, err)
			}
		})
	}
}
