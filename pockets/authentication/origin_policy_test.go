package authentication

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	delivery "github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	environment "github.com/gopernicus/gopernicus/sdk/pkg/environment"
	"github.com/gopernicus/gopernicus/sdk/pkg/web"
)

// TestBrowserOriginPolicyReachesMountedGates proves BrowserConfig.OriginPolicy is
// threaded from the root constructor to the mounted origin gate: a namespace
// origin clears the credential-establishment gate on /auth/logout beside the
// exact AllowedOrigins list, while lookalikes, reserved labels and extra labels
// are still refused.
func TestBrowserOriginPolicyReachesMountedGates(t *testing.T) {
	policy, err := web.NewOriginPolicy(web.OriginPolicyConfig{Namespaces: []web.OriginNamespace{
		{Scheme: "https", Suffix: "flight.example.com", Ports: []int{443, 8443}, Reserved: []string{"api"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := constructorConfig{
		Hasher: stubHasher{}, Mailer: stubMailer{}, TokenSigner: stubSigner{},
		RuntimeMode: environment.ModeDevelopment, DeliveryMode: delivery.ModeOff,
		AllowedOrigins: []string{"https://app.example.com"},
		OriginPolicy:   policy,
	}
	svc, err := newFixture(testRepositories(Repositories{}), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := web.NewWebHandler()
	if err := svc.HTTP.Register(pockets.Mount{Router: h}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	tests := []struct {
		origin string
		want   int
	}{
		{"https://acme.flight.example.com", http.StatusOK},
		{"https://acme.flight.example.com:8443", http.StatusOK},
		{"https://app.example.com", http.StatusOK},
		{"https://api.flight.example.com", http.StatusForbidden},
		{"https://a.b.flight.example.com", http.StatusForbidden},
		{"https://evilflight.example.com", http.StatusForbidden},
		{"https://acme.flight.example.com.evil.com", http.StatusForbidden},
		{"https://[acme.flight.example.com]", http.StatusForbidden},
		{"https://[acme.flight.example.com]:8443", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != tt.want {
				t.Fatalf("logout from %s = %d, want %d; body=%s", tt.origin, rec.Code, tt.want, rec.Body)
			}
		})
	}
}
