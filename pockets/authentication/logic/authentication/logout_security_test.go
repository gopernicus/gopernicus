package authentication

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestLogoutRequiresAuthenticatedProof(t *testing.T) {
	h := newHarness(t, nil)
	pair := h.loginPair(t, "logout-proof@example.com", "password123456789")
	uid, sid, ok := h.svc.verifyBearerClaims(pair.AccessToken)
	if !ok {
		t.Fatal("login did not issue valid access proof")
	}
	payload, err := json.Marshal(map[string]string{"user_id": uid, "session_id": sid})
	if err != nil {
		t.Fatal(err)
	}
	forged := "x." + base64.RawURLEncoding.EncodeToString(payload) + ".x"
	if err := h.svc.Logout(context.Background(), "", forged); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("forged token: %v", err)
	}
	if _, err := h.sess.Get(context.Background(), sid); err != nil {
		t.Fatalf("forged logout deleted victim session: %v", err)
	}
	h.signer.now = func() time.Time { return time.Now().Add(24 * time.Hour) }
	if err := h.svc.Logout(context.Background(), "", pair.AccessToken); !errors.Is(err, sdk.ErrUnauthorized) {
		t.Fatalf("expired access-only logout: %v", err)
	}
	if err := h.svc.Logout(context.Background(), pair.RefreshToken, pair.AccessToken); err != nil {
		t.Fatalf("refresh proof should revoke despite expired access: %v", err)
	}
	if _, err := h.sess.Get(context.Background(), sid); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("session survived authenticated logout: %v", err)
	}
	if err := h.svc.Logout(context.Background(), pair.RefreshToken, ""); err != nil {
		t.Fatalf("repeated refresh-only logout: %v", err)
	}
}

type logoutFailureSessions struct {
	session.SessionRepository
	lookupErr error
	deleteErr error
}

func (s logoutFailureSessions) GetByRefreshHash(ctx context.Context, hash string) (session.Session, session.RefreshMatch, error) {
	if s.lookupErr != nil {
		return session.Session{}, 0, s.lookupErr
	}
	return s.SessionRepository.GetByRefreshHash(ctx, hash)
}

func (s logoutFailureSessions) Delete(ctx context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.SessionRepository.Delete(ctx, id)
}

func TestLogoutPropagatesRevocationFailure(t *testing.T) {
	for _, phase := range []string{"lookup", "delete"} {
		t.Run(phase, func(t *testing.T) {
			h := newHarness(t, nil)
			pair := h.loginPair(t, "logout-failure@example.com", "password123456789")
			failed := logoutFailureSessions{SessionRepository: h.sess}
			if phase == "lookup" {
				failed.lookupErr = sdk.ErrUnavailable
			} else {
				failed.deleteErr = sdk.ErrUnavailable
			}
			h.svc.sessions = failed
			if err := h.svc.Logout(context.Background(), pair.RefreshToken, pair.AccessToken); !errors.Is(err, sdk.ErrUnavailable) {
				t.Fatalf("revocation failure hidden: %v", err)
			}
			h.svc.sessions = h.sess
			if _, err := h.svc.Refresh(context.Background(), pair.RefreshToken); err != nil {
				t.Fatalf("fixture should retain usable credential after failed revoke: %v", err)
			}
		})
	}
}
