package turso

import (
	"context"
	"errors"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestNilOptionRejectedBeforeProbe(t *testing.T) {
	if _, err := Repositories(context.Background(), &tursodb.DB{}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Repositories nil option: %v", err)
	}
	if _, err := RelationshipRepository(context.Background(), &tursodb.DB{}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("RelationshipRepository nil option: %v", err)
	}
}
