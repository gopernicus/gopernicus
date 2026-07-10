// Package storetest is the conformance suite for the auth feature's repository
// ports: every store that fills an auth.Repositories — the in-package reference
// implementation, a host memstore, and each dialect adapter (features/authentication/
// stores/turso, .../postgres) — should pass Run against a freshly wired,
// isolated Repositories. The port doc comments are the spec; this suite is their
// executable form.
//
// It imports stdlib + sdk + the auth feature's own packages only (guard G2
// forbids a driver import here), so features/authentication's own `go test ./...` runs it
// against the reference implementation (see reference_test.go).
//
// The machine-identity ports (serviceaccount, apikey) are the first paged ports
// to land (design §4.1/§9): the ServiceAccounts.List and
// APIKeys.ListByServiceAccount sub-runners assert cursor pagination AND the
// pinned ORDER BY created_at DESC, id DESC tiebreak — including a same-created_at
// collision case that proves identical order and NextCursor across
// implementations. The key/email-lookup ports (users, sessions, codes, tokens,
// oauth) instead exercise the sentinel contract, uniqueness, upsert, and
// expired-at-read behavior.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/features/authentication/domain/verification"
	"github.com/gopernicus/gopernicus/sdk/crud"
	"github.com/gopernicus/gopernicus/sdk/cryptids"
	"github.com/gopernicus/gopernicus/sdk/errs"
	"github.com/gopernicus/gopernicus/sdk/identity"
)

// ids is the suite's entity-ID generator: the default nanoid strategy, matching
// the feature's zero-value Config.IDs.
var ids = cryptids.IDGenerator{}

// dbIDs is the cryptids.Database strategy: entities reach Create with an empty
// ID and the store must assign the database-generated key (amended D10).
var dbIDs = cryptids.NewGenerator(cryptids.Database)

// suiteBase is a fixed reference instant. Expiry cases offset from time.Now so
// the reference impl's real-clock expiry check observes the intended state.
var suiteBase = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// Run exercises the auth.Repositories contract against a clean, isolated set
// obtained from newRepos for each leaf subtest. newRepos MUST return a CLEAN,
// isolated Repositories per call: SQL-backed harnesses truncate their tables;
// memory harnesses return a fresh instance.
func Run(t *testing.T, newRepos func(t *testing.T) auth.Repositories) {
	t.Helper()

	t.Run("Users", func(t *testing.T) {
		t.Run("CRUDRoundTrip", func(t *testing.T) { testUsersCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testUsersAbsent(t, newRepos(t)) })
		t.Run("EmailUniqueness", func(t *testing.T) { testUsersEmailUniqueness(t, newRepos(t)) })
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testUsersDBGeneratedID(t, newRepos(t)) })
	})

	t.Run("Passwords", func(t *testing.T) {
		t.Run("SetGetUpsert", func(t *testing.T) { testPasswords(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testPasswordsAbsent(t, newRepos(t)) })
	})

	t.Run("Sessions", func(t *testing.T) {
		t.Run("CreateGetDelete", func(t *testing.T) { testSessionsCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testSessionsAbsent(t, newRepos(t)) })
		t.Run("ExpiredAtRead", func(t *testing.T) { testSessionsExpired(t, newRepos(t)) })
		t.Run("DeleteByUser", func(t *testing.T) { testSessionsDeleteByUser(t, newRepos(t)) })
	})

	t.Run("VerificationCodes", func(t *testing.T) {
		t.Run("CreateGetDelete", func(t *testing.T) { testCodesCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testCodesAbsent(t, newRepos(t)) })
		t.Run("ExpiredAtRead", func(t *testing.T) { testCodesExpired(t, newRepos(t)) })
	})

	t.Run("VerificationTokens", func(t *testing.T) {
		t.Run("CreateGetDelete", func(t *testing.T) { testTokensCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testTokensAbsent(t, newRepos(t)) })
		t.Run("ExpiredAtRead", func(t *testing.T) { testTokensExpired(t, newRepos(t)) })
	})

	// OAuth ports are optional (a host that wires no providers leaves them nil).
	// When present they are exercised in full; when absent the group skips
	// LOUDLY — a silent green would claim OAuth conformance nothing verified.
	t.Run("OAuthAccounts", func(t *testing.T) {
		if newRepos(t).OAuthAccounts == nil {
			t.Skip("OAuthAccounts not wired — OAuth account conformance NOT verified for this Repositories")
		}
		t.Run("CRUDRoundTrip", func(t *testing.T) { testOAuthAccountsCRUD(t, newRepos(t)) })
		t.Run("ProviderUniqueness", func(t *testing.T) { testOAuthAccountsUniqueness(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testOAuthAccountsAbsent(t, newRepos(t)) })
		t.Run("ListByUser", func(t *testing.T) { testOAuthAccountsListByUser(t, newRepos(t)) })
		t.Run("DeleteAbsentNotFound", func(t *testing.T) { testOAuthAccountsDeleteAbsent(t, newRepos(t)) })
	})

	t.Run("OAuthStates", func(t *testing.T) {
		if newRepos(t).OAuthStates == nil {
			t.Skip("OAuthStates not wired — OAuth state conformance NOT verified for this Repositories")
		}
		t.Run("ConsumeSingleUse", func(t *testing.T) { testOAuthStatesConsume(t, newRepos(t)) })
		t.Run("ConsumeExpiredDeletes", func(t *testing.T) { testOAuthStatesExpired(t, newRepos(t)) })
		t.Run("ConsumeUnknown", func(t *testing.T) { testOAuthStatesUnknown(t, newRepos(t)) })
	})

	// Machine-identity ports are optional (both-or-neither; a host that wires no
	// machine repos leaves them nil). When present they are exercised in full;
	// when absent the group skips LOUDLY.
	t.Run("ServiceAccounts", func(t *testing.T) {
		if newRepos(t).ServiceAccounts == nil {
			t.Skip("ServiceAccounts not wired — service-account conformance NOT verified for this Repositories")
		}
		t.Run("CRUDRoundTrip", func(t *testing.T) { testServiceAccountsCRUD(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testServiceAccountsAbsent(t, newRepos(t)) })
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testServiceAccountsDBGeneratedID(t, newRepos(t)) })
		t.Run("ListOrderingPagination", func(t *testing.T) { testServiceAccountsListPaged(t, newRepos(t)) })
		t.Run("ListSameCreatedAtCollision", func(t *testing.T) { testServiceAccountsListCollision(t, newRepos(t)) })
		runPagedFamily(t, newRepos,
			func(repos auth.Repositories, ctx context.Context, req crud.ListRequest) (crud.Page[serviceaccount.ServiceAccount], error) {
				return repos.ServiceAccounts.List(ctx, req)
			},
			func(t *testing.T, repos auth.Repositories) ([]serviceaccount.ServiceAccount, int) {
				created := seedServiceAccounts(t, repos.ServiceAccounts)
				return created, len(created)
			},
			func(sa serviceaccount.ServiceAccount) string { return sa.ID },
			func(sa serviceaccount.ServiceAccount) time.Time { return sa.CreatedAt },
		)
	})

	t.Run("APIKeys", func(t *testing.T) {
		if newRepos(t).APIKeys == nil {
			t.Skip("APIKeys not wired — API-key conformance NOT verified for this Repositories")
		}
		t.Run("GetByHashUnknown", func(t *testing.T) { testAPIKeysGetByHashUnknown(t, newRepos(t)) })
		t.Run("GetByHashValidNullExpiry", func(t *testing.T) { testAPIKeysGetByHashValid(t, newRepos(t)) })
		t.Run("GetByHashRevokedReturnsRecord", func(t *testing.T) { testAPIKeysGetByHashRevoked(t, newRepos(t)) })
		t.Run("GetByHashExpiredReturnsRecord", func(t *testing.T) { testAPIKeysGetByHashExpired(t, newRepos(t)) })
		t.Run("MintUniqueness", func(t *testing.T) { testAPIKeysMintUniqueness(t, newRepos(t)) })
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testAPIKeysDBGeneratedID(t, newRepos(t)) })
		t.Run("TouchLastUsed", func(t *testing.T) { testAPIKeysTouchLastUsed(t, newRepos(t)) })
		t.Run("RevokeAbsentNotFound", func(t *testing.T) { testAPIKeysRevokeAbsent(t, newRepos(t)) })
		t.Run("ListOrderingPagination", func(t *testing.T) { testAPIKeysListPaged(t, newRepos(t)) })
		t.Run("ListSameCreatedAtCollision", func(t *testing.T) { testAPIKeysListCollision(t, newRepos(t)) })
		runPagedFamily(t, newRepos,
			func(repos auth.Repositories, ctx context.Context, req crud.ListRequest) (crud.Page[apikey.APIKey], error) {
				return repos.APIKeys.ListByServiceAccount(ctx, "sa-seed", req)
			},
			func(t *testing.T, repos auth.Repositories) ([]apikey.APIKey, int) {
				return seedAPIKeys(t, repos.APIKeys)
			},
			func(k apikey.APIKey) string { return k.ID },
			func(k apikey.APIKey) time.Time { return k.CreatedAt },
		)
	})

	// SecurityEvents is optional and independent (ratified AV9): a host that
	// wires no audit rail leaves it nil. When present it is exercised in full;
	// when absent the group skips LOUDLY — a silent green would falsely claim
	// audit-rail conformance.
	t.Run("SecurityEvents", func(t *testing.T) {
		if newRepos(t).SecurityEvents == nil {
			t.Skip("SecurityEvents not wired — audit-rail conformance NOT verified for this Repositories")
		}
		t.Run("CreateAndListOrdering", func(t *testing.T) { testSecurityEventsCreateList(t, newRepos(t)) })
		t.Run("ListFilters", func(t *testing.T) { testSecurityEventsFilters(t, newRepos(t)) })
		t.Run("ListPagination", func(t *testing.T) { testSecurityEventsListPaged(t, newRepos(t)) })
		t.Run("ListSameCreatedAtCollision", func(t *testing.T) { testSecurityEventsListCollision(t, newRepos(t)) })
		t.Run("DetailsRoundTrip", func(t *testing.T) { testSecurityEventsDetails(t, newRepos(t)) })
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testSecurityEventsDBGeneratedID(t, newRepos(t)) })
		runPagedFamily(t, newRepos,
			func(repos auth.Repositories, ctx context.Context, req crud.ListRequest) (crud.Page[securityevent.SecurityEvent], error) {
				return repos.SecurityEvents.List(ctx, securityevent.ListFilter{UserID: "u-seed"}, req)
			},
			func(t *testing.T, repos auth.Repositories) ([]securityevent.SecurityEvent, int) {
				return seedSecurityEvents(t, repos.SecurityEvents)
			},
			func(evt securityevent.SecurityEvent) string { return evt.ID },
			func(evt securityevent.SecurityEvent) time.Time { return evt.CreatedAt },
		)
	})

	// Invitations is optional (deny-by-absence: a host wiring no Granter leaves
	// it nil). When present it is exercised in full; when absent the group skips
	// LOUDLY — a silent green would falsely claim invitation conformance.
	t.Run("Invitations", func(t *testing.T) {
		if newRepos(t).Invitations == nil {
			t.Skip("Invitations not wired — invitation conformance NOT verified for this Repositories")
		}
		t.Run("CreateGetRoundTrip", func(t *testing.T) { testInvitationsCRUD(t, newRepos(t)) })
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testInvitationsDBGeneratedID(t, newRepos(t)) })
		t.Run("PartialPendingUniqueness", func(t *testing.T) { testInvitationsUniqueness(t, newRepos(t)) })
		t.Run("KindRoundTripVerbatim", func(t *testing.T) { testInvitationsKindRoundTrip(t, newRepos(t)) })
		t.Run("CrossKindCoexistence", func(t *testing.T) { testInvitationsCrossKindCoexistence(t, newRepos(t)) })
		t.Run("GetByTokenHashUnknown", func(t *testing.T) { testInvitationsTokenUnknown(t, newRepos(t)) })
		t.Run("GetByTokenHashExpired", func(t *testing.T) { testInvitationsTokenExpired(t, newRepos(t)) })
		t.Run("StatusTransitions", func(t *testing.T) { testInvitationsStatusTransitions(t, newRepos(t)) })
		t.Run("UpdateStatusAbsentNotFound", func(t *testing.T) { testInvitationsUpdateAbsent(t, newRepos(t)) })
		t.Run("ListByResourcePagination", func(t *testing.T) { testInvitationsListByResourcePaged(t, newRepos(t)) })
		t.Run("ListByResourceCollision", func(t *testing.T) { testInvitationsListByResourceCollision(t, newRepos(t)) })
		t.Run("ListBySubjectPagination", func(t *testing.T) { testInvitationsListBySubjectPaged(t, newRepos(t)) })
		t.Run("ListBySubjectCollision", func(t *testing.T) { testInvitationsListBySubjectCollision(t, newRepos(t)) })
		t.Run("ByResource", func(t *testing.T) {
			runPagedFamily(t, newRepos,
				func(repos auth.Repositories, ctx context.Context, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
					return repos.Invitations.ListByResource(ctx, "project", "res-seed", req)
				},
				func(t *testing.T, repos auth.Repositories) ([]invitation.Invitation, int) {
					return seedInvitationsByResource(t, repos.Invitations)
				},
				func(inv invitation.Invitation) string { return inv.ID },
				func(inv invitation.Invitation) time.Time { return inv.CreatedAt },
			)
		})
		t.Run("BySubject", func(t *testing.T) {
			runPagedFamily(t, newRepos,
				func(repos auth.Repositories, ctx context.Context, req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
					return repos.Invitations.ListBySubject(ctx, "subject@seed.example", req)
				},
				func(t *testing.T, repos auth.Repositories) ([]invitation.Invitation, int) {
					return seedInvitationsBySubject(t, repos.Invitations)
				},
				func(inv invitation.Invitation) string { return inv.ID },
				func(inv invitation.Invitation) time.Time { return inv.CreatedAt },
			)
		})
	})
}

// --- Users ---

func testUsersCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Users

	u, err := user.NewUser(ids, "Alice@Example.com", "Alice", suiteBase)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	created, err := repo.Create(ctx, u)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Email != "alice@example.com" {
		t.Errorf("Create did not persist normalized email: %q", created.Email)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.DisplayName != "Alice" {
		t.Fatalf("Get: name=%q err=%v", got.DisplayName, err)
	}
	byEmail, err := repo.GetByEmail(ctx, "alice@example.com")
	if err != nil || byEmail.ID != created.ID {
		t.Fatalf("GetByEmail: id=%q err=%v", byEmail.ID, err)
	}

	got.MarkVerified(suiteBase.Add(time.Minute))
	if _, err := repo.Update(ctx, got.ID, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reget, err := repo.Get(ctx, created.ID)
	if err != nil || !reget.EmailVerified {
		t.Fatalf("Get after Update: verified=%v err=%v", reget.EmailVerified, err)
	}
}

func testUsersAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Users

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if _, err := repo.GetByEmail(ctx, "ghost@example.com"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetByEmail(absent): err=%v, want ErrNotFound", err)
	}
	absent, _ := user.NewUser(ids, "ghost@example.com", "Ghost", suiteBase)
	if _, err := repo.Update(ctx, "nope", absent); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Update(absent): err=%v, want ErrNotFound", err)
	}
}

func testUsersEmailUniqueness(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Users

	a, _ := user.NewUser(ids, "dup@example.com", "First", suiteBase)
	if _, err := repo.Create(ctx, a); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Same address in a different case normalizes to the same email → collision.
	b, _ := user.NewUser(ids, "DUP@example.com", "Second", suiteBase)
	if _, err := repo.Create(ctx, b); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("Create colliding email: err=%v, want ErrAlreadyExists", err)
	}
	other, _ := user.NewUser(ids, "other@example.com", "Other", suiteBase)
	if _, err := repo.Create(ctx, other); err != nil {
		t.Errorf("Create distinct email: err=%v, want nil", err)
	}
}

// --- Passwords ---

func testPasswords(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Passwords

	if err := repo.Set(ctx, "u1", "hash-one"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := repo.Get(ctx, "u1")
	if err != nil || got != "hash-one" {
		t.Fatalf("Get: %q err=%v", got, err)
	}
	// Set upserts: a second Set replaces, never collides.
	if err := repo.Set(ctx, "u1", "hash-two"); err != nil {
		t.Fatalf("Set (replace): %v", err)
	}
	got, err = repo.Get(ctx, "u1")
	if err != nil || got != "hash-two" {
		t.Fatalf("Get after replace: %q err=%v", got, err)
	}
}

func testPasswordsAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.Passwords.Get(ctx, "nobody"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
}

// --- Sessions ---

func testSessionsCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	sess := session.NewSession("u1", time.Hour, time.Now())
	if _, err := repo.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, sess.Token)
	if err != nil || got.UserID != "u1" {
		t.Fatalf("Get: user=%q err=%v", got.UserID, err)
	}
	if err := repo.Delete(ctx, sess.Token); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, sess.Token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testSessionsAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

func testSessionsExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	// A session created one hour ago with a one-minute lifetime is expired now.
	sess := session.NewSession("u1", time.Minute, time.Now().Add(-time.Hour))
	if _, err := repo.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Get(ctx, sess.Token); !errors.Is(err, errs.ErrExpired) {
		t.Errorf("Get(expired): err=%v, want ErrExpired", err)
	}
}

// testSessionsDeleteByUser asserts the bulk, idempotent DeleteByUser contract:
// it removes every session for the target user, leaves other users' sessions
// intact, and returns nil on a second call when none remain (never ErrNotFound).
func testSessionsDeleteByUser(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	a1 := session.NewSession("userA", time.Hour, time.Now())
	a2 := session.NewSession("userA", time.Hour, time.Now())
	b1 := session.NewSession("userB", time.Hour, time.Now())
	for _, s := range []session.Session{a1, a2, b1} {
		if _, err := repo.Create(ctx, s); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	if err := repo.DeleteByUser(ctx, "userA"); err != nil {
		t.Fatalf("DeleteByUser(userA): %v", err)
	}
	for _, tok := range []string{a1.Token, a2.Token} {
		if _, err := repo.Get(ctx, tok); !errors.Is(err, errs.ErrNotFound) {
			t.Errorf("Get(userA session after DeleteByUser): err=%v, want ErrNotFound", err)
		}
	}
	if got, err := repo.Get(ctx, b1.Token); err != nil || got.UserID != "userB" {
		t.Errorf("userB session removed by DeleteByUser(userA): user=%q err=%v", got.UserID, err)
	}

	// Idempotent: a repeat with zero matching rows returns nil, not ErrNotFound.
	if err := repo.DeleteByUser(ctx, "userA"); err != nil {
		t.Errorf("second DeleteByUser(userA): err=%v, want nil", err)
	}
}

// --- VerificationCodes ---

func testCodesCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationCodes

	code := verification.NewCode("u1", time.Hour, time.Now())
	if _, err := repo.Create(ctx, code); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, code.Code)
	if err != nil || got.UserID != "u1" {
		t.Fatalf("Get: user=%q err=%v", got.UserID, err)
	}
	if err := repo.Delete(ctx, code.Code); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, code.Code); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testCodesAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationCodes
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

func testCodesExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationCodes

	code := verification.NewCode("u1", time.Minute, time.Now().Add(-time.Hour))
	if _, err := repo.Create(ctx, code); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Get(ctx, code.Code); !errors.Is(err, errs.ErrExpired) {
		t.Errorf("Get(expired): err=%v, want ErrExpired", err)
	}
}

// --- VerificationTokens ---

func testTokensCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationTokens

	tok := verification.NewToken("u1", time.Hour, time.Now())
	if _, err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, tok.Token)
	if err != nil || got.UserID != "u1" {
		t.Fatalf("Get: user=%q err=%v", got.UserID, err)
	}
	if err := repo.Delete(ctx, tok.Token); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, tok.Token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testTokensAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationTokens
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

func testTokensExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.VerificationTokens

	tok := verification.NewToken("u1", time.Minute, time.Now().Add(-time.Hour))
	if _, err := repo.Create(ctx, tok); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Get(ctx, tok.Token); !errors.Is(err, errs.ErrExpired) {
		t.Errorf("Get(expired): err=%v, want ErrExpired", err)
	}
}

// --- OAuthAccounts ---

func testOAuthAccountsCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.OAuthAccounts

	acct, err := oauthaccount.New("user1", "google", "google-123", suiteBase)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	acct.ProviderEmail = "u@example.com"
	acct.ProviderEmailVerified = true
	acct.AccessToken = "cipher-abc" // the store persists the opaque (encrypted) value verbatim
	acct.TokenType = "Bearer"

	created, err := repo.Create(ctx, acct)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.UserID != "user1" {
		t.Errorf("Create UserID = %q, want user1", created.UserID)
	}

	got, err := repo.GetByProvider(ctx, "google", "google-123")
	if err != nil {
		t.Fatalf("GetByProvider: %v", err)
	}
	if got.UserID != "user1" || got.ProviderEmail != "u@example.com" || got.AccessToken != "cipher-abc" {
		t.Errorf("GetByProvider round-trip lost data: %+v", got)
	}

	if err := repo.Delete(ctx, "user1", "google"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByProvider(ctx, "google", "google-123"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetByProvider after Delete: err=%v, want ErrNotFound", err)
	}
}

func testOAuthAccountsUniqueness(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.OAuthAccounts

	a, _ := oauthaccount.New("user1", "google", "dup-123", suiteBase)
	if _, err := repo.Create(ctx, a); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// The SAME provider identity claimed by a different user → collision.
	b, _ := oauthaccount.New("user2", "google", "dup-123", suiteBase)
	if _, err := repo.Create(ctx, b); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("Create colliding (provider, provider_user_id): err=%v, want ErrAlreadyExists", err)
	}
	// A distinct provider identity is fine.
	c, _ := oauthaccount.New("user2", "google", "other-456", suiteBase)
	if _, err := repo.Create(ctx, c); err != nil {
		t.Errorf("Create distinct identity: err=%v, want nil", err)
	}
}

func testOAuthAccountsAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.OAuthAccounts.GetByProvider(ctx, "google", "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetByProvider(absent): err=%v, want ErrNotFound", err)
	}
}

func testOAuthAccountsListByUser(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.OAuthAccounts

	g1, _ := oauthaccount.New("user1", "google", "g1", suiteBase)
	gh1, _ := oauthaccount.New("user1", "github", "gh1", suiteBase)
	g2, _ := oauthaccount.New("user2", "google", "g2", suiteBase)
	for _, a := range []oauthaccount.OAuthAccount{g1, gh1, g2} {
		if _, err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	list, err := repo.ListByUser(ctx, "user1")
	if err != nil {
		t.Fatalf("ListByUser(user1): %v", err)
	}
	if len(list) != 2 {
		t.Errorf("ListByUser(user1) len = %d, want 2", len(list))
	}
	providers := map[string]bool{}
	for _, a := range list {
		providers[a.Provider] = true
		if a.UserID != "user1" {
			t.Errorf("ListByUser(user1) returned a foreign user: %+v", a)
		}
	}
	if !providers["google"] || !providers["github"] {
		t.Errorf("ListByUser(user1) providers = %v, want google+github", providers)
	}

	if list, _ := repo.ListByUser(ctx, "user2"); len(list) != 1 {
		t.Errorf("ListByUser(user2) len = %d, want 1", len(list))
	}
	empty, err := repo.ListByUser(ctx, "nobody")
	if err != nil {
		t.Errorf("ListByUser(nobody): err=%v, want nil", err)
	}
	if len(empty) != 0 {
		t.Errorf("ListByUser(nobody) len = %d, want 0", len(empty))
	}
}

func testOAuthAccountsDeleteAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if err := repos.OAuthAccounts.Delete(ctx, "nobody", "google"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

// --- OAuthStates ---

func testOAuthStatesConsume(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.OAuthStates

	payload := []byte(`{"code_verifier":"v","nonce":"n"}`)
	st := oauthstate.New("google", oauthstate.PurposeFlow, payload, time.Hour, time.Now())
	if _, err := repo.Create(ctx, st); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Consume(ctx, st.Token)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.Provider != "google" || got.Purpose != oauthstate.PurposeFlow || string(got.Payload) != string(payload) {
		t.Errorf("Consume round-trip lost data: %+v", got)
	}

	// Single-use: a second Consume of the same token → ErrNotFound (it is gone).
	if _, err := repo.Consume(ctx, st.Token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("second Consume: err=%v, want ErrNotFound", err)
	}
}

func testOAuthStatesExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.OAuthStates

	// Created one hour ago with a one-minute lifetime → expired now.
	st := oauthstate.New("google", oauthstate.PurposePendingLink, []byte("payload"), time.Minute, time.Now().Add(-time.Hour))
	if _, err := repo.Create(ctx, st); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Expired Consume deletes the row AND returns ErrExpired.
	if _, err := repo.Consume(ctx, st.Token); !errors.Is(err, errs.ErrExpired) {
		t.Errorf("Consume(expired): err=%v, want ErrExpired", err)
	}
	// Row is gone (deleted regardless of expiry): a follow-up Consume → ErrNotFound.
	if _, err := repo.Consume(ctx, st.Token); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Consume after expired-delete: err=%v, want ErrNotFound", err)
	}
}

func testOAuthStatesUnknown(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.OAuthStates.Consume(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Consume(unknown): err=%v, want ErrNotFound", err)
	}
}

// --- ServiceAccounts ---

func testServiceAccountsCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ServiceAccounts

	sa, err := serviceaccount.New(ids, "deployer", "CI deploy bot", "admin-1", false, "", suiteBase)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	created, err := repo.Create(ctx, sa)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != sa.ID || created.Name != "deployer" || created.CreatedBy != "admin-1" {
		t.Errorf("Create round-trip lost data: %+v", created)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.Description != "CI deploy bot" {
		t.Fatalf("Get: %+v err=%v", got, err)
	}

	got.Description = "updated"
	got.UpdatedAt = suiteBase.Add(time.Hour)
	updated, err := repo.Update(ctx, got.ID, got)
	if err != nil || updated.Description != "updated" {
		t.Fatalf("Update: %+v err=%v", updated, err)
	}
	reget, err := repo.Get(ctx, created.ID)
	if err != nil || reget.Description != "updated" {
		t.Fatalf("Get after Update: %+v err=%v", reget, err)
	}

	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testServiceAccountsAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ServiceAccounts

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	absent, _ := serviceaccount.New(ids, "ghost", "", "admin", false, "", suiteBase)
	if _, err := repo.Update(ctx, "nope", absent); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Update(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Delete(absent): err=%v, want ErrNotFound", err)
	}
}

// testServiceAccountsListPaged asserts the crud-typed List pages through every
// record in the pinned created_at DESC, id DESC order.
func testServiceAccountsListPaged(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ServiceAccounts

	created := make([]serviceaccount.ServiceAccount, 0, 5)
	for i := 0; i < 5; i++ {
		sa, err := serviceaccount.New(ids, fmt.Sprintf("sa-%d", i), "", "admin", false, "", suiteBase.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		c, err := repo.Create(ctx, sa)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}

	want := serviceAccountIDsSorted(created)
	got := pageAllServiceAccounts(t, repo, 2)
	if !equalStrings(got, want) {
		t.Errorf("paged order = %v, want %v", got, want)
	}
}

// testServiceAccountsListCollision proves the id tiebreak: several accounts with
// an identical created_at page in a stable order determined by id DESC.
func testServiceAccountsListCollision(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ServiceAccounts

	created := make([]serviceaccount.ServiceAccount, 0, 4)
	for i := 0; i < 4; i++ {
		sa, err := serviceaccount.New(ids, fmt.Sprintf("col-%d", i), "", "admin", false, "", suiteBase)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		c, err := repo.Create(ctx, sa)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}

	want := serviceAccountIDsSorted(created)
	got := pageAllServiceAccounts(t, repo, 2)
	if !equalStrings(got, want) {
		t.Errorf("collision paged order = %v, want %v (id tiebreak)", got, want)
	}
}

// --- APIKeys ---

func testAPIKeysGetByHashUnknown(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.APIKeys.GetByHash(ctx, "no-such-hash"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetByHash(unknown): err=%v, want ErrNotFound", err)
	}
}

// testAPIKeysGetByHashValid is the pinned NULL-expiry case: a live key with no
// ExpiresAt is returned as a record (catches both a stray SQL expiry filter and
// a NULL-handling bug).
func testAPIKeysGetByHashValid(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.APIKeys

	k := mustCreateAPIKey(t, repo, "sa-1", "valid", "hash-valid", time.Time{}, suiteBase)
	got, err := repo.GetByHash(ctx, "hash-valid")
	if err != nil {
		t.Fatalf("GetByHash(valid): %v", err)
	}
	if got.ID != k.ID || !got.ExpiresAt.IsZero() {
		t.Errorf("GetByHash(valid) = %+v, want id=%s, never-expires", got, k.ID)
	}
}

// testAPIKeysGetByHashRevoked is the pinned revoked case: a revoked key is still
// RETURNED (revocation is a service branch, not a store filter).
func testAPIKeysGetByHashRevoked(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.APIKeys

	k := mustCreateAPIKey(t, repo, "sa-1", "revoked", "hash-revoked", time.Time{}, suiteBase)
	if err := repo.Revoke(ctx, k.ID, suiteBase.Add(time.Minute)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, err := repo.GetByHash(ctx, "hash-revoked")
	if err != nil {
		t.Fatalf("GetByHash(revoked) must still return the record: %v", err)
	}
	if got.ID != k.ID || !got.Revoked() {
		t.Errorf("GetByHash(revoked) = %+v, want a revoked record for %s", got, k.ID)
	}
}

// testAPIKeysGetByHashExpired is the pinned expired case: a past-expiry key is
// still RETURNED (expiry is a service branch, not a store filter).
func testAPIKeysGetByHashExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.APIKeys

	k := mustCreateAPIKey(t, repo, "sa-1", "expired", "hash-expired", suiteBase.Add(-time.Hour), suiteBase.Add(-2*time.Hour))
	got, err := repo.GetByHash(ctx, "hash-expired")
	if err != nil {
		t.Fatalf("GetByHash(expired) must still return the record: %v", err)
	}
	if got.ID != k.ID || !got.Expired(time.Now()) {
		t.Errorf("GetByHash(expired) = %+v, want an expired record for %s", got, k.ID)
	}
}

func testAPIKeysMintUniqueness(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.APIKeys

	a := mustCreateAPIKey(t, repo, "sa-1", "a", "hash-a", time.Time{}, suiteBase)
	b := mustCreateAPIKey(t, repo, "sa-1", "b", "hash-b", time.Time{}, suiteBase.Add(time.Minute))
	ga, err := repo.GetByHash(ctx, "hash-a")
	if err != nil {
		t.Fatalf("GetByHash(a): %v", err)
	}
	gb, err := repo.GetByHash(ctx, "hash-b")
	if err != nil {
		t.Fatalf("GetByHash(b): %v", err)
	}
	if ga.ID != a.ID || gb.ID != b.ID || ga.ID == gb.ID {
		t.Errorf("distinct hashes resolved wrong: a=%s b=%s", ga.ID, gb.ID)
	}

	// A colliding key_hash is rejected (the store's uniqueness invariant).
	dup, _ := apikey.New(ids, "sa-1", "dup", "prefix", "hash-a", time.Time{}, suiteBase)
	if _, err := repo.Create(ctx, dup); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("Create colliding key_hash: err=%v, want ErrAlreadyExists", err)
	}
}

func testAPIKeysTouchLastUsed(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.APIKeys

	k := mustCreateAPIKey(t, repo, "sa-1", "touch", "hash-touch", time.Time{}, suiteBase)
	if !k.LastUsedAt.IsZero() {
		t.Errorf("a fresh key already has LastUsedAt: %v", k.LastUsedAt)
	}
	at := suiteBase.Add(time.Hour)
	if err := repo.TouchLastUsed(ctx, k.ID, at); err != nil {
		t.Fatalf("TouchLastUsed: %v", err)
	}
	got, err := repo.GetByHash(ctx, "hash-touch")
	if err != nil {
		t.Fatalf("GetByHash after touch: %v", err)
	}
	if got.LastUsedAt.IsZero() || !got.LastUsedAt.Equal(at) {
		t.Errorf("LastUsedAt = %v, want %v", got.LastUsedAt, at)
	}
	if err := repo.TouchLastUsed(ctx, "nope", at); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("TouchLastUsed(absent): err=%v, want ErrNotFound", err)
	}
}

func testAPIKeysRevokeAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if err := repos.APIKeys.Revoke(ctx, "nope", suiteBase); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Revoke(absent): err=%v, want ErrNotFound", err)
	}
}

// testAPIKeysListPaged asserts ListByServiceAccount pages through one account's
// keys in the pinned order and excludes another account's keys.
func testAPIKeysListPaged(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.APIKeys

	created := make([]apikey.APIKey, 0, 5)
	for i := 0; i < 5; i++ {
		k := mustCreateAPIKey(t, repo, "sa-list", fmt.Sprintf("k-%d", i), fmt.Sprintf("hash-%d", i), time.Time{}, suiteBase.Add(time.Duration(i)*time.Minute))
		created = append(created, k)
	}
	// A key under a different service account must not appear.
	mustCreateAPIKey(t, repo, "sa-other", "other", "hash-other", time.Time{}, suiteBase)
	_ = ctx

	want := apiKeyIDsSorted(created)
	got := pageAllAPIKeys(t, repo, "sa-list", 2)
	if !equalStrings(got, want) {
		t.Errorf("paged order = %v, want %v", got, want)
	}
}

// testAPIKeysListCollision proves the id tiebreak for a service account's keys.
func testAPIKeysListCollision(t *testing.T, repos auth.Repositories) {
	repo := repos.APIKeys

	created := make([]apikey.APIKey, 0, 4)
	for i := 0; i < 4; i++ {
		k := mustCreateAPIKey(t, repo, "sa-col", fmt.Sprintf("c-%d", i), fmt.Sprintf("hc-%d", i), time.Time{}, suiteBase)
		created = append(created, k)
	}

	want := apiKeyIDsSorted(created)
	got := pageAllAPIKeys(t, repo, "sa-col", 2)
	if !equalStrings(got, want) {
		t.Errorf("collision paged order = %v, want %v (id tiebreak)", got, want)
	}
}

// --- shared paging helpers ---

func mustCreateAPIKey(t *testing.T, repo apikey.APIKeyRepository, saID, name, hash string, expiresAt, createdAt time.Time) apikey.APIKey {
	t.Helper()
	prefix := hash
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	k, err := apikey.New(ids, saID, name, prefix, hash, expiresAt, createdAt)
	if err != nil {
		t.Fatalf("apikey.New: %v", err)
	}
	created, err := repo.Create(context.Background(), k)
	if err != nil {
		t.Fatalf("apikey Create: %v", err)
	}
	return created
}

func serviceAccountIDsSorted(sas []serviceaccount.ServiceAccount) []string {
	sorted := append([]serviceaccount.ServiceAccount(nil), sas...)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].ID > sorted[j].ID
	})
	ids := make([]string, len(sorted))
	for i, sa := range sorted {
		ids[i] = sa.ID
	}
	return ids
}

func apiKeyIDsSorted(keys []apikey.APIKey) []string {
	sorted := append([]apikey.APIKey(nil), keys...)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].ID > sorted[j].ID
	})
	ids := make([]string, len(sorted))
	for i, k := range sorted {
		ids[i] = k.ID
	}
	return ids
}

func pageAllServiceAccounts(t *testing.T, repo serviceaccount.ServiceAccountRepository, limit int) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	cursor := ""
	for i := 0; i < 100; i++ { // bound against a runaway cursor
		page, err := repo.List(ctx, crud.ListRequest{Limit: limit, Cursor: cursor})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, sa := range page.Items {
			ids = append(ids, sa.ID)
		}
		if !page.HasMore || page.NextCursor == "" {
			return ids
		}
		cursor = page.NextCursor
	}
	t.Fatalf("pageAllServiceAccounts did not terminate")
	return nil
}

func pageAllAPIKeys(t *testing.T, repo apikey.APIKeyRepository, saID string, limit int) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	cursor := ""
	for i := 0; i < 100; i++ { // bound against a runaway cursor
		page, err := repo.ListByServiceAccount(ctx, saID, crud.ListRequest{Limit: limit, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListByServiceAccount: %v", err)
		}
		for _, k := range page.Items {
			ids = append(ids, k.ID)
		}
		if !page.HasMore || page.NextCursor == "" {
			return ids
		}
		cursor = page.NextCursor
	}
	t.Fatalf("pageAllAPIKeys did not terminate")
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- SecurityEvents ---

// testSecurityEventsCreateList appends a mixed set and asserts an unfiltered List
// returns them all in the pinned created_at DESC, id DESC order.
func testSecurityEventsCreateList(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.SecurityEvents

	created := make([]securityevent.SecurityEvent, 0, 4)
	for i := 0; i < 4; i++ {
		evt := securityevent.New(ids, securityevent.TypeLogin, securityevent.StatusSuccess, suiteBase.Add(time.Duration(i)*time.Minute))
		evt.UserID = "u1"
		c, err := repo.Create(ctx, evt)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if c.ID != evt.ID {
			t.Errorf("Create round-trip lost the id: got %q want %q", c.ID, evt.ID)
		}
		created = append(created, c)
	}

	want := securityEventIDsSorted(created)
	got := pageAllSecurityEvents(t, repo, securityevent.ListFilter{}, 25)
	if !equalStrings(got, want) {
		t.Errorf("List order = %v, want %v (created_at DESC, id DESC)", got, want)
	}
}

// testSecurityEventsFilters asserts each filter dimension (user, type, status,
// and the [Since, Until) time window) selects exactly the matching rows.
func testSecurityEventsFilters(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.SecurityEvents

	mk := func(userID, typ, status string, at time.Time) securityevent.SecurityEvent {
		evt := securityevent.New(ids, typ, status, at)
		evt.UserID = userID
		c, err := repo.Create(ctx, evt)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		return c
	}

	uaLogin := mk("ua", securityevent.TypeLogin, securityevent.StatusSuccess, suiteBase)
	uaFail := mk("ua", securityevent.TypeLogin, securityevent.StatusFailure, suiteBase.Add(time.Minute))
	ubReg := mk("ub", securityevent.TypeRegister, securityevent.StatusSuccess, suiteBase.Add(2*time.Minute))
	uaLater := mk("ua", securityevent.TypeLogout, securityevent.StatusSuccess, suiteBase.Add(time.Hour))

	cases := []struct {
		name   string
		filter securityevent.ListFilter
		want   []string
	}{
		{"by_user", securityevent.ListFilter{UserID: "ua"}, []string{uaLater.ID, uaFail.ID, uaLogin.ID}},
		{"by_type", securityevent.ListFilter{EventType: securityevent.TypeLogin}, []string{uaFail.ID, uaLogin.ID}},
		{"by_status", securityevent.ListFilter{EventStatus: securityevent.StatusFailure}, []string{uaFail.ID}},
		{"by_user_and_type", securityevent.ListFilter{UserID: "ua", EventType: securityevent.TypeLogin}, []string{uaFail.ID, uaLogin.ID}},
		{"time_window", securityevent.ListFilter{Since: suiteBase.Add(time.Minute), Until: suiteBase.Add(3 * time.Minute)}, []string{ubReg.ID, uaFail.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pageAllSecurityEvents(t, repo, tc.filter, 25)
			if !equalStrings(got, tc.want) {
				t.Errorf("filter %+v: got %v, want %v", tc.filter, got, tc.want)
			}
		})
	}
}

// testSecurityEventsListPaged asserts List pages through the full population in
// the pinned order across a small page size.
func testSecurityEventsListPaged(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.SecurityEvents

	created := make([]securityevent.SecurityEvent, 0, 5)
	for i := 0; i < 5; i++ {
		evt := securityevent.New(ids, securityevent.TypeLogin, securityevent.StatusSuccess, suiteBase.Add(time.Duration(i)*time.Minute))
		c, err := repo.Create(ctx, evt)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}

	want := securityEventIDsSorted(created)
	got := pageAllSecurityEvents(t, repo, securityevent.ListFilter{}, 2)
	if !equalStrings(got, want) {
		t.Errorf("paged order = %v, want %v", got, want)
	}
}

// testSecurityEventsListCollision proves the id tiebreak: several events with an
// identical created_at page in a stable order determined by id DESC, with a
// consistent NextCursor across implementations.
func testSecurityEventsListCollision(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.SecurityEvents

	created := make([]securityevent.SecurityEvent, 0, 4)
	for i := 0; i < 4; i++ {
		evt := securityevent.New(ids, securityevent.TypeLogin, securityevent.StatusSuccess, suiteBase)
		c, err := repo.Create(ctx, evt)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}

	want := securityEventIDsSorted(created)
	got := pageAllSecurityEvents(t, repo, securityevent.ListFilter{}, 2)
	if !equalStrings(got, want) {
		t.Errorf("collision paged order = %v, want %v (id tiebreak)", got, want)
	}
}

// testSecurityEventsDetails is the pinned Details round-trip: a nil map, an empty
// map, and a populated map all store, and read back as a NON-NIL map — a non-nil
// EMPTY map for the nil/empty cases, the same populated map for the third
// (uniform across backends, whether the store persists '{}' or NULL).
func testSecurityEventsDetails(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.SecurityEvents

	nilEvt := securityevent.New(ids, securityevent.TypeLogin, securityevent.StatusFailure, suiteBase)
	nilEvt.UserID = "u-nil"
	nilEvt.Details = nil

	emptyEvt := securityevent.New(ids, securityevent.TypeLogin, securityevent.StatusFailure, suiteBase.Add(time.Minute))
	emptyEvt.UserID = "u-empty"
	emptyEvt.Details = map[string]any{}

	fullEvt := securityevent.New(ids, securityevent.TypeAPIKeyAuth, securityevent.StatusSuccess, suiteBase.Add(2*time.Minute))
	fullEvt.UserID = "u-full"
	fullEvt.Details = map[string]any{"key_prefix": "abc12345", "provider": "google"}

	for _, evt := range []securityevent.SecurityEvent{nilEvt, emptyEvt, fullEvt} {
		if _, err := repo.Create(ctx, evt); err != nil {
			t.Fatalf("Create(%s): %v", evt.UserID, err)
		}
	}

	read := func(userID string) securityevent.SecurityEvent {
		page, err := repo.List(ctx, securityevent.ListFilter{UserID: userID}, crud.ListRequest{Limit: 10})
		if err != nil {
			t.Fatalf("List(%s): %v", userID, err)
		}
		if len(page.Items) != 1 {
			t.Fatalf("List(%s) len = %d, want 1", userID, len(page.Items))
		}
		return page.Items[0]
	}

	for _, userID := range []string{"u-nil", "u-empty"} {
		got := read(userID)
		if got.Details == nil {
			t.Errorf("Details for %s read back nil, want a non-nil empty map", userID)
		}
		if len(got.Details) != 0 {
			t.Errorf("Details for %s = %v, want empty", userID, got.Details)
		}
	}

	full := read("u-full")
	if full.Details == nil || full.Details["key_prefix"] != "abc12345" || full.Details["provider"] != "google" {
		t.Errorf("populated Details round-trip lost data: %v", full.Details)
	}
}

func securityEventIDsSorted(events []securityevent.SecurityEvent) []string {
	sorted := append([]securityevent.SecurityEvent(nil), events...)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].ID > sorted[j].ID
	})
	ids := make([]string, len(sorted))
	for i, evt := range sorted {
		ids[i] = evt.ID
	}
	return ids
}

func pageAllSecurityEvents(t *testing.T, repo securityevent.SecurityEventRepository, filter securityevent.ListFilter, limit int) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	cursor := ""
	for i := 0; i < 100; i++ { // bound against a runaway cursor
		page, err := repo.List(ctx, filter, crud.ListRequest{Limit: limit, Cursor: cursor})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, evt := range page.Items {
			ids = append(ids, evt.ID)
		}
		if !page.HasMore || page.NextCursor == "" {
			return ids
		}
		cursor = page.NextCursor
	}
	t.Fatalf("pageAllSecurityEvents did not terminate")
	return nil
}

// --- Invitations ---

func testInvitationsCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	// A live (future-expiry) invite, so the GetByTokenHash read below returns the
	// record rather than the read-time ErrExpired. An empty identifier kind
	// defaults to email (New's default) — the default-email back-compat case.
	inv, err := invitation.New(ids, "project", "p1", "member", "invitee@example.com", "", "inviter-1", "hash-crud", false, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	created, err := repo.Create(ctx, inv)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID != inv.ID || created.Status != invitation.StatusPending {
		t.Errorf("Create round-trip lost data: %+v", created)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.Identifier != "invitee@example.com" || got.Relation != "member" {
		t.Fatalf("Get: %+v err=%v", got, err)
	}
	// Default-email back-compat: an omitted kind persists and reads back as email.
	if got.IdentifierKind != identity.KindEmail {
		t.Errorf("IdentifierKind = %q, want %q (default)", got.IdentifierKind, identity.KindEmail)
	}

	byHash, err := repo.GetByTokenHash(ctx, "hash-crud")
	if err != nil || byHash.ID != created.ID {
		t.Fatalf("GetByTokenHash: id=%q err=%v", byHash.ID, err)
	}

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
}

// testInvitationsUniqueness is the pinned PARTIAL-index case (plan-cut
// amendment): a second PENDING invite for the same (resource_type, resource_id,
// identifier, relation) → ErrAlreadyExists; a differing relation is fine; and
// after UpdateStatus moves the first off pending, a NEW pending invite for the
// same tuple SUCCEEDS.
func testInvitationsUniqueness(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	a := mustNewInvitation(t, "project", "p1", "member", "dup@example.com", "inviter-1", "hash-a", suiteBase)
	if _, err := repo.Create(ctx, a); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Same pending tuple → collision.
	b := mustNewInvitation(t, "project", "p1", "member", "dup@example.com", "inviter-1", "hash-b", suiteBase)
	if _, err := repo.Create(ctx, b); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("Create colliding pending tuple: err=%v, want ErrAlreadyExists", err)
	}
	// A different relation is a different tuple → fine.
	c := mustNewInvitation(t, "project", "p1", "admin", "dup@example.com", "inviter-1", "hash-c", suiteBase)
	if _, err := repo.Create(ctx, c); err != nil {
		t.Errorf("Create distinct relation: err=%v, want nil", err)
	}

	// Move the first off pending; a NEW pending invite for the same tuple now
	// SUCCEEDS (partial uniqueness covers pending rows only).
	if _, err := repo.UpdateStatus(ctx, a.ID, invitation.StatusUpdate{
		Status:    invitation.StatusDeclined,
		TokenHash: a.TokenHash,
		ExpiresAt: a.ExpiresAt,
		UpdatedAt: suiteBase.Add(time.Minute),
	}); err != nil {
		t.Fatalf("UpdateStatus(declined): %v", err)
	}
	d := mustNewInvitation(t, "project", "p1", "member", "dup@example.com", "inviter-1", "hash-d", suiteBase.Add(2*time.Minute))
	if _, err := repo.Create(ctx, d); err != nil {
		t.Errorf("Create pending after prior moved off pending: err=%v, want nil (partial index)", err)
	}
}

// testInvitationsKindRoundTrip proves the identifier_kind column round-trips and
// is store-verbatim: a non-email kind persists and reads back unchanged, and the
// identifier is stored EXACTLY as written (no store-side normalization — that is
// the service's job). A phone-kind identifier keeps its casing/spacing verbatim.
func testInvitationsKindRoundTrip(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	inv, err := invitation.New(ids, "project", "p1", "member", "+1 (555) 010-2345", identity.KindPhone, "inviter-1", "hash-phone", false, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	created, err := repo.Create(ctx, inv)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.IdentifierKind != identity.KindPhone {
		t.Errorf("Create IdentifierKind = %q, want %q", created.IdentifierKind, identity.KindPhone)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.IdentifierKind != identity.KindPhone || got.Identifier != "+1 (555) 010-2345" {
		t.Errorf("round-trip lost kind/identifier verbatim: kind=%q identifier=%q", got.IdentifierKind, got.Identifier)
	}
	byHash, err := repo.GetByTokenHash(ctx, "hash-phone")
	if err != nil || byHash.IdentifierKind != identity.KindPhone {
		t.Errorf("GetByTokenHash: kind=%q err=%v", byHash.IdentifierKind, err)
	}
}

// testInvitationsCrossKindCoexistence proves identifier_kind is part of the
// partial pending-tuple uniqueness key: the SAME (resource, identifier, relation)
// value may have a pending invitation under two DIFFERENT kinds simultaneously —
// the collision is per (resource, kind, identifier, relation). This is the case
// that forces identifier_kind into the pending unique index.
func testInvitationsCrossKindCoexistence(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	// Same identifier string, two kinds. (Contrived but exact: the value space is
	// what the index must disambiguate by kind.)
	emailInv, err := invitation.New(ids, "project", "p1", "member", "coexist@example.com", identity.KindEmail, "inviter-1", "hash-ck-email", false, time.Hour, suiteBase)
	if err != nil {
		t.Fatalf("New(email): %v", err)
	}
	if _, err := repo.Create(ctx, emailInv); err != nil {
		t.Fatalf("Create(email): %v", err)
	}
	phoneInv, err := invitation.New(ids, "project", "p1", "member", "coexist@example.com", identity.KindPhone, "inviter-1", "hash-ck-phone", false, time.Hour, suiteBase)
	if err != nil {
		t.Fatalf("New(phone): %v", err)
	}
	if _, err := repo.Create(ctx, phoneInv); err != nil {
		t.Errorf("Create(phone) same value different kind: err=%v, want nil (kind is part of the key)", err)
	}

	// A THIRD pending invite matching the email tuple exactly (same kind) still
	// collides — the kind widens the key, it does not remove the per-kind guard.
	dup, err := invitation.New(ids, "project", "p1", "member", "coexist@example.com", identity.KindEmail, "inviter-1", "hash-ck-dup", false, time.Hour, suiteBase)
	if err != nil {
		t.Fatalf("New(dup): %v", err)
	}
	if _, err := repo.Create(ctx, dup); !errors.Is(err, errs.ErrAlreadyExists) {
		t.Errorf("Create(dup email tuple): err=%v, want ErrAlreadyExists", err)
	}
}

func testInvitationsTokenUnknown(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.Invitations.GetByTokenHash(ctx, "no-such-hash"); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("GetByTokenHash(unknown): err=%v, want ErrNotFound", err)
	}
}

// testInvitationsTokenExpired asserts a token-hash read past ExpiresAt surfaces
// errs.ErrExpired (mirroring the session/verification/oauthstate precedent).
func testInvitationsTokenExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	// Created one hour ago with a one-minute lifetime → expired now.
	inv, err := invitation.New(ids, "project", "p1", "member", "expired@example.com", "", "inviter-1", "hash-expired", false, time.Minute, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := repo.Create(ctx, inv); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.GetByTokenHash(ctx, "hash-expired"); !errors.Is(err, errs.ErrExpired) {
		t.Errorf("GetByTokenHash(expired): err=%v, want ErrExpired", err)
	}
}

// testInvitationsStatusTransitions round-trips an accept transition through
// UpdateStatus: status, accepted-at, and resolved-subject-id persist.
func testInvitationsStatusTransitions(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	inv := mustNewInvitation(t, "project", "p1", "member", "trans@example.com", "inviter-1", "hash-trans", suiteBase)
	if _, err := repo.Create(ctx, inv); err != nil {
		t.Fatalf("Create: %v", err)
	}

	acceptedAt := suiteBase.Add(time.Minute)
	updated, err := repo.UpdateStatus(ctx, inv.ID, invitation.StatusUpdate{
		Status:            invitation.StatusAccepted,
		TokenHash:         inv.TokenHash,
		ExpiresAt:         inv.ExpiresAt,
		AcceptedAt:        acceptedAt,
		ResolvedSubjectID: "user-42",
		UpdatedAt:         acceptedAt,
	})
	if err != nil {
		t.Fatalf("UpdateStatus(accepted): %v", err)
	}
	if updated.Status != invitation.StatusAccepted || updated.ResolvedSubjectID != "user-42" || !updated.AcceptedAt.Equal(acceptedAt) {
		t.Errorf("accept transition lost data: %+v", updated)
	}

	reget, err := repo.Get(ctx, inv.ID)
	if err != nil || reget.Status != invitation.StatusAccepted || reget.ResolvedSubjectID != "user-42" {
		t.Fatalf("Get after UpdateStatus: %+v err=%v", reget, err)
	}
}

func testInvitationsUpdateAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.Invitations.UpdateStatus(ctx, "nope", invitation.StatusUpdate{Status: invitation.StatusCancelled, UpdatedAt: suiteBase}); !errors.Is(err, errs.ErrNotFound) {
		t.Errorf("UpdateStatus(absent): err=%v, want ErrNotFound", err)
	}
}

// testInvitationsListByResourcePaged pages a resource's invitations in the
// pinned created_at DESC, id DESC order and excludes another resource's rows.
func testInvitationsListByResourcePaged(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	created := make([]invitation.Invitation, 0, 5)
	for i := 0; i < 5; i++ {
		// Distinct identifier per row keeps each a distinct pending tuple.
		inv := mustNewInvitation(t, "project", "res-list", "member", fmt.Sprintf("u%d@example.com", i), "inviter-1", fmt.Sprintf("hash-r%d", i), suiteBase.Add(time.Duration(i)*time.Minute))
		c, err := repo.Create(ctx, inv)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}
	// A row under a different resource must not appear.
	other := mustNewInvitation(t, "project", "res-other", "member", "x@example.com", "inviter-1", "hash-other", suiteBase)
	if _, err := repo.Create(ctx, other); err != nil {
		t.Fatalf("Create(other): %v", err)
	}

	want := invitationIDsSorted(created)
	got := pageAllInvitations(t, func(req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
		return repo.ListByResource(ctx, "project", "res-list", req)
	}, 2)
	if !equalStrings(got, want) {
		t.Errorf("ListByResource paged order = %v, want %v", got, want)
	}
}

func testInvitationsListByResourceCollision(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	created := make([]invitation.Invitation, 0, 4)
	for i := 0; i < 4; i++ {
		inv := mustNewInvitation(t, "project", "res-col", fmt.Sprintf("rel-%d", i), fmt.Sprintf("c%d@example.com", i), "inviter-1", fmt.Sprintf("hash-rc%d", i), suiteBase)
		c, err := repo.Create(ctx, inv)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}

	want := invitationIDsSorted(created)
	got := pageAllInvitations(t, func(req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
		return repo.ListByResource(ctx, "project", "res-col", req)
	}, 2)
	if !equalStrings(got, want) {
		t.Errorf("ListByResource collision order = %v, want %v (id tiebreak)", got, want)
	}
}

// testInvitationsListBySubjectPaged pages an invitee's invitations (keyed on
// identifier) in the pinned order and excludes another invitee's rows.
func testInvitationsListBySubjectPaged(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	created := make([]invitation.Invitation, 0, 5)
	for i := 0; i < 5; i++ {
		// Same identifier, distinct resource keeps each a distinct pending tuple.
		inv := mustNewInvitation(t, "project", fmt.Sprintf("res-%d", i), "member", "subject@example.com", "inviter-1", fmt.Sprintf("hash-s%d", i), suiteBase.Add(time.Duration(i)*time.Minute))
		c, err := repo.Create(ctx, inv)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}
	// A row for another invitee must not appear.
	other := mustNewInvitation(t, "project", "res-x", "member", "someone@example.com", "inviter-1", "hash-sx", suiteBase)
	if _, err := repo.Create(ctx, other); err != nil {
		t.Fatalf("Create(other): %v", err)
	}

	want := invitationIDsSorted(created)
	got := pageAllInvitations(t, func(req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
		return repo.ListBySubject(ctx, "subject@example.com", req)
	}, 2)
	if !equalStrings(got, want) {
		t.Errorf("ListBySubject paged order = %v, want %v", got, want)
	}
}

func testInvitationsListBySubjectCollision(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	created := make([]invitation.Invitation, 0, 4)
	for i := 0; i < 4; i++ {
		inv := mustNewInvitation(t, "project", fmt.Sprintf("resc-%d", i), "member", "collide@example.com", "inviter-1", fmt.Sprintf("hash-sc%d", i), suiteBase)
		c, err := repo.Create(ctx, inv)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}

	want := invitationIDsSorted(created)
	got := pageAllInvitations(t, func(req crud.ListRequest) (crud.Page[invitation.Invitation], error) {
		return repo.ListBySubject(ctx, "collide@example.com", req)
	}, 2)
	if !equalStrings(got, want) {
		t.Errorf("ListBySubject collision order = %v, want %v (id tiebreak)", got, want)
	}
}

// mustNewInvitation builds an email-kind pending invitation (the empty kind
// defaults to email in New) — the common shape the list/collision cases seed.
// Kind-specific cases build their invitations inline with an explicit kind.
func mustNewInvitation(t *testing.T, resourceType, resourceID, relation, identifier, invitedBy, tokenHash string, createdAt time.Time) invitation.Invitation {
	t.Helper()
	inv, err := invitation.New(ids, resourceType, resourceID, relation, identifier, "", invitedBy, tokenHash, false, time.Hour, createdAt)
	if err != nil {
		t.Fatalf("invitation.New: %v", err)
	}
	return inv
}

func invitationIDsSorted(invs []invitation.Invitation) []string {
	sorted := append([]invitation.Invitation(nil), invs...)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].ID > sorted[j].ID
	})
	ids := make([]string, len(sorted))
	for i, inv := range sorted {
		ids[i] = inv.ID
	}
	return ids
}

func pageAllInvitations(t *testing.T, list func(crud.ListRequest) (crud.Page[invitation.Invitation], error), limit int) []string {
	t.Helper()
	var ids []string
	cursor := ""
	for i := 0; i < 100; i++ { // bound against a runaway cursor
		page, err := list(crud.ListRequest{Limit: limit, Cursor: cursor})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, inv := range page.Items {
			ids = append(ids, inv.ID)
		}
		if !page.HasMore || page.NextCursor == "" {
			return ids
		}
		cursor = page.NextCursor
	}
	t.Fatalf("pageAllInvitations did not terminate")
	return nil
}

// --- the standard paginated-port case family (pinned in 03-authentication.md) ---
//
// Every paginated port runs the same six cases — Order, PrevPage, OffsetMode,
// WithCount, StaleCursorOrderChange, CursorOffsetExclusive — over a small seeded
// population. They are generic in the record type T; a port supplies a scoped
// list closure, a fresh-population seed, and id/created_at projections. The seed
// returns the in-scope population plus its expected count (foreign rows a filter
// must exclude are not counted).

// pagedCase bundles the closures a generic list case needs: list runs a List
// against an already-scoped filter; idOf and createdAt project a record's
// identity and order value.
type pagedCase[T any] struct {
	list      func(ctx context.Context, req crud.ListRequest) (crud.Page[T], error)
	idOf      func(T) string
	createdAt func(T) time.Time
}

// runPagedFamily wires the six standard cases for one paginated port. Each case
// obtains a clean, isolated Repositories from newRepos and seeds its own
// population, matching the suite's per-leaf isolation contract.
func runPagedFamily[T any](
	t *testing.T,
	newRepos func(t *testing.T) auth.Repositories,
	scope func(repos auth.Repositories, ctx context.Context, req crud.ListRequest) (crud.Page[T], error),
	seed func(t *testing.T, repos auth.Repositories) (created []T, wantTotal int),
	idOf func(T) string,
	createdAt func(T) time.Time,
) {
	t.Helper()

	newCase := func(repos auth.Repositories) pagedCase[T] {
		return pagedCase[T]{
			list:      func(ctx context.Context, req crud.ListRequest) (crud.Page[T], error) { return scope(repos, ctx, req) },
			idOf:      idOf,
			createdAt: createdAt,
		}
	}

	t.Run("Order", func(t *testing.T) {
		repos := newRepos(t)
		created, _ := seed(t, repos)
		runOrderCase(t, newCase(repos), created)
	})
	t.Run("PrevPage", func(t *testing.T) {
		repos := newRepos(t)
		created, _ := seed(t, repos)
		runPrevPageCase(t, newCase(repos), created)
	})
	t.Run("OffsetMode", func(t *testing.T) {
		repos := newRepos(t)
		created, _ := seed(t, repos)
		runOffsetModeCase(t, newCase(repos), created)
	})
	t.Run("WithCount", func(t *testing.T) {
		repos := newRepos(t)
		created, wantTotal := seed(t, repos)
		runWithCountCase(t, newCase(repos), created, wantTotal)
	})
	t.Run("StaleCursorOrderChange", func(t *testing.T) {
		repos := newRepos(t)
		created, _ := seed(t, repos)
		runStaleCursorCase(t, newCase(repos), created)
	})
	t.Run("CursorOffsetExclusive", func(t *testing.T) {
		repos := newRepos(t)
		_, _ = seed(t, repos)
		runCursorOffsetExclusiveCase(t, newCase(repos))
	})
}

// runOrderCase asserts explicit asc + desc ordering on created_at page through
// the full population in the correct total order (created_at then id tiebreak in
// the same direction), re-asserting the tiebreak under asc via the seeded
// same-created_at pair.
func runOrderCase[T any](t *testing.T, pc pagedCase[T], created []T) {
	t.Helper()
	wantAsc := sortedIDs(created, pc.idOf, pc.createdAt, true)
	gotAsc := pageAllOrdered(t, pc, crud.NewOrder("created_at", crud.ASC), 2)
	if !equalStrings(gotAsc, wantAsc) {
		t.Errorf("asc order = %v, want %v", gotAsc, wantAsc)
	}

	wantDesc := sortedIDs(created, pc.idOf, pc.createdAt, false)
	gotDesc := pageAllOrdered(t, pc, crud.NewOrder("created_at", crud.DESC), 2)
	if !equalStrings(gotDesc, wantDesc) {
		t.Errorf("desc order = %v, want %v", gotDesc, wantDesc)
	}
}

// runPrevPageCase asserts the reverse-probe semantics: the first page has no
// prev; page 2 has a prev whose (partial) window means "previous is the first
// page" and round-trips to page 1's IDs; page 3 has a full prior window whose
// PreviousCursor round-trips to page 2's IDs.
func runPrevPageCase[T any](t *testing.T, pc pagedCase[T], created []T) {
	t.Helper()
	ctx := context.Background()
	desc := sortedIDs(created, pc.idOf, pc.createdAt, false)
	if len(desc) < 6 {
		t.Fatalf("prev-page case needs >= 6 seeded rows, got %d", len(desc))
	}

	page1, err := pc.list(ctx, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if page1.HasPrev {
		t.Errorf("first page HasPrev = true, want false")
	}
	if got := idsOf(page1.Items, pc.idOf); !equalStrings(got, desc[0:2]) {
		t.Errorf("page1 = %v, want %v", got, desc[0:2])
	}

	page2, err := pc.list(ctx, crud.ListRequest{Limit: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if !page2.HasPrev {
		t.Errorf("page2 HasPrev = false, want true")
	}
	if got := idsOf(page2.Items, pc.idOf); !equalStrings(got, desc[2:4]) {
		t.Errorf("page2 = %v, want %v", got, desc[2:4])
	}
	// Only one row precedes the page-1 cursor, so page 2's prior window is
	// partial ⇒ empty PreviousCursor meaning "the previous page is the first page".
	if page2.PreviousCursor != "" {
		t.Errorf("page2 PreviousCursor = %q, want empty (partial prior window)", page2.PreviousCursor)
	}
	back := backPage(t, pc, page2.PreviousCursor)
	if got := idsOf(back.Items, pc.idOf); !equalStrings(got, desc[0:2]) {
		t.Errorf("previous of page2 = %v, want page1 %v", got, desc[0:2])
	}

	page3, err := pc.list(ctx, crud.ListRequest{Limit: 2, Cursor: page2.NextCursor})
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if !page3.HasPrev {
		t.Errorf("page3 HasPrev = false, want true")
	}
	if page3.PreviousCursor == "" {
		t.Errorf("page3 PreviousCursor empty, want a full prior window")
	}
	back3 := backPage(t, pc, page3.PreviousCursor)
	if got := idsOf(back3.Items, pc.idOf); !equalStrings(got, desc[2:4]) {
		t.Errorf("previous of page3 = %v, want page2 %v", got, desc[2:4])
	}
}

// runOffsetModeCase asserts offset traversal (explicit StrategyOffset) yields the
// identical ID sequence as cursor traversal, HasPrev iff offset > 0, that offset
// pages emit no cursors at any offset, and that Offset 0 under the offset strategy
// is the first page.
func runOffsetModeCase[T any](t *testing.T, pc pagedCase[T], created []T) {
	t.Helper()
	ctx := context.Background()

	cursorIDs := pageAllOrdered(t, pc, crud.Order{}, 2)

	var offsetIDs []string
	for off := 0; off < 100; off += 2 {
		page, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Offset: off})
		if err != nil {
			t.Fatalf("offset page at %d: %v", off, err)
		}
		offsetIDs = append(offsetIDs, idsOf(page.Items, pc.idOf)...)
		// Offset strategy emits no cursors at any offset — the caller does the
		// offset arithmetic.
		if page.NextCursor != "" || page.PreviousCursor != "" {
			t.Errorf("offset page at %d carried a cursor (next=%q prev=%q)", off, page.NextCursor, page.PreviousCursor)
		}
		if want := off > 0; page.HasPrev != want {
			t.Errorf("offset page at %d HasPrev = %v, want %v", off, page.HasPrev, want)
		}
		if !page.HasMore {
			break
		}
	}
	if !equalStrings(offsetIDs, cursorIDs) {
		t.Errorf("offset traversal = %v, want cursor traversal %v", offsetIDs, cursorIDs)
	}

	// OffsetZero: the offset strategy with Offset 0 is the first page — HasPrev
	// false and no cursors. This is the wart the explicit strategy fixes: Offset 0
	// no longer silently means cursor mode.
	zero, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("offset-zero page: %v", err)
	}
	if zero.HasPrev {
		t.Errorf("offset-zero HasPrev = true, want false")
	}
	if zero.NextCursor != "" || zero.PreviousCursor != "" {
		t.Errorf("offset-zero carried a cursor (next=%q prev=%q)", zero.NextCursor, zero.PreviousCursor)
	}
	if got, want := idsOf(zero.Items, pc.idOf), cursorIDs[:len(zero.Items)]; !equalStrings(got, want) {
		t.Errorf("offset-zero page = %v, want first page %v", got, want)
	}
}

// runWithCountCase asserts Total equals the filtered row count in both modes and
// is nil when unrequested.
func runWithCountCase[T any](t *testing.T, pc pagedCase[T], created []T, wantTotal int) {
	t.Helper()
	ctx := context.Background()

	cursorPage, err := pc.list(ctx, crud.ListRequest{Limit: 2, WithCount: true})
	if err != nil {
		t.Fatalf("cursor+count: %v", err)
	}
	if cursorPage.Total == nil || *cursorPage.Total != int64(wantTotal) {
		t.Errorf("cursor-mode Total = %v, want %d", cursorPage.Total, wantTotal)
	}

	offsetPage, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Offset: 2, WithCount: true})
	if err != nil {
		t.Fatalf("offset+count: %v", err)
	}
	if offsetPage.Total == nil || *offsetPage.Total != int64(wantTotal) {
		t.Errorf("offset-mode Total = %v, want %d", offsetPage.Total, wantTotal)
	}

	noCount, err := pc.list(ctx, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("no-count: %v", err)
	}
	if noCount.Total != nil {
		t.Errorf("Total = %v, want nil when count not requested", *noCount.Total)
	}
}

// runStaleCursorCase asserts a cursor minted under a different sort field is
// treated as the first page (no error, no skew). The order field is the only
// staleness key the cursor codec carries, so a token authored for a different
// column decodes to nil and the store returns the first page.
func runStaleCursorCase[T any](t *testing.T, pc pagedCase[T], created []T) {
	t.Helper()
	ctx := context.Background()

	first, err := pc.list(ctx, crud.ListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}

	stale, err := crud.EncodeCursor("updated_at", pc.createdAt(created[0]), pc.idOf(created[0]))
	if err != nil {
		t.Fatalf("EncodeCursor: %v", err)
	}
	got, err := pc.list(ctx, crud.ListRequest{Limit: 2, Cursor: stale})
	if err != nil {
		t.Fatalf("stale cursor: err=%v, want first page", err)
	}
	if g, w := idsOf(got.Items, pc.idOf), idsOf(first.Items, pc.idOf); !equalStrings(g, w) {
		t.Errorf("stale cursor = %v, want first page %v (treated as first page)", g, w)
	}
	if got.HasPrev {
		t.Errorf("stale-cursor first page HasPrev = true, want false")
	}
}

// runCursorOffsetExclusiveCase asserts each per-strategy conflict is rejected
// with the invalid-input kind: a cursor strategy carrying a non-zero offset, and
// an offset strategy carrying a cursor.
func runCursorOffsetExclusiveCase[T any](t *testing.T, pc pagedCase[T]) {
	t.Helper()
	ctx := context.Background()
	if _, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyCursor, Limit: 2, Offset: 2}); !errors.Is(err, errs.ErrInvalidInput) {
		t.Errorf("cursor strategy + offset: err=%v, want ErrInvalidInput", err)
	}
	if _, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Cursor: "anything"}); !errors.Is(err, errs.ErrInvalidInput) {
		t.Errorf("offset strategy + cursor: err=%v, want ErrInvalidInput", err)
	}
}

// backPage requests the previous page: an empty previousCursor means "the
// previous page is the first page", so a first-page request is issued.
func backPage[T any](t *testing.T, pc pagedCase[T], previousCursor string) crud.Page[T] {
	t.Helper()
	page, err := pc.list(context.Background(), crud.ListRequest{Limit: 2, Cursor: previousCursor})
	if err != nil {
		t.Fatalf("previous page: %v", err)
	}
	return page
}

// sortedIDs returns the ids of items sorted by (created_at, id) in the given
// direction — the total order every paginated port must page in.
func sortedIDs[T any](items []T, idOf func(T) string, createdAt func(T) time.Time, asc bool) []string {
	sorted := append([]T(nil), items...)
	sort.Slice(sorted, func(i, j int) bool {
		ti, tj := createdAt(sorted[i]), createdAt(sorted[j])
		if !ti.Equal(tj) {
			if asc {
				return ti.Before(tj)
			}
			return ti.After(tj)
		}
		if asc {
			return idOf(sorted[i]) < idOf(sorted[j])
		}
		return idOf(sorted[i]) > idOf(sorted[j])
	})
	return idsOf(sorted, idOf)
}

// idsOf projects each item's id.
func idsOf[T any](items []T, idOf func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = idOf(it)
	}
	return out
}

// pageAllOrdered pages forward through the whole population under order, threading
// order into every request, and returns the collected ids in traversal order.
func pageAllOrdered[T any](t *testing.T, pc pagedCase[T], order crud.Order, limit int) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	cursor := ""
	for i := 0; i < 100; i++ { // bound against a runaway cursor
		page, err := pc.list(ctx, crud.ListRequest{Limit: limit, Cursor: cursor, Order: order})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		ids = append(ids, idsOf(page.Items, pc.idOf)...)
		if !page.HasMore || page.NextCursor == "" {
			return ids
		}
		cursor = page.NextCursor
	}
	t.Fatalf("pageAllOrdered did not terminate")
	return nil
}

// --- seeds for the paginated-port case family ---
//
// familyMinutes is the created_at spread each seed uses: six rows with one
// same-created_at pair (indices 2 and 3) so the family exercises three pages of
// two plus the id tiebreak.
var familyMinutes = []int{0, 1, 2, 2, 3, 4}

func seedServiceAccounts(t *testing.T, repo serviceaccount.ServiceAccountRepository) []serviceaccount.ServiceAccount {
	t.Helper()
	ctx := context.Background()
	created := make([]serviceaccount.ServiceAccount, 0, len(familyMinutes))
	for i, m := range familyMinutes {
		sa, err := serviceaccount.New(ids, fmt.Sprintf("fam-sa-%d", i), "", "admin", false, "", suiteBase.Add(time.Duration(m)*time.Minute))
		if err != nil {
			t.Fatalf("serviceaccount.New: %v", err)
		}
		c, err := repo.Create(ctx, sa)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}
	return created
}

func seedAPIKeys(t *testing.T, repo apikey.APIKeyRepository) ([]apikey.APIKey, int) {
	t.Helper()
	created := make([]apikey.APIKey, 0, len(familyMinutes))
	for i, m := range familyMinutes {
		k := mustCreateAPIKey(t, repo, "sa-seed", fmt.Sprintf("fam-k-%d", i), fmt.Sprintf("fam-hash-%d", i), time.Time{}, suiteBase.Add(time.Duration(m)*time.Minute))
		created = append(created, k)
	}
	// Foreign rows under a different service account must not be listed or counted.
	mustCreateAPIKey(t, repo, "sa-foreign", "fk-0", "fam-foreign-0", time.Time{}, suiteBase)
	mustCreateAPIKey(t, repo, "sa-foreign", "fk-1", "fam-foreign-1", time.Time{}, suiteBase.Add(time.Minute))
	return created, len(created)
}

func seedSecurityEvents(t *testing.T, repo securityevent.SecurityEventRepository) ([]securityevent.SecurityEvent, int) {
	t.Helper()
	ctx := context.Background()
	created := make([]securityevent.SecurityEvent, 0, len(familyMinutes))
	for _, m := range familyMinutes {
		evt := securityevent.New(ids, securityevent.TypeLogin, securityevent.StatusSuccess, suiteBase.Add(time.Duration(m)*time.Minute))
		evt.UserID = "u-seed"
		c, err := repo.Create(ctx, evt)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}
	// Foreign rows under a different user must not be listed or counted.
	for i := 0; i < 2; i++ {
		evt := securityevent.New(ids, securityevent.TypeLogin, securityevent.StatusSuccess, suiteBase.Add(time.Duration(i)*time.Minute))
		evt.UserID = "u-foreign"
		if _, err := repo.Create(ctx, evt); err != nil {
			t.Fatalf("Create(foreign): %v", err)
		}
	}
	return created, len(created)
}

func seedInvitationsByResource(t *testing.T, repo invitation.InvitationRepository) ([]invitation.Invitation, int) {
	t.Helper()
	ctx := context.Background()
	created := make([]invitation.Invitation, 0, len(familyMinutes))
	for i, m := range familyMinutes {
		// Distinct identifier per row keeps each a distinct pending tuple.
		inv := mustNewInvitation(t, "project", "res-seed", "member", fmt.Sprintf("u%d@seed.example", i), "inviter-1", fmt.Sprintf("fam-r-%d", i), suiteBase.Add(time.Duration(m)*time.Minute))
		c, err := repo.Create(ctx, inv)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}
	// Foreign rows under a different resource must not be listed or counted.
	for i := 0; i < 2; i++ {
		inv := mustNewInvitation(t, "project", "res-foreign", "member", fmt.Sprintf("f%d@seed.example", i), "inviter-1", fmt.Sprintf("fam-rf-%d", i), suiteBase.Add(time.Duration(i)*time.Minute))
		if _, err := repo.Create(ctx, inv); err != nil {
			t.Fatalf("Create(foreign): %v", err)
		}
	}
	return created, len(created)
}

func seedInvitationsBySubject(t *testing.T, repo invitation.InvitationRepository) ([]invitation.Invitation, int) {
	t.Helper()
	ctx := context.Background()
	created := make([]invitation.Invitation, 0, len(familyMinutes))
	for i, m := range familyMinutes {
		// Same identifier, distinct resource keeps each a distinct pending tuple.
		inv := mustNewInvitation(t, "project", fmt.Sprintf("res-%d", i), "member", "subject@seed.example", "inviter-1", fmt.Sprintf("fam-s-%d", i), suiteBase.Add(time.Duration(m)*time.Minute))
		c, err := repo.Create(ctx, inv)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, c)
	}
	// Foreign rows for a different invitee must not be listed or counted.
	for i := 0; i < 2; i++ {
		inv := mustNewInvitation(t, "project", fmt.Sprintf("res-other-%d", i), "member", "other@seed.example", "inviter-1", fmt.Sprintf("fam-sf-%d", i), suiteBase.Add(time.Duration(i)*time.Minute))
		if _, err := repo.Create(ctx, inv); err != nil {
			t.Fatalf("Create(foreign): %v", err)
		}
	}
	return created, len(created)
}

// --- amended D10: database-generated keys on empty ID ---

// The five tests below prove the Create side of the cryptids.Database strategy:
// an entity constructed with the Database generator reaches the store with an
// empty ID, and Create must hand back a store-assigned, non-empty key under
// which the row is readable. SQL adapters satisfy this by omitting the id
// column and reading the schema default back with RETURNING (migration
// 0012_id_defaults); memory implementations assign at insert.

func testUsersDBGeneratedID(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, err := user.NewUser(dbIDs, "dbgen@example.com", "DB Gen", suiteBase)
	if err != nil {
		t.Fatalf("NewUser: %v", err)
	}
	if u.ID != "" {
		t.Fatalf("Database strategy minted a non-empty ID: %q", u.ID)
	}
	created, err := repos.Users.Create(ctx, u)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty ID — the store did not assign a database-generated key")
	}
	got, err := repos.Users.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get by generated id: %v", err)
	}
	if got.Email != "dbgen@example.com" {
		t.Errorf("row under generated key has email %q", got.Email)
	}
}

func testServiceAccountsDBGeneratedID(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	sa, err := serviceaccount.New(dbIDs, "db-gen", "", "admin-1", false, "", suiteBase)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	created, err := repos.ServiceAccounts.Create(ctx, sa)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty ID — the store did not assign a database-generated key")
	}
	got, err := repos.ServiceAccounts.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get by generated id: %v", err)
	}
	if got.Name != "db-gen" {
		t.Errorf("row under generated key has name %q", got.Name)
	}
}

func testAPIKeysDBGeneratedID(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	k, err := apikey.New(dbIDs, "sa-1", "db-gen", "prefix12", "hash-dbgen", time.Time{}, suiteBase)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	created, err := repos.APIKeys.Create(ctx, k)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty ID — the store did not assign a database-generated key")
	}
	got, err := repos.APIKeys.GetByHash(ctx, "hash-dbgen")
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("GetByHash id %q, want the generated key %q", got.ID, created.ID)
	}
}

func testSecurityEventsDBGeneratedID(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	evt := securityevent.New(dbIDs, securityevent.TypeLogin, securityevent.StatusSuccess, suiteBase)
	created, err := repos.SecurityEvents.Create(ctx, evt)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty ID — the store did not assign a database-generated key")
	}
}

func testInvitationsDBGeneratedID(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	inv, err := invitation.New(dbIDs, "project", "p-dbgen", "member", "dbgen@example.com", "", "inviter-1", "hash-dbgen-inv", false, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	created, err := repos.Invitations.Create(ctx, inv)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty ID — the store did not assign a database-generated key")
	}
	got, err := repos.Invitations.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get by generated id: %v", err)
	}
	if got.TokenHash != "hash-dbgen-inv" {
		t.Errorf("row under generated key has token hash %q", got.TokenHash)
	}
}
