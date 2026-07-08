package entrysvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/cms/domain/content"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/errs"
	sdkevents "github.com/gopernicus/gopernicus/sdk/events"
)

// fakeRepo is an in-memory content.EntryRepository for service tests.
type fakeRepo struct {
	entries map[string]content.Entry
	terms   map[string][]string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{entries: map[string]content.Entry{}, terms: map[string][]string{}}
}

func (r *fakeRepo) Create(_ context.Context, e content.Entry) (content.Entry, error) {
	for _, ex := range r.entries {
		if ex.Type == e.Type && ex.Slug == e.Slug {
			return content.Entry{}, errs.ErrAlreadyExists
		}
	}
	r.entries[e.ID] = e
	return e, nil
}

func (r *fakeRepo) Update(_ context.Context, id string, e content.Entry) (content.Entry, error) {
	if _, ok := r.entries[id]; !ok {
		return content.Entry{}, errs.ErrNotFound
	}
	r.entries[id] = e
	return e, nil
}

func (r *fakeRepo) Get(_ context.Context, id string) (content.Entry, error) {
	e, ok := r.entries[id]
	if !ok {
		return content.Entry{}, errs.ErrNotFound
	}
	return e, nil
}

func (r *fakeRepo) GetBySlug(_ context.Context, typ, slug string) (content.Entry, error) {
	for _, e := range r.entries {
		if e.Type == typ && e.Slug == slug {
			return e, nil
		}
	}
	return content.Entry{}, errs.ErrNotFound
}

func (r *fakeRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.entries[id]; !ok {
		return errs.ErrNotFound
	}
	delete(r.entries, id)
	return nil
}

func (r *fakeRepo) List(_ context.Context, q content.EntryQuery) (crud.Page[content.Entry], error) {
	var items []content.Entry
	for _, e := range r.entries {
		if e.Type == q.Type && (q.Status == "" || e.Status == q.Status) {
			items = append(items, e)
		}
	}
	return crud.Page[content.Entry]{Items: items}, nil
}

func (r *fakeRepo) ListByTerm(_ context.Context, termID string, q content.EntryQuery) (crud.Page[content.Entry], error) {
	return crud.Page[content.Entry]{}, nil
}

func (r *fakeRepo) SetTerms(_ context.Context, entryID string, termIDs []string) error {
	r.terms[entryID] = termIDs
	return nil
}

func testRegistry(t *testing.T) *content.Registry {
	t.Helper()
	reg := content.NewRegistry()
	if err := reg.Register(content.ContentType{
		Slug: "article", Singular: "Article", Plural: "Articles", Routable: true,
		Fields: []content.FieldDef{
			{Key: "subtitle", Kind: content.KindText},
			{Key: "rating", Kind: content.KindNumber, Required: true},
			{Key: "related", Kind: content.KindRelation, RelTo: "article"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func fixedClock() Clock {
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return now }
}

func TestService_CreateAndGet(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, testRegistry(t), fixedClock())
	ctx := context.Background()

	e, err := svc.Create(ctx, "article", Input{
		Title:  "Hello World",
		Status: content.StatusPublished,
		Fields: content.Fields{
			"subtitle": {Raw: "a subtitle"},
			"rating":   {Raw: "4"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.Slug != "hello-world" || e.Type != "article" {
		t.Fatalf("entry = %+v", e)
	}
	got, err := svc.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Fields.Int("rating") != 4 || got.Fields.String("subtitle") != "a subtitle" {
		t.Fatalf("fields = %+v", got.Fields)
	}
}

func TestService_Create_UnknownType(t *testing.T) {
	svc := NewService(newFakeRepo(), testRegistry(t), fixedClock())
	if _, err := svc.Create(context.Background(), "ghost", Input{Title: "x"}); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestService_Create_MissingRequiredField(t *testing.T) {
	svc := NewService(newFakeRepo(), testRegistry(t), fixedClock())
	_, err := svc.Create(context.Background(), "article", Input{Title: "x", Fields: content.Fields{"subtitle": {Raw: "y"}}})
	if !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestService_Create_RelationMustExist(t *testing.T) {
	svc := NewService(newFakeRepo(), testRegistry(t), fixedClock())
	_, err := svc.Create(context.Background(), "article", Input{
		Title:  "x",
		Fields: content.Fields{"rating": {Raw: "1"}, "related": {Raw: "no-such-entry"}},
	})
	if !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestService_Create_RelationResolves(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, testRegistry(t), fixedClock())
	ctx := context.Background()

	target, err := svc.Create(ctx, "article", Input{Title: "Target", Fields: content.Fields{"rating": {Raw: "1"}}})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := svc.Create(ctx, "article", Input{
		Title:  "Source",
		Fields: content.Fields{"rating": {Raw: "1"}, "related": {Raw: target.ID}},
	}); err != nil {
		t.Fatalf("create source with relation: %v", err)
	}
}

func TestService_PublishUnpublish(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, testRegistry(t), fixedClock())
	ctx := context.Background()

	e, err := svc.Create(ctx, "article", Input{Title: "Draft", Status: content.StatusDraft, Fields: content.Fields{"rating": {Raw: "1"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pub, err := svc.Publish(ctx, e.ID)
	if err != nil || pub.Status != content.StatusPublished || pub.PublishedAt == nil {
		t.Fatalf("publish: %v %+v", err, pub)
	}
	unp, err := svc.Unpublish(ctx, e.ID)
	if err != nil || unp.Status != content.StatusDraft {
		t.Fatalf("unpublish: %v %+v", err, unp)
	}
	if unp.PublishedAt == nil {
		t.Fatal("PublishedAt should be preserved as history after unpublish")
	}
}

func TestService_EditReplacesFields(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, testRegistry(t), fixedClock())
	ctx := context.Background()

	e, err := svc.Create(ctx, "article", Input{Title: "Orig", Fields: content.Fields{"rating": {Raw: "1"}, "subtitle": {Raw: "old"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	edited, err := svc.Edit(ctx, e.ID, Input{Title: "Orig", Fields: content.Fields{"rating": {Raw: "2"}}})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if edited.Fields.Int("rating") != 2 {
		t.Fatalf("rating = %d, want 2", edited.Fields.Int("rating"))
	}
	if _, ok := edited.Fields["subtitle"]; ok {
		t.Fatal("edit should have dropped the omitted optional subtitle")
	}
}

// recordingBus wraps a Memory bus and forces synchronous delivery so a test can
// assert on the content events entrysvc emits deterministically (WithSync).
type recordingBus struct {
	bus  *sdkevents.Memory
	seen []sdkevents.Event
}

func newRecordingBus(t *testing.T) *recordingBus {
	t.Helper()
	rb := &recordingBus{bus: sdkevents.NewMemory()}
	sub, err := rb.bus.Subscribe("*", func(_ context.Context, e sdkevents.Event) error {
		rb.seen = append(rb.seen, e)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	t.Cleanup(func() {
		_ = sub.Unsubscribe()
		_ = rb.bus.Close(context.Background())
	})
	return rb
}

// Emit forces WithSync so the recording handler has run before Emit returns.
func (rb *recordingBus) Emit(ctx context.Context, e sdkevents.Event, opts ...sdkevents.EmitOption) error {
	return rb.bus.Emit(ctx, e, append(opts, sdkevents.WithSync())...)
}

func (rb *recordingBus) last() sdkevents.Event { return rb.seen[len(rb.seen)-1] }

// assertContentEvent checks the type and the aggregate metadata (aggregate_type
// "entry", aggregate_id = entryID) an entrysvc content event must carry.
func assertContentEvent(t *testing.T, e sdkevents.Event, wantType, wantID string) {
	t.Helper()
	if e.Type() != wantType {
		t.Fatalf("event type = %q, want %q", e.Type(), wantType)
	}
	md, ok := e.(sdkevents.Metadata)
	if !ok {
		t.Fatalf("event %q does not carry Metadata", e.Type())
	}
	if md.AggregateType() == nil || *md.AggregateType() != "entry" {
		t.Fatalf("aggregate_type = %v, want \"entry\"", md.AggregateType())
	}
	if md.AggregateID() == nil || *md.AggregateID() != wantID {
		t.Fatalf("aggregate_id = %v, want %q", md.AggregateID(), wantID)
	}
}

func TestService_Emits(t *testing.T) {
	rb := newRecordingBus(t)
	svc := NewService(newFakeRepo(), testRegistry(t), fixedClock(), rb)
	ctx := context.Background()

	e, err := svc.Create(ctx, "article", Input{Title: "One", Fields: content.Fields{"rating": {Raw: "1"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	assertContentEvent(t, rb.last(), "content.updated", e.ID)

	if _, err := svc.Edit(ctx, e.ID, Input{Title: "One", Fields: content.Fields{"rating": {Raw: "2"}}}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	assertContentEvent(t, rb.last(), "content.updated", e.ID)

	if _, err := svc.Publish(ctx, e.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	assertContentEvent(t, rb.last(), "content.published", e.ID)

	if _, err := svc.Unpublish(ctx, e.ID); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	assertContentEvent(t, rb.last(), "content.updated", e.ID)

	if err := svc.SetTerms(ctx, e.ID, []string{"t1"}); err != nil {
		t.Fatalf("set terms: %v", err)
	}
	assertContentEvent(t, rb.last(), "content.updated", e.ID)

	if err := svc.Delete(ctx, e.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	assertContentEvent(t, rb.last(), "content.deleted", e.ID)
}

// failingEmitter always returns an error to prove emit errors are swallowed:
// the domain write already succeeded, so the caller sees no error.
type failingEmitter struct{}

func (failingEmitter) Emit(context.Context, sdkevents.Event, ...sdkevents.EmitOption) error {
	return errors.New("emit boom")
}

func TestService_EmitErrorNotReturned(t *testing.T) {
	svc := NewService(newFakeRepo(), testRegistry(t), fixedClock(), failingEmitter{})
	e, err := svc.Create(context.Background(), "article", Input{Title: "Resilient", Fields: content.Fields{"rating": {Raw: "1"}}})
	if err != nil {
		t.Fatalf("Create returned emit error to caller: %v", err)
	}
	if e.ID == "" {
		t.Fatal("entry was not created despite emit failure")
	}
}

func TestService_NilEmitterNoPanic(t *testing.T) {
	svc := NewService(newFakeRepo(), testRegistry(t), fixedClock(), nil)
	if _, err := svc.Create(context.Background(), "article", Input{Title: "Quiet", Fields: content.Fields{"rating": {Raw: "1"}}}); err != nil {
		t.Fatalf("create with nil emitter: %v", err)
	}
}
