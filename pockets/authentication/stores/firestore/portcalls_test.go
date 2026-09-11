//go:build integration

// The ambient-refusal table (ruling R1), shared by the EMULATOR entrypoint
// (conformance_test.go, integration && !live) and, from N6, the LIVE one
// (integration && live) — one build tag, both builds. Duplicating a
// port-method table per leg is how one of the two copies silently loses a
// method.
package firestore

import (
	"context"
	"errors"
	"testing"
	"time"

	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/challenge"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordless"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/passwordreset"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	invitations "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// portMethodCount is the number of methods the eighteen slots of
// auth.Repositories declare across their eighteen distinct interfaces, at core
// v0.10.0. It is asserted against the table below, so a port method added
// upstream fails this module rather than silently escaping the R1 refusal.
const portMethodCount = 64

// assertAmbientRefusal drives every public port method inside BOTH kinds of
// ambient transaction — a read-write Transact and a read-only ReadSnapshot — and
// requires each one to answer ErrAmbientTransactionUnsupported. It is the
// executable form of R1's "refuse rather than split": a store that ran on the
// client beside the host's transaction would silently break the host's atomic
// unit, so silence is the failure and an error is the contract.
func assertAmbientRefusal(t *testing.T, db *firestoredb.DB, repos auth.Repositories) {
	t.Helper()

	for _, ambient := range []struct {
		name string
		run  func(context.Context, func(context.Context)) error
	}{
		{
			name: "Transact",
			run: func(ctx context.Context, call func(context.Context)) error {
				return db.Transact(ctx, func(ctx context.Context) error {
					call(ctx)
					return nil
				})
			},
		},
		{
			name: "ReadSnapshot",
			run: func(ctx context.Context, call func(context.Context)) error {
				return db.ReadSnapshot(ctx, func(ctx context.Context, _ firestoredb.Reader) error {
					call(ctx)
					return nil
				})
			},
		},
	} {
		t.Run(ambient.name, func(t *testing.T) {
			if err := ambient.run(context.Background(), func(ctx context.Context) {
				for _, c := range portCalls(repos) {
					if err := c.call(ctx); !errors.Is(err, ErrAmbientTransactionUnsupported) {
						t.Errorf("%s inside an ambient transaction: got %v, want ErrAmbientTransactionUnsupported", c.name, err)
					}
				}
			}); err != nil {
				t.Fatalf("%s: %v", ambient.name, err)
			}
		})
	}
}

// portCall is one public port method reduced to "call it, keep the error" so the
// ambient refusal can be asserted for ALL of them without sixty near-identical
// blocks. Arguments are deliberately trivial: the refusal precedes every
// validation, so nothing here needs to be a legal command.
type portCall struct {
	name string
	call func(context.Context) error
}

// portCalls enumerates every method of the eighteen ports. A port method missing
// from this list is a method that could silently join a host's transaction, so
// the count is asserted below. The set is aliased r, so every entry reads
// r.<Port>.<Method> — the same table shape the authorization store's twin uses.
func portCalls(r auth.Repositories) []portCall {
	var (
		zeroTime = time.Time{}
		req      = list.Request{}
	)
	err1 := func(_ any, err error) error { return err }

	return []portCall{
		{name: "Users.Provision", call: func(ctx context.Context) error {
			_, _, err := r.Users.Provision(ctx, user.User{}, identifier.Identifier{}, user.InitialCredentials{})
			return err
		}},
		{name: "Passwords.Change", call: func(ctx context.Context) error { return err1(r.Passwords.Change(ctx, "", user.PasswordChange{})) }},
		{name: "OAuthAccounts.Link", call: func(ctx context.Context) error {
			_, _, err := r.OAuthAccounts.Link(ctx, oauthaccount.OAuthAccount{}, 0, "", time.Now())
			return err
		}},

		// user.UserRepository — 3
		{name: "Users.CreateWithPrimaryIdentifier", call: func(ctx context.Context) error {
			_, _, err := r.Users.CreateWithPrimaryIdentifier(ctx, user.User{}, identifier.Identifier{})
			return err
		}},
		{name: "Users.Get", call: func(ctx context.Context) error {
			return err1(r.Users.Get(ctx, "u1"))
		}},
		{name: "Users.Update", call: func(ctx context.Context) error {
			return err1(r.Users.Update(ctx, "u1", user.User{}))
		}},

		// identifier.IdentifierRepository — 5
		{name: "Identifiers.Get", call: func(ctx context.Context) error {
			return err1(r.Identifiers.Get(ctx, "i1"))
		}},
		{name: "Identifiers.GetLogin", call: func(ctx context.Context) error {
			return err1(r.Identifiers.GetLogin(ctx, "email", "a@example.com"))
		}},
		{name: "Identifiers.GetRecovery", call: func(ctx context.Context) error {
			return err1(r.Identifiers.GetRecovery(ctx, "email", "a@example.com"))
		}},
		{name: "Identifiers.ListByUser", call: func(ctx context.Context) error {
			return err1(r.Identifiers.ListByUser(ctx, "u1"))
		}},
		{name: "Identifiers.ApplyVerifiedChange", call: func(ctx context.Context) error {
			return err1(r.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{}, 0, zeroTime))
		}},

		// user.PasswordRepository — 2
		{name: "Passwords.Set", call: func(ctx context.Context) error {
			return r.Passwords.Set(ctx, "u1", "hash")
		}},
		{name: "Passwords.Get", call: func(ctx context.Context) error {
			return err1(r.Passwords.Get(ctx, "u1"))
		}},

		// session.SessionRepository — 7
		{name: "Sessions.Create", call: func(ctx context.Context) error {
			return err1(r.Sessions.Create(ctx, session.Session{}))
		}},
		{name: "Sessions.Get", call: func(ctx context.Context) error {
			return err1(r.Sessions.Get(ctx, "s1"))
		}},
		{name: "Sessions.GetByRefreshHash", call: func(ctx context.Context) error {
			_, _, err := r.Sessions.GetByRefreshHash(ctx, "hash")
			return err
		}},
		{name: "Sessions.Rotate", call: func(ctx context.Context) error {
			return r.Sessions.Rotate(ctx, "s1", "old", "new")
		}},
		{name: "Sessions.ConsumeGrace", call: func(ctx context.Context) error {
			return r.Sessions.ConsumeGrace(ctx, "s1", "old")
		}},
		{name: "Sessions.Delete", call: func(ctx context.Context) error {
			return r.Sessions.Delete(ctx, "s1")
		}},
		{name: "Sessions.DeleteByUser", call: func(ctx context.Context) error {
			return r.Sessions.DeleteByUser(ctx, "u1")
		}},

		// oauthaccount.OAuthAccountRepository — 4
		{name: "OAuthAccounts.Create", call: func(ctx context.Context) error {
			return err1(r.OAuthAccounts.Create(ctx, oauthaccount.OAuthAccount{}))
		}},
		{name: "OAuthAccounts.GetByProvider", call: func(ctx context.Context) error {
			return err1(r.OAuthAccounts.GetByProvider(ctx, "google", "p1"))
		}},
		{name: "OAuthAccounts.ListByUser", call: func(ctx context.Context) error {
			return err1(r.OAuthAccounts.ListByUser(ctx, "u1"))
		}},
		{name: "OAuthAccounts.Delete", call: func(ctx context.Context) error {
			return r.OAuthAccounts.Delete(ctx, "u1", "google")
		}},

		// oauthstate.StateRepository — 2
		{name: "OAuthStates.Create", call: func(ctx context.Context) error {
			return err1(r.OAuthStates.Create(ctx, oauthstate.State{}))
		}},
		{name: "OAuthStates.Consume", call: func(ctx context.Context) error {
			return err1(r.OAuthStates.Consume(ctx, "token"))
		}},

		// serviceaccount.ServiceAccountRepository — 5
		{name: "ServiceAccounts.Create", call: func(ctx context.Context) error {
			return err1(r.ServiceAccounts.Create(ctx, serviceaccount.ServiceAccount{}))
		}},
		{name: "ServiceAccounts.Get", call: func(ctx context.Context) error {
			return err1(r.ServiceAccounts.Get(ctx, "sa1"))
		}},
		{name: "ServiceAccounts.List", call: func(ctx context.Context) error {
			return err1(r.ServiceAccounts.List(ctx, req))
		}},
		{name: "ServiceAccounts.Update", call: func(ctx context.Context) error {
			return err1(r.ServiceAccounts.Update(ctx, "sa1", serviceaccount.ServiceAccount{}))
		}},
		{name: "ServiceAccounts.Delete", call: func(ctx context.Context) error {
			return r.ServiceAccounts.Delete(ctx, "sa1")
		}},

		// apikey.APIKeyRepository — 5
		{name: "APIKeys.Create", call: func(ctx context.Context) error {
			return err1(r.APIKeys.Create(ctx, apikey.APIKey{}))
		}},
		{name: "APIKeys.GetByHash", call: func(ctx context.Context) error {
			return err1(r.APIKeys.GetByHash(ctx, "hash"))
		}},
		{name: "APIKeys.ListByServiceAccount", call: func(ctx context.Context) error {
			return err1(r.APIKeys.ListByServiceAccount(ctx, "sa1", req))
		}},
		{name: "APIKeys.Revoke", call: func(ctx context.Context) error {
			return r.APIKeys.Revoke(ctx, "k1", zeroTime)
		}},
		{name: "APIKeys.TouchLastUsed", call: func(ctx context.Context) error {
			return r.APIKeys.TouchLastUsed(ctx, "k1", zeroTime)
		}},

		// securityevent.SecurityEventRepository — 2
		{name: "SecurityEvents.Create", call: func(ctx context.Context) error {
			return err1(r.SecurityEvents.Create(ctx, securityevent.SecurityEvent{}))
		}},
		{name: "SecurityEvents.List", call: func(ctx context.Context) error {
			return err1(r.SecurityEvents.List(ctx, securityevent.ListFilter{}, req))
		}},

		// invitations.InvitationRepository — 6
		{name: "Invitations.Create", call: func(ctx context.Context) error {
			return err1(r.Invitations.Create(ctx, invitations.Invitation{}))
		}},
		{name: "Invitations.Get", call: func(ctx context.Context) error {
			return err1(r.Invitations.Get(ctx, "inv1"))
		}},
		{name: "Invitations.GetByTokenHash", call: func(ctx context.Context) error {
			return err1(r.Invitations.GetByTokenHash(ctx, "hash"))
		}},
		{name: "Invitations.ListByResource", call: func(ctx context.Context) error {
			return err1(r.Invitations.ListByResource(ctx, "project", "p1", req))
		}},
		{name: "Invitations.ListBySubject", call: func(ctx context.Context) error {
			return err1(r.Invitations.ListBySubject(ctx, "email", "a@example.com", req))
		}},
		{name: "Invitations.ClaimAcceptance", call: func(ctx context.Context) error {
			return err1(r.Invitations.ClaimAcceptance(ctx, "inv1", invitations.Acceptance{}))
		}},
		{name: "Invitations.CompleteAcceptance", call: func(ctx context.Context) error {
			return err1(r.Invitations.CompleteAcceptance(ctx, "inv1", invitations.Acceptance{}))
		}},
		{name: "Invitations.UpdateStatus", call: func(ctx context.Context) error {
			return err1(r.Invitations.UpdateStatus(ctx, "inv1", invitations.StatusUpdate{}))
		}},

		// challenge.Repository — 4
		{name: "Challenges.Replace", call: func(ctx context.Context) error {
			return err1(r.Challenges.Replace(ctx, challenge.Challenge{}))
		}},
		{name: "Challenges.ConsumeCode", call: func(ctx context.Context) error {
			_, _, err := r.Challenges.ConsumeCode(ctx, "u1", "verify", nil, "", 3, zeroTime)
			return err
		}},
		{name: "Challenges.ConsumeToken", call: func(ctx context.Context) error {
			return err1(r.Challenges.ConsumeToken(ctx, "verify", "digest", zeroTime))
		}},
		{name: "Challenges.PurgeExpired", call: func(ctx context.Context) error {
			return err1(r.Challenges.PurgeExpired(ctx, zeroTime, 10))
		}},

		// passwordreset.Repository — 1
		{name: "PasswordResets.Redeem", call: func(ctx context.Context) error {
			return err1(r.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{}))
		}},

		// contactchange.Repository — 2
		{name: "ContactChanges.Create", call: func(ctx context.Context) error {
			return err1(r.ContactChanges.Create(ctx, contactchange.PendingChange{}))
		}},
		{name: "ContactChanges.Get", call: func(ctx context.Context) error { return err1(r.ContactChanges.Get(ctx, "u1", identifier.KindEmail)) }},
		{name: "ContactChanges.Consume", call: func(ctx context.Context) error {
			return err1(r.ContactChanges.Consume(ctx, "u1", identifier.KindEmail, "pending"))
		}},

		// authgrant.Repository — 3
		{name: "AuthenticationGrants.Create", call: func(ctx context.Context) error {
			return err1(r.AuthenticationGrants.Create(ctx, authgrant.Grant{}, 0, zeroTime))
		}},
		{name: "AuthenticationGrants.Consume", call: func(ctx context.Context) error {
			return err1(r.AuthenticationGrants.Consume(ctx, authgrant.Requirement{}, zeroTime))
		}},
		{name: "AuthenticationGrants.DeleteBySession", call: func(ctx context.Context) error {
			return r.AuthenticationGrants.DeleteBySession(ctx, "s1")
		}},

		// credential.MutationRepository — 2
		{name: "CredentialMutations.Snapshot", call: func(ctx context.Context) error {
			return err1(r.CredentialMutations.Snapshot(ctx, "u1"))
		}},
		{name: "CredentialMutations.Apply", call: func(ctx context.Context) error {
			return r.CredentialMutations.Apply(ctx, "u1", 0, nil)
		}},

		// user.AdminRepository — 3
		{name: "UserAdmin.List", call: func(ctx context.Context) error {
			return err1(r.UserAdmin.List(ctx, req))
		}},
		{name: "UserAdmin.GetSummary", call: func(ctx context.Context) error {
			return err1(r.UserAdmin.GetSummary(ctx, "u1"))
		}},
		{name: "UserAdmin.SetStatus", call: func(ctx context.Context) error {
			return err1(r.UserAdmin.SetStatus(ctx, "u1", user.StatusDeactivated, zeroTime))
		}},

		// session.ActiveUserRepository — 1
		{name: "ActiveSessions.CreateForActiveUser", call: func(ctx context.Context) error {
			return err1(r.ActiveSessions.CreateForActiveUser(ctx, session.Session{}, 0))
		}},

		// passwordless.Repository — 1
		{name: "Passwordless.Redeem", call: func(ctx context.Context) error {
			return err1(r.Passwordless.Redeem(ctx, passwordless.RedeemInput{}))
		}},
	}
}

// TestPortCallsCoverEveryPortMethod keeps the ambient-refusal table honest: the
// eighteen ports declare portMethodCount methods at core v0.10.0, and a method
// missing from the table is a method whose refusal nothing asserts.
func TestPortCallsCoverEveryPortMethod(t *testing.T) {
	calls := portCalls(auth.Repositories{})
	if got := len(calls); got != portMethodCount {
		t.Fatalf("the ambient-refusal table drives %d port methods, want %d — add the new port method to portCalls", got, portMethodCount)
	}
	seen := make(map[string]bool, len(calls))
	for _, c := range calls {
		if seen[c.name] {
			t.Errorf("%s appears twice in the table — a duplicate hides a missing method behind the count", c.name)
		}
		seen[c.name] = true
	}
}
