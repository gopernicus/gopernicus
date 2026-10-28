package protection

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/gopernicus/gopernicus/sdk"
)

// maxBlocklistLineBytes bounds one blocklist entry so a malformed file cannot
// make the scanner buffer grow without limit.
const maxBlocklistLineBytes = 64 << 10

// utf8BOM is the byte order mark some editors write at the start of a file.
const utf8BOM = "\ufeff"

// ErrBlocklistEmpty is returned by NewBlocklistChecker when the reader holds no
// entry: an empty list checks nothing, and wiring it would silence the
// production password_breach_check_none WARN while blocking nothing.
var ErrBlocklistEmpty = fmt.Errorf("protection: password blocklist has no entries: %w", sdk.ErrInvalidInput)

// BlocklistChecker reports a password as compromised when it appears in a
// host-supplied list. It satisfies the authentication service's
// CompromisedPasswordChecker structurally. Entries and candidates are compared
// after strings.TrimSpace and strings.ToLower; Unicode normalization (NFKC) is
// not applied, because it is not in the standard library. The pocket ships no
// list: a host reads its own file (for example a top-N extract of a breach
// corpus or a common-password list). The whole list is held in memory, roughly
// the entries' bytes plus map overhead (tens of MB per million entries), so
// prefer a top-N extract over a full corpus; a full corpus belongs behind a
// remote checker. Construct with NewBlocklistChecker; the zero value blocks
// nothing.
type BlocklistChecker struct {
	entries map[string]struct{}
}

// NewBlocklistChecker reads one entry per line from r. A leading UTF-8 byte
// order mark is stripped and blank lines are skipped. A read failure is returned; a reader with no entries is
// ErrBlocklistEmpty. Errors never carry an entry.
func NewBlocklistChecker(r io.Reader) (*BlocklistChecker, error) {
	if r == nil {
		return nil, fmt.Errorf("protection: password blocklist reader is nil: %w", sdk.ErrInvalidInput)
	}
	entries := map[string]struct{}{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), maxBlocklistLineBytes)
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line, first = strings.TrimPrefix(line, utf8BOM), false
		}
		entry := normalizeBlocklistEntry(line)
		if entry == "" {
			continue
		}
		entries[entry] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("protection: read password blocklist after %d entries: %w", len(entries), err)
	}
	if len(entries) == 0 {
		return nil, ErrBlocklistEmpty
	}
	return &BlocklistChecker{entries: entries}, nil
}

// IsCompromised reports whether password, normalized, is on the list. It never
// returns an error.
func (c *BlocklistChecker) IsCompromised(_ context.Context, password string) (bool, error) {
	if c == nil {
		return false, nil
	}
	_, found := c.entries[normalizeBlocklistEntry(password)]
	return found, nil
}

// Len reports how many distinct entries the list holds.
func (c *BlocklistChecker) Len() int {
	if c == nil {
		return 0
	}
	return len(c.entries)
}

func normalizeBlocklistEntry(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
