package roles

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestConstructionRejectsNilDependencies(t *testing.T) {
	var typed *fakeRoleStore
	for _, s := range []*fakeRoleStore{nil, typed} {
		if _, e := NewService(s); !errors.Is(e, sdk.ErrInvalidInput) {
			t.Fatalf("service: %v", e)
		}
		if _, e := NewWriter(s); !errors.Is(e, sdk.ErrInvalidInput) {
			t.Fatalf("writer: %v", e)
		}
	}
	if _, e := NewService(&fakeRoleStore{}, WithSnapshots(typed)); !errors.Is(e, sdk.ErrInvalidInput) {
		t.Fatalf("snapshot: %v", e)
	}
	if _, e := NewService(&fakeRoleStore{}, nil); !errors.Is(e, sdk.ErrInvalidInput) {
		t.Fatalf("nil option: %v", e)
	}
}
