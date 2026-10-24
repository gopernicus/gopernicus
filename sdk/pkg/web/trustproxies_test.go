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
		trustRealIP    bool
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
			name:           "X-Real-IP ignored by default when XFF absent",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xRealIP:        "8.8.8.8",
			want:           "10.0.0.1",
		},
		{
			name:           "X-Real-IP trusted when opted in and XFF absent",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xRealIP:        "8.8.8.8",
			trustRealIP:    true,
			want:           "8.8.8.8",
		},
		{
			name:           "X-Real-IP opted in loses to XFF",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "7.7.7.7",
			xRealIP:        "8.8.8.8",
			trustRealIP:    true,
			want:           "7.7.7.7",
		},
		{
			name:           "X-Real-IP opted in ignored at count 0",
			trustedProxies: 0,
			remoteAddr:     "10.0.0.1:1234",
			xRealIP:        "8.8.8.8",
			trustRealIP:    true,
			want:           "10.0.0.1",
		},
		{
			name:           "X-Real-IP opted in but unparsable attributes the peer",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xRealIP:        "not-an-ip",
			trustRealIP:    true,
			want:           "10.0.0.1",
		},
		{
			name:           "X-Real-IP opted in is canonicalized",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xRealIP:        " [2001:DB8::7]:443 ",
			trustRealIP:    true,
			want:           "2001:db8::7",
		},
		{
			name:           "IPv6 hop is canonicalized",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "9.9.9.9, 2001:DB8:0:0::1",
			want:           "2001:db8::1",
		},
		{
			name:           "ported IPv4 hop drops the port",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "203.0.113.5:8080",
			want:           "203.0.113.5",
		},
		{
			name:           "bracketed ported IPv6 hop drops brackets and port",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "[2001:db8::2]:443",
			want:           "2001:db8::2",
		},
		{
			name:           "IPv6 zone is dropped",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "fe80::1%eth0",
			want:           "fe80::1",
		},
		{
			name:           "IPv4-mapped IPv6 hop is unmapped",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "::ffff:192.0.2.1",
			want:           "192.0.2.1",
		},
		{
			name:           "garbage hop attributes the peer",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "not-an-ip",
			want:           "10.0.0.1",
		},
		{
			name:           "garbage chosen hop beside a valid one attributes the peer",
			trustedProxies: 2,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "<script>, 3.3.3.3",
			want:           "10.0.0.1",
		},
		{
			name:           "empty chosen hop attributes the peer",
			trustedProxies: 1,
			remoteAddr:     "10.0.0.1:1234",
			xForwardedFor:  "9.9.9.9, ",
			want:           "10.0.0.1",
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
			mw := TrustProxies(tt.trustedProxies)
			if tt.trustRealIP {
				mw = TrustProxiesWithConfig(TrustProxiesConfig{TrustedProxyCount: tt.trustedProxies, TrustRealIPHeader: true})
			}
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestTrustProxies_EqualsCountOnlyConfig(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		for _, header := range []http.Header{
			{},
			{"X-Forwarded-For": {"9.9.9.9, 2.2.2.2"}},
			{"X-Real-IP": {"8.8.8.8"}},
			{"X-Forwarded-For": {"bogus"}, "X-Real-IP": {"8.8.8.8"}},
		} {
			resolve := func(mw Middleware) string {
				var got string
				h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got, _ = ClientIP(r.Context()) }))
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.RemoteAddr = "10.0.0.1:1234"
				req.Header = header.Clone()
				h.ServeHTTP(httptest.NewRecorder(), req)
				return got
			}
			if a, b := resolve(TrustProxies(count)), resolve(TrustProxiesWithConfig(TrustProxiesConfig{TrustedProxyCount: count})); a != b {
				t.Errorf("count %d headers %v: TrustProxies = %q, TrustProxiesWithConfig = %q", count, header, a, b)
			}
		}
	}
}

func TestClientIP_AbsentFromContext(t *testing.T) {
	if ip, ok := ClientIP(context.Background()); ok || ip != "" {
		t.Errorf("ClientIP(bare ctx) = (%q, %v), want (\"\", false)", ip, ok)
	}
}
