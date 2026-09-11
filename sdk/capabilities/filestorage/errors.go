package filestorage

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

var (
	// ErrObjectNotFound identifies missing objects and matches sdk.ErrNotFound.
	ErrObjectNotFound = fmt.Errorf("storage object: %w", sdk.ErrNotFound)

	// ErrInvalidPath identifies invalid keys/prefixes and matches sdk.ErrInvalidInput.
	ErrInvalidPath = fmt.Errorf("storage path: %w", sdk.ErrInvalidInput)
)
