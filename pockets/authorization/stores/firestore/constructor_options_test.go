package firestore

import (
	"errors"
	"testing"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"

	"github.com/gopernicus/gopernicus/sdk"
)

func TestNilOptionRejectedBeforeProbe(t *testing.T) {
	if _, err := Repositories(t.Context(), &firestoredb.DB{}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Repositories nil option: %v", err)
	}
	if _, err := RelationshipRepository(t.Context(), &firestoredb.DB{}, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("RelationshipRepository nil option: %v", err)
	}
}
