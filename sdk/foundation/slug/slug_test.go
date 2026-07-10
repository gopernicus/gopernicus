package slug

import "testing"

func TestMake(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ascii lowering", "Hello World", "hello-world"},
		{"punctuation collapses to single hyphen", "Hello, World!!!", "hello-world"},
		{"runs of whitespace collapse", "Hello   World", "hello-world"},
		{"leading and trailing whitespace trimmed", "  Hello World  ", "hello-world"},
		{"leading and trailing punctuation trimmed", "--Hello World--", "hello-world"},
		{"digits are kept", "Product 3000", "product-3000"},
		// D-5 behavior change: common Latin-1 accented letters fold to their
		// ASCII base before the keep-[a-z0-9] pass, so they are transliterated
		// rather than dropped as separators.
		{"single accented word folds", "Café", "cafe"},
		{"accent folding across words", "Café Résumé", "cafe-resume"},
		{"tilde n folds", "El Niño", "el-nino"},
		{"cedilla folds", "Curaçao", "curacao"},
		{"eszett folds to single s", "Straße", "strase"},
		{"umlauts fold", "Müller Öl", "muller-ol"},
		{"non-folded non-ASCII still drops as a separator", "Emoji 🚀 Rocket", "emoji-rocket"},
		{"empty input", "", ""},
		{"only punctuation collapses to empty", "!!!", ""},
		{"only whitespace collapses to empty", "   ", ""},
		{"already a slug is unchanged", "already-a-slug", "already-a-slug"},
		{"mixed case with numbers and symbols", "  Widget_3000 (New!) ", "widget-3000-new"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Make(tt.in); got != tt.want {
				t.Errorf("Make(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMake_Idempotent(t *testing.T) {
	inputs := []string{
		"Hello World",
		"  --Hello, World!!--  ",
		"Café",
		"Café Résumé",
		"Straße",
		"already-a-slug",
		"",
		"Widget_3000 (New!)",
	}
	for _, in := range inputs {
		once := Make(in)
		twice := Make(once)
		if once != twice {
			t.Errorf("Make(Make(%q)) = %q, want %q (Make(%q))", in, twice, once, in)
		}
	}
}
