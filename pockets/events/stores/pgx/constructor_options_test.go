package pgx

import (
	"context"
	"errors"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestNilOptionRejectedBeforeProbe(t *testing.T) {
	if _, err := New(context.Background(), &pgxdb.DB{}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("New nil option: %v", err)
	}
}
