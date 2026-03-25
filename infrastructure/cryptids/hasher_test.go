package cryptids

import "testing"

func TestSHA256Hasher(t *testing.T) {
	hasher := NewSHA256Hasher()

	t.Run("returns 64-char hex", func(t *testing.T) {
		hash, err := hasher.Hash("test-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hash) != 64 {
			t.Errorf("len = %d, want 64", len(hash))
		}
		for _, ch := range hash {
			if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
				t.Errorf("non-hex character: %c", ch)
			}
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		h1, _ := hasher.Hash("same-key")
		h2, _ := hasher.Hash("same-key")
		if h1 != h2 {
			t.Error("different hashes for same input")
		}
	})

	t.Run("different inputs differ", func(t *testing.T) {
		h1, _ := hasher.Hash("key1")
		h2, _ := hasher.Hash("key2")
		if h1 == h2 {
			t.Error("same hash for different inputs")
		}
	})

	t.Run("rejects empty key", func(t *testing.T) {
		_, err := hasher.Hash("")
		if err == nil {
			t.Fatal("expected error for empty key")
		}
	})

	t.Run("case sensitive", func(t *testing.T) {
		h1, _ := hasher.Hash("password")
		h2, _ := hasher.Hash("Password")
		if h1 == h2 {
			t.Error("hashes should differ for different case")
		}
	})

	t.Run("known vectors", func(t *testing.T) {
		tests := []struct {
			input, want string
		}{
			{"hello", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
			{"world", "486ea46224d1bb4fb680f34f7c9ad96a8f24ec88be73ea8e5a6c65260e9cb8a7"},
		}
		for _, tt := range tests {
			hash, err := hasher.Hash(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hash != tt.want {
				t.Errorf("Hash(%q) = %s, want %s", tt.input, hash, tt.want)
			}
		}
	})
}

func BenchmarkSHA256Hash(b *testing.B) {
	hasher := NewSHA256Hasher()
	for i := 0; i < b.N; i++ {
		_, _ = hasher.Hash("benchmark-key")
	}
}
