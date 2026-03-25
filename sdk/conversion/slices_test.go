package conversion

import "testing"

func TestOverlap(t *testing.T) {
	got := Overlap([]string{"a", "b", "c"}, []string{"b", "c", "d"})
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Errorf("Overlap(strings) = %v, want [b c]", got)
	}

	gotInt := Overlap([]int{1, 2, 3, 4}, []int{2, 4, 6})
	if len(gotInt) != 2 || gotInt[0] != 2 || gotInt[1] != 4 {
		t.Errorf("Overlap(ints) = %v, want [2 4]", gotInt)
	}

	gotEmpty := Overlap([]string{"a"}, []string{"b"})
	if len(gotEmpty) != 0 {
		t.Errorf("Overlap(no match) = %v, want []", gotEmpty)
	}

	gotNil := Overlap[string](nil, []string{"a"})
	if len(gotNil) != 0 {
		t.Errorf("Overlap(nil, ...) = %v, want []", gotNil)
	}
}
