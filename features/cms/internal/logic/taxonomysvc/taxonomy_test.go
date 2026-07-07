package taxonomysvc

import (
	"context"
	"errors"
	"github.com/gopernicus/gopernicus/features/cms/logic/taxonomy"
	"sort"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk/errs"
)

// fakeTerms is an in-memory TermRepository for driving the service.
type fakeTerms struct {
	byID map[string]taxonomy.Term
}

func newFakeTerms() *fakeTerms { return &fakeTerms{byID: map[string]taxonomy.Term{}} }

func (f *fakeTerms) Get(ctx context.Context, id string) (taxonomy.Term, error) {
	t, ok := f.byID[id]
	if !ok {
		return taxonomy.Term{}, errs.ErrNotFound
	}
	return t, nil
}
func (f *fakeTerms) GetBySlug(ctx context.Context, kind taxonomy.Kind, slug string) (taxonomy.Term, error) {
	for _, t := range f.byID {
		if t.Kind == kind && t.Slug == slug {
			return t, nil
		}
	}
	return taxonomy.Term{}, errs.ErrNotFound
}
func (f *fakeTerms) ListByKind(ctx context.Context, kind taxonomy.Kind) ([]taxonomy.Term, error) {
	var out []taxonomy.Term
	for _, t := range f.byID {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (f *fakeTerms) Create(ctx context.Context, t taxonomy.Term) (taxonomy.Term, error) {
	for _, ex := range f.byID {
		if ex.Kind == t.Kind && ex.Slug == t.Slug {
			return taxonomy.Term{}, errs.ErrAlreadyExists
		}
	}
	f.byID[t.ID] = t
	return t, nil
}
func (f *fakeTerms) Update(ctx context.Context, id string, t taxonomy.Term) (taxonomy.Term, error) {
	if _, ok := f.byID[id]; !ok {
		return taxonomy.Term{}, errs.ErrNotFound
	}
	f.byID[id] = t
	return t, nil
}
func (f *fakeTerms) Delete(ctx context.Context, id string) error {
	delete(f.byID, id)
	return nil
}

func clock(start time.Time) Clock {
	n := 0
	return func() time.Time {
		t := start.Add(time.Duration(n) * time.Second)
		n++
		return t
	}
}

func TestService_Terms(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeTerms(), clock(time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)))

	cat, err := svc.CreateTerm(ctx, taxonomy.KindCategory, "News & Updates", "")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	if cat.Slug != "news-updates" || cat.Kind != taxonomy.KindCategory {
		t.Errorf("unexpected category: %+v", cat)
	}

	// A tag may share the slug of a category (uniqueness is per-kind).
	tag, err := svc.CreateTerm(ctx, taxonomy.KindTag, "News Updates", "parent-ignored")
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	if tag.Slug != "news-updates" || tag.ParentID != "" {
		t.Errorf("tag should be flat with shared slug: %+v", tag)
	}

	// Get + by slug.
	if got, err := svc.GetTermBySlug(ctx, taxonomy.KindCategory, "news-updates"); err != nil || got.ID != cat.ID {
		t.Fatalf("get category by slug: %v %+v", err, got)
	}

	// List by kind.
	cats, err := svc.ListTerms(ctx, taxonomy.KindCategory)
	if err != nil || len(cats) != 1 {
		t.Fatalf("list categories: %v %+v", err, cats)
	}

	// Edit.
	edited, err := svc.EditTerm(ctx, cat.ID, "Announcements", "")
	if err != nil || edited.Slug != "announcements" {
		t.Fatalf("edit: %v %+v", err, edited)
	}

	// Delete.
	if err := svc.DeleteTerm(ctx, cat.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetTerm(ctx, cat.ID); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("deleted term should be gone")
	}
}

func TestService_TermErrors(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeTerms(), clock(time.Now()))

	if _, err := svc.CreateTerm(ctx, taxonomy.Kind("bogus"), "x", ""); !errors.Is(err, errs.ErrInvalidInput) {
		t.Errorf("bad kind: %v", err)
	}
	if _, err := svc.CreateTerm(ctx, taxonomy.KindTag, "  ", ""); !errors.Is(err, errs.ErrInvalidInput) {
		t.Errorf("blank name: %v", err)
	}
	if _, err := svc.CreateTerm(ctx, taxonomy.KindTag, "Go", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateTerm(ctx, taxonomy.KindTag, "Go", ""); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("dup slug per kind: %v", err)
	}
}
