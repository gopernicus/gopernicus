package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/sdk/email"
	"github.com/gopernicus/gopernicus/sdk/feature"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// --- compile-time seam assertions ---

var (
	// Register conforms to the feature contract's registration signature.
	_ func(feature.Mount, Repositories, Config) error = Register
	// Service.RequireUser is a web.Middleware via its method value.
	_ web.Middleware = (&Service{}).RequireUser
)

// stubHasher / stubMailer satisfy the required Config ports for the
// happy-path constructor test.
type stubHasher struct{}

func (stubHasher) HashPassword(string) (string, error) { return "x", nil }
func (stubHasher) VerifyPassword(string, string) error { return nil }

type stubMailer struct{}

func (stubMailer) Send(context.Context, email.Message) error { return nil }

func TestNewServiceRequiresHasher(t *testing.T) {
	_, err := NewService(Repositories{}, Config{Mailer: stubMailer{}})
	if !errors.Is(err, ErrHasherRequired) {
		t.Errorf("nil Hasher: err=%v, want ErrHasherRequired", err)
	}
}

func TestNewServiceRequiresMailer(t *testing.T) {
	_, err := NewService(Repositories{}, Config{Hasher: stubHasher{}})
	if !errors.Is(err, ErrMailerRequired) {
		t.Errorf("nil Mailer: err=%v, want ErrMailerRequired", err)
	}
}

func TestNewServiceDefaultsRateLimiter(t *testing.T) {
	// A nil RateLimiter must not error — it defaults to an in-memory limiter.
	svc, err := NewService(Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc == nil {
		t.Fatal("NewService returned nil Service")
	}
}

func TestRegisterRequiresHasher(t *testing.T) {
	err := Register(feature.Mount{}, Repositories{}, Config{Mailer: stubMailer{}})
	if !errors.Is(err, ErrHasherRequired) {
		t.Errorf("Register nil Hasher: err=%v, want ErrHasherRequired", err)
	}
}

func TestRegisterMountsRoutes(t *testing.T) {
	h := web.NewWebHandler()
	err := Register(feature.Mount{Router: h}, Repositories{}, Config{Hasher: stubHasher{}, Mailer: stubMailer{}})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// The mounted logout route exists and is session-gated (401 without one),
	// proving the routes were registered onto the mount's router.
	req := httptest.NewRequest("POST", "/auth/logout", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("logout status = %d, want 401 (route mounted + gated)", rec.Code)
	}
}
