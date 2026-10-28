package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
)

// RunDelegatedRotationFence proves the first-party refresh paths never move a
// delegated (OAuth2) session: Rotate, ConsumeGrace and, when the store
// implements session.ActivityRepository, RotateAt each refuse a delegated row
// with session.ErrRotationConflict and leave it unchanged. A delegated grant
// rotates only through oauth2.Repository.RotateRefresh. It runs outside Run so
// a host store adopts it explicitly.
func RunDelegatedRotationFence(t *testing.T, newRepos func(*testing.T) auth.Repositories) {
	t.Helper()
	t.Run("DelegatedRotationFence", func(t *testing.T) {
		r := newRepos(t)
		ctx := context.Background()
		now := time.Now().UTC().Truncate(time.Microsecond)
		s, _ := session.NewSession("u-delegated-fence", time.Hour, now)
		s.Profile = session.ProfileDelegated
		s.Delegation = session.Delegation{Issuer: "https://auth.example.test", ClientID: "https://client.example.test/metadata.json", Resource: "https://mcp.example.test"}
		s.RefreshTokenHash = "delegated-current-" + s.ID
		s.PreviousRefreshTokenHash = "delegated-previous-" + s.ID
		if _, err := r.Sessions.Create(ctx, s); err != nil {
			t.Fatalf("Create(delegated): %v", err)
		}

		if err := r.Sessions.Rotate(ctx, s.ID, s.RefreshTokenHash, "rotated-"+s.ID); !errors.Is(err, session.ErrRotationConflict) {
			t.Errorf("Rotate(delegated) = %v, want ErrRotationConflict", err)
		}
		if err := r.Sessions.ConsumeGrace(ctx, s.ID, s.PreviousRefreshTokenHash); !errors.Is(err, session.ErrRotationConflict) {
			t.Errorf("ConsumeGrace(delegated) = %v, want ErrRotationConflict", err)
		}
		if a, ok := r.Sessions.(session.ActivityRepository); ok {
			if err := a.RotateAt(ctx, s.ID, s.RefreshTokenHash, "rotated-at-"+s.ID, now); !errors.Is(err, session.ErrRotationConflict) {
				t.Errorf("RotateAt(delegated) = %v, want ErrRotationConflict", err)
			}
		}

		got, match, err := r.Sessions.GetByRefreshHash(ctx, s.RefreshTokenHash)
		if err != nil || match != session.RefreshMatchCurrent {
			t.Fatalf("GetByRefreshHash(original) = match %d err %v, want the untouched row", match, err)
		}
		if got.RefreshTokenHash != s.RefreshTokenHash || got.PreviousRefreshTokenHash != s.PreviousRefreshTokenHash || got.PreviousUsed || got.RotationCount != 0 {
			t.Fatalf("delegated row moved: current %q previous %q used %v rotations %d", got.RefreshTokenHash, got.PreviousRefreshTokenHash, got.PreviousUsed, got.RotationCount)
		}
	})
}
