package authentication

import (
	"context"
	"net/http"
	"testing"

	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/authsvc"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
	"github.com/gopernicus/gopernicus/sdk/foundation/web"
)

// AV3D-4.4 — the enumeration-safe unauthenticated starts must never report a 202-accepted
// lie after the bounded in-process outbox drops the work. A capacity/closed admission
// rejection maps to an honest 503, identical for a known and an unknown identifier
// (admission precedes any account lookup), so the HTTP class carries no existence signal.

// capacityQueue is a delivery outbox seam that always rejects admission with a fixed
// in-process error, standing in for a saturated or closed bounded runtime at the
// transport boundary.
type capacityQueue struct{ err error }

func (q capacityQueue) Enqueue(context.Context, delivery.Command) (delivery.Receipt, error) {
	return delivery.Receipt{}, q.err
}
func (q capacityQueue) Replace(context.Context, delivery.Command) (delivery.Receipt, error) {
	return delivery.Receipt{}, q.err
}
func (q capacityQueue) Status(context.Context, string) (delivery.Status, error) {
	return delivery.Status{}, sdk.ErrNotFound
}

// testQueue is the delivery outbox seam authsvc.Deps.Queue accepts (matching stubQueue and
// capacityQueue); it lets the saturation harness inject a rejecting queue.
type testQueue interface {
	Enqueue(context.Context, delivery.Command) (delivery.Receipt, error)
	Replace(context.Context, delivery.Command) (delivery.Receipt, error)
	Status(context.Context, string) (delivery.Status, error)
}

// newSaturationHandler builds a real authsvc.Service over in-memory fakes with the given
// delivery outbox seam, mounts the routes, and returns the handler. passwordless enables
// the passwordless start route.
func newSaturationHandler(t *testing.T, q testQueue, passwordless bool) http.Handler {
	t.Helper()
	users := newMemUsers()
	passwords := &memPasswords{m: map[string]string{}}
	sessions := &memSessions{m: map[string]session.Session{}}
	challenges := &memChallenges{byID: map[string]challenge.Challenge{}}
	router, err := delivery.NewRouter(delivery.Deps{Mailer: nopMailer{}, MailFrom: "noreply@example.com"})
	if err != nil {
		t.Fatalf("delivery.NewRouter: %v", err)
	}
	deps := authsvc.Deps{
		Users:          users,
		Identifiers:    newMemIdentifiers(users),
		Passwords:      passwords,
		Sessions:       sessions,
		Challenges:     challenges,
		Protector:      memProtector{},
		PasswordResets: &memPasswordResets{ch: challenges, pw: passwords, sess: sessions},
		Hasher:         fakeHasher{},
		Deliver:        router,
		Queue:          q,
		Limiter:        ratelimiter.NewMemory(),
		Cookie:         authsvc.CookieConfig{},
		TokenSigner:    newFakeSigner(),
	}
	if passwordless {
		deps.Passwordless = []string{"email"}
		deps.PublicAuthBaseURL = "https://auth.example.com"
	}
	svc := authsvc.NewService(deps)
	h := web.NewWebHandler()
	Mount(h, svc, nil, "", MutationSecurity{}, nil)
	return h
}

// TestForgotPasswordSaturationReturns503NotAccepted proves the forgot-password JSON
// transport maps a saturated/closed in-process outbox to an honest 503 — never a
// 202-accepted lie after dropping the work — identically for a known and an unknown
// address.
func TestForgotPasswordSaturationReturns503NotAccepted(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"capacity", delivery.ErrDeliveryCapacity},
		{"closed", delivery.ErrDeliveryClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSaturationHandler(t, capacityQueue{err: tc.err}, false)
			codes := make([]int, 0, 2)
			for _, addr := range []string{"known@example.com", "ghost@example.com"} {
				rec := do(t, h, "POST", "/auth/password/forgot", `{"email":"`+addr+`"}`)
				if rec.Code == http.StatusAccepted {
					t.Fatalf("forgot(%s) returned 202 accepted after admission was dropped (%s)", addr, tc.name)
				}
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("forgot(%s) under %s = %d, want 503", addr, tc.name, rec.Code)
				}
				codes = append(codes, rec.Code)
			}
			if codes[0] != codes[1] {
				t.Fatalf("known/unknown HTTP class differ under %s: %d vs %d (enumeration signal)", tc.name, codes[0], codes[1])
			}
		})
	}
}

// TestPasswordlessStartSaturationReturns503NotAccepted proves the passwordless start JSON
// transport maps a saturated/closed in-process outbox to an honest 503 — never a
// 202-accepted lie — identically for a known and an unknown identifier.
func TestPasswordlessStartSaturationReturns503NotAccepted(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"capacity", delivery.ErrDeliveryCapacity},
		{"closed", delivery.ErrDeliveryClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSaturationHandler(t, capacityQueue{err: tc.err}, true)
			codes := make([]int, 0, 2)
			for _, id := range []string{"known@example.com", "ghost@example.com"} {
				body := `{"identifier_kind":"email","identifier":"` + id + `"}`
				rec := do(t, h, "POST", "/auth/passwordless/start", body)
				if rec.Code == http.StatusAccepted {
					t.Fatalf("passwordless start(%s) returned 202 accepted after admission was dropped (%s)", id, tc.name)
				}
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("passwordless start(%s) under %s = %d, want 503", id, tc.name, rec.Code)
				}
				codes = append(codes, rec.Code)
			}
			if codes[0] != codes[1] {
				t.Fatalf("known/unknown HTTP class differ under %s: %d vs %d (enumeration signal)", tc.name, codes[0], codes[1])
			}
		})
	}
}

// TestFormFailureDeliveryUnavailableMapsTo503 pins the HTML form arm: a bounded
// in-process outbox admission rejection now wraps sdk.ErrUnavailable, so the shared
// formFailure mapper resolves it to 503 through the domain-error writer — no delivery
// special-case remains. Both the capacity and shutdown sentinels, and any raw
// sdk.ErrUnavailable, resolve to 503; sdk.ErrConflict stays 409 (state contention).
func TestFormFailureDeliveryUnavailableMapsTo503(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"capacity", delivery.ErrDeliveryCapacity, http.StatusServiceUnavailable},
		{"closed", delivery.ErrDeliveryClosed, http.StatusServiceUnavailable},
		{"raw-unavailable", sdk.ErrUnavailable, http.StatusServiceUnavailable},
		{"conflict-stays-409", sdk.ErrConflict, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := formFailure(tc.err, "generic")
			if status != tc.want {
				t.Fatalf("formFailure(%s) = %d, want %d", tc.name, status, tc.want)
			}
		})
	}
}
