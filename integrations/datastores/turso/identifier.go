package turso

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

var (
	// dangerousIdentifierChars rejects the characters that let an identifier
	// break out of its position into arbitrary SQL: quotes, backslash,
	// parentheses, and the statement separator.
	dangerousIdentifierChars = regexp.MustCompile(`[;'"\\()]`)
	// identifierSegment is the allow-list for one dotted segment of an
	// identifier: an ASCII letter followed by letters, digits, or underscores.
	identifierSegment = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
)

// QuoteIdentifier validates a SQL identifier against a strict allow-list and
// returns it double-quoted, so a column or pk name can be interpolated into
// query text without opening an injection seam. SQLite/libSQL quotes
// identifiers with double quotes (the SQL-standard form); a bare name quotes to
// "name" and a dotted "schema.table" quotes each segment to "schema"."table".
// Any disallowed character (quote, backslash, parenthesis, semicolon) or a
// segment that is not letter-then-word-chars returns an error wrapping
// sdk.ErrInvalidInput. The list toolkit only quotes single column and pk
// identifiers, so a raw-expression PK (a keyset tiebreak built from an
// expression rather than a column) is rejected here — stores derive such keys
// into a wrapping subquery column instead.
func QuoteIdentifier(name string) (string, error) {
	if dangerousIdentifierChars.MatchString(name) {
		return "", fmt.Errorf("identifier %q contains disallowed characters: %w", name, sdk.ErrInvalidInput)
	}

	segments := strings.Split(name, ".")
	quoted := make([]string, len(segments))
	for i, segment := range segments {
		if !identifierSegment.MatchString(segment) {
			return "", fmt.Errorf("invalid identifier segment %q: %w", segment, sdk.ErrInvalidInput)
		}
		quoted[i] = `"` + segment + `"`
	}

	return strings.Join(quoted, "."), nil
}
