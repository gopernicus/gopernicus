package bcrypt

import (
	"sync"
	"testing"

	vendor "golang.org/x/crypto/bcrypt"
)

func TestWithCostConcurrentReuse(t *testing.T) {
	opt := WithCost(-1)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 100 {
				if h := New(opt); h.cost != vendor.DefaultCost {
					t.Errorf("cost = %d", h.cost)
				}
			}
		})
	}
	wg.Wait()
	h := New(WithCost(vendor.MinCost), opt)
	hash, err := h.HashPassword("option reuse")
	if err != nil {
		t.Fatal(err)
	}
	cost, err := vendor.Cost([]byte(hash))
	if err != nil || cost != vendor.DefaultCost {
		t.Fatalf("hash cost = %d, %v", cost, err)
	}
}

func TestNewRejectsNilOption(t *testing.T) {
	defer func() {
		if got := recover(); got != "bcrypt: nil Option" {
			t.Fatalf("panic = %v", got)
		}
	}()
	New(nil)
}
