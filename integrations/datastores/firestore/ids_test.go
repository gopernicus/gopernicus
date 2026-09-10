package firestore_test

import (
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// hexDigits is the only alphabet a KeyHash may use.
const hexDigits = "0123456789abcdef"

// idAlphabet is the vendor's auto-id alphabet, reproduced by NewID.
const idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// TestNewID pins the shape (20 characters of [A-Za-z0-9]) and that ids do not
// repeat across a large sample.
func TestNewID(t *testing.T) {
	const sample = 10000
	seen := make(map[string]struct{}, sample)

	for range sample {
		id := firestore.NewID()
		if len(id) != 20 {
			t.Fatalf("NewID() = %q, want 20 characters", id)
		}
		for _, r := range id {
			if !strings.ContainsRune(idAlphabet, r) {
				t.Fatalf("NewID() = %q contains %q, outside the vendor's [A-Za-z0-9] alphabet", id, r)
			}
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID() repeated %q within %d draws", id, sample)
		}
		seen[id] = struct{}{}
	}
}

// TestKeyHashShape proves the output is always a legal Firestore document id:
// 64 lowercase hex characters — no slash, not "." or "..", not __x__, far
// inside the 1500-byte limit.
func TestKeyHashShape(t *testing.T) {
	cases := [][]string{
		nil,
		{""},
		{"users", "alice@example.com"},
		{"doc", "with/slash", "and\x00null"},
		{strings.Repeat("x", 100000)},
		{"日本語", "🙂"},
	}
	for _, parts := range cases {
		got := firestore.KeyHash(parts...)
		if len(got) != 64 {
			t.Errorf("KeyHash(%q) = %q, want 64 characters", parts, got)
		}
		if strings.Trim(got, hexDigits) != "" {
			t.Errorf("KeyHash(%q) = %q, want lowercase hex only", parts, got)
		}
		if strings.Contains(got, "/") || got == "." || got == ".." ||
			(strings.HasPrefix(got, "__") && strings.HasSuffix(got, "__")) {
			t.Errorf("KeyHash(%q) = %q, not a legal Firestore document id", parts, got)
		}
	}
}

// TestKeyHashIsUnambiguous is the reason for the length prefix: no two distinct
// tuples may share an encoding, however the bytes are shuffled between
// components — including empty components and separator-looking content.
func TestKeyHashIsUnambiguous(t *testing.T) {
	tuples := [][]string{
		nil,
		{""},
		{"", ""},
		{"ab", "c"},
		{"a", "bc"},
		{"abc"},
		{"a/b"},
		{"a", "b"},
		{"a\x00b"},
		{"users", "a", "b"},
		{"users", "a/b"},
		{"日本", "語"},
		{"日", "本語"},
	}

	seen := make(map[string][]string, len(tuples))
	for _, parts := range tuples {
		got := firestore.KeyHash(parts...)
		if other, dup := seen[got]; dup {
			t.Errorf("KeyHash collision: %q and %q both hash to %s", other, parts, got)
			continue
		}
		seen[got] = parts
	}
}

// TestKeyHashIsDeterministic pins the encoding itself: the same tuple must hash
// identically across calls AND across releases, because the value is a stored
// document id. The golden values below are the encoding's contract — if a
// change makes them fail, every existing document's id changed, which is a
// migration, not a refactor.
func TestKeyHashIsDeterministic(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{nil, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{[]string{""}, "af5570f5a1810b7af78caf4bc70a660f0df51e42baf91d4de5b2328de0e83dfc"},
		{[]string{"users", "alice@example.com"}, "032a890cc22751602864745d330b38862e3c511ed9ffe6c2fe21f587d0645e25"},
	}
	for _, tc := range cases {
		got := firestore.KeyHash(tc.parts...)
		if got != tc.want {
			t.Errorf("KeyHash(%q) = %s, want %s — the stored-id encoding changed", tc.parts, got, tc.want)
		}
		if again := firestore.KeyHash(tc.parts...); again != got {
			t.Errorf("KeyHash(%q) is not deterministic: %s then %s", tc.parts, got, again)
		}
	}
}

// TestKeyHashLongInput proves a key well past Firestore's 1500-byte document-id
// limit still produces a legal id — the whole point of compatibility note N1.
func TestKeyHashLongInput(t *testing.T) {
	// The authorization relationship tuple: six components, each at the
	// port's 256-byte maximum, is 1536 bytes before separators.
	parts := make([]string, 6)
	for i := range parts {
		parts[i] = strings.Repeat("k", 256)
	}
	if got := firestore.KeyHash(parts...); len(got) != 64 {
		t.Fatalf("KeyHash over a 1536-byte tuple = %q (%d characters), want 64", got, len(got))
	}

	long := strings.Repeat("z", 1<<20)
	if firestore.KeyHash(long) == firestore.KeyHash(long+"z") {
		t.Error("KeyHash collided on two 1 MiB inputs differing by one byte")
	}
}
