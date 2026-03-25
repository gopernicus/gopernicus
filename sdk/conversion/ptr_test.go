package conversion

import "testing"

func TestPtr(t *testing.T) {
	s := Ptr("hello")
	if *s != "hello" {
		t.Errorf("Ptr(string) = %q, want %q", *s, "hello")
	}

	n := Ptr(42)
	if *n != 42 {
		t.Errorf("Ptr(int) = %d, want %d", *n, 42)
	}

	b := Ptr(true)
	if *b != true {
		t.Errorf("Ptr(bool) = %v, want true", *b)
	}
}

func TestDeref(t *testing.T) {
	s := "hello"
	if got := Deref(&s); got != "hello" {
		t.Errorf("Deref(&string) = %q, want %q", got, "hello")
	}

	if got := Deref[string](nil); got != "" {
		t.Errorf("Deref[string](nil) = %q, want empty", got)
	}

	n := 42
	if got := Deref(&n); got != 42 {
		t.Errorf("Deref(&int) = %d, want 42", got)
	}

	if got := Deref[int](nil); got != 0 {
		t.Errorf("Deref[int](nil) = %d, want 0", got)
	}

	if got := Deref[bool](nil); got != false {
		t.Errorf("Deref[bool](nil) = %v, want false", got)
	}
}

func TestDerefOr(t *testing.T) {
	s := "hello"
	if got := DerefOr(&s, "fallback"); got != "hello" {
		t.Errorf("DerefOr(&string, fallback) = %q, want %q", got, "hello")
	}

	if got := DerefOr[string](nil, "fallback"); got != "fallback" {
		t.Errorf("DerefOr[string](nil, fallback) = %q, want %q", got, "fallback")
	}

	n := 0
	if got := DerefOr(&n, 99); got != 0 {
		t.Errorf("DerefOr(&0, 99) = %d, want 0 (zero value is valid)", got)
	}

	if got := DerefOr[int](nil, 99); got != 99 {
		t.Errorf("DerefOr[int](nil, 99) = %d, want 99", got)
	}
}
