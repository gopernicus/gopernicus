package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrustProxies_ResolvesClientIP(t *testing.T) {
	tests := []struct {
		name           string
		trustedProxies int
		remoteAddr     string
		xForwardedFor  string
		xRealIP        string
		want           string
	}{
		{
			name:           "count 0 trusts RemoteAddr and ignores spoofed XFF",
			trustedProxies: 0,
			remoteAddr:     "127.0.0.1:54321",
			xForwardedFor:  "6.6.6.6",
			want:           "127.0.0.1",
		},
		{
			name:           "count 0 no headers strips RemoteAddr port",
			trustedProxies: 0,
			remoteAddr:     "203.0.113.9:40000",
			want:           "203.0.113.9",
		},
		{
			name:           "count 1 over multi-hop XFF picks index len-1",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "9.9.9.9, 2.2.2.2, 3.3.3.3",
			want:           "3.3.3.3",
		},
		{
			name:           "count 2 over multi-hop XFF picks index len-2",
			trustedProxies: 2,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "9.9.9.9, 2.2.2.2, 3.3.3.3",
			want:           "2.2.2.2",
		},
		{
			name:           "count exceeding hops clamps to leftmost",
			trustedProxies: 5,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "9.9.9.9, 2.2.2.2",
			want:           "9.9.9.9",
		},
		{
			name:           "X-Real-IP fallback when XFF absent and count > 0",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xRealIP:        "8.8.8.8",
			want:           "8.8.8.8",
		},
		{
			name:           "count > 0 no headers falls back to RemoteAddr",
			trustedProxies: 1,
			remoteAddr:     "198.51.100.7:2222",
			want:           "198.51.100.7",
		},
		{
			name:           "malformed RemoteAddr without port returns raw value",
			trustedProxies: 0,
			remoteAddr:     "203.0.113.42",
			want:           "203.0.113.42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			var ok bool
			h := TrustProxies(tt.trustedProxies)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, ok = ClientIP(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tt.xForwardedFor)
			}
			if tt.xRealIP != "" {
				req.Header.Set("X-Real-IP", tt.xRealIP)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)

			if !ok {
				t.Fatal("ClientIP not present on context after TrustProxies ran")
			}
			if got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClientIP_AbsentFromContext(t *testing.T) {
	if ip, ok := ClientIP(context.Background()); ok || ip != "" {
		t.Errorf("ClientIP(bare ctx) = (%q, %v), want (\"\", false)", ip, ok)
	}
}
