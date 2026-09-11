package pgx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	pgxdb "github.com/gopernicus/gopernicus/integrations/datastores/pgxdb"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestNilOptionRejectedBeforeProbe(t *testing.T) {
	if _, err := Repositories(context.Background(), &pgxdb.DB{}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Repositories nil option: %v", err)
	}
}

func TestNilOptionPanicsWithDiagnostic(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "nil option") {
			t.Fatalf("panic = %v; want nil option diagnostic", recovered)
		}
	}()
	NewUserStore(&pgxdb.DB{}, nil)
}
