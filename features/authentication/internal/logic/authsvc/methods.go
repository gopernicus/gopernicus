package authsvc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/sdk"
)

// ErrCredentialInventoryUnavailable is returned by Methods when the credential-
// mutation rail is not wired (nil CredentialMutations). The masked method
// inventory reads its authoritative projection from that repository's Snapshot
// (design §5.1/§5.6), so with it absent the read fails CLOSED rather than
// fabricating a partial inventory. Wraps sdk.ErrForbidden (→ 403).
var ErrCredentialInventoryUnavailable = fmt.Errorf("credential inventory subsystem not wired: %w", sdk.ErrForbidden)

// MethodsView is the masked credential/identifier inventory the live-session-gated
// GET /auth/methods returns (design §5.1). It is the single read surface that
// replaced the original's has_password + /auth/oauth/linked round trips: password
// presence, typed OAuth methods, and the user's active identifiers with their
// uses, primary flag, and proof time. Identifier values are MASKED here — a full
// value is only ever produced by a separate, explicitly authorized service method,
// never by a query flag accepted from HTTP. Each removable hint is policy-derived
// (design §5.6) and ADVISORY: the server-side guard on the mutation stays
// authoritative (the read is TOCTOU).
type MethodsView struct {
	HasPassword bool
	OAuth       []OAuthMethodView
	Identifiers []IdentifierMethodView
}

// OAuthMethodView is one linked provider in the inventory: the provider name, when
// it was linked, its honest assurance, and the advisory removable hint.
type OAuthMethodView struct {
	Provider  string
	LinkedAt  time.Time
	Assurance string
	Removable bool
}

// IdentifierMethodView is one active identifier in the inventory. MaskedValue is
// the redacted address (never the full value); VerifiedAt is the proof time (zero
// → unverified); Uses records the roles it serves; Primary marks the primary of
// its kind; Removable is the advisory policy hint.
type IdentifierMethodView struct {
	ID          string
	Kind        string
	MaskedValue string
	VerifiedAt  time.Time
	Uses        credential.IdentifierUses
	Primary     bool
	Removable   bool
}

// Methods builds the masked method inventory for userID (design §5.1). The
// authoritative projection is the credential-mutation Snapshot — the same typed
// MethodSet the policy evaluates and the mutation rail serializes — so the read
// and the guarding mutations never disagree about what a user has. It then
// enriches each entry with display-only data the projection does not carry: the
// masked identifier value and proof time (from the identifier rail) and the OAuth
// link time. Every removable hint is computed by evaluating the credential policy
// against the post-removal projection; the hint is advisory, and the mutation's
// own guard re-runs the policy under revision serialization.
//
// It fails CLOSED when the credential rail is unwired. Replaced identifier rows
// never appear (Snapshot and ListByUser both project active rows only).
func (s *Service) Methods(ctx context.Context, userID string) (MethodsView, error) {
	if s.credentialMutations == nil {
		return MethodsView{}, ErrCredentialInventoryUnavailable
	}
	snap, err := s.credentialMutations.Snapshot(ctx, userID)
	if err != nil {
		return MethodsView{}, err
	}

	// Enrich identifiers with their masked value and proof time — the projection
	// carries neither (it is policy-shaped). Active rows only; a replaced row is
	// absent from ListByUser just as it is from Snapshot.
	details := make(map[string]identifier.Identifier)
	if s.identifiers != nil {
		idents, err := s.identifiers.ListByUser(ctx, userID)
		if err != nil {
			return MethodsView{}, err
		}
		for _, it := range idents {
			details[it.ID] = it
		}
	}

	// Enrich OAuth links with their link time — the projection carries only
	// provider + assurance.
	linkedAt := make(map[string]time.Time)
	if s.oauthAccounts != nil {
		accts, err := s.oauthAccounts.ListByUser(ctx, userID)
		if err != nil {
			return MethodsView{}, err
		}
		for _, a := range accts {
			linkedAt[a.Provider] = a.LinkedAt
		}
	}

	view := MethodsView{HasPassword: snap.HasPassword}
	for _, o := range snap.OAuth {
		view.OAuth = append(view.OAuth, OAuthMethodView{
			Provider:  o.Provider,
			LinkedAt:  linkedAt[o.Provider],
			Assurance: string(o.Assurance),
			Removable: s.removable(ctx, snap, credential.UnlinkOAuth{Provider: o.Provider}),
		})
	}
	for _, m := range snap.Identifiers {
		entry := IdentifierMethodView{
			ID:        m.ID,
			Kind:      m.Kind,
			Uses:      m.Uses,
			Primary:   m.Primary,
			Removable: s.removable(ctx, snap, credential.RetireIdentifier{IdentifierID: m.ID}),
		}
		if it, ok := details[m.ID]; ok {
			entry.MaskedValue = maskIdentifier(it.Kind, it.NormalizedValue)
			entry.VerifiedAt = it.VerifiedAt
		}
		view.Identifiers = append(view.Identifiers, entry)
	}
	return view, nil
}

// removable reports the advisory policy hint for removing a method: it evaluates
// the credential policy against the projection that would result from applying
// mutation. A nil policy error means the removal would leave a safe method set, so
// the host may offer the removal UI; the mutation itself re-runs the same policy
// under revision serialization (the read is advisory — design §5.6).
func (s *Service) removable(ctx context.Context, current credential.MethodSet, m credential.Mutation) bool {
	return s.credentialPolicy.EvaluateMutation(ctx, current, current.With(m)) == nil
}

// maskIdentifier redacts an identifier value for the default method inventory
// (design §5.1). Email keeps the first local-part rune and the domain; phone keeps
// the last four digits; an unrecognized kind is fully masked. The masking is
// deliberately conservative: enough for the owner to recognize which address it is
// without exposing the full value to a screen capture or shoulder surfer.
func maskIdentifier(kind identifier.Kind, value string) string {
	switch kind {
	case identifier.KindEmail:
		return maskEmail(value)
	case identifier.KindPhone:
		return maskPhone(value)
	default:
		return maskAll
	}
}

// maskAll is the fully redacted placeholder for a value nothing may be revealed of.
const maskAll = "***"

// maskEmail reveals the first rune of the local part and the whole domain
// (a***@example.com); a single-rune or empty local part is fully masked.
func maskEmail(value string) string {
	at := strings.LastIndexByte(value, '@')
	if at <= 0 {
		return maskAll
	}
	local, domain := value[:at], value[at:]
	r := []rune(local)
	if len(r) <= 1 {
		return maskAll + domain
	}
	return string(r[0]) + maskAll + domain
}

// maskPhone reveals only the last four digits (***4567); fewer than four digits is
// fully masked so a short value never round-trips whole.
func maskPhone(value string) string {
	digits := make([]rune, 0, len(value))
	for _, r := range value {
		if r >= '0' && r <= '9' {
			digits = append(digits, r)
		}
	}
	if len(digits) < 4 {
		return maskAll
	}
	return maskAll + string(digits[len(digits)-4:])
}
