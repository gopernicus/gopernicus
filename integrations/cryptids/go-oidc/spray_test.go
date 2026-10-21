package gooidc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

const sprayKids = 1000

// TestKidSpray pins the amplification bound: 1,000 distinct unknown kids cost
// at most one JWKS fetch per floor window, every one is rejected as an
// unknown key (never an outage), and the rejections log once per window.
func TestKidSpray(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()

	tokens := make([]string, sprayKids)
	for i := range tokens {
		token, err := f.issuer.MintWithUnpublishedKey(fmt.Sprintf("spray-%04d", i), map[string]any{"sub": "s"})
		if err != nil {
			t.Fatal(err)
		}
		tokens[i] = token
	}

	spray := func() {
		var wg sync.WaitGroup
		errs := make(chan error, len(tokens))
		for _, token := range tokens {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := f.verifier.Verify(ctx, token)
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if !errors.Is(err, ErrUnknownKey) {
				t.Fatalf("spray error = %v, want ErrUnknownKey", err)
			}
		}
	}

	spray()
	if n := f.issuer.JWKSRequests(); n != 1 {
		t.Fatalf("JWKS requests after a cold spray = %d, want 1", n)
	}
	if got := f.logs.count("gooidc: rejected tokens signed with an unknown key id"); got != 1 {
		t.Fatalf("unknown-kid log lines = %d, want 1 per window", got)
	}

	// The same window: no fetch, no further log line.
	spray()
	if n := f.issuer.JWKSRequests(); n != 1 {
		t.Fatalf("JWKS requests inside the floor = %d, want 1", n)
	}

	// The next window admits one fetch, and the first rejection reports what
	// the previous window suppressed. Kids confirmed absent by a fetch this
	// process started are cached negatively and never refetch.
	f.clock.Advance(RefetchFloor + unknownKidLogWindow)
	spray()
	if n := f.issuer.JWKSRequests(); n > 2 {
		t.Fatalf("JWKS requests after one more window = %d, want at most 2", n)
	}
	if got := f.logs.count("gooidc: rejected tokens signed with an unknown key id"); got != 2 {
		t.Fatalf("unknown-kid log lines = %d, want 2 (one per window)", got)
	}
	suppressed, ok := f.logs.attr("gooidc: rejected tokens signed with an unknown key id", "suppressed_in_previous_window")
	if !ok || suppressed.Int64() != 2*sprayKids-1 {
		t.Fatalf("suppressed_in_previous_window = %v, want %d", suppressed, 2*sprayKids-1)
	}

	// A valid token still verifies after the spray.
	if _, err := f.verifier.Verify(ctx, f.mint(t, nil)); err != nil {
		t.Fatalf("Verify after spray: %v", err)
	}
}

// TestRepeatedBogusKidIsCachedNegatively pins the negative cache: a kid a
// fetch confirmed absent does not spend another fetch in later floor windows.
func TestRepeatedBogusKidIsCachedNegatively(t *testing.T) {
	f := newFixture(t, nil)
	ctx := context.Background()
	token, err := f.issuer.MintWithUnpublishedKey("bogus", map[string]any{"sub": "s"})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := f.verifier.Verify(ctx, token); !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("Verify = %v, want ErrUnknownKey", err)
		}
		f.clock.Advance(RefetchFloor)
	}
	if n := f.issuer.JWKSRequests(); n != 1 {
		t.Fatalf("JWKS requests = %d, want 1 inside the negative TTL", n)
	}
	f.clock.Advance(negativeKidTTL)
	if _, err := f.verifier.Verify(ctx, token); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("Verify = %v, want ErrUnknownKey", err)
	}
	if n := f.issuer.JWKSRequests(); n != 2 {
		t.Fatalf("JWKS requests = %d, want 2 once the negative entry expired", n)
	}
}
