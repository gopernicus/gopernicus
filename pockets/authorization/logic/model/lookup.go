package model

import (
	"fmt"

	"github.com/gopernicus/gopernicus/sdk"
)

// ErrEnumerationContended means discovered grants repeatedly changed before
// verification. No partial results are returned. Hosts may retry the whole call
// and use errors.Is to attach Retry-After to the default HTTP 503 response.
var ErrEnumerationContended = fmt.Errorf("authorization: enumeration contended: %w", sdk.ErrUnavailable)
