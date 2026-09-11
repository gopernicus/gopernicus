package sdk

import (
	"strings"
	"testing"
)

func TestDefaultIDAlphabet_UniqueAndSized(t *testing.T) {
	if len(DefaultIDAlphabet) != 52 {
		t.Fatalf("DefaultIDAlphabet has %d bytes, want 52", len(DefaultIDAlphabet))
	}
	if err := uniqueBytes(DefaultIDAlphabet); err != nil {
		t.Fatalf("DefaultIDAlphabet has duplicate bytes: %v", err)
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
	if len(id) != DefaultIDLength {
		t.Fatalf("len = %d, want %d", len(id), DefaultIDLength)
	}
	for i := 0; i < len(id); i++ {
		if !strings.ContainsRune(DefaultIDAlphabet, rune(id[i])) {
			t.Fatalf("byte %q not in DefaultIDAlphabet", id[i])
		}
	}
	two, _ := IDGenerator{}.Generate()
	if id == two {
		t.Fatal("two Generate calls returned the same ID")
	}
}

func TestNewIDGenerator_NilMeansDefault(t *testing.T) {
	id, err := NewIDGenerator(nil).Generate()
	if err != nil || len(id) != DefaultIDLength {
		t.Fatalf("NewIDGenerator(nil): %q, %v", id, err)
	}
}

func TestNanoID_CustomAlphabetAndSize(t *testing.T) {
	fn, err := NanoID("0123456789", 8)
	if err != nil {
		t.Fatalf("NanoID: %v", err)
	}
	id, err := NewIDGenerator(fn).Generate()
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
		"one Unicode rune":  {"é", 1},
		"Unicode alphabet":  {"aé", 8},
		"non-UTF8 byte":     {"a\xff", 8},
	} {
		if _, err := NanoID(tc.alphabet, tc.size); err == nil {
			t.Errorf("%s: expected construction error, got nil", name)
		}
	}
}

func TestIDGenerator_StrategySwap(t *testing.T) {
	called := false
	g := NewIDGenerator(func() (string, error) {
		called = true
		return "swapped", nil
	})
	id, err := g.Generate()
	if err != nil || id != "swapped" || !called {
		t.Fatalf("swap not honored: %q, %v, called=%v", id, err, called)
	}
}

func TestIDGeneratorMustGenerate_ReturnsString(t *testing.T) {
	if got := (IDGenerator{}).MustGenerate(); len(got) != DefaultIDLength {
		t.Fatalf("MustGenerate len = %d, want %d", len(got), DefaultIDLength)
	}
}

func TestDatabaseID_YieldsEmptyIntentionally(t *testing.T) {
	id, err := NewIDGenerator(DatabaseID).Generate()
	if err != nil {
		t.Fatalf("DatabaseID strategy errored: %v", err)
	}
	if id != "" {
		t.Fatalf("DatabaseID strategy yielded %q, want empty", id)
	}
	// MustGenerate must NOT panic on the intentional empty — emptiness is the
	// store-boundary signal, not a failure.
	if got := NewIDGenerator(DatabaseID).MustGenerate(); got != "" {
		t.Fatalf("MustGenerate = %q, want empty", got)
	}
}

func TestDefaultIDDistributionCoverage(t *testing.T) {
	g := IDGenerator{}
	seen := make(map[byte]bool, len(DefaultIDAlphabet))
	for i := 0; i < 10_000; i++ {
		id := g.MustGenerate()
		for j := 0; j < len(id); j++ {
			seen[id[j]] = true
		}
	}
	for i := 0; i < len(DefaultIDAlphabet); i++ {
		if !seen[DefaultIDAlphabet[i]] {
			t.Errorf("alphabet byte %q never appeared across 10k IDs", DefaultIDAlphabet[i])
		}
	}
}
