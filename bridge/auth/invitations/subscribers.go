package invitations

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gopernicus/gopernicus/core/auth/authentication"
	invitationscore "github.com/gopernicus/gopernicus/core/auth/invitations"
	"github.com/gopernicus/gopernicus/core/repositories/auth/users"
	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer"
	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// userGetter looks up a user by ID on the authentication engine's minimal
// User shape. Any authentication.UserRepository satisfies it — in a generated
// project that's the emitted users satisfier (satisfiers.NewUserSatisfier),
// so no project-local adapter is needed.
type userGetter interface {
	Get(ctx context.Context, id string) (authentication.User, error)
}

// ResourceNameResolver returns a human-friendly name for a resource. Best
// effort: return "" when the resource can't be resolved or the type isn't
// supported — the email templates fall back to the resource type label.
type ResourceNameResolver func(ctx context.Context, resourceType, resourceID string) string

// DestinationPathResolver returns a relative frontend path for a resource
// (e.g. "/d/d_campaign_01"). The subscriber appends it to the invitation's
// validated redirect origin to build the full link. Return "" to fall back
// to the origin root.
//
// The path must begin with a single "/" and carry no scheme/host — anything
// else is rejected to prevent open-redirect via invitation email.
type DestinationPathResolver func(ctx context.Context, resourceType, resourceID string) string

// Subscribers handles invitation event subscriptions.
//
// Three concerns live here:
//   - handleEmailVerified: on user.email_verified, resolves pending
//     auto-accept invitations for the user's address.
//   - handleInvitationSent: on invitation.sent, delivers the invitation
//     email. Auto-accept invitations use the "shared with you" template
//     linking straight to the resource; others use the "please accept"
//     template with a token link.
//   - handleMemberAdded: on member.added, notifies the added user with a
//     link straight to the resource.
//
// The emailer is optional (WithEmailer): when absent, the email handlers are
// not registered and only auto-resolve runs — current behavior for projects
// that haven't wired invitation emails.
type Subscribers struct {
	invitations     *invitationscore.Inviter
	users           userGetter
	emailer         *emailer.Emailer
	resolveName     ResourceNameResolver
	resolveDestPath DestinationPathResolver
	log             *slog.Logger
	subs            []events.Subscription
}

// SubscriberOption configures optional Subscribers dependencies.
type SubscriberOption func(*Subscribers)

// WithEmailer enables invitation email delivery: invitation.sent and
// member.added subscriptions are registered and rendered through e using
// the invitations:invite, invitations:shared, and invitations:member_added
// templates.
func WithEmailer(e *emailer.Emailer) SubscriberOption {
	return func(s *Subscribers) { s.emailer = e }
}

// WithResourceNameResolver supplies the resource-name lookup used in email
// copy ("invited to <name>"). Unset, templates fall back to the resource
// type label.
func WithResourceNameResolver(f ResourceNameResolver) SubscriberOption {
	return func(s *Subscribers) { s.resolveName = f }
}

// WithDestinationPathResolver supplies the frontend-path lookup used to
// deep-link emails to the shared resource. Unset, links land on the
// redirect origin's root.
func WithDestinationPathResolver(f DestinationPathResolver) SubscriberOption {
	return func(s *Subscribers) { s.resolveDestPath = f }
}

// NewSubscribers creates invitation event subscribers.
//
// The frontend origin for each email link travels on the event itself
// (InvitationSentEvent.RedirectURL / MemberAddedEvent.RedirectURL),
// validated against the allowed-frontends list at the HTTP bridge before
// emission. The subscriber has no configured default; events missing a
// RedirectURL are logged and skipped.
func NewSubscribers(invitations *invitationscore.Inviter, userRepo userGetter, log *slog.Logger, opts ...SubscriberOption) *Subscribers {
	s := &Subscribers{invitations: invitations, users: userRepo, log: log}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// nameFor invokes the resolver if set, else returns empty.
func (s *Subscribers) nameFor(ctx context.Context, resourceType, resourceID string) string {
	if s.resolveName == nil {
		return ""
	}
	return s.resolveName(ctx, resourceType, resourceID)
}

// openLinkFor builds an absolute URL to the resource by joining the
// per-event redirect origin with the resolver's relative path. Validates
// that the resolver returned a relative path; rejects anything that looks
// like a scheme/host to prevent open-redirect.
func (s *Subscribers) openLinkFor(ctx context.Context, origin, resourceType, resourceID string) string {
	if s.resolveDestPath == nil {
		return origin
	}
	path := s.resolveDestPath(ctx, resourceType, resourceID)
	if !isSafeRelativePath(path) {
		return origin
	}
	return origin + path
}

// isSafeRelativePath accepts paths that start with a single "/" and cannot
// be interpreted as a scheme-relative or absolute URL.
func isSafeRelativePath(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	if strings.HasPrefix(p, "//") {
		return false
	}
	if strings.Contains(p, "://") {
		return false
	}
	return true
}

// Register subscribes to events on the given bus.
func (s *Subscribers) Register(bus events.Bus) error {
	sub, err := bus.Subscribe("user.email_verified", events.TypedHandler(s.handleEmailVerified))
	if err != nil {
		return fmt.Errorf("subscribe to user.email_verified: %w", err)
	}
	s.subs = append(s.subs, sub)

	if s.emailer != nil {
		invSub, err := bus.Subscribe("invitation.sent", events.TypedHandler(s.handleInvitationSent))
		if err != nil {
			return fmt.Errorf("subscribe to invitation.sent: %w", err)
		}
		s.subs = append(s.subs, invSub)

		memberSub, err := bus.Subscribe("member.added", events.TypedHandler(s.handleMemberAdded))
		if err != nil {
			return fmt.Errorf("subscribe to member.added: %w", err)
		}
		s.subs = append(s.subs, memberSub)
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

// handleInvitationSent delivers the invitation email when an invitation is
// created or resent. Branches template by AutoAccept: auto-accept invitations
// get a "shared with you" email pointing straight to the resource; others get
// a "please accept" email with a token link.
func (s *Subscribers) handleInvitationSent(ctx context.Context, e invitationscore.InvitationSentEvent) error {
	if e.RedirectURL == "" {
		s.log.ErrorContext(ctx, "invitation email skipped: RedirectURL missing on event",
			slog.String("invitation_id", e.InvitationID),
			slog.String("email", e.Identifier),
		)
		return nil
	}

	inviterName := ""
	if inviter, err := s.users.Get(ctx, e.InvitedBy); err != nil {
		s.log.WarnContext(ctx, "invitation email: inviter lookup failed",
			slog.String("inviter_id", e.InvitedBy),
			slog.String("error", err.Error()),
		)
	} else {
		inviterName = inviter.DisplayName
	}

	resourceName := s.nameFor(ctx, e.ResourceType, e.ResourceID)

	var req emailer.SendRequest
	if e.AutoAccept {
		// Direct-share style: link straight to the resource. If the invitee
		// isn't registered yet, the frontend auth layer redirects them to
		// register, and ResolveOnRegistration auto-claims the invitation on
		// email verification.
		req = emailer.SendRequest{
			To:       e.Identifier,
			Subject:  "You've been added",
			Template: "invitations:shared",
			Data: map[string]any{
				"InviterName":  inviterName,
				"ResourceType": e.ResourceType,
				"ResourceID":   e.ResourceID,
				"ResourceName": resourceName,
				"Relation":     e.Relation,
				"OpenLink":     s.openLinkFor(ctx, e.RedirectURL, e.ResourceType, e.ResourceID),
			},
		}
	} else {
		acceptLink := e.RedirectURL + "/invitations/accept?token=" + e.Token
		req = emailer.SendRequest{
			To:       e.Identifier,
			Subject:  "You've been invited",
			Template: "invitations:invite",
			Data: map[string]any{
				"InviterName":  inviterName,
				"ResourceType": e.ResourceType,
				"ResourceID":   e.ResourceID,
				"ResourceName": resourceName,
				"Relation":     e.Relation,
				"AcceptLink":   acceptLink,
				"ExpiresIn":    fmt.Sprintf("%d days", invitationscore.InvitationExpiryDays),
			},
		}
	}

	if err := s.emailer.RenderAndSend(ctx, req); err != nil {
		s.log.ErrorContext(ctx, "failed to send invitation email",
			slog.String("invitation_id", e.InvitationID),
			slog.String("email", e.Identifier),
			slog.Bool("auto_accept", e.AutoAccept),
			slog.String("error", err.Error()),
		)
		return err
	}

	s.log.InfoContext(ctx, "invitation email sent",
		slog.String("invitation_id", e.InvitationID),
		slog.String("email", e.Identifier),
		slog.Bool("auto_accept", e.AutoAccept),
	)
	return nil
}

// handleMemberAdded notifies an existing user that they were added directly
// to a resource (the auto-accept + known-user path that bypasses the
// invitation record). Only user subjects are notified — service accounts and
// groups skip.
func (s *Subscribers) handleMemberAdded(ctx context.Context, e invitationscore.MemberAddedEvent) error {
	if e.SubjectType != "user" {
		return nil
	}

	if e.RedirectURL == "" {
		s.log.ErrorContext(ctx, "member added email skipped: RedirectURL missing on event",
			slog.String("user_id", e.SubjectID),
			slog.String("resource_type", e.ResourceType),
			slog.String("resource_id", e.ResourceID),
		)
		return nil
	}

	addedUser, err := s.users.Get(ctx, e.SubjectID)
	if err != nil {
		s.log.ErrorContext(ctx, "member added: user lookup failed",
			slog.String("user_id", e.SubjectID),
			slog.String("error", err.Error()),
		)
		return err
	}

	inviterName := ""
	if inviter, err := s.users.Get(ctx, e.AddedBy); err != nil {
		s.log.WarnContext(ctx, "member added: inviter lookup failed",
			slog.String("inviter_id", e.AddedBy),
			slog.String("error", err.Error()),
		)
	} else {
		inviterName = inviter.DisplayName
	}

	err = s.emailer.RenderAndSend(ctx, emailer.SendRequest{
		To:       addedUser.Email,
		Subject:  "You've been added",
		Template: "invitations:member_added",
		Data: map[string]any{
			"DisplayName":  addedUser.DisplayName,
			"InviterName":  inviterName,
			"ResourceType": e.ResourceType,
			"ResourceID":   e.ResourceID,
			"ResourceName": s.nameFor(ctx, e.ResourceType, e.ResourceID),
			"Relation":     e.Relation,
			"OpenLink":     s.openLinkFor(ctx, e.RedirectURL, e.ResourceType, e.ResourceID),
		},
	})
	if err != nil {
		s.log.ErrorContext(ctx, "failed to send member added email",
			slog.String("user_id", e.SubjectID),
			slog.String("email", addedUser.Email),
			slog.String("error", err.Error()),
		)
		return err
	}

	s.log.InfoContext(ctx, "member added email sent",
		slog.String("user_id", e.SubjectID),
		slog.String("email", addedUser.Email),
	)
	return nil
}
