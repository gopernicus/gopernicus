package invitations

import (
	"context"
	"fmt"
	"log/slog"

	invitationscore "github.com/gopernicus/gopernicus/core/auth/invitations"
	"github.com/gopernicus/gopernicus/core/repositories/auth/users"
	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// userGetter looks up a user by ID. Satisfied by *users.Repository.
type userGetter interface {
	Get(ctx context.Context, userID string) (users.User, error)
}

// Subscribers handles invitation event subscriptions.
type Subscribers struct {
	invitations *invitationscore.Inviter
	users       userGetter
	log         *slog.Logger
	subs        []events.Subscription
}

// NewSubscribers creates invitation event subscribers.
func NewSubscribers(invitations *invitationscore.Inviter, userRepo userGetter, log *slog.Logger) *Subscribers {
	return &Subscribers{invitations: invitations, users: userRepo, log: log}
}

// Register subscribes to events on the given bus.
func (s *Subscribers) Register(bus events.Bus) error {
	sub, err := bus.Subscribe("user.email_verified", events.TypedHandler(s.handleEmailVerified))
	if err != nil {
		return fmt.Errorf("subscribe to user.email_verified: %w", err)
	}
	s.subs = append(s.subs, sub)

	return nil
}

// Unsubscribe removes all event subscriptions.
func (s *Subscribers) Unsubscribe() {
	for _, sub := range s.subs {
		_ = sub.Unsubscribe()
	}
	s.subs = nil
}

// handleEmailVerified resolves pending invitations when a user verifies their email.
func (s *Subscribers) handleEmailVerified(ctx context.Context, e users.UserEmailVerifiedEvent) error {
	user, err := s.users.Get(ctx, e.UserID)
	if err != nil {
		s.log.ErrorContext(ctx, "failed to look up user for invitation resolution",
			slog.String("user_id", e.UserID),
			slog.String("error", err.Error()),
		)
		return err
	}

	resolved, err := s.invitations.ResolveOnRegistration(ctx, user.Email, invitationscore.IdentifierTypeEmail, "user", e.UserID)
	if err != nil {
		s.log.ErrorContext(ctx, "failed to resolve invitations on email verification",
			slog.String("user_id", e.UserID),
			slog.String("email", user.Email),
			slog.String("error", err.Error()),
		)
		return err
	}

	if resolved > 0 {
		s.log.InfoContext(ctx, "resolved pending invitations on email verification",
			slog.String("user_id", e.UserID),
			slog.Int("resolved", resolved),
		)
	}

	return nil
}
