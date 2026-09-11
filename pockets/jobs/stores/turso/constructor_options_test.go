package turso

import (
	"fmt"
	"strings"
	"testing"

	tursodb "github.com/gopernicus/gopernicus/integrations/datastores/turso"
)

func TestNilOptionPanicsWithDiagnostic(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "nil option") {
			t.Fatalf("panic = %v; want nil option diagnostic", recovered)
		}
	}()
	NewQueueStore(&tursodb.DB{}, nil)
}
