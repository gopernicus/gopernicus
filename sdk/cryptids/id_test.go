package cryptids

import (
	"strings"
	"testing"
)

func TestAlphabet_UniqueAndSized(t *testing.T) {
	if len(Alphabet) != 52 {
		t.Fatalf("Alphabet has %d bytes, want 52", len(Alphabet))
	}
	if err := uniqueBytes(Alphabet); err != nil {
		t.Fatalf("Alphabet has duplicate bytes: %v", err)
	}
}

func TestDefaultNanoID_ConstantsAreValid(t *testing.T) {
	// defaultNanoID discards NanoID's error; this guards that doing so stays
	// structurally safe.
	if _, err := NanoID("", 0); err != nil {
		t.Fatalf("NanoID defaults must never error: %v", err)
	}
	if defaultNanoID == nil {
		t.Fatal("defaultNanoID is nil")
	}
}

func TestIDGenerator_ZeroValueEmitsDefaults(t *testing.T) {
	id, err := IDGenerator{}.Generate()
	if err != nil {
		t.Fatalf("zero-value Generate: %v", err)
	}
	if len(id) != DefaultLength {
		t.Fatalf("len = %d, want %d", len(id), DefaultLength)
	}
	for i := 0; i < len(id); i++ {
		if !strings.ContainsRune(Alphabet, rune(id[i])) {
			t.Fatalf("byte %q not in Alphabet", id[i])
		}
	}
	two, _ := IDGenerator{}.Generate()
	if id == two {
		t.Fatal("two Generate calls returned the same ID")
	}
}

func TestNewGenerator_NilMeansDefault(t *testing.T) {
	id, err := NewGenerator(nil).Generate()
	if err != nil || len(id) != DefaultLength {
		t.Fatalf("NewGenerator(nil): %q, %v", id, err)
	}
}

func TestNanoID_CustomAlphabetAndSize(t *testing.T) {
	fn, err := NanoID("0123456789", 8)
	if err != nil {
		t.Fatalf("NanoID: %v", err)
	}
	id, err := NewGenerator(fn).Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(id) != 8 {
		t.Fatalf("len = %d, want 8", len(id))
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			t.Fatalf("byte %q outside custom alphabet", id[i])
		}
	}
}

func TestNanoID_ValidationAtConstruction(t *testing.T) {
	for name, tc := range map[string]struct {
		alphabet string
		size     int
	}{
		"one-char alphabet": {"a", 21},
		"duplicate bytes":   {"abca", 21},
		"negative size":     {"abc", -3},
	} {
		if _, err := NanoID(tc.alphabet, tc.size); err == nil {
			t.Errorf("%s: expected construction error, got nil", name)
		}
	}
}

func TestIDGenerator_StrategySwap(t *testing.T) {
	called := false
	g := NewGenerator(func() (string, error) {
		called = true
		return "swapped", nil
	})
	id, err := g.Generate()
	if err != nil || id != "swapped" || !called {
		t.Fatalf("swap not honored: %q, %v, called=%v", id, err, called)
	}
}

func TestMustGenerate_ReturnsString(t *testing.T) {
	if got := (IDGenerator{}).MustGenerate(); len(got) != DefaultLength {
		t.Fatalf("MustGenerate len = %d, want %d", len(got), DefaultLength)
	}
}

func TestDatabase_YieldsEmptyIntentionally(t *testing.T) {
	id, err := NewGenerator(Database).Generate()
	if err != nil {
		t.Fatalf("Database strategy errored: %v", err)
	}
	if id != "" {
		t.Fatalf("Database strategy yielded %q, want empty", id)
	}
	// MustGenerate must NOT panic on the intentional empty — emptiness is the
	// store-boundary signal, not a failure.
	if got := NewGenerator(Database).MustGenerate(); got != "" {
		t.Fatalf("MustGenerate = %q, want empty", got)
	}
}

func TestDefault_DistributionCoverage(t *testing.T) {
	g := IDGenerator{}
	seen := make(map[byte]bool, len(Alphabet))
	for i := 0; i < 10_000; i++ {
		id := g.MustGenerate()
		for j := 0; j < len(id); j++ {
			seen[id[j]] = true
		}
	}
	for i := 0; i < len(Alphabet); i++ {
		if !seen[Alphabet[i]] {
			t.Errorf("alphabet byte %q never appeared across 10k IDs", Alphabet[i])
		}
	}
}
