package protection

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestBlocklistCheckerMatchesNormalizedEntries(t *testing.T) {
	c, err := NewBlocklistChecker(strings.NewReader("Password123456789\n\n  correcthorsebattery  \r\nletmein-letmein-1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (blank lines skipped)", c.Len())
	}
	for _, candidate := range []string{"password123456789", "PASSWORD123456789", " correcthorsebattery", "LetMeIn-LetMeIn-1"} {
		if found, err := c.IsCompromised(context.Background(), candidate); err != nil || !found {
			t.Errorf("IsCompromised(%q) = %v, %v; want true", candidate, found, err)
		}
	}
	for _, candidate := range []string{"password12345678", "correct horse battery", ""} {
		if found, err := c.IsCompromised(context.Background(), candidate); err != nil || found {
			t.Errorf("IsCompromised(%q) = %v, %v; want false", candidate, found, err)
		}
	}
}

func TestBlocklistCheckerRefusesEmptyAndUnreadableLists(t *testing.T) {
	for _, list := range []string{"", "\n\n   \n"} {
		if _, err := NewBlocklistChecker(strings.NewReader(list)); !errors.Is(err, ErrBlocklistEmpty) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Errorf("list %q: err = %v, want ErrBlocklistEmpty", list, err)
		}
	}
	if _, err := NewBlocklistChecker(nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("nil reader: err = %v", err)
	}
	broken := io.MultiReader(strings.NewReader("hunter2-secret-entry\n"), iotest.ErrReader(errors.New("disk gone")))
	_, err := NewBlocklistChecker(broken)
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("read failure = %v; want an error that carries no entry", err)
	}
	oversized := strings.NewReader(strings.Repeat("a", maxBlocklistLineBytes+1))
	if _, err := NewBlocklistChecker(oversized); err == nil {
		t.Fatal("an oversized line was accepted")
	}
}

func TestBlocklistCheckerZeroValueBlocksNothing(t *testing.T) {
	var nilChecker *BlocklistChecker
	if found, err := nilChecker.IsCompromised(context.Background(), "x"); found || err != nil {
		t.Fatalf("nil checker = %v, %v", found, err)
	}
	if found, _ := (&BlocklistChecker{}).IsCompromised(context.Background(), "x"); found {
		t.Fatal("zero checker reported a match")
	}
}

func TestChallengeProtectorReportsKeyIDs(t *testing.T) {
	p, err := NewHMACChallengeProtector(HMACKeyRing{Active: "p2", Keys: map[string][]byte{"p2": testPepper(2), "dev": testPepper(1)}})
	if err != nil {
		t.Fatal(err)
	}
	ids := p.KeyIDs()
	if strings.Join(ids, ",") != "dev,p2" || p.LegacyKeyID() != "" {
		t.Fatalf("KeyIDs = %v, LegacyKeyID = %q", ids, p.LegacyKeyID())
	}
	ids[0] = "mutated"
	if p.KeyIDs()[0] != "dev" {
		t.Fatal("KeyIDs returned the protector's own slice")
	}
}

func TestBlocklistCheckerStripsLeadingBOM(t *testing.T) {
	c, err := NewBlocklistChecker(strings.NewReader("\ufeffhunter2hunter2hunter2\nsecond-entry-second\n"))
	if err != nil {
		t.Fatal(err)
	}
	if found, _ := c.IsCompromised(context.Background(), "hunter2hunter2hunter2"); !found {
		t.Fatal("the first entry was not matched behind a BOM")
	}
}
