package authentication

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

// The IdentityConfig.Registration values.
const (
	// RegistrationOpen is the zero value: anyone may create an account on every
	// path the host wires.
	RegistrationOpen RegistrationMode = ""
	// RegistrationInvitationOnly admits an account creation only for an address
	// with a pending invitation.
	RegistrationInvitationOnly RegistrationMode = "invitation_only"
	// RegistrationClosed admits no account creation: the roster is frozen.
	RegistrationClosed RegistrationMode = "closed"
)

// The registration refusal codes: the machine code of the refusal's 403 and
// the ?auth= marker of a browser OAuth callback.
const (
	RegistrationCodeClosed             = "registration_closed"
	RegistrationCodeInvitationRequired = "registration_invitation_required"
)

var (
	// ErrRegistrationClosed refuses an account creation under
	// RegistrationClosed. It wraps sdk.ErrForbidden (403).
	ErrRegistrationClosed = fmt.Errorf("authentication: registration is closed: %w", sdk.ErrForbidden)
	// ErrRegistrationInvitationRequired refuses an account creation under
	// RegistrationInvitationOnly for an address with no pending invitation. It
	// wraps sdk.ErrForbidden (403).
	ErrRegistrationInvitationRequired = fmt.Errorf("authentication: registration requires an invitation: %w", sdk.ErrForbidden)
	// ErrRegistrationModeRequiresInvitations is returned by New when
	// IdentityConfig.Registration is invitation_only without an invitation
	// service that can look up a pending invitation (WithInvitations).
	ErrRegistrationModeRequiresInvitations = fmt.Errorf("authentication: IdentityConfig.Registration invitation_only requires invitations (a Granter): %w", sdk.ErrInvalidInput)
	// ErrRegistrationClosedWithProvisioning is returned by New when
	// IdentityConfig.Registration is closed beside an external provider with
	// ProvisionOnFirstVerification: the two contradict each other.
	ErrRegistrationClosedWithProvisioning = fmt.Errorf("authentication: IdentityConfig.Registration closed contradicts ExternalProvider.ProvisionOnFirstVerification: %w", sdk.ErrInvalidInput)
)

// RegistrationMode selects who may create an account: open, invitation_only
// or closed.
type RegistrationMode string

// invitationLookup is the optional pending-invitation lookup asserted on the
// invitation resolver; the invitation service implements it.
type invitationLookup interface {
	HasPendingInvitation(ctx context.Context, kind, value string) (bool, error)
}

// RegistrationRefusal is a registration refusal from the OAuth callback,
// carrying the flow's validated destination so a browser transport can land the
// user there with the refusal Code. It unwraps to ErrRegistrationClosed or
// ErrRegistrationInvitationRequired.
type RegistrationRefusal struct {
	Code       string
	RedirectTo string
	err        error
}

func (e *RegistrationRefusal) Error() string { return e.err.Error() }

func (e *RegistrationRefusal) Unwrap() error { return e.err }

// registrationOpenName is the explicit spelling of RegistrationOpen.
const registrationOpenName = "open"

// String names the mode as the boot line and configuration spell it.
func (m RegistrationMode) String() string {
	if m == RegistrationOpen {
		return registrationOpenName
	}
	return string(m)
}

// normalizeRegistrationMode maps the explicit "open" to the zero value and
// refuses an unknown mode, in every runtime mode: a typo never means open.
func normalizeRegistrationMode(mode RegistrationMode) (RegistrationMode, error) {
	switch mode {
	case registrationOpenName:
		return RegistrationOpen, nil
	case RegistrationOpen, RegistrationInvitationOnly, RegistrationClosed:
		return mode, nil
	}
	return "", fmt.Errorf("authentication: IdentityConfig.Registration (AUTH_REGISTRATION) %q is not open, invitation_only or closed: %w", mode, sdk.ErrInvalidInput)
}

// RegistrationMode reports the resolved IdentityConfig.Registration.
func (s *Service) RegistrationMode() RegistrationMode { return s.registration }

// registrationRefusal decides an account creation for the normalized address
// (kind, value) under the host's mode: an empty reason admits it; otherwise the
// securityevent reason names the refusal. A lookup failure is returned as-is.
func (s *Service) registrationRefusal(ctx context.Context, kind, value string) (string, error) {
	return registrationRefusal(ctx, s.registration, s.invitations, kind, value)
}

func registrationRefusal(ctx context.Context, mode RegistrationMode, resolver invitationResolver, kind, value string) (string, error) {
	switch mode {
	case RegistrationClosed:
		return securityevent.ReasonRegistrationClosed, nil
	case RegistrationInvitationOnly:
		lookup, ok := resolver.(invitationLookup)
		if !ok {
			return securityevent.ReasonInvitationRequired, nil
		}
		pending, err := lookup.HasPendingInvitation(ctx, kind, value)
		if err != nil {
			return "", err
		}
		if !pending {
			return securityevent.ReasonInvitationRequired, nil
		}
	}
	return "", nil
}

// registrationError maps a refusal reason to its sentinel.
func registrationError(reason string) error {
	if reason == securityevent.ReasonRegistrationClosed {
		return ErrRegistrationClosed
	}
	return ErrRegistrationInvitationRequired
}

// registrationCode maps a refusal sentinel to its wire code.
func registrationCode(err error) string {
	if errors.Is(err, ErrRegistrationClosed) {
		return RegistrationCodeClosed
	}
	return RegistrationCodeInvitationRequired
}
