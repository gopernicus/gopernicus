package memory

import (
	"fmt"
	"strings"
	"testing"
)

func TestNilOptionPanicsWithDiagnostic(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "nil option") {
			t.Fatalf("panic = %v; want nil option diagnostic", recovered)
		}
	}()
	NewQueue(nil)
}
