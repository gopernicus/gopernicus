package sdk

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestUUIDv7_CanonicalVersionAndVariant(t *testing.T) {
	gen := UUIDv7()
	for i := 0; i < 1_000; i++ {
		id, err := gen()
		if err != nil {
			t.Fatalf("UUIDv7: %v", err)
		}
		if len(id) != 36 {
			t.Fatalf("len(%q) = %d, want 36", id, len(id))
		}
		for j := 0; j < len(id); j++ {
			c := id[j]
			switch j {
			case 8, 13, 18, 23:
				if c != '-' {
					t.Fatalf("%q: byte %d = %q, want '-'", id, j, c)
				}
			default:
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					t.Fatalf("%q: byte %d = %q, want lowercase hex", id, j, c)
				}
			}
		}
		if id[14] != '7' {
			t.Fatalf("%q: version nibble = %q, want '7'", id, id[14])
		}
		if !strings.ContainsRune("89ab", rune(id[19])) {
			t.Fatalf("%q: variant nibble = %q, want binary 10xx", id, id[19])
		}
	}
}

func TestUUIDv7_TimestampIsUnixMillis(t *testing.T) {
	before := time.Now().UnixMilli()
	id, _ := UUIDv7()()
	after := time.Now().UnixMilli()
	ms, err := strconv.ParseInt(strings.ReplaceAll(id[:13], "-", ""), 16, 64)
	if err != nil {
		t.Fatalf("parse timestamp of %q: %v", id, err)
	}
	if ms < before || ms > after+1 {
		t.Fatalf("timestamp %d outside [%d, %d]", ms, before, after+1)
	}
}

func TestUUIDv7_Unique(t *testing.T) {
	gen := UUIDv7()
	seen := make(map[string]struct{}, 100_000)
	for i := 0; i < 100_000; i++ {
		id, _ := gen()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q after %d mints", id, i)
		}
		seen[id] = struct{}{}
	}
}

func TestUUIDv7_MonotonicWithinGenerator(t *testing.T) {
	gen := UUIDv7()
	prev, _ := gen()
	for i := 0; i < 10_000; i++ {
		id, _ := gen()
		if id <= prev {
			t.Fatalf("mint %d: %q does not sort after %q", i, id, prev)
		}
		prev = id
	}
}

func TestUUIDv7_EachGeneratorMonotonic(t *testing.T) {
	a, b := UUIDv7(), UUIDv7()
	prevA, _ := a()
	prevB, _ := b()
	for i := 0; i < 5_000; i++ {
		idA, _ := a()
		idB, _ := b()
		if idA <= prevA {
			t.Fatalf("generator a, mint %d: %q does not sort after %q", i, idA, prevA)
		}
		if idB <= prevB {
			t.Fatalf("generator b, mint %d: %q does not sort after %q", i, idB, prevB)
		}
		prevA, prevB = idA, idB
	}
}

func TestUUIDv7_ConcurrentMintsUniqueAndOrderedPerGoroutine(t *testing.T) {
	gen := UUIDv7()
	const workers, per = 8, 2_000
	results := make([][]string, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				id, _ := gen()
				results[w] = append(results[w], id)
			}
		}(w)
	}
	wg.Wait()
	seen := make(map[string]struct{}, workers*per)
	for w, ids := range results {
		for i, id := range ids {
			if _, dup := seen[id]; dup {
				t.Fatalf("duplicate id %q", id)
			}
			seen[id] = struct{}{}
			if i > 0 && id <= ids[i-1] {
				t.Fatalf("worker %d mint %d: %q does not sort after %q", w, i, id, ids[i-1])
			}
		}
	}
}

func TestIDGeneratorOr_ConfiguredWins(t *testing.T) {
	g := NewIDGenerator(func() (string, error) { return "configured", nil }).Or(func() (string, error) {
		return "fallback", nil
	})
	if id, err := g.Generate(); err != nil || id != "configured" {
		t.Fatalf("Or on a configured generator = %q, %v; want configured", id, err)
	}
}

func TestIDGeneratorOr_ZeroValueTakesFallback(t *testing.T) {
	g := IDGenerator{}.Or(func() (string, error) { return "fallback", nil })
	if id, err := g.Generate(); err != nil || id != "fallback" {
		t.Fatalf("Or on the zero value = %q, %v; want fallback", id, err)
	}
	id, err := IDGenerator{}.Or(UUIDv7()).Generate()
	if err != nil || len(id) != 36 || id[14] != '7' {
		t.Fatalf("Or(UUIDv7()) on the zero value = %q, %v; want a UUIDv7", id, err)
	}
}

func TestIDGeneratorOr_ForcedNanoIDSurvivesClassDefault(t *testing.T) {
	nano, err := NanoID("", 0)
	if err != nil {
		t.Fatalf("NanoID: %v", err)
	}
	id, err := NewIDGenerator(nano).Or(UUIDv7()).Generate()
	if err != nil || len(id) != DefaultIDLength {
		t.Fatalf("forced NanoID through Or = %q, %v; want a %d-char NanoID", id, err, DefaultIDLength)
	}
}

func TestIDGenerator_ZeroValueStillNanoIDAfterOr(t *testing.T) {
	var g IDGenerator
	_ = g.Or(UUIDv7())
	id, err := g.Generate()
	if err != nil || len(id) != DefaultIDLength {
		t.Fatalf("zero value after Or = %q, %v; want a NanoID", id, err)
	}
}
