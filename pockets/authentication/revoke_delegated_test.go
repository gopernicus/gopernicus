package authentication

import (
	"context"
	"errors"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
)

// delegatedSessionManagement adds session.DelegatedRevoker to the recorder's
// management stub.
type delegatedSessionManagement struct {
	recorderSessionManagement
	users []string
}

func (d *delegatedSessionManagement) RevokeDelegatedForUser(_ context.Context, userID string) (int, error) {
	d.users = append(d.users, userID)
	return 3, nil
}

func TestRevokeDelegatedUserSessions(t *testing.T) {
	cfg := browserBaseConfig()
	build := func(t *testing.T, events *recorderEvents, management any) *authlogic.Service {
		t.Helper()
		repos := Repositories{SecurityEvents: events}
		switch m := management.(type) {
		case *delegatedSessionManagement:
			repos.SessionManagement = m
		case recorderSessionManagement:
			repos.SessionManagement = m
		}
		c, err := New(testRepositories(repos), cfg.TokenSigner, cfg.RuntimeMode, cfg.DeliveryMode,
			WithPassword(PasswordConfig{Hasher: cfg.Hasher}), WithDelivery(DeliveryConfig{Mailer: cfg.Mailer}))
		if err != nil {
			t.Fatal(err)
		}
		return c.Authentication
	}

	t.Run("RecordsTheCount", func(t *testing.T) {
		events, management := &recorderEvents{}, &delegatedSessionManagement{}
		svc := build(t, events, management)
		if err := svc.RevokeDelegatedUserSessions(context.Background(), "u-1"); err != nil {
			t.Fatal(err)
		}
		if len(management.users) != 1 || management.users[0] != "u-1" {
			t.Fatalf("revoked for %v, want [u-1]", management.users)
		}
		if len(events.created) != 1 {
			t.Fatalf("recorded %d rows, want 1", len(events.created))
		}
		got := events.created[0]
		if got.EventType != securityevent.TypeOAuth2ConnectionsRevoked || got.EventStatus != securityevent.StatusSuccess || got.UserID != "u-1" || got.Details["sessions"] != 3 {
			t.Fatalf("row = %+v", got)
		}
	})
	t.Run("UnsupportedStore", func(t *testing.T) {
		events := &recorderEvents{}
		svc := build(t, events, recorderSessionManagement{})
		if err := svc.RevokeDelegatedUserSessions(context.Background(), "u-1"); !errors.Is(err, authlogic.ErrDelegatedRevocationUnsupported) {
			t.Fatalf("err = %v, want ErrDelegatedRevocationUnsupported", err)
		}
		if len(events.created) != 0 {
			t.Fatalf("recorded %+v on a refusal", events.created)
		}
	})
	t.Run("NoUser", func(t *testing.T) {
		svc := build(t, &recorderEvents{}, &delegatedSessionManagement{})
		if err := svc.RevokeDelegatedUserSessions(context.Background(), ""); err == nil {
			t.Fatal("an empty user was accepted")
		}
	})
}
