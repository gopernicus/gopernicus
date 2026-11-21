package oauth2

import (
	"sync"
	"time"
)

// exchangeAudit coalesces oauth2_token_exchanged rows per delegated session
// under Config.ExchangeAuditWindow. It is process-local: each replica keeps its
// own marks, so N replicas write at most N rows per connection per window.
type exchangeAudit struct {
	window time.Duration

	mu        sync.Mutex
	marks     map[string]exchangeMark
	lastSweep time.Time
}

// exchangeMark is when a session's last row was written, its latest exchange,
// and how many exchanges were coalesced since the row.
type exchangeMark struct {
	recorded, seen time.Time
	suppressed     int
}

func newExchangeAudit(window time.Duration) *exchangeAudit {
	if window <= 0 {
		return nil
	}
	return &exchangeAudit{window: window, marks: map[string]exchangeMark{}}
}

// admit reports whether this exchange of sessionID writes a row and, when it
// does, how many exchanges were coalesced since the previous row. The first
// exchange this process sees writes; later ones inside the window are counted;
// the first after the window writes with the count. Marks of sessions with no
// exchange for a whole window are swept at most once per window, so a
// connection that went quiet costs nothing and its pending count is dropped.
func (a *exchangeAudit) admit(sessionID string, now time.Time) (bool, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if now.Sub(a.lastSweep) >= a.window {
		for id, mark := range a.marks {
			if now.Sub(mark.seen) >= a.window {
				delete(a.marks, id)
			}
		}
		a.lastSweep = now
	}
	mark, ok := a.marks[sessionID]
	if ok && now.Sub(mark.recorded) < a.window {
		mark.suppressed++
		mark.seen = now
		a.marks[sessionID] = mark
		return false, 0
	}
	a.marks[sessionID] = exchangeMark{recorded: now, seen: now}
	return true, mark.suppressed
}
