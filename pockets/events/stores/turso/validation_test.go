package turso

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

func TestNilDependenciesAndInvalidBatch(t *testing.T) {
	valid := sdkevents.Record{EventID: "valid", Type: "test.valid"}
	if _, err := New(context.Background(), nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("New(nil) = %v", err)
	}
	if err := (&Store{}).AppendTx(context.Background(), nil, valid); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("AppendTx(nil) = %v", err)
	}
	if err := insertRecords(context.Background(), nil, valid, sdkevents.Record{Type: "test.invalid"}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("invalid batch = %v", err)
	}
}
