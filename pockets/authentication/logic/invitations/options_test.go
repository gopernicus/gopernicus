package invitations

import (
	"errors"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestConstructorRejectsNilOption(t *testing.T) {
	if _, err := New(nil, nil, nil); !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "nil option") {
		t.Fatalf("nil option: %v", err)
	}
}
