package filestorage

import (
	"fmt"
	"io/fs"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gopernicus/gopernicus/sdk"
)

// ValidatePath checks a nonempty relative UTF-8 object key. Slash separates
// segments; empty, dot and dot-dot segments, backslashes and NUL are invalid.
// Names such as .segovia, two..dots and names containing spaces remain valid.
// It never cleans, slugs, case-folds or normalizes a key.
func ValidatePath(path string) error {
	if path == "." || !fs.ValidPath(path) || strings.ContainsAny(path, "\\\x00") {
		return ErrInvalidPath
	}
	return nil
}

// ValidatePrefix checks a literal key prefix. Empty and a trailing slash are
// valid, as are partial final components (including '.' matching '.segovia').
// Complete directory components follow the same rules as ValidatePath.
func ValidatePrefix(prefix string) error {
	if !utf8.ValidString(prefix) || strings.ContainsAny(prefix, "\\\x00") || strings.HasPrefix(prefix, "/") {
		return ErrInvalidPath
	}
	if i := strings.LastIndexByte(prefix, '/'); i >= 0 {
		return ValidatePath(prefix[:i])
	}
	return nil
}

// ValidateRange checks nonnegative offsets, length -1 (to EOF) or nonnegative
// lengths, and representability of the inclusive end offset for finite ranges.
func ValidateRange(offset, length int64) error {
	if offset < 0 || length < -1 || (length > 0 && offset > math.MaxInt64-(length-1)) {
		return fmt.Errorf("storage byte range: %w", sdk.ErrInvalidInput)
	}
	return nil
}

// ValidateExpiry checks the signed-read policy shared by storage adapters:
// whole seconds from one second through seven days, inclusive.
func ValidateExpiry(expiry time.Duration) error {
	if expiry < time.Second || expiry > 7*24*time.Hour || expiry%time.Second != 0 {
		return fmt.Errorf("storage signed URL expiry must be whole seconds from 1 second through 7 days: %w", sdk.ErrInvalidInput)
	}
	return nil
}
