package authentication

import (
	"context"
	"embed"
	"fmt"
	"log/slog"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer"
	"github.com/gopernicus/gopernicus/infrastructure/events"
)

//go:embed templates/*
var authTemplates embed.FS

// AuthTemplates returns the embedded email templates for use with
// [emailer.WithContentTemplates]. Register during app wiring:
//
//	emailer.WithContentTemplates("authentication", authbridge.AuthTemplates(), emailer.LayerCore)
func AuthTemplates() embed.FS {
	return authTemplates
}

// Subscribers handles auth event subscriptions for email delivery.
//
// Each sensitive operation gets its own typed handler. Application-defined
// sensitive operations should follow the same pattern: define your own
// event type, write your own subscriber that subscribes to it, and register
// your own template under your own emailer namespace. The framework's
// IssueVerificationCode / ConsumeVerificationCode primitives provide the
// reusable crypto/lockout machinery — see
// workshop/documentation/docs/gopernicus/topics/auth/sensitive-operations.md.
type Subscribers struct {
	emailer     *emailer.Emailer
	log         *slog.Logger
	frontendURL string
	subs        []events.Subscription
}

// NewSubscribers creates auth email subscribers.
//
// frontendURL is the base URL of the frontend app (e.g., "http://localhost:5173")
// used to construct links in emails (password reset, etc.).
func NewSubscribers(e *emailer.Emailer, log *slog.Logger, frontendURL string) *Subscribers {
	return &Subscribers{emailer: e, log: log, frontendURL: frontendURL}
}

// Register subscribes to auth events on the given bus.
func (s *Subscribers) Register(bus events.Bus) error {
	handlers := []struct {
		topic   string
		handler events.Handler
	}{
		{authentication.EventTypeVerificationCodeRequested, events.TypedHandler(s.handleVerificationCode)},
		{authentication.EventTypePasswordResetRequested, events.TypedHandler(s.handlePasswordReset)},
		{authentication.EventTypeOAuthLinkVerificationRequested, events.TypedHandler(s.handleOAuthLinkVerification)},
		{authentication.EventTypeRemovePasswordCodeRequested, events.TypedHandler(s.handleRemovePasswordCode)},
		{authentication.EventTypeUnlinkOAuthCodeRequested, events.TypedHandler(s.handleUnlinkOAuthCode)},
	}

	for _, h := range handlers {
		sub, err := bus.Subscribe(h.topic, h.handler)
		if err != nil {
			return fmt.Errorf("subscribe to %s: %w", h.topic, err)
		}
		s.subs = append(s.subs, sub)
	}

	return nil
}

// Unsubscribe removes all event subscriptions.
func (s *Subscribers) Unsubscribe() {
	for _, sub := range s.subs {
		_ = sub.Unsubscribe()
	}
	s.subs = nil
}

// ---------------------------------------------------------------------------
// Event handlers
// ---------------------------------------------------------------------------

func (s *Subscribers) handleVerificationCode(ctx context.Context, e authentication.VerificationCodeRequestedEvent) error {
	err := s.emailer.RenderAndSend(ctx, emailer.SendRequest{
		To:       e.Email,
		Subject:  "Verify your email",
		Template: "authentication:verification",
		Data: map[string]any{
			"DisplayName": e.DisplayName,
			"Code":        e.Code,
			"ExpiresIn":   e.ExpiresIn,
		},
	})
	if err != nil {
		s.log.ErrorContext(ctx, "failed to send verification email",
			slog.String("email", e.Email),
			slog.String("error", err.Error()),
		)
		return err
	}
	return nil
}

func (s *Subscribers) handlePasswordReset(ctx context.Context, e authentication.PasswordResetRequestedEvent) error {
	var resetLink string
	if e.ResetURL != "" {
		resetLink = fmt.Sprintf("%s?token=%s", e.ResetURL, e.Token)
	} else if s.frontendURL != "" {
		resetLink = fmt.Sprintf("%s/reset-password?token=%s", s.frontendURL, e.Token)
	}

	err := s.emailer.RenderAndSend(ctx, emailer.SendRequest{
		To:       e.Email,
		Subject:  "Reset your password",
		Template: "authentication:password_reset",
		Data: map[string]any{
			"DisplayName": e.DisplayName,
			"Token":       e.Token,
			"ResetLink":   resetLink,
			"ExpiresIn":   e.ExpiresIn,
		},
	})
	if err != nil {
		s.log.ErrorContext(ctx, "failed to send password reset email",
			slog.String("email", e.Email),
			slog.String("error", err.Error()),
		)
		return err
	}
	return nil
}

func (s *Subscribers) handleOAuthLinkVerification(ctx context.Context, e authentication.OAuthLinkVerificationRequestedEvent) error {
	err := s.emailer.RenderAndSend(ctx, emailer.SendRequest{
		To:       e.Email,
		Subject:  "Verify account link",
		Template: "authentication:oauth_link_verification",
		Data: map[string]any{
			"DisplayName": e.DisplayName,
			"Provider":    e.Provider,
			"Code":        e.Code,
			"ExpiresIn":   e.ExpiresIn,
		},
	})
	if err != nil {
		s.log.ErrorContext(ctx, "failed to send oauth link verification email",
			slog.String("email", e.Email),
			slog.String("provider", e.Provider),
			slog.String("error", err.Error()),
		)
		return err
	}
	return nil
}

func (s *Subscribers) handleRemovePasswordCode(ctx context.Context, e authentication.RemovePasswordCodeRequestedEvent) error {
	err := s.emailer.RenderAndSend(ctx, emailer.SendRequest{
		To:       e.Email,
		Subject:  "Confirm removal of your password",
		Template: "authentication:remove_password_verification",
		Data: map[string]any{
			"DisplayName": e.DisplayName,
			"Code":        e.Code,
			"ExpiresIn":   e.ExpiresIn,
		},
	})
	if err != nil {
		s.log.ErrorContext(ctx, "failed to send remove password verification email",
			slog.String("email", e.Email),
			slog.String("error", err.Error()),
		)
		return err
	}
	return nil
}

func (s *Subscribers) handleUnlinkOAuthCode(ctx context.Context, e authentication.UnlinkOAuthCodeRequestedEvent) error {
	err := s.emailer.RenderAndSend(ctx, emailer.SendRequest{
		To:       e.Email,
		Subject:  fmt.Sprintf("Confirm disconnect of %s from your account", e.Provider),
		Template: "authentication:unlink_oauth_verification",
		Data: map[string]any{
			"DisplayName": e.DisplayName,
			"Provider":    e.Provider,
			"Code":        e.Code,
			"ExpiresIn":   e.ExpiresIn,
		},
	})
	if err != nil {
		s.log.ErrorContext(ctx, "failed to send unlink oauth verification email",
			slog.String("email", e.Email),
			slog.String("provider", e.Provider),
			slog.String("error", err.Error()),
		)
		return err
	}
	return nil
}
