package media

import (
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

// TestNewAsset_StorageKeyIgnoresEntityIDs pins the storage key's random
// component to the package-private NanoID generator: it is a blob path, not a
// record id, so neither the host's generator nor the record-class UUIDv7
// default (nor sdk.DatabaseID's empty id) reaches it.
func TestNewAsset_StorageKeyIgnoresEntityIDs(t *testing.T) {
	if storageKeys.Func != nil {
		t.Fatal("storageKeys must stay the zero IDGenerator (the NanoID default)")
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for name, ids := range map[string]sdk.IDGenerator{
		"fixed":    sdk.NewIDGenerator(func() (string, error) { return "fixed-1", nil }),
		"uuidv7":   sdk.NewIDGenerator(sdk.UUIDv7()),
		"database": sdk.NewIDGenerator(sdk.DatabaseID),
	} {
		t.Run(name, func(t *testing.T) {
			a, err := NewAsset(ids, "My Photo.JPG", "image/jpeg", 10, now)
			if err != nil {
				t.Fatalf("NewAsset: %v", err)
			}
			parts := strings.Split(a.StorageKey, "/")
			if len(parts) != 3 || parts[0] != "assets" || parts[2] != "my-photo.jpg" {
				t.Fatalf("StorageKey = %q, want assets/<key>/my-photo.jpg", a.StorageKey)
			}
			key := parts[1]
			if len(key) != sdk.DefaultIDLength {
				t.Errorf("storage key component %q has length %d, want %d (NanoID)", key, len(key), sdk.DefaultIDLength)
			}
			for _, c := range key {
				if !strings.ContainsRune(sdk.DefaultIDAlphabet, c) {
					t.Errorf("storage key component %q has %q outside the NanoID alphabet", key, c)
					break
				}
			}
			if a.ID != "" && strings.Contains(a.StorageKey, a.ID) {
				t.Errorf("StorageKey %q carries the entity id %q", a.StorageKey, a.ID)
			}
		})
	}
}
