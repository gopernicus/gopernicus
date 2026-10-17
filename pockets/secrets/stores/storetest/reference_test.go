package storetest

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/secrets/logic/vault"
	"github.com/gopernicus/gopernicus/pockets/secrets/stores/memory"
)

// TestMemoryReference runs the suite against the memory store, so the suite
// itself is checked against the reference on every `go test ./...`.
func TestMemoryReference(t *testing.T) {
	RunRepository(t, func(*testing.T) vault.Repository { return memory.New() })
}
