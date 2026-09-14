package decisions

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestCachePolicyResolution(t *testing.T) {
	base := CachePolicy{Namespace: "fixture", MaxStaleness: time.Second}
	resolved, err := base.resolve()
	if err != nil || resolved.PollInterval != 250*time.Millisecond || resolved.PollTimeout != 250*time.Millisecond || resolved.CacheTimeout != 25*time.Millisecond || resolved.EntryTTL != 5*time.Minute || resolved.MaxEntryBytes != 64<<10 || resolved.MaxFillBytes != 1<<20 || resolved.MaxFillEntries != 256 {
		t.Fatalf("resolved=%+v/%v", resolved, err)
	}
	for _, change := range []func(*CachePolicy){
		func(p *CachePolicy) { p.Namespace = " " }, func(p *CachePolicy) { p.MaxStaleness = 0 }, func(p *CachePolicy) { p.MaxStaleness = time.Nanosecond },
		func(p *CachePolicy) { p.PollInterval = -1 }, func(p *CachePolicy) { p.PollTimeout = -1 },
		func(p *CachePolicy) { p.PollTimeout = 750 * time.Millisecond },
		func(p *CachePolicy) {
			p.MaxStaleness = math.MaxInt64
			p.PollInterval = math.MaxInt64 - 1
			p.PollTimeout = math.MaxInt64 - 1
		},
		func(p *CachePolicy) { p.CacheTimeout = -1 }, func(p *CachePolicy) { p.EntryTTL = -1 }, func(p *CachePolicy) { p.MaxEntryBytes = -1 }, func(p *CachePolicy) { p.MaxFillBytes = 1 }, func(p *CachePolicy) { p.MaxFillEntries = -1 },
	} {
		p := base
		change(&p)
		if _, err := p.resolve(); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("accepted %+v: %v", p, err)
		}
	}
}

func TestCacheVersionValidation(t *testing.T) {
	for _, v := range []CacheVersion{{}, {Epoch: "0123456789abcdef0123456789abcdeF"}, {Epoch: "0123456789abcdef0123456789abcdef", Generation: -1}} {
		if !errors.Is(v.Validate(), ErrCacheVersion) {
			t.Fatalf("accepted %+v", v)
		}
	}
	if err := (CacheVersion{Epoch: "0123456789abcdef0123456789abcdef", Generation: math.MaxInt64}).Validate(); err != nil {
		t.Fatal(err)
	}
}
