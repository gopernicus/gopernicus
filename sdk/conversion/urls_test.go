package conversion

import "testing"

func TestToURLSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"Hello  World!", "hello-world"},
		{"Café Résumé", "cafe-resume"},
		{"already-slugified", "already-slugified"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"under_scores_too", "under-scores-too"},
		{"MiXeD CaSe", "mixed-case"},
		{"special!@#$%chars", "specialchars"},
		{"multiple---hyphens", "multiple-hyphens"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := ToURLSlug(tt.input); got != tt.want {
			t.Errorf("ToURLSlug(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
