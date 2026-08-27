package authentication

import (
	"net/url"
	"testing"
)

// oauth-pending-link plan D3 — the pending-link callback lands the SPA on its
// destination carrying a legible outcome (auth=link_sent + provider) so it can
// render a "check your email" state instead of a dead end. The augmentation goes
// through url.Values, so existing query values and any fragment survive.

func TestPendingLinkRedirect(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		provider string
		// wantQuery is the exact expected query values (order-independent).
		wantQuery map[string]string
		wantPath  string
		wantFrag  string
	}{
		{
			name:      "empty target uses the same-origin default",
			target:    "",
			provider:  "google",
			wantPath:  "/",
			wantQuery: map[string]string{"auth": "link_sent", "provider": "google"},
		},
		{
			name:      "existing query is preserved",
			target:    "https://app.example.com/welcome?app=console",
			provider:  "github",
			wantPath:  "/welcome",
			wantQuery: map[string]string{"app": "console", "auth": "link_sent", "provider": "github"},
		},
		{
			name:      "existing fragment is preserved",
			target:    "https://app.example.com/welcome#section",
			provider:  "google",
			wantPath:  "/welcome",
			wantQuery: map[string]string{"auth": "link_sent", "provider": "google"},
			wantFrag:  "section",
		},
		{
			name:      "provider with URL-significant bytes is encoded",
			target:    "/return",
			provider:  "weird provider/x",
			wantPath:  "/return",
			wantQuery: map[string]string{"auth": "link_sent", "provider": "weird provider/x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pendingLinkRedirect(tt.target, tt.provider)
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("pendingLinkRedirect(%q, %q) = %q, unparseable: %v", tt.target, tt.provider, got, err)
			}
			if u.Path != tt.wantPath {
				t.Errorf("path = %q, want %q (from %q)", u.Path, tt.wantPath, got)
			}
			if u.Fragment != tt.wantFrag {
				t.Errorf("fragment = %q, want %q (from %q)", u.Fragment, tt.wantFrag, got)
			}
			q := u.Query()
			if len(q) != len(tt.wantQuery) {
				t.Errorf("query %v, want %v (from %q)", q, tt.wantQuery, got)
			}
			for k, want := range tt.wantQuery {
				if q.Get(k) != want {
					t.Errorf("query[%q] = %q, want %q (from %q)", k, q.Get(k), want, got)
				}
			}
		})
	}
}
