package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestConstructorStartupContext(t *testing.T) {
	constructors := []struct {
		name  string
		build func(context.Context, *firestoredb.DB, ...Option) error
	}{
		{"Repositories", func(ctx context.Context, db *firestoredb.DB, opts ...Option) error {
			_, err := Repositories(ctx, db, opts...)
			return err
		}},
		{"RelationshipRepository", func(ctx context.Context, db *firestoredb.DB, opts ...Option) error {
			_, err := RelationshipRepository(ctx, db, opts...)
			return err
		}},
	}
	policies := []struct {
		name string
		opts []Option
	}{
		{"probe", nil},
		{"without probe", []Option{WithoutIndexProbe()}},
		{"audit probe", []Option{WithAudit()}},
		{"audit without probe", []Option{WithAudit(), WithoutIndexProbe()}},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			if err := constructor.build(t.Context(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("nil database: %v", err)
			}
			for _, policy := range policies {
				t.Run(policy.name, func(t *testing.T) {
					canceled, cancel := context.WithCancel(t.Context())
					cancel()
					expired, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
					defer stop()
					for _, ctx := range []context.Context{canceled, expired} {
						// A zero DB cannot perform a probe; cancellation must win first.
						if err := constructor.build(ctx, &firestoredb.DB{}, policy.opts...); !errors.Is(err, ctx.Err()) {
							t.Fatalf("canceled startup = %v; want %v", err, ctx.Err())
						}
					}
				})
			}
		})
	}
}

func TestConstructorBorrowsDatabaseWithoutProbing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db := &firestoredb.DB{}
	repos, err := Repositories(ctx, db, WithoutIndexProbe())
	if err != nil {
		t.Fatalf("construct without probe: %v", err)
	}
	if repos.Relationships.(*relationshipStore).db != db || repos.Roles.(*roleStore).db != db || repos.Mutations.(*mutationStore).db != db {
		t.Fatal("constructor did not borrow the supplied database")
	}
	repository, err := RelationshipRepository(ctx, db, WithoutIndexProbe())
	if err != nil || repository.(*relationshipStore).db != db {
		t.Fatalf("relationship constructor did not borrow the database: %v", err)
	}
}
