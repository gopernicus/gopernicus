package cryptids

import (
	"strings"
	"testing"
)

func TestGenerateID(t *testing.T) {
	t.Run("correct length", func(t *testing.T) {
		id, err := GenerateID()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(id) != idLength {
			t.Errorf("len = %d, want %d", len(id), idLength)
		}
	})

	t.Run("valid alphabet characters", func(t *testing.T) {
		id, err := GenerateID()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, ch := range id {
			if !strings.ContainsRune(idAlphabet, ch) {
				t.Errorf("invalid character: %c", ch)
			}
		}
	})

	t.Run("unique IDs", func(t *testing.T) {
		seen := make(map[string]bool)
		for i := 0; i < 1000; i++ {
			id, err := GenerateID()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if seen[id] {
				t.Fatalf("duplicate ID: %s", id)
			}
			seen[id] = true
		}
	})

	t.Run("URL safe", func(t *testing.T) {
		id, err := GenerateID()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, unsafe := range []string{"/", "+", "=", "?", "&", "#", " "} {
			if strings.Contains(id, unsafe) {
				t.Errorf("contains URL-unsafe character: %s", unsafe)
			}
		}
	})
}

func TestGenerateCustomID(t *testing.T) {
	t.Run("custom length", func(t *testing.T) {
		for _, size := range []int{1, 5, 16, 64, 128} {
			id, err := GenerateCustomID(idAlphabet, size)
			if err != nil {
				t.Fatalf("size %d: unexpected error: %v", size, err)
			}
			if len(id) != size {
				t.Errorf("size %d: len = %d", size, len(id))
			}
		}
	})

	t.Run("custom alphabet", func(t *testing.T) {
		alphabet := "abc123"
		id, err := GenerateCustomID(alphabet, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, ch := range id {
			if !strings.ContainsRune(alphabet, ch) {
				t.Errorf("character not in alphabet: %c", ch)
			}
		}
	})

	t.Run("binary alphabet", func(t *testing.T) {
		id, err := GenerateCustomID("01", 32)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, ch := range id {
			if ch != '0' && ch != '1' {
				t.Errorf("non-binary character: %c", ch)
			}
		}
	})

	t.Run("rejects short alphabet", func(t *testing.T) {
		_, err := GenerateCustomID("a", 10)
		if err == nil {
			t.Fatal("expected error for single-char alphabet")
		}
	})

	t.Run("rejects empty alphabet", func(t *testing.T) {
		_, err := GenerateCustomID("", 10)
		if err == nil {
			t.Fatal("expected error for empty alphabet")
		}
	})

	t.Run("rejects zero size", func(t *testing.T) {
		_, err := GenerateCustomID(idAlphabet, 0)
		if err == nil {
			t.Fatal("expected error for zero size")
		}
	})

	t.Run("rejects negative size", func(t *testing.T) {
		_, err := GenerateCustomID(idAlphabet, -1)
		if err == nil {
			t.Fatal("expected error for negative size")
		}
	})
}

func TestIDAlphabet(t *testing.T) {
	t.Run("excludes confusing characters", func(t *testing.T) {
		excluded := []string{"O", "I", "o", "i", "a", "e", "u"}
		for _, ch := range excluded {
			if strings.Contains(idAlphabet, ch) {
				t.Errorf("alphabet contains excluded character: %s", ch)
			}
		}
	})

	t.Run("contains expected character classes", func(t *testing.T) {
		if !strings.ContainsAny(idAlphabet, "bcdfghjklmnpqrstvwxy") {
			t.Error("missing lowercase letters")
		}
		if !strings.ContainsAny(idAlphabet, "ZBCDFGHJKLMNPQRSTVWXYZ") {
			t.Error("missing uppercase letters")
		}
		if !strings.ContainsAny(idAlphabet, "0123456789") {
			t.Error("missing digits")
		}
	})
}

func BenchmarkGenerateID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = GenerateID()
	}
}

func TestGenerateUUID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := GenerateUUID()
		if err != nil {
			t.Fatalf("GenerateUUID: %v", err)
		}
		if len(id) != 36 {
			t.Fatalf("GenerateUUID length = %d, want 36 (%q)", len(id), id)
		}
		if id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
			t.Fatalf("GenerateUUID %q missing canonical dashes", id)
		}
		if id[14] != '4' {
			t.Fatalf("GenerateUUID %q version nibble = %c, want 4", id, id[14])
		}
		switch id[19] {
		case '8', '9', 'a', 'b':
		default:
			t.Fatalf("GenerateUUID %q variant nibble = %c, want 8/9/a/b", id, id[19])
		}
		if seen[id] {
			t.Fatalf("GenerateUUID produced duplicate %q", id)
		}
		seen[id] = true
	}
}
