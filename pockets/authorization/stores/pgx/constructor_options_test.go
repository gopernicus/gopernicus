package pgx

import (
	"context"
	"errors"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestNilOptionRejectedBeforeProbe(t *testing.T) {
	if _, err := Repositories(context.Background(), &pgxdb.DB{}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Repositories nil option: %v", err)
	}
	if _, err := RelationshipRepository(context.Background(), &pgxdb.DB{}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("RelationshipRepository nil option: %v", err)
	}
}
