package crud

import (
	"fmt"
	"testing"
)

func TestFieldZeroValueIsAbsent(t *testing.T) {
	var f Field[string]

	if f.Set {
		t.Error("zero-value Field.Set = true, want false (absent)")
	}
	if f.Value != "" {
		t.Errorf("zero-value Field.Value = %q, want the zero value", f.Value)
	}
}

func TestSome(t *testing.T) {
	f := Some("hello")

	if !f.Set {
		t.Error("Some(...).Set = false, want true")
	}
	if f.Value != "hello" {
		t.Errorf("Some(...).Value = %q, want %q", f.Value, "hello")
	}
}

func TestSomeCarriesTheZeroValue(t *testing.T) {
	// An explicit empty string is a WRITE, not an absence — the distinction
	// Field exists for.
	f := Some("")

	if !f.Set {
		t.Error("Some(\"\").Set = false, want true")
	}
	if got := Overlay("current", f); got != "" {
		t.Errorf("Overlay(current, Some(\"\")) = %q, want %q", got, "")
	}
}

func TestOverlayLeavesCurrentWhenAbsent(t *testing.T) {
	var absent Field[int]

	if got := Overlay(42, absent); got != 42 {
		t.Errorf("Overlay(42, absent) = %d, want 42", got)
	}
}

func TestOverlayReplacesWhenSet(t *testing.T) {
	if got := Overlay(42, Some(7)); got != 7 {
		t.Errorf("Overlay(42, Some(7)) = %d, want 7", got)
	}
}

func TestOverlayNullableExplicitClear(t *testing.T) {
	summary := "the old summary"
	current := &summary

	if got := Overlay(current, Field[*string]{}); got != current {
		t.Errorf("Overlay(current, absent) = %v, want the current pointer", got)
	}

	got := Overlay(current, Some[*string](nil))
	if got != nil {
		t.Errorf("Overlay(current, Some[*string](nil)) = %v, want nil (explicit clear)", *got)
	}

	replacement := "the new summary"
	if got := Overlay(current, Some(&replacement)); got == nil || *got != replacement {
		t.Errorf("Overlay(current, Some(&replacement)) = %v, want %q", got, replacement)
	}
}

func TestOverlayStructValue(t *testing.T) {
	type tag struct{ Name string }

	current := []tag{{Name: "go"}}
	if got := Overlay(current, Field[[]tag]{}); len(got) != 1 || got[0].Name != "go" {
		t.Errorf("Overlay(current, absent) = %+v, want the current slice", got)
	}
	if got := Overlay(current, Some([]tag{})); len(got) != 0 {
		t.Errorf("Overlay(current, Some(empty)) = %+v, want an empty slice", got)
	}
}

// ExampleOverlay shows a two-field sparse patch: the caller sent a new title
// and cleared the summary, and left everything else alone.
func ExampleOverlay() {
	type article struct {
		Title   string
		Summary *string
		Slug    string
	}

	type patch struct {
		Title   Field[string]
		Summary Field[*string]
		Slug    Field[string]
	}

	summary := "the old summary"
	current := article{Title: "Old title", Summary: &summary, Slug: "old-title"}

	in := patch{
		Title:   Some("New title"),
		Summary: Some[*string](nil), // explicit clear
		// Slug is absent: unchanged.
	}

	current.Title = Overlay(current.Title, in.Title)
	current.Summary = Overlay(current.Summary, in.Summary)
	current.Slug = Overlay(current.Slug, in.Slug)

	fmt.Println(current.Title, current.Summary == nil, current.Slug)
	// Output: New title true old-title
}
