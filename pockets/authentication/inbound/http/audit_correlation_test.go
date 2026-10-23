package authenticationhttp

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// TestAuditRowRequestIDFromTheEdge drives the request-id middleware into an
// audited route: an ordinary X-Request-ID lands on the row, and a 200-byte one
// is blanked on the row while the request itself still succeeds.
func TestAuditRowRequestIDFromTheEdge(t *testing.T) {
	for _, tc := range []struct{ name, header, want string }{
		{"ordinary id recorded", "req-0f3c", "req-0f3c"},
		{"200-byte id blanked", strings.Repeat("x", 200), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCredentialHarness(t)
			raw, _, _ := h.mintKey(t, "bot", false, "", time.Time{})
			h.events.mu.Lock()
			h.events.events = nil
			h.events.mu.Unlock()

			r := h.credentialRequest(raw, "")
			r.Header.Set(web.RequestIDHeader, tc.header)
			guard := h.svc.RequireAPIKey()
			rec, _ := serveGuarded(h, func(next http.Handler) http.Handler { return web.RequestID()(guard(next)) }, r)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
			}
			e := requireEvent(t, h.events, securityevent.TypeAPIKeyAuth, securityevent.StatusSuccess)
			if e.RequestID != tc.want {
				t.Fatalf("row request_id = %q, want %q", e.RequestID, tc.want)
			}
		})
	}
}
