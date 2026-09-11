package pgx

import (
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestRepositoriesRejectNilDatabase(t *testing.T) {
	if _, err := Repositories(t.Context(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Repositories nil database: %v", err)
	}
	if _, err := RelationshipRepository(t.Context(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("RelationshipRepository nil database: %v", err)
	}
}
