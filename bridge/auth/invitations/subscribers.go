package invitations

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	invitationscore "github.com/gopernicus/gopernicus/core/auth/invitations"
	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// Subscribers handles invitation event subscriptions.
type Subscribers struct {
	invitations *invitationscore.Inviter
	log         *slog.Logger
	subs        []events.Subscription
}

// NewSubscribers creates invitation event subscribers.
func NewSubscribers(invitations *invitationscore.Inviter, log *slog.Logger) *Subscribers {
	return &Subscribers{invitations: invitations, log: log}
}

// Register subscribes to events on the given bus.
func (s *Subscribers) Register(bus events.Bus) error {
	sub, err := bus.Subscribe(authentication.EventTypeEmailVerified, events.TypedHandler(s.handleEmailVerified))
	if err != nil {
		return fmt.Errorf("subscribe to %s: %w", authentication.EventTypeEmailVerified, err)
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
func (s *Subscribers) handleEmailVerified(ctx context.Context, e authentication.EmailVerifiedEvent) error {
	resolved, err := s.invitations.ResolveOnRegistration(ctx, e.Email, invitationscore.IdentifierTypeEmail, "user", e.UserID)
	if err != nil {
		s.log.ErrorContext(ctx, "failed to resolve invitations on email verification",
			slog.String("user_id", e.UserID),
			slog.String("email", e.Email),
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
