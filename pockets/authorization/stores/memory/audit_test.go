package memory

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/audit"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/mutations"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

func TestAuditPreparationFailureDoesNotPublishFacts(t *testing.T) {
	store := New(WithAudit())
	ctx := audit.WithSource(context.Background(), audit.Source{System: "test"})
	seed := rel("doc", "d", "viewer", "user", "u")
	if err := store.Relationships().CreateRelationships(ctx, []relationships.CreateRelationship{seed}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Audit().List(ctx, audit.Filter{}, list.Request{})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the publication boundary with a candidate audit cannot represent.
	// The candidate must never replace the valid stored state.
	err = store.rel.st.write(ctx, func(next *state) error {
		next.role = append(next.role, roleRow{subjectType: "user", subjectID: "u", role: "bad\x01role"})
		return nil
	})
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("audit preparation accepted invalid fact: %v", err)
	}
	if len(store.rel.st.role) != 0 {
		t.Fatal("failed audit preparation published candidate role")
	}
	after, err := store.Audit().List(ctx, audit.Filter{}, list.Request{})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed audit changed history: %+v %v", after, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	err = store.rel.st.write(canceled, func(next *state) error { next.rel = nil; cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled candidate: %v", err)
	}
	present, err := store.Relationships().CheckRelationExists(ctx, "doc", "d", "viewer", "user", "u")
	if err != nil || !present {
		t.Fatal("canceled candidate removed committed tuple")
	}
}

func TestAuditReadsRetainedHistoryWhenRecordingDisabled(t *testing.T) {
	store := New(WithAudit())
	ctx := audit.WithSource(context.Background(), audit.Source{System: "test"})
	if err := store.Relationships().CreateRelationships(ctx, []relationships.CreateRelationship{rel("doc", "d", "viewer", "user", "u")}); err != nil {
		t.Fatal(err)
	}
	// Reopening durable storage with recording off has the same retained-state
	// contract. Memory models that state change here without exposing a live toggle.
	store.rel.st.recordAudit = false
	if err := store.Relationships().CreateRelationships(context.Background(), []relationships.CreateRelationship{rel("doc", "d2", "viewer", "user", "u")}); err != nil {
		t.Fatal(err)
	}
	page, err := store.Audit().List(context.Background(), audit.Filter{}, list.Request{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Change.Relationship.ResourceID != "d" {
		t.Fatalf("disabled reader lost retained history: %+v %v", page, err)
	}
}

func TestRawWriterCanceledBehindGuardCannotPublish(t *testing.T) {
	store := New(WithAudit())
	ctx := audit.WithSource(context.Background(), audit.Source{System: "test"})
	entered, release := make(chan struct{}), make(chan struct{})
	guarded := make(chan error, 1)
	go func() {
		_, err := store.Mutations().ApplyGuarded(ctx, grantOwner(t, "d", "owner"), func(ctx context.Context, v mutations.StoreDecisionView) error {
			ok, err := v.CheckRelation(ctx, mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d"}, "viewer", "user", "u")
			if err != nil {
				return err
			}
			if ok {
				return errors.New("unexpected initial viewer")
			}
			close(entered)
			<-release
			ok, err = v.CheckRelation(ctx, mutations.Target{Kind: mutations.TargetResource, Type: "doc", ID: "d"}, "viewer", "user", "u")
			if err != nil {
				return err
			}
			if ok {
				return errors.New("raw writer changed guard snapshot")
			}
			return nil
		}, nil)
		guarded <- err
	}()
	<-entered
	base, cancel := context.WithCancel(ctx)
	rawctx := &lockWaitContext{Context: base, checked: make(chan struct{})}
	raw := make(chan error, 1)
	go func() {
		raw <- store.Relationships().CreateRelationships(rawctx, []relationships.CreateRelationship{rel("doc", "d", "viewer", "user", "u")})
	}()
	<-rawctx.checked
	cancel()
	close(release)
	if err := <-guarded; err != nil {
		t.Fatal(err)
	}
	if err := <-raw; !errors.Is(err, context.Canceled) {
		t.Fatalf("blocked raw write ignored cancellation: %v", err)
	}
	page, err := store.Audit().List(ctx, audit.Filter{}, list.Request{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Change.Relationship.Relation != "owner" {
		t.Fatalf("raw writer leaked a fact/audit: %+v %v", page, err)
	}
}
