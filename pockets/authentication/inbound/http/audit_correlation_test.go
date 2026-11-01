package authenticationhttp

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// TestAuditRowRequestIDFromTheEdge drives the request-id middleware into an
// audited route: an ordinary X-Request-ID lands on the row, and a 200-byte one
// is replaced at the edge — the row carries the minted id, never the inbound
// value. A revoked key drives it: its blocked apikey_auth row is the row a
// key request writes (a successful one writes none, #314). The recorder's own blanking
// past MaxCorrelationIDLen is pinned in securityevent.
func TestAuditRowRequestIDFromTheEdge(t *testing.T) {
	for _, tc := range []struct{ name, header, want string }{
		{"ordinary id recorded", "req-0f3c", "req-0f3c"},
		{"200-byte id replaced at the edge", strings.Repeat("x", 200), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCredentialHarness(t)
			raw, keyID, _ := h.mintKey(t, "bot", false, "", time.Time{})
			if err := h.svc.RevokeAPIKey(context.Background(), keyID); err != nil {
				t.Fatalf("RevokeAPIKey: %v", err)
			}
			h.events.mu.Lock()
			h.events.events = nil
			h.events.mu.Unlock()

			r := h.credentialRequest(raw, "")
			r.Header.Set(web.RequestIDHeader, tc.header)
			guard := h.svc.RequireAPIKey()
			rec, _ := serveGuarded(h, func(next http.Handler) http.Handler { return web.RequestID()(guard(next)) }, r)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body)
			}
			e := requireEvent(t, h.events, securityevent.TypeAPIKeyAuth, securityevent.StatusBlocked)
			want := tc.want
			if want == "" {
				want = rec.Header().Get(web.RequestIDHeader)
				if want == "" || want == tc.header {
					t.Fatalf("echoed request id = %q, want a minted one", want)
				}
			}
			if e.RequestID != want {
				t.Fatalf("row request_id = %q, want %q", e.RequestID, want)
			}
		})
	}
}
