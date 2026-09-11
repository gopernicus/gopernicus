package cachertest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/capabilities/cacher"
)

// RunPrefix exercises the optional literal-prefix contract. Each factory result
// must implement PrefixDeleter; missing support fails rather than skipping.
func RunPrefix(t *testing.T, newStorer func(*testing.T) cacher.Storer) {
	t.Helper()
	for _, prefix := range []string{"user:1", "literal*", "literal?", "literal[ab]", "literal\\", ""} {
		t.Run("Literal_"+prefix, func(t *testing.T) {
			store := newStorer(t)
			deleter, ok := store.(cacher.PrefixDeleter)
			if !ok {
				t.Fatal("factory must implement cacher.PrefixDeleter")
			}
			ctx := context.Background()
			keys := []string{prefix + "one", prefix + "two", "other", "literalXone", "literalaone", "user:2"}
			for _, key := range keys {
				if err := store.Set(ctx, key, []byte(key), 0); err != nil {
					t.Fatal(err)
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := deleter.DeletePrefix(canceled, ""); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled prefix = %v", err)
			}
			for _, key := range keys {
				if _, found, err := store.Get(ctx, key); err != nil || !found {
					t.Fatalf("canceled prefix deleted %q: %v", key, err)
				}
			}
			if err := deleter.DeletePrefix(ctx, prefix); err != nil {
				t.Fatal(err)
			}
			for _, key := range keys {
				_, found, err := store.Get(ctx, key)
				if err != nil || found == strings.HasPrefix(key, prefix) {
					t.Errorf("Get(%q) found=%v, err=%v after deleting prefix %q", key, found, err, prefix)
				}
			}
		})
	}
}
