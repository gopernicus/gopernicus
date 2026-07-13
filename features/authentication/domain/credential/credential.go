// Package credential is the credential-mutation policy and optimistic-mutation
// domain (design §5.6). It replaces the rejected scalar authentication-method
// count with a typed MethodSet a policy reasons over, a closed set of typed
// Mutation variants, and a revision-CAS repository contract that serializes
// concurrent self-removal through users.auth_revision without running the policy
// inside SQL.
//
// The domain is deliberately projection-shaped: MethodSet is the typed view of a
// user's password, OAuth links, and identifiers that both the policy evaluator
// and the /auth/methods read surface consume — not the persistence aggregate
// (identifiers own their table in the identifier domain). MethodSet.With computes
// the post-mutation projection so a service can evaluate a proposed state before
// the store applies it, and the store's MutationRepository.Apply performs the
// same typed mutation atomically under the expected revision.
package credential

import (
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// IdentifierUses records the roles a verified identifier serves (design §2.3,
// §5.6). One identifier may serve several. The policy reasons over these to
// decide whether a mutation leaves a direct login method and a verified recovery
// method standing; the persistence entity and its normalization are the
// identifier domain's concern (phase 1), so this projection carries only the
// role flags the policy needs.
type IdentifierUses struct {
	Login        bool
	Recovery     bool
	Notification bool
}

// OAuthMethod is one linked OAuth/OIDC provider in a MethodSet. Ordinary v3 OAuth
// is a direct login method at AAL1 (design §12.1); Assurance carries the honest
// level so a stronger host policy can reason over it.
type OAuthMethod struct {
	Provider  string
	Assurance session.AssuranceLevel
}

// IdentifierMethod is one identifier in a MethodSet. Kind is an identity.Kind
// string (identity.KindEmail / identity.KindPhone); Uses records the roles it
// serves; Verified reports whether the value is proven; Primary marks the one
// primary identifier of its kind. An unverified identifier is neither a login nor
// a recovery method.
type IdentifierMethod struct {
	ID       string
	Kind     string
	Uses     IdentifierUses
	Verified bool
	Primary  bool
}

// loginDescriptor maps a verified identifier to its honest authentication-method
// descriptor (design §5.6): email → one-time code, phone → SMS OTP (PSTN). The
// second result is false for an unknown kind, so an unrecognized identifier never
// silently counts as a login or recovery method.
func (m IdentifierMethod) loginDescriptor() (session.AuthenticationMethod, bool) {
	switch m.Kind {
	case identity.KindEmail:
		return session.DescribeMethod(session.MethodEmailCode)
	case identity.KindPhone:
		return session.DescribeMethod(session.MethodSMSCode)
	default:
		return session.AuthenticationMethod{}, false
	}
}

// MethodSet is the typed inventory a CredentialPolicy evaluates and a
// MutationRepository snapshots (design §5.6). AuthRevision is the optimistic
// serialization token the store increments on every applied mutation; a Snapshot
// carries the revision it was read at so Apply can reject a stale mutation.
type MethodSet struct {
	AuthRevision int64
	HasPassword  bool
	OAuth        []OAuthMethod
	Identifiers  []IdentifierMethod
}

// LoginMethods returns the honest descriptors of every direct login method in the
// set: the password, each OAuth link, and each verified login-enabled identifier.
// The policy reasons over these descriptors rather than a scalar count.
func (s MethodSet) LoginMethods() []session.AuthenticationMethod {
	var out []session.AuthenticationMethod
	if s.HasPassword {
		if d, ok := session.DescribeMethod(session.MethodPassword); ok {
			out = append(out, d)
		}
	}
	for range s.OAuth {
		if d, ok := session.DescribeMethod(session.MethodOAuth); ok {
			out = append(out, d)
		}
	}
	for _, id := range s.Identifiers {
		if id.Verified && id.Uses.Login {
			if d, ok := id.loginDescriptor(); ok {
				out = append(out, d)
			}
		}
	}
	return out
}

// VerifiedRecoveryMethods returns the honest descriptors of every verified
// recovery identifier in the set. PSTN rides each descriptor so a policy can mark
// SMS restricted rather than treating it as a strong recovery channel.
func (s MethodSet) VerifiedRecoveryMethods() []session.AuthenticationMethod {
	var out []session.AuthenticationMethod
	for _, id := range s.Identifiers {
		if id.Verified && id.Uses.Recovery {
			if d, ok := id.loginDescriptor(); ok {
				out = append(out, d)
			}
		}
	}
	return out
}

// With returns the post-mutation projection of the set, leaving the receiver
// unchanged (a fresh backing array is built for any mutated slice). A service
// computes proposed := current.With(m) to evaluate policy before the store's
// Apply performs the same typed mutation; AuthRevision is the store's concern and
// is copied verbatim.
func (s MethodSet) With(m Mutation) MethodSet {
	switch mut := m.(type) {
	case RemovePassword:
		s.HasPassword = false
	case UnlinkOAuth:
		s.OAuth = removeOAuth(s.OAuth, mut.Provider)
	case RetireIdentifier:
		s.Identifiers = retireIdentifier(s.Identifiers, mut.IdentifierID, mut.ReplacementPrimaryID)
	case ChangeIdentifierUses:
		s.Identifiers = changeIdentifierUses(s.Identifiers, mut.IdentifierID, mut.Uses, mut.MakePrimary)
	}
	return s
}

func removeOAuth(links []OAuthMethod, provider string) []OAuthMethod {
	out := make([]OAuthMethod, 0, len(links))
	for _, l := range links {
		if l.Provider != provider {
			out = append(out, l)
		}
	}
	return out
}

func retireIdentifier(ids []IdentifierMethod, id, replacementPrimaryID string) []IdentifierMethod {
	out := make([]IdentifierMethod, 0, len(ids))
	for _, m := range ids {
		if m.ID == id {
			continue
		}
		if m.ID == replacementPrimaryID {
			m.Primary = true
		}
		out = append(out, m)
	}
	return out
}

func changeIdentifierUses(ids []IdentifierMethod, id string, uses IdentifierUses, makePrimary bool) []IdentifierMethod {
	out := make([]IdentifierMethod, 0, len(ids))
	for _, m := range ids {
		if m.ID == id {
			m.Uses = uses
			if makePrimary {
				m.Primary = true
			}
		} else if makePrimary {
			// A single primary per set: promoting one demotes the rest.
			m.Primary = false
		}
		out = append(out, m)
	}
	return out
}

// Mutation is the closed v3 set of typed credential/identifier mutations a
// revision-CAS Apply performs (design §5.6). It is a sealed sum type: only this
// package defines variants (the unexported isMutation marker seals it), so a
// store's Apply switches exhaustively on the concrete type with no open default,
// and the typed tables are coordinated without a uniform credential storage
// model.
type Mutation interface {
	isMutation()
}

// RemovePassword deletes the user's password credential (design §5.3).
type RemovePassword struct{}

// UnlinkOAuth removes the link to Provider (design §5.4).
type UnlinkOAuth struct {
	Provider string
}

// RetireIdentifier retires the identifier IdentifierID (design §5.5). When the
// retired identifier was primary, ReplacementPrimaryID names the identifier
// promoted to primary in the same atomic operation; it is empty when the retired
// identifier was not primary.
type RetireIdentifier struct {
	IdentifierID         string
	ReplacementPrimaryID string
}

// ChangeIdentifierUses sets the identifier's uses and optionally promotes it to
// primary (design §5.5).
type ChangeIdentifierUses struct {
	IdentifierID string
	Uses         IdentifierUses
	MakePrimary  bool
}

func (RemovePassword) isMutation()       {}
func (UnlinkOAuth) isMutation()          {}
func (RetireIdentifier) isMutation()     {}
func (ChangeIdentifierUses) isMutation() {}
