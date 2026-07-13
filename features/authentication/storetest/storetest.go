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
// implementations. The key/email-lookup ports (users, sessions, oauth) instead
// exercise the sentinel contract, uniqueness, upsert, and expired-at-read
// behavior.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/features/authentication"
	"github.com/gopernicus/gopernicus/features/authentication/domain/apikey"
	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/deliveryjob"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/oauthstate"
	"github.com/gopernicus/gopernicus/features/authentication/domain/passwordreset"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/serviceaccount"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
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
		t.Run("DBGeneratedIDOnEmpty", func(t *testing.T) { testUsersDBGeneratedID(t, newRepos(t)) })
	})

	// UserIdentifiers backs the v3 identity-discovery rail (design §2.2). It is
	// optional here only for forward-compatibility with a store that has not yet
	// wired it; the bundled stores, the reference, and authmem all do. When absent
	// the group skips LOUDLY — a silent green would falsely claim identifier
	// conformance.
	t.Run("UserIdentifiers", func(t *testing.T) {
		if newRepos(t).Identifiers == nil {
			t.Skip("Identifiers not wired — identifier conformance NOT verified for this Repositories")
		}
		t.Run("AtomicCreateDBGeneratedIDs", func(t *testing.T) { testIdentifiersAtomicCreate(t, newRepos(t)) })
		t.Run("AtomicCreateRollbackOnLostClaim", func(t *testing.T) { testIdentifiersCreateRollback(t, newRepos(t)) })
		t.Run("ActiveOnlyReads", func(t *testing.T) { testIdentifiersActiveOnly(t, newRepos(t)) })
		t.Run("LoginRecoveryLookup", func(t *testing.T) { testIdentifiersLoginRecoveryLookup(t, newRepos(t)) })
		t.Run("MultipleIdentifiersPerUser", func(t *testing.T) { testIdentifiersMultiple(t, newRepos(t)) })
		t.Run("SharedNotificationOnlyAddress", func(t *testing.T) { testIdentifiersSharedNotification(t, newRepos(t)) })
		t.Run("AuthenticationClaimCollision", func(t *testing.T) { testIdentifiersClaimCollision(t, newRepos(t)) })
		t.Run("OneActivePrimaryPerUserKind", func(t *testing.T) { testIdentifiersOnePrimary(t, newRepos(t)) })
		t.Run("ApplyVerifiedChangeUseTimestampRoundTrip", func(t *testing.T) { testIdentifiersApplyRoundTrip(t, newRepos(t)) })
		t.Run("ApplyVerifiedChangeReplacementHistory", func(t *testing.T) { testIdentifiersReplacementHistory(t, newRepos(t)) })
		t.Run("ApplyVerifiedChangeRevisionConflict", func(t *testing.T) { testIdentifiersRevisionConflict(t, newRepos(t)) })
		t.Run("ApplyVerifiedChangeRollbackOnLostClaim", func(t *testing.T) { testIdentifiersApplyRollback(t, newRepos(t)) })
		t.Run("ConcurrentClaimArbitration", func(t *testing.T) { testIdentifiersConcurrentClaim(t, newRepos(t)) })
	})

	t.Run("Passwords", func(t *testing.T) {
		t.Run("SetGetUpsert", func(t *testing.T) { testPasswords(t, newRepos(t)) })
		t.Run("AbsentNotFound", func(t *testing.T) { testPasswordsAbsent(t, newRepos(t)) })
	})

	t.Run("Sessions", func(t *testing.T) {
		t.Run("GetByIDRoundTrip", func(t *testing.T) { testSessionsGetByID(t, newRepos(t)) })
		t.Run("GetByRefreshHash", func(t *testing.T) { testSessionsGetByRefreshHash(t, newRepos(t)) })
		t.Run("Rotation", func(t *testing.T) { testSessionsRotation(t, newRepos(t)) })
		t.Run("SingleSlotGrace", func(t *testing.T) { testSessionsSingleSlotGrace(t, newRepos(t)) })
		t.Run("RotateCASConflict", func(t *testing.T) { testSessionsRotateConflict(t, newRepos(t)) })
		t.Run("ConsumeGrace", func(t *testing.T) { testSessionsConsumeGrace(t, newRepos(t)) })
		t.Run("EmptyPreviousGuard", func(t *testing.T) { testSessionsEmptyPreviousGuard(t, newRepos(t)) })
		t.Run("RotationKeepsExpiresAt", func(t *testing.T) { testSessionsRotationKeepsExpiry(t, newRepos(t)) })
		t.Run("DeleteAndDeleteByUser", func(t *testing.T) { testSessionsDelete(t, newRepos(t)) })
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
		t.Run("ListBySubjectKindIsolation", func(t *testing.T) { testInvitationsListBySubjectKindIsolation(t, newRepos(t)) })
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
					return repos.Invitations.ListBySubject(ctx, identity.KindEmail, "subject@seed.example", req)
				},
				func(t *testing.T, repos auth.Repositories) ([]invitation.Invitation, int) {
					return seedInvitationsBySubject(t, repos.Invitations)
				},
				func(inv invitation.Invitation) string { return inv.ID },
				func(inv invitation.Invitation) time.Time { return inv.CreatedAt },
			)
		})
	})

	// Challenges backs the atomic secret rail (design §3.2). It is optional here
	// (the SQL adapters land in phase 2; phase 0 proves the contract against the
	// reference and authmem). When present it is exercised in full; when absent
	// the group skips LOUDLY — a silent green would falsely claim atomic-challenge
	// conformance.
	t.Run("Challenges", func(t *testing.T) {
		if newRepos(t).Challenges == nil {
			t.Skip("Challenges not wired — atomic-challenge conformance NOT verified for this Repositories")
		}
		t.Run("ReplaceIsSingleActivePerUserPurpose", func(t *testing.T) { testChallengeReplace(t, newRepos(t)) })
		t.Run("ConsumeCodeRedeem", func(t *testing.T) { testChallengeConsumeCodeRedeem(t, newRepos(t)) })
		t.Run("ConsumeCodeKeyCandidateSelection", func(t *testing.T) { testChallengeKeyCandidate(t, newRepos(t)) })
		t.Run("ConsumeCodeContextMismatchConsumes", func(t *testing.T) { testChallengeContextMismatch(t, newRepos(t)) })
		t.Run("ConsumeCodeAttemptIncrementAndLockout", func(t *testing.T) { testChallengeAttemptsLockout(t, newRepos(t)) })
		t.Run("ConsumeCodeExpiredDeletes", func(t *testing.T) { testChallengeConsumeCodeExpired(t, newRepos(t)) })
		t.Run("ConsumeCodeEmptyDigestRejected", func(t *testing.T) { testChallengeEmptyDigest(t, newRepos(t)) })
		t.Run("ConsumeTokenRedeem", func(t *testing.T) { testChallengeConsumeToken(t, newRepos(t)) })
		t.Run("ConsumeTokenExpiredDeletes", func(t *testing.T) { testChallengeConsumeTokenExpired(t, newRepos(t)) })
		t.Run("ConsumeTokenEmptyNeverMatches", func(t *testing.T) { testChallengeConsumeTokenEmpty(t, newRepos(t)) })
		t.Run("PurgeExpiredBounded", func(t *testing.T) { testChallengePurgeExpired(t, newRepos(t)) })
		t.Run("ConcurrentCodeSingleWinner", func(t *testing.T) { testChallengeConcurrentCode(t, newRepos(t)) })
		t.Run("ConcurrentTokenSingleWinner", func(t *testing.T) { testChallengeConcurrentToken(t, newRepos(t)) })
		t.Run("ConcurrentLockoutSingleWinner", func(t *testing.T) { testChallengeConcurrentLockout(t, newRepos(t)) })
	})

	// ContactChanges backs the pending-value flow state of an identifier
	// add/change (design §2.4). Optional here (the SQL adapters land in phase 2;
	// phase 1 proves the contract against the reference and authmem). When absent
	// the group skips LOUDLY — a silent green would falsely claim contact-change
	// conformance.
	t.Run("ContactChanges", func(t *testing.T) {
		if newRepos(t).ContactChanges == nil {
			t.Skip("ContactChanges not wired — contact-change conformance NOT verified for this Repositories")
		}
		t.Run("CreateReplacesPriorPerUserKind", func(t *testing.T) { testContactChangeReplace(t, newRepos(t)) })
		t.Run("ValueAndUseRoundTrip", func(t *testing.T) { testContactChangeRoundTrip(t, newRepos(t)) })
		t.Run("ConsumeIsSingleUse", func(t *testing.T) { testContactChangeSingleUse(t, newRepos(t)) })
		t.Run("ConsumeExpiredDeletes", func(t *testing.T) { testContactChangeExpired(t, newRepos(t)) })
		t.Run("ConsumeMissingNotFound", func(t *testing.T) { testContactChangeMissing(t, newRepos(t)) })
		t.Run("ConcurrentConsumeSingleWinner", func(t *testing.T) { testContactChangeConcurrentConsume(t, newRepos(t)) })
	})

	// AuthenticationGrants backs recent-authentication / step-up (design §5.0).
	// Optional for the same reason as Challenges. When absent the group skips
	// LOUDLY.
	t.Run("AuthenticationGrants", func(t *testing.T) {
		if newRepos(t).AuthenticationGrants == nil {
			t.Skip("AuthenticationGrants not wired — step-up grant conformance NOT verified for this Repositories")
		}
		t.Run("ConsumeSpendsMatchingGrant", func(t *testing.T) { testGrantConsume(t, newRepos(t)) })
		t.Run("ConsumeContextMismatchNotFound", func(t *testing.T) { testGrantContextMismatch(t, newRepos(t)) })
		t.Run("ConsumeExpiredDeletes", func(t *testing.T) { testGrantExpired(t, newRepos(t)) })
		t.Run("ConsumeSingleUse", func(t *testing.T) { testGrantSingleUse(t, newRepos(t)) })
		t.Run("MetadataRoundTrip", func(t *testing.T) { testGrantMetadataRoundTrip(t, newRepos(t)) })
		t.Run("DeleteBySessionCascade", func(t *testing.T) { testGrantDeleteBySession(t, newRepos(t)) })
		t.Run("ConcurrentConsumeSingleWinner", func(t *testing.T) { testGrantConcurrentConsume(t, newRepos(t)) })
	})

	// CredentialMutations backs the revision-serialized credential/identifier
	// mutation rail (design §5.6). Optional here (the SQL adapters land in phase
	// 2). This suite proves the revision-CAS Apply mechanics drivable through the
	// port and its credential sibling repos; the policy+reload single-winner proof
	// (which needs the phase-1 identifier tables) is a reference-specific race test
	// in reference_test.go. When absent the group skips LOUDLY.
	t.Run("CredentialMutations", func(t *testing.T) {
		if newRepos(t).CredentialMutations == nil {
			t.Skip("CredentialMutations not wired — revision-CAS conformance NOT verified for this Repositories")
		}
		t.Run("SnapshotProjectsCredentials", func(t *testing.T) { testCredentialSnapshot(t, newRepos(t)) })
		t.Run("SnapshotUnknownUserNotFound", func(t *testing.T) { testCredentialSnapshotUnknown(t, newRepos(t)) })
		t.Run("ApplyIncrementsRevisionOnce", func(t *testing.T) { testCredentialApplyRevision(t, newRepos(t)) })
		t.Run("ApplyStaleRevisionConflict", func(t *testing.T) { testCredentialApplyStale(t, newRepos(t)) })
		t.Run("ConcurrentApplySingleWinner", func(t *testing.T) { testCredentialConcurrentApply(t, newRepos(t)) })
	})

	// DeliveryJobs backs the durable enumeration-safe outbox (design §6.1.1).
	// Optional here (the SQL adapters land in phase 2; phase 0 proves the contract
	// against the reference and authmem). When present it is exercised in full;
	// when absent the group skips LOUDLY — a silent green would falsely claim
	// at-least-once outbox conformance.
	t.Run("DeliveryJobs", func(t *testing.T) {
		if newRepos(t).DeliveryJobs == nil {
			t.Skip("DeliveryJobs not wired — durable-outbox conformance NOT verified for this Repositories")
		}
		t.Run("EnqueueIsIdempotentByKey", func(t *testing.T) { testDeliveryEnqueueIdempotent(t, newRepos(t)) })
		t.Run("ReplaceCancelsPriorPending", func(t *testing.T) { testDeliveryReplace(t, newRepos(t)) })
		t.Run("ClaimLeasesDueJob", func(t *testing.T) { testDeliveryClaim(t, newRepos(t)) })
		t.Run("ClaimSkipsFutureAndLeased", func(t *testing.T) { testDeliveryClaimNotDue(t, newRepos(t)) })
		t.Run("ClaimReclaimsExpiredLease", func(t *testing.T) { testDeliveryClaimReclaim(t, newRepos(t)) })
		t.Run("SucceedIsIdempotent", func(t *testing.T) { testDeliverySucceed(t, newRepos(t)) })
		t.Run("RetryWithBackoff", func(t *testing.T) { testDeliveryRetry(t, newRepos(t)) })
		t.Run("FailIsTerminal", func(t *testing.T) { testDeliveryFail(t, newRepos(t)) })
		t.Run("CompleteRejectsReclaimedLease", func(t *testing.T) { testDeliveryReclaimedLease(t, newRepos(t)) })
		t.Run("CancelTerminates", func(t *testing.T) { testDeliveryCancel(t, newRepos(t)) })
		t.Run("PurgeTerminalBounded", func(t *testing.T) { testDeliveryPurge(t, newRepos(t)) })
		t.Run("GetLatestByIdempotencyKey", func(t *testing.T) { testDeliveryGetLatest(t, newRepos(t)) })
		t.Run("ConcurrentClaimSingleWinner", func(t *testing.T) { testDeliveryConcurrentClaim(t, newRepos(t)) })
	})

	// PasswordResets backs the atomic reset composition (design §5.9). Optional
	// here (the SQL adapters land alongside it in phase 3); when absent the group
	// skips LOUDLY — a silent green would falsely claim atomic-reset conformance.
	// The success case exercises the full composition, so it also requires the
	// Passwords/Sessions/Challenges (and, when wired, AuthenticationGrants) ports.
	t.Run("PasswordResets", func(t *testing.T) {
		if newRepos(t).PasswordResets == nil {
			t.Skip("PasswordResets not wired — atomic-reset conformance NOT verified for this Repositories")
		}
		t.Run("RedeemAppliesFullComposition", func(t *testing.T) { testPasswordResetRedeem(t, newRepos(t)) })
		t.Run("UnknownTokenGenericFailure", func(t *testing.T) { testPasswordResetUnknown(t, newRepos(t)) })
		t.Run("ExpiredTokenGenericFailure", func(t *testing.T) { testPasswordResetExpired(t, newRepos(t)) })
		t.Run("ConcurrentRedeemSingleWinner", func(t *testing.T) { testPasswordResetConcurrent(t, newRepos(t)) })
	})
}

// --- Users ---

func testUsersCRUD(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Users

	u := user.NewUser(ids, "Alice", suiteBase)
	ident, err := identifier.New(ids, idNorm, "", identifier.KindEmail, "alice@example.com", loginRecoveryUses, true, suiteBase, suiteBase)
	if err != nil {
		t.Fatalf("identifier.New: %v", err)
	}
	created, createdIdent, err := repo.CreateWithPrimaryIdentifier(ctx, u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier: %v", err)
	}
	if createdIdent.NormalizedValue != "alice@example.com" {
		t.Errorf("primary identifier not normalized: %q", createdIdent.NormalizedValue)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.DisplayName != "Alice" {
		t.Fatalf("Get: name=%q err=%v", got.DisplayName, err)
	}

	got.DisplayName = "Alice B"
	got.UpdatedAt = suiteBase.Add(time.Minute)
	if _, err := repo.Update(ctx, got.ID, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reget, err := repo.Get(ctx, created.ID)
	if err != nil || reget.DisplayName != "Alice B" {
		t.Fatalf("Get after Update: name=%q err=%v", reget.DisplayName, err)
	}
}

func testUsersAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Users

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	absent := user.NewUser(ids, "Ghost", suiteBase)
	if _, err := repo.Update(ctx, "nope", absent); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Update(absent): err=%v, want ErrNotFound", err)
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
	if _, err := repos.Passwords.Get(ctx, "nobody"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
}

// --- Sessions ---

// newSession builds a session row with an explicit refresh-token hash, so the
// rotation/grace cases can pin exact hash values rather than opaque minted ones.
// The id is minted (app-owned, §1.1); createdAt/expiresAt derive from ttl.
func newSession(userID, refreshHash string, ttl time.Duration, now time.Time) session.Session {
	s, _ := session.NewSession(userID, ttl, now)
	s.RefreshTokenHash = refreshHash
	return s
}

// case 1: Get(id) round-trip; unknown → ErrNotFound; expired → ErrExpired.
func testSessionsGetByID(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	sess := newSession("u1", "hash-live", time.Hour, time.Now())
	created, err := repo.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.UserID != "u1" || got.RefreshTokenHash != "hash-live" {
		t.Fatalf("Get: %+v err=%v", got, err)
	}
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(unknown): err=%v, want ErrNotFound", err)
	}

	expired := newSession("u1", "hash-expired", time.Minute, time.Now().Add(-time.Hour))
	if _, err := repo.Create(ctx, expired); err != nil {
		t.Fatalf("Create(expired): %v", err)
	}
	if _, err := repo.Get(ctx, expired.ID); !errors.Is(err, sdk.ErrExpired) {
		t.Errorf("Get(expired): err=%v, want ErrExpired", err)
	}
}

// case 2: GetByRefreshHash current-match round-trip; unknown → ErrNotFound;
// an expired row is returned VERBATIM (expiry is a service branch).
func testSessionsGetByRefreshHash(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	sess := newSession("u1", "hash-current", time.Hour, time.Now())
	created, err := repo.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, match, err := repo.GetByRefreshHash(ctx, "hash-current")
	if err != nil || got.ID != created.ID || match != session.RefreshMatchCurrent {
		t.Fatalf("GetByRefreshHash(current): id=%q match=%d err=%v", got.ID, match, err)
	}
	if _, _, err := repo.GetByRefreshHash(ctx, "no-such-hash"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetByRefreshHash(unknown): err=%v, want ErrNotFound", err)
	}

	// An expired row is still RETURNED verbatim (no store-side expiry filter).
	expired := newSession("u2", "hash-exp-refresh", time.Minute, time.Now().Add(-time.Hour))
	if _, err := repo.Create(ctx, expired); err != nil {
		t.Fatalf("Create(expired): %v", err)
	}
	got, match, err = repo.GetByRefreshHash(ctx, "hash-exp-refresh")
	if err != nil || got.ID != expired.ID || match != session.RefreshMatchCurrent {
		t.Errorf("GetByRefreshHash(expired) must return the row verbatim: id=%q match=%d err=%v", got.ID, match, err)
	}
}

// case 3: rotation sets previous=hashA, previous_used=false, rotation_count++;
// resolve(hashB)=(row, current); resolve(hashA)=(row, previous).
func testSessionsRotation(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	sess := newSession("u1", "hashA", time.Hour, time.Now())
	created, err := repo.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Rotate(ctx, created.ID, "hashA", "hashB"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get after Rotate: %v", err)
	}
	if got.RefreshTokenHash != "hashB" || got.PreviousRefreshTokenHash != "hashA" || got.PreviousUsed || got.RotationCount != 1 {
		t.Errorf("Rotate state = {current=%q prev=%q used=%v count=%d}, want {hashB hashA false 1}",
			got.RefreshTokenHash, got.PreviousRefreshTokenHash, got.PreviousUsed, got.RotationCount)
	}
	if r, m, err := repo.GetByRefreshHash(ctx, "hashB"); err != nil || r.ID != created.ID || m != session.RefreshMatchCurrent {
		t.Errorf("resolve(hashB) = (%q, %d, %v), want (%q, current, nil)", r.ID, m, err, created.ID)
	}
	if r, m, err := repo.GetByRefreshHash(ctx, "hashA"); err != nil || r.ID != created.ID || m != session.RefreshMatchPrevious {
		t.Errorf("resolve(hashA) = (%q, %d, %v), want (%q, previous, nil)", r.ID, m, err, created.ID)
	}
}

// case 4: single-slot grace — a second rotation hashB→hashC drops hashA from the
// previous slot, so resolve(hashA) → ErrNotFound.
func testSessionsSingleSlotGrace(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	sess := newSession("u1", "hashA", time.Hour, time.Now())
	created, err := repo.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Rotate(ctx, created.ID, "hashA", "hashB"); err != nil {
		t.Fatalf("Rotate A→B: %v", err)
	}
	if err := repo.Rotate(ctx, created.ID, "hashB", "hashC"); err != nil {
		t.Fatalf("Rotate B→C: %v", err)
	}
	if _, _, err := repo.GetByRefreshHash(ctx, "hashA"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("resolve(hashA) after two rotations: err=%v, want ErrNotFound (single previous slot)", err)
	}
	if r, m, err := repo.GetByRefreshHash(ctx, "hashB"); err != nil || r.ID != created.ID || m != session.RefreshMatchPrevious {
		t.Errorf("resolve(hashB) = (%q, %d, %v), want (previous)", r.ID, m, err)
	}
}

// case 5: Rotate with a stale expectedCurrentHash → ErrRotationConflict, and the
// row is unchanged.
func testSessionsRotateConflict(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	sess := newSession("u1", "hashA", time.Hour, time.Now())
	created, err := repo.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Rotate(ctx, created.ID, "stale-hash", "hashZ"); !errors.Is(err, session.ErrRotationConflict) {
		t.Fatalf("Rotate(stale): err=%v, want ErrRotationConflict", err)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RefreshTokenHash != "hashA" || got.PreviousRefreshTokenHash != "" || got.RotationCount != 0 {
		t.Errorf("row changed after failed CAS: %+v", got)
	}
}

// case 6: ConsumeGrace flips previous_used once; a second call → ErrRotationConflict.
func testSessionsConsumeGrace(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	sess := newSession("u1", "hashA", time.Hour, time.Now())
	created, err := repo.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Rotate(ctx, created.ID, "hashA", "hashB"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if err := repo.ConsumeGrace(ctx, created.ID, "hashA"); err != nil {
		t.Fatalf("ConsumeGrace: %v", err)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil || !got.PreviousUsed {
		t.Fatalf("after ConsumeGrace: used=%v err=%v, want used", got.PreviousUsed, err)
	}
	if err := repo.ConsumeGrace(ctx, created.ID, "hashA"); !errors.Is(err, session.ErrRotationConflict) {
		t.Errorf("second ConsumeGrace: err=%v, want ErrRotationConflict", err)
	}
}

// case 7: empty-previous guard — a fresh session (NULL previous) round-trips, and
// GetByRefreshHash("") → ErrNotFound, never a fresh row.
func testSessionsEmptyPreviousGuard(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	sess := newSession("u1", "hash-fresh", time.Hour, time.Now())
	created, err := repo.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.PreviousRefreshTokenHash != "" {
		t.Fatalf("fresh session previous = %q err=%v, want empty", got.PreviousRefreshTokenHash, err)
	}
	if _, _, err := repo.GetByRefreshHash(ctx, ""); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf(`GetByRefreshHash(""): err=%v, want ErrNotFound (empty hash never matches)`, err)
	}
}

// case 8: rotation does not modify expires_at (D2 fixed horizon).
func testSessionsRotationKeepsExpiry(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	sess := newSession("u1", "hashA", time.Hour, time.Now())
	created, err := repo.Create(ctx, sess)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := created.ExpiresAt
	if err := repo.Rotate(ctx, created.ID, "hashA", "hashB"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.ExpiresAt.Equal(before) {
		t.Errorf("Rotate changed ExpiresAt: before=%v after=%v", before, got.ExpiresAt)
	}
}

// case 9: Delete(unknown) → ErrNotFound; DeleteByUser is idempotent and leaves
// other users' sessions intact.
func testSessionsDelete(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Sessions

	if err := repo.Delete(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Delete(unknown): err=%v, want ErrNotFound", err)
	}

	a1 := newSession("userA", "ha1", time.Hour, time.Now())
	a2 := newSession("userA", "ha2", time.Hour, time.Now())
	b1 := newSession("userB", "hb1", time.Hour, time.Now())
	for _, s := range []session.Session{a1, a2, b1} {
		if _, err := repo.Create(ctx, s); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	if err := repo.DeleteByUser(ctx, "userA"); err != nil {
		t.Fatalf("DeleteByUser(userA): %v", err)
	}
	for _, id := range []string{a1.ID, a2.ID} {
		if _, err := repo.Get(ctx, id); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("Get(userA session after DeleteByUser): err=%v, want ErrNotFound", err)
		}
	}
	if got, err := repo.Get(ctx, b1.ID); err != nil || got.UserID != "userB" {
		t.Errorf("userB session removed by DeleteByUser(userA): user=%q err=%v", got.UserID, err)
	}
	// Idempotent: a repeat with zero matching rows returns nil, not ErrNotFound.
	if err := repo.DeleteByUser(ctx, "userA"); err != nil {
		t.Errorf("second DeleteByUser(userA): err=%v, want nil", err)
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
	if _, err := repo.GetByProvider(ctx, "google", "google-123"); !errors.Is(err, sdk.ErrNotFound) {
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
	if _, err := repo.Create(ctx, b); !errors.Is(err, sdk.ErrAlreadyExists) {
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
	if _, err := repos.OAuthAccounts.GetByProvider(ctx, "google", "nope"); !errors.Is(err, sdk.ErrNotFound) {
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
	if err := repos.OAuthAccounts.Delete(ctx, "nobody", "google"); !errors.Is(err, sdk.ErrNotFound) {
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
	if _, err := repo.Consume(ctx, st.Token); !errors.Is(err, sdk.ErrNotFound) {
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
	if _, err := repo.Consume(ctx, st.Token); !errors.Is(err, sdk.ErrExpired) {
		t.Errorf("Consume(expired): err=%v, want ErrExpired", err)
	}
	// Row is gone (deleted regardless of expiry): a follow-up Consume → ErrNotFound.
	if _, err := repo.Consume(ctx, st.Token); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Consume after expired-delete: err=%v, want ErrNotFound", err)
	}
}

func testOAuthStatesUnknown(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.OAuthStates.Consume(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
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
	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get after Delete: err=%v, want ErrNotFound", err)
	}
}

func testServiceAccountsAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ServiceAccounts

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Get(absent): err=%v, want ErrNotFound", err)
	}
	absent, _ := serviceaccount.New(ids, "ghost", "", "admin", false, "", suiteBase)
	if _, err := repo.Update(ctx, "nope", absent); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Update(absent): err=%v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
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
	if _, err := repos.APIKeys.GetByHash(ctx, "no-such-hash"); !errors.Is(err, sdk.ErrNotFound) {
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
	if _, err := repo.Create(ctx, dup); !errors.Is(err, sdk.ErrAlreadyExists) {
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
	if err := repo.TouchLastUsed(ctx, "nope", at); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("TouchLastUsed(absent): err=%v, want ErrNotFound", err)
	}
}

func testAPIKeysRevokeAbsent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if err := repos.APIKeys.Revoke(ctx, "nope", suiteBase); !errors.Is(err, sdk.ErrNotFound) {
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

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, sdk.ErrNotFound) {
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
	if _, err := repo.Create(ctx, b); !errors.Is(err, sdk.ErrAlreadyExists) {
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
	if _, err := repo.Create(ctx, dup); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("Create(dup email tuple): err=%v, want ErrAlreadyExists", err)
	}
}

func testInvitationsTokenUnknown(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.Invitations.GetByTokenHash(ctx, "no-such-hash"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetByTokenHash(unknown): err=%v, want ErrNotFound", err)
	}
}

// testInvitationsTokenExpired asserts a token-hash read past ExpiresAt surfaces
// sdk.ErrExpired (mirroring the session/verification/oauthstate precedent).
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
	if _, err := repo.GetByTokenHash(ctx, "hash-expired"); !errors.Is(err, sdk.ErrExpired) {
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
	if _, err := repos.Invitations.UpdateStatus(ctx, "nope", invitation.StatusUpdate{Status: invitation.StatusCancelled, UpdatedAt: suiteBase}); !errors.Is(err, sdk.ErrNotFound) {
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
		return repo.ListBySubject(ctx, identity.KindEmail, "subject@example.com", req)
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
		return repo.ListBySubject(ctx, identity.KindEmail, "collide@example.com", req)
	}, 2)
	if !equalStrings(got, want) {
		t.Errorf("ListBySubject collision order = %v, want %v (id tiebreak)", got, want)
	}
}

// testInvitationsListBySubjectKindIsolation proves ListBySubject filters BOTH
// (identifier_kind, identifier): the same normalized string invited under email
// and under phone resolves to disjoint sets, so a kind-blind lookup can never
// cross-resolve one kind's invitations into another's (the design §7 kind-
// collision bug this filter fixes).
func testInvitationsListBySubjectKindIsolation(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Invitations

	// Same identifier string under two kinds — a contrived but exact collision.
	const shared = "+15550000001"
	emailInv, err := invitation.New(ids, "project", "res-e", "member", shared, identity.KindEmail, "inviter-1", "hash-iso-e", false, time.Hour, suiteBase)
	if err != nil {
		t.Fatalf("New(email): %v", err)
	}
	if _, err := repo.Create(ctx, emailInv); err != nil {
		t.Fatalf("Create(email): %v", err)
	}
	phoneInv, err := invitation.New(ids, "project", "res-p", "member", shared, identity.KindPhone, "inviter-1", "hash-iso-p", false, time.Hour, suiteBase)
	if err != nil {
		t.Fatalf("New(phone): %v", err)
	}
	if _, err := repo.Create(ctx, phoneInv); err != nil {
		t.Fatalf("Create(phone): %v", err)
	}

	emailPage, err := repo.ListBySubject(ctx, identity.KindEmail, shared, crud.ListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListBySubject(email): %v", err)
	}
	if len(emailPage.Items) != 1 || emailPage.Items[0].IdentifierKind != identity.KindEmail {
		t.Errorf("ListBySubject(email) leaked across kinds: %+v", emailPage.Items)
	}
	phonePage, err := repo.ListBySubject(ctx, identity.KindPhone, shared, crud.ListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListBySubject(phone): %v", err)
	}
	if len(phonePage.Items) != 1 || phonePage.Items[0].IdentifierKind != identity.KindPhone {
		t.Errorf("ListBySubject(phone) leaked across kinds: %+v", phonePage.Items)
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
	if _, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyCursor, Limit: 2, Offset: 2}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("cursor strategy + offset: err=%v, want ErrInvalidInput", err)
	}
	if _, err := pc.list(ctx, crud.ListRequest{Strategy: crud.StrategyOffset, Limit: 2, Cursor: "anything"}); !errors.Is(err, sdk.ErrInvalidInput) {
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
// column and reading the schema default back with RETURNING (the id DEFAULT
// each entity table's CREATE carries); memory implementations assign at insert.

func testUsersDBGeneratedID(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u := user.NewUser(dbIDs, "DB Gen", suiteBase)
	if u.ID != "" {
		t.Fatalf("Database strategy minted a non-empty ID: %q", u.ID)
	}
	ident, err := identifier.New(dbIDs, idNorm, "", identifier.KindEmail, "dbgen@example.com", loginRecoveryUses, true, suiteBase, suiteBase)
	if err != nil {
		t.Fatalf("identifier.New: %v", err)
	}
	created, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx, u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateWithPrimaryIdentifier returned an empty ID — the store did not assign a database-generated key")
	}
	if _, err := repos.Users.Get(ctx, created.ID); err != nil {
		t.Fatalf("Get by generated id: %v", err)
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

// --- Challenges (design §3.2) ---

// newChallenge builds a code/token challenge for the atomic-secret suite. keyID
// is empty for token challenges (no pepper); attempts seeds the wrong-code
// counter for the lockout race.
func newChallenge(userID, purpose, keyID, digest string, bind []byte, attempts int, ttl time.Duration, now time.Time) challenge.Challenge {
	now = now.UTC()
	return challenge.Challenge{
		UserID:         userID,
		Purpose:        purpose,
		SecretDigest:   digest,
		ProtectorKeyID: keyID,
		Context:        bind,
		AttemptCount:   attempts,
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
		Version:        1,
	}
}

func codeCandidates(pairs ...string) []challenge.DigestCandidate {
	out := make([]challenge.DigestCandidate, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, challenge.DigestCandidate{KeyID: pairs[i], Digest: pairs[i+1]})
	}
	return out
}

// Replace keeps ONE active row per (user, purpose) and enforces the
// (purpose, secret_digest) unique claim.
func testChallengeReplace(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	r1, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeChangeEmail, "k1", "dig-1", nil, 0, time.Hour, time.Now()))
	if err != nil || r1.ID == "" {
		t.Fatalf("Replace #1: id=%q err=%v", r1.ID, err)
	}
	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeChangeEmail, "k1", "dig-2", nil, 0, time.Hour, time.Now())); err != nil {
		t.Fatalf("Replace #2: %v", err)
	}
	// The prior digest is gone: presenting it against the single active row is a
	// wrong code, not a redemption.
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeChangeEmail, codeCandidates("k1", "dig-1"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeRejected {
		t.Fatalf("stale digest outcome = %s, want rejected (old row must be gone)", out)
	}
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeChangeEmail, codeCandidates("k1", "dig-2"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeRedeemed {
		t.Fatalf("current digest outcome = %s, want redeemed", out)
	}
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeChangeEmail, codeCandidates("k1", "dig-2"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeNotFound {
		t.Fatalf("second redeem outcome = %s, want not_found (single-use)", out)
	}

	// A colliding (purpose, secret_digest) across users is the unique-claim error.
	if _, err := repo.Replace(ctx, newChallenge("u2", challenge.PurposeUnlinkOAuth, "k1", "dig-shared", nil, 0, time.Hour, time.Now())); err != nil {
		t.Fatalf("Replace u2: %v", err)
	}
	if _, err := repo.Replace(ctx, newChallenge("u3", challenge.PurposeUnlinkOAuth, "k1", "dig-shared", nil, 0, time.Hour, time.Now())); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("colliding (purpose,digest) err=%v, want ErrAlreadyExists", err)
	}
}

// ConsumeCode redeems a correct code with a matching context and returns the
// consumed binding; a second consume is single-use not-found.
func testChallengeConsumeCodeRedeem(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginOTP, "k1", "dig", []byte("ctx-A"), 0, 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	consumed, out, err := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "dig"), "ctx-A", challenge.MaxAttempts, time.Now())
	if err != nil || out != challenge.OutcomeRedeemed {
		t.Fatalf("ConsumeCode: out=%s err=%v, want redeemed", out, err)
	}
	if consumed.UserID != "u1" || consumed.Purpose != challenge.PurposeLoginOTP || string(consumed.Context) != "ctx-A" {
		t.Fatalf("consumed = %+v, want u1/login_otp/ctx-A", consumed)
	}
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "dig"), "ctx-A", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeNotFound {
		t.Fatalf("second consume outcome = %s, want not_found", out)
	}
}

// ConsumeCode selects the candidate whose KeyID matches the row's ProtectorKeyID
// (rotation), and rejects when no candidate names the row's key.
func testChallengeKeyCandidate(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginOTP, "k2", "dig-k2", nil, 0, 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	// Old + new key candidates present: the k2 candidate is selected and matches.
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "dig-k1", "k2", "dig-k2"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeRedeemed {
		t.Fatalf("rotation candidate outcome = %s, want redeemed", out)
	}
	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginOTP, "k2", "dig-k2", nil, 0, 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace #2: %v", err)
	}
	// No candidate names key k2 → wrong code, no redemption.
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "dig-k2"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeRejected {
		t.Fatalf("missing-key candidate outcome = %s, want rejected", out)
	}
}

// A correct code with a mismatched bound context is consumed anyway (anti-probing).
func testChallengeContextMismatch(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeUnlinkOAuth, "k1", "dig", []byte("provider:google"), 0, 15*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeUnlinkOAuth, codeCandidates("k1", "dig"), "provider:github", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeContextMismatch {
		t.Fatalf("mismatch outcome = %s, want context_mismatch", out)
	}
	// The valid secret did not survive the wrong-context replay.
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeUnlinkOAuth, codeCandidates("k1", "dig"), "provider:google", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeNotFound {
		t.Fatalf("post-mismatch outcome = %s, want not_found (consumed)", out)
	}
}

// Wrong codes increment atomically; the maxAttempts-th wrong code deletes the row.
func testChallengeAttemptsLockout(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginOTP, "k1", "dig", nil, 0, 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	for i := 0; i < challenge.MaxAttempts-1; i++ {
		if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "wrong"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeRejected {
			t.Fatalf("wrong attempt #%d outcome = %s, want rejected (row survives)", i+1, out)
		}
	}
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "wrong"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeLockedOut {
		t.Fatalf("lockout attempt outcome = %s, want locked_out", out)
	}
	// The row is gone: even the correct code cannot redeem after lockout.
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "dig"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeNotFound {
		t.Fatalf("post-lockout outcome = %s, want not_found (row deleted)", out)
	}
}

// An expired code row is deleted at consume time.
func testChallengeConsumeCodeExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginOTP, "k1", "dig", nil, 0, -time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "dig"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeExpired {
		t.Fatalf("expired outcome = %s, want expired", out)
	}
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "dig"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeNotFound {
		t.Fatalf("post-expiry outcome = %s, want not_found (deleted)", out)
	}
}

// An empty candidate digest never matches (the empty-hash guard); the attempt is
// counted as a wrong code.
func testChallengeEmptyDigest(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginOTP, "k1", "dig", nil, 0, 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", ""), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeRejected {
		t.Fatalf("empty-digest outcome = %s, want rejected (never matches)", out)
	}
}

// ConsumeToken is a single-use delete-returning by (purpose, digest).
func testChallengeConsumeToken(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginMagicLink, "", "tok-dig", []byte("email:a@x.example"), 0, 15*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	consumed, err := repo.ConsumeToken(ctx, challenge.PurposeLoginMagicLink, "tok-dig", time.Now())
	if err != nil || consumed.UserID != "u1" || string(consumed.Context) != "email:a@x.example" {
		t.Fatalf("ConsumeToken = %+v err=%v, want u1 binding", consumed, err)
	}
	if _, err := repo.ConsumeToken(ctx, challenge.PurposeLoginMagicLink, "tok-dig", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second ConsumeToken err=%v, want ErrNotFound", err)
	}
}

// An expired token row is deleted and reports ErrExpired.
func testChallengeConsumeTokenExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginMagicLink, "", "tok-exp", nil, 0, -time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, err := repo.ConsumeToken(ctx, challenge.PurposeLoginMagicLink, "tok-exp", time.Now()); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("expired ConsumeToken err=%v, want ErrExpired", err)
	}
	if _, err := repo.ConsumeToken(ctx, challenge.PurposeLoginMagicLink, "tok-exp", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("post-expiry ConsumeToken err=%v, want ErrNotFound (deleted)", err)
	}
}

// An empty presented digest never matches a token row.
func testChallengeConsumeTokenEmpty(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginMagicLink, "", "tok-dig", nil, 0, 15*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, err := repo.ConsumeToken(ctx, challenge.PurposeLoginMagicLink, "", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("empty ConsumeToken err=%v, want ErrNotFound", err)
	}
}

// PurgeExpired removes at most limit expired rows and never a live one.
func testChallengePurgeExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	for i, dig := range []string{"exp-1", "exp-2", "exp-3"} {
		if _, err := repo.Replace(ctx, newChallenge(fmt.Sprintf("exp-u%d", i), challenge.PurposeLoginOTP, "k1", dig, nil, 0, -time.Minute, time.Now())); err != nil {
			t.Fatalf("Replace expired %s: %v", dig, err)
		}
	}
	if _, err := repo.Replace(ctx, newChallenge("live-u", challenge.PurposeLoginOTP, "k1", "live-dig", nil, 0, time.Hour, time.Now())); err != nil {
		t.Fatalf("Replace live: %v", err)
	}
	n, err := repo.PurgeExpired(ctx, time.Now(), 2)
	if err != nil || n != 2 {
		t.Fatalf("PurgeExpired(limit=2) = %d err=%v, want 2", n, err)
	}
	n, err = repo.PurgeExpired(ctx, time.Now(), 10)
	if err != nil || n != 1 {
		t.Fatalf("PurgeExpired(limit=10) = %d err=%v, want 1 (last expired)", n, err)
	}
	// The live row survives.
	if _, out, _ := repo.ConsumeCode(ctx, "live-u", challenge.PurposeLoginOTP, codeCandidates("k1", "live-dig"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeRedeemed {
		t.Fatalf("live row outcome after purge = %s, want redeemed (never purged)", out)
	}
}

// Two simultaneous correct code redemptions: exactly one wins.
func testChallengeConcurrentCode(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginOTP, "k1", "dig", nil, 0, 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	const n = 8
	outcomes := make([]challenge.ConsumeOutcome, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "dig"), "", challenge.MaxAttempts, time.Now())
			outcomes[i] = out
		}(i)
	}
	close(start)
	wg.Wait()
	if got := countOutcome(outcomes, challenge.OutcomeRedeemed); got != 1 {
		t.Fatalf("redeemed winners = %d, want exactly 1 (%v)", got, outcomes)
	}
}

// Two simultaneous token redemptions: exactly one wins.
func testChallengeConcurrentToken(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginMagicLink, "", "tok", nil, 0, 15*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = repo.ConsumeToken(ctx, challenge.PurposeLoginMagicLink, "tok", time.Now())
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("token redemption winners = %d, want exactly 1 (%v)", wins, errs)
	}
}

// Two simultaneous wrong codes at the lockout boundary: exactly one performs the
// lockout delete.
func testChallengeConcurrentLockout(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.Challenges

	if _, err := repo.Replace(ctx, newChallenge("u1", challenge.PurposeLoginOTP, "k1", "dig", nil, challenge.MaxAttempts-1, 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	const n = 8
	outcomes := make([]challenge.ConsumeOutcome, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, out, _ := repo.ConsumeCode(ctx, "u1", challenge.PurposeLoginOTP, codeCandidates("k1", "wrong"), "", challenge.MaxAttempts, time.Now())
			outcomes[i] = out
		}(i)
	}
	close(start)
	wg.Wait()
	if got := countOutcome(outcomes, challenge.OutcomeLockedOut); got != 1 {
		t.Fatalf("lockout winners = %d, want exactly 1 (%v)", got, outcomes)
	}
}

// --- PasswordResets (design §5.9) ---

const passwordResetPurpose = challenge.PurposePasswordReset

// resetPurgePurposes mirrors the service's password/reset purge set.
var resetPurgePurposes = []string{challenge.PurposePasswordReset, challenge.PurposeRemovePassword}

// Redeem consumes the live reset token, sets the password, and revokes every
// session, grant, and outstanding password/reset challenge in one atomic step.
func testPasswordResetRedeem(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const (
		userID  = "u-reset"
		digest  = "reset-token-digest"
		newHash = "hash:reset-new"
	)
	if err := repos.Passwords.Set(ctx, userID, "hash:reset-old"); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	s1, err := repos.Sessions.Create(ctx, newSession(userID, "reset-rh-1", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("seed session 1: %v", err)
	}
	s2, err := repos.Sessions.Create(ctx, newSession(userID, "reset-rh-2", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("seed session 2: %v", err)
	}
	if _, err := repos.Challenges.Replace(ctx, newChallenge(userID, passwordResetPurpose, "", digest, nil, 0, time.Hour, time.Now())); err != nil {
		t.Fatalf("seed reset challenge: %v", err)
	}
	if _, err := repos.Challenges.Replace(ctx, newChallenge(userID, challenge.PurposeRemovePassword, "k1", "remove-dig", nil, 0, time.Hour, time.Now())); err != nil {
		t.Fatalf("seed remove_password challenge: %v", err)
	}
	if repos.AuthenticationGrants != nil {
		if _, err := repos.AuthenticationGrants.Create(ctx, newGrant(s1.ID, userID, challenge.PurposeRemovePassword, "ctx", 5*time.Minute, time.Now())); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
	}

	res, err := repos.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{
		Purpose:                passwordResetPurpose,
		TokenDigest:            digest,
		NewPasswordHash:        newHash,
		PurgeChallengePurposes: resetPurgePurposes,
		Now:                    time.Now(),
	})
	if err != nil || res.UserID != userID {
		t.Fatalf("Redeem = %+v err=%v, want user %s", res, err, userID)
	}
	if got, _ := repos.Passwords.Get(ctx, userID); got != newHash {
		t.Errorf("password = %q, want %q (reset applied)", got, newHash)
	}
	for _, s := range []string{s1.ID, s2.ID} {
		if _, err := repos.Sessions.Get(ctx, s); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("session %s Get err=%v, want ErrNotFound (revoked)", s, err)
		}
	}
	if _, out, _ := repos.Challenges.ConsumeCode(ctx, userID, challenge.PurposeRemovePassword, codeCandidates("k1", "remove-dig"), "", challenge.MaxAttempts, time.Now()); out != challenge.OutcomeNotFound {
		t.Errorf("remove_password challenge outcome = %s, want not_found (purged)", out)
	}
	if repos.AuthenticationGrants != nil {
		if _, err := repos.AuthenticationGrants.Consume(ctx, s1.ID, challenge.PurposeRemovePassword, "ctx", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
			t.Errorf("grant Consume err=%v, want ErrNotFound (revoked)", err)
		}
	}
	// The token is single-use: a second redemption finds nothing.
	if _, err := repos.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{
		Purpose: passwordResetPurpose, TokenDigest: digest, NewPasswordHash: newHash, PurgeChallengePurposes: resetPurgePurposes, Now: time.Now(),
	}); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("second Redeem err=%v, want ErrNotFound (single-use)", err)
	}
}

// An unknown token is the single generic failure; nothing is changed.
func testPasswordResetUnknown(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if err := repos.Passwords.Set(ctx, "u-unknown", "hash:keep"); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	if _, err := repos.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{
		Purpose: passwordResetPurpose, TokenDigest: "no-such-digest", NewPasswordHash: "hash:new", PurgeChallengePurposes: resetPurgePurposes, Now: time.Now(),
	}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Redeem(unknown) err=%v, want ErrNotFound", err)
	}
	if got, _ := repos.Passwords.Get(ctx, "u-unknown"); got != "hash:keep" {
		t.Errorf("password = %q, want unchanged hash:keep (no partial reset)", got)
	}
	// An empty digest never matches a live challenge.
	if _, err := repos.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{
		Purpose: passwordResetPurpose, TokenDigest: "", NewPasswordHash: "hash:new", Now: time.Now(),
	}); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Redeem(empty digest) err=%v, want ErrNotFound", err)
	}
}

// An expired reset token is not live: it is the single generic failure and no
// state changes.
func testPasswordResetExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const userID = "u-reset-exp"
	if err := repos.Passwords.Set(ctx, userID, "hash:keep"); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	if _, err := repos.Challenges.Replace(ctx, newChallenge(userID, passwordResetPurpose, "", "exp-dig", nil, 0, -time.Minute, time.Now())); err != nil {
		t.Fatalf("seed expired reset challenge: %v", err)
	}
	if _, err := repos.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{
		Purpose: passwordResetPurpose, TokenDigest: "exp-dig", NewPasswordHash: "hash:new", PurgeChallengePurposes: resetPurgePurposes, Now: time.Now(),
	}); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Redeem(expired) err=%v, want ErrNotFound", err)
	}
	if got, _ := repos.Passwords.Get(ctx, userID); got != "hash:keep" {
		t.Errorf("password = %q, want unchanged (expired token applied nothing)", got)
	}
}

// Two simultaneous resets of one token: exactly one commits.
func testPasswordResetConcurrent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	const (
		userID = "u-reset-race"
		digest = "race-digest"
	)
	if err := repos.Passwords.Set(ctx, userID, "hash:old"); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	if _, err := repos.Challenges.Replace(ctx, newChallenge(userID, passwordResetPurpose, "", digest, nil, 0, time.Hour, time.Now())); err != nil {
		t.Fatalf("seed reset challenge: %v", err)
	}
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = repos.PasswordResets.Redeem(ctx, passwordreset.RedeemInput{
				Purpose: passwordResetPurpose, TokenDigest: digest, NewPasswordHash: "hash:new", PurgeChallengePurposes: resetPurgePurposes, Now: time.Now(),
			})
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("reset winners = %d, want exactly 1 (%v)", wins, errs)
	}
}

func countOutcome(outcomes []challenge.ConsumeOutcome, want challenge.ConsumeOutcome) int {
	n := 0
	for _, o := range outcomes {
		if o == want {
			n++
		}
	}
	return n
}

// --- AuthenticationGrants (design §5.0) ---

func newGrant(sessionID, userID, purpose, ctxDigest string, ttl time.Duration, now time.Time) authgrant.Grant {
	now = now.UTC()
	return authgrant.Grant{
		SessionID:       sessionID,
		UserID:          userID,
		Purpose:         purpose,
		ContextDigest:   ctxDigest,
		Methods:         []session.AuthenticationMethod{{Kind: session.MethodPassword, Assurance: session.AssuranceAAL1}},
		Assurance:       session.AssuranceAAL1,
		AuthenticatedAt: now,
		CreatedAt:       now,
		ExpiresAt:       now.Add(ttl),
	}
}

// Consume spends the grant matching (session, purpose, context) and binds the user.
func testGrantConsume(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.AuthenticationGrants

	if _, err := repo.Create(ctx, newGrant("s1", "u1", "set_password", "ctx-A", 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Create: %v", err)
	}
	g, err := repo.Consume(ctx, "s1", "set_password", "ctx-A", time.Now())
	if err != nil || g.UserID != "u1" || g.Purpose != "set_password" {
		t.Fatalf("Consume = %+v err=%v, want u1/set_password", g, err)
	}
	if _, err := repo.Consume(ctx, "s1", "set_password", "ctx-A", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second Consume err=%v, want ErrNotFound (single-use)", err)
	}
}

// A context mismatch never spends the grant; the correct context still can.
func testGrantContextMismatch(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.AuthenticationGrants

	if _, err := repo.Create(ctx, newGrant("s1", "u1", "unlink_oauth", "provider:google", 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Consume(ctx, "s1", "unlink_oauth", "provider:github", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("mismatched-context Consume err=%v, want ErrNotFound", err)
	}
	if _, err := repo.Consume(ctx, "s1", "unlink_oauth", "provider:google", time.Now()); err != nil {
		t.Fatalf("correct-context Consume err=%v, want nil (grant survived mismatch)", err)
	}
}

// An expired grant is single-use consumed at Consume time.
func testGrantExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.AuthenticationGrants

	if _, err := repo.Create(ctx, newGrant("s1", "u1", "set_password", "ctx-A", -time.Minute, time.Now())); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Consume(ctx, "s1", "set_password", "ctx-A", time.Now()); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("expired Consume err=%v, want ErrExpired", err)
	}
	if _, err := repo.Consume(ctx, "s1", "set_password", "ctx-A", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("post-expiry Consume err=%v, want ErrNotFound (single-use)", err)
	}
}

// A consumed grant cannot be consumed again.
func testGrantSingleUse(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.AuthenticationGrants

	if _, err := repo.Create(ctx, newGrant("s1", "u1", "set_password", "ctx-A", 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Consume(ctx, "s1", "set_password", "ctx-A", time.Now()); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if _, err := repo.Consume(ctx, "s1", "set_password", "ctx-A", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("re-Consume err=%v, want ErrNotFound", err)
	}
}

// The session authentication metadata round-trips through the grant.
func testGrantMetadataRoundTrip(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.AuthenticationGrants

	if _, err := repo.Create(ctx, newGrant("s1", "u1", "set_password", "ctx-A", 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Create: %v", err)
	}
	g, err := repo.Consume(ctx, "s1", "set_password", "ctx-A", time.Now())
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if g.Assurance != session.AssuranceAAL1 || len(g.Methods) != 1 || g.Methods[0].Kind != session.MethodPassword {
		t.Fatalf("grant metadata = assurance %q methods %+v, want aal1/[password]", g.Assurance, g.Methods)
	}
}

// DeleteBySession invalidates a session's grants and is idempotent.
func testGrantDeleteBySession(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.AuthenticationGrants

	if _, err := repo.Create(ctx, newGrant("s1", "u1", "set_password", "ctx-A", 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Create s1/set_password: %v", err)
	}
	if _, err := repo.Create(ctx, newGrant("s1", "u1", "unlink_oauth", "ctx-B", 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Create s1/unlink_oauth: %v", err)
	}
	if _, err := repo.Create(ctx, newGrant("s2", "u2", "set_password", "ctx-C", 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Create s2: %v", err)
	}
	if err := repo.DeleteBySession(ctx, "s1"); err != nil {
		t.Fatalf("DeleteBySession(s1): %v", err)
	}
	if _, err := repo.Consume(ctx, "s1", "set_password", "ctx-A", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("s1 grant after cascade err=%v, want ErrNotFound", err)
	}
	if _, err := repo.Consume(ctx, "s1", "unlink_oauth", "ctx-B", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("s1 second grant after cascade err=%v, want ErrNotFound", err)
	}
	if _, err := repo.Consume(ctx, "s2", "set_password", "ctx-C", time.Now()); err != nil {
		t.Fatalf("s2 grant err=%v, want survivable", err)
	}
	if err := repo.DeleteBySession(ctx, "s-unknown"); err != nil {
		t.Fatalf("DeleteBySession(unknown) err=%v, want nil (idempotent)", err)
	}
}

// Two simultaneous consumes of one grant: exactly one wins.
func testGrantConcurrentConsume(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.AuthenticationGrants

	if _, err := repo.Create(ctx, newGrant("s1", "u1", "set_password", "ctx-A", 5*time.Minute, time.Now())); err != nil {
		t.Fatalf("Create: %v", err)
	}
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = repo.Consume(ctx, "s1", "set_password", "ctx-A", time.Now())
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("grant consume winners = %d, want exactly 1 (%v)", wins, errs)
	}
}

// --- CredentialMutations (design §5.6) ---

// seedCredentialUser creates a user with a password and, when OAuthAccounts is
// wired, one linked provider — a two-direct-login-method credential state the
// revision-CAS mechanics are exercised against. It reports the user ID and
// whether an oauth link was seeded.
func seedCredentialUser(t *testing.T, repos auth.Repositories) (userID string, hasOAuth bool) {
	t.Helper()
	ctx := context.Background()

	u := user.NewUser(ids, "Cred", suiteBase)
	ident, err := identifier.New(ids, idNorm, "", identifier.KindEmail, "cred@example.com", loginRecoveryUses, true, suiteBase, suiteBase)
	if err != nil {
		t.Fatalf("identifier.New: %v", err)
	}
	created, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx, u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier: %v", err)
	}
	if err := repos.Passwords.Set(ctx, created.ID, "hash-cred"); err != nil {
		t.Fatalf("Passwords.Set: %v", err)
	}
	if repos.OAuthAccounts != nil {
		acct, err := oauthaccount.New(created.ID, "google", "google-cred", suiteBase)
		if err != nil {
			t.Fatalf("oauthaccount.New: %v", err)
		}
		if _, err := repos.OAuthAccounts.Create(ctx, acct); err != nil {
			t.Fatalf("OAuthAccounts.Create: %v", err)
		}
		hasOAuth = true
	}
	return created.ID, hasOAuth
}

// Snapshot projects the typed credential state and starts at revision 0.
func testCredentialSnapshot(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.CredentialMutations

	userID, hasOAuth := seedCredentialUser(t, repos)
	set, err := repo.Snapshot(ctx, userID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !set.HasPassword {
		t.Errorf("Snapshot HasPassword = false, want true")
	}
	if set.AuthRevision != 0 {
		t.Errorf("Snapshot AuthRevision = %d, want 0", set.AuthRevision)
	}
	if hasOAuth && len(set.OAuth) != 1 {
		t.Errorf("Snapshot OAuth = %+v, want one google link", set.OAuth)
	}
}

// Snapshot of a user with no credential state → ErrNotFound.
func testCredentialSnapshotUnknown(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	if _, err := repos.CredentialMutations.Snapshot(ctx, "nobody"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Snapshot(unknown): err=%v, want ErrNotFound", err)
	}
}

// Apply at the expected revision mutates the typed source and increments the
// revision exactly once.
func testCredentialApplyRevision(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.CredentialMutations

	userID, _ := seedCredentialUser(t, repos)
	if err := repo.Apply(ctx, userID, 0, credential.RemovePassword{}); err != nil {
		t.Fatalf("Apply(RemovePassword, rev=0): %v", err)
	}
	set, err := repo.Snapshot(ctx, userID)
	if err != nil {
		t.Fatalf("Snapshot after Apply: %v", err)
	}
	if set.HasPassword {
		t.Errorf("password survived RemovePassword")
	}
	if set.AuthRevision != 1 {
		t.Errorf("AuthRevision after one Apply = %d, want 1", set.AuthRevision)
	}
}

// Apply at a stale revision → ErrConflict, and the credential state is unchanged.
func testCredentialApplyStale(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.CredentialMutations

	userID, _ := seedCredentialUser(t, repos)
	if err := repo.Apply(ctx, userID, 0, credential.RemovePassword{}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	// The revision advanced to 1; a second Apply at the stale expected 0 conflicts.
	if err := repo.Apply(ctx, userID, 0, credential.RemovePassword{}); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale Apply: err=%v, want ErrConflict", err)
	}
	set, err := repo.Snapshot(ctx, userID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if set.AuthRevision != 1 {
		t.Errorf("AuthRevision after conflicted Apply = %d, want 1 (unchanged)", set.AuthRevision)
	}
}

// Two simultaneous Applies at the same expected revision: exactly one commits,
// the revision advances by exactly one, and no partial mutation occurs.
func testCredentialConcurrentApply(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.CredentialMutations

	userID, hasOAuth := seedCredentialUser(t, repos)

	// Prefer two DIFFERENT mutations when oauth is available so a double-commit
	// would be observable in the projection; otherwise race the same mutation.
	mutations := []credential.Mutation{credential.RemovePassword{}, credential.RemovePassword{}}
	if hasOAuth {
		mutations[1] = credential.UnlinkOAuth{Provider: "google"}
	}

	errs := make([]error, len(mutations))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range mutations {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = repo.Apply(ctx, userID, 0, mutations[i])
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, sdk.ErrConflict):
		default:
			t.Fatalf("unexpected Apply error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent Apply winners = %d, want exactly 1 (%v)", wins, errs)
	}
	set, err := repo.Snapshot(ctx, userID)
	if err != nil {
		t.Fatalf("Snapshot after race: %v", err)
	}
	if set.AuthRevision != 1 {
		t.Errorf("AuthRevision after concurrent Apply = %d, want 1", set.AuthRevision)
	}
	// Exactly one of the two mutations took effect: password and oauth cannot both
	// be gone when different mutations raced.
	if hasOAuth && !set.HasPassword && len(set.OAuth) == 0 {
		t.Errorf("both mutations committed: HasPassword=%v OAuth=%+v", set.HasPassword, set.OAuth)
	}
}

// --- DeliveryJobs (design §6.1.1) ---

func newDeliveryJob(key string, availableIn time.Duration, now time.Time) deliveryjob.Job {
	now = now.UTC()
	return deliveryjob.Job{
		Kind:           string(identity.KindEmail),
		Purpose:        challenge.PurposeLoginMagicLink,
		IdempotencyKey: key,
		Payload:        []byte("opaque-ciphertext-" + key),
		State:          deliveryjob.StatePending,
		AvailableAt:    now.Add(availableIn),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// Enqueue is idempotent by IdempotencyKey: a second enqueue of the same key
// returns the existing job and creates no second row.
func testDeliveryEnqueueIdempotent(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	j1, err := repo.Enqueue(ctx, newDeliveryJob("key-A", 0, time.Now()))
	if err != nil || j1.ID == "" {
		t.Fatalf("Enqueue #1: id=%q err=%v", j1.ID, err)
	}
	j2, err := repo.Enqueue(ctx, newDeliveryJob("key-A", 0, time.Now()))
	if err != nil {
		t.Fatalf("Enqueue #2: %v", err)
	}
	if j2.ID != j1.ID {
		t.Fatalf("idempotent enqueue returned id=%q, want existing %q", j2.ID, j1.ID)
	}
	// Exactly one job is claimable for the key.
	if _, err := repo.Claim(ctx, time.Now(), "w1", time.Minute); err != nil {
		t.Fatalf("Claim #1: %v", err)
	}
	if _, err := repo.Claim(ctx, time.Now(), "w2", time.Minute); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Claim #2 err=%v, want ErrNotFound (only one job existed)", err)
	}
}

// A user-requested resend (Replace) cancels the earlier pending job and enqueues a
// fresh one.
func testDeliveryReplace(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	old, err := repo.Enqueue(ctx, newDeliveryJob("key-R", 0, time.Now()))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	fresh, err := repo.Replace(ctx, newDeliveryJob("key-R", 0, time.Now()))
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if fresh.ID == old.ID {
		t.Fatalf("Replace returned the old id %q, want a fresh job", fresh.ID)
	}
	if fresh.State != deliveryjob.StatePending {
		t.Fatalf("replacement state = %q, want pending", fresh.State)
	}
	// Exactly one job is now claimable, and it is the fresh one (the old pending
	// job was canceled, not left to also deliver).
	claimed, err := repo.Claim(ctx, time.Now(), "w1", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.ID != fresh.ID {
		t.Fatalf("claimed id=%q, want the replacement %q", claimed.ID, fresh.ID)
	}
	if _, err := repo.Claim(ctx, time.Now(), "w2", time.Minute); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second Claim err=%v, want ErrNotFound (prior job canceled)", err)
	}
}

// GetLatestByIdempotencyKey is the read-only status projection: it returns the
// most-recently-created job for a key (the live row after a resend leaves canceled
// tombstones), never leases or mutates, and reports sdk.ErrNotFound for an unknown
// key.
func testDeliveryGetLatest(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	if _, err := repo.GetLatestByIdempotencyKey(ctx, "key-none"); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("unknown key err=%v, want ErrNotFound", err)
	}

	if _, err := repo.Enqueue(ctx, newDeliveryJob("key-L", 0, time.Now().Add(-time.Minute))); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// The replacement is created later but still immediately due.
	replacement := newDeliveryJob("key-L", 0, time.Now())
	replacement.AvailableAt = time.Now().UTC().Add(-time.Second)
	fresh, err := repo.Replace(ctx, replacement)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got, err := repo.GetLatestByIdempotencyKey(ctx, "key-L")
	if err != nil {
		t.Fatalf("GetLatestByIdempotencyKey: %v", err)
	}
	if got.ID != fresh.ID {
		t.Fatalf("latest id=%q, want the replacement %q (not a canceled tombstone)", got.ID, fresh.ID)
	}
	// The read never leases: the job stays claimable.
	claimed, err := repo.Claim(ctx, time.Now(), "w1", time.Minute)
	if err != nil {
		t.Fatalf("Claim after status read: %v", err)
	}
	if claimed.ID != fresh.ID {
		t.Fatalf("claimed id=%q, want %q (status read must not lease)", claimed.ID, fresh.ID)
	}
	// A terminal state is observable through the same read.
	if err := repo.Succeed(ctx, claimed.ID, "w1", time.Now()); err != nil {
		t.Fatalf("Succeed: %v", err)
	}
	done, err := repo.GetLatestByIdempotencyKey(ctx, "key-L")
	if err != nil {
		t.Fatalf("GetLatestByIdempotencyKey after succeed: %v", err)
	}
	if done.State != deliveryjob.StateSucceeded {
		t.Fatalf("status state=%q, want %q", done.State, deliveryjob.StateSucceeded)
	}
}

// Claim leases the due job, increments its attempt count, and records the lease.
func testDeliveryClaim(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	if _, err := repo.Enqueue(ctx, newDeliveryJob("key-C", 0, time.Now())); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	claimed, err := repo.Claim(ctx, time.Now(), "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed.LeaseID != "worker-1" {
		t.Fatalf("LeaseID = %q, want worker-1", claimed.LeaseID)
	}
	if claimed.AttemptCount != 1 {
		t.Fatalf("AttemptCount = %d, want 1", claimed.AttemptCount)
	}
	if !claimed.Leased(time.Now()) {
		t.Fatalf("claimed job should report Leased at now")
	}
}

// Claim skips a not-yet-due job and a live-leased job.
func testDeliveryClaimNotDue(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	// Future available_at is not due.
	if _, err := repo.Enqueue(ctx, newDeliveryJob("key-future", time.Hour, time.Now())); err != nil {
		t.Fatalf("Enqueue future: %v", err)
	}
	if _, err := repo.Claim(ctx, time.Now(), "w1", time.Minute); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Claim future err=%v, want ErrNotFound", err)
	}
	// A live-leased job is not re-claimable.
	if _, err := repo.Enqueue(ctx, newDeliveryJob("key-live", 0, time.Now())); err != nil {
		t.Fatalf("Enqueue live: %v", err)
	}
	if _, err := repo.Claim(ctx, time.Now(), "w1", time.Hour); err != nil {
		t.Fatalf("Claim live: %v", err)
	}
	if _, err := repo.Claim(ctx, time.Now(), "w2", time.Minute); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second Claim of leased job err=%v, want ErrNotFound", err)
	}
}

// A job whose lease has expired without completion becomes claimable again
// (at-least-once semantics after a crashed worker).
func testDeliveryClaimReclaim(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	if _, err := repo.Enqueue(ctx, newDeliveryJob("key-reclaim", 0, time.Now())); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	first, err := repo.Claim(ctx, time.Now(), "worker-crashed", time.Minute)
	if err != nil {
		t.Fatalf("Claim #1: %v", err)
	}
	// Advance past the lease: a later Claim reclaims the still-pending job and the
	// attempt count advances.
	future := time.Now().Add(2 * time.Minute)
	second, err := repo.Claim(ctx, future, "worker-2", time.Minute)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("reclaimed id=%q, want the same job %q", second.ID, first.ID)
	}
	if second.LeaseID != "worker-2" || second.AttemptCount != 2 {
		t.Fatalf("reclaim lease=%q attempts=%d, want worker-2 / 2", second.LeaseID, second.AttemptCount)
	}
}

// Succeed is a terminal, idempotent transition for the lease holder.
func testDeliverySucceed(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	if _, err := repo.Enqueue(ctx, newDeliveryJob("key-S", 0, time.Now())); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	claimed, err := repo.Claim(ctx, time.Now(), "w1", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := repo.Succeed(ctx, claimed.ID, "w1", time.Now()); err != nil {
		t.Fatalf("Succeed: %v", err)
	}
	// Idempotent: a duplicate at-least-once report is a no-op success.
	if err := repo.Succeed(ctx, claimed.ID, "w1", time.Now()); err != nil {
		t.Fatalf("idempotent Succeed: %v", err)
	}
	// A succeeded job is terminal — never re-claimed.
	if _, err := repo.Claim(ctx, time.Now(), "w2", time.Minute); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Claim after success err=%v, want ErrNotFound", err)
	}
}

// Retry reschedules a leased job with backoff, clears the lease, and keeps it
// pending so it is claimable at the new available time.
func testDeliveryRetry(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	if _, err := repo.Enqueue(ctx, newDeliveryJob("key-RT", 0, time.Now())); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	claimed, err := repo.Claim(ctx, time.Now(), "w1", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	backoffTo := time.Now().Add(30 * time.Second)
	if err := repo.Retry(ctx, claimed.ID, "w1", backoffTo, "transient: provider timeout", time.Now()); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	// Not due until the backoff time.
	if _, err := repo.Claim(ctx, time.Now(), "w2", time.Minute); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Claim before backoff err=%v, want ErrNotFound", err)
	}
	// Due after backoff, and reclaimable by a fresh worker.
	again, err := repo.Claim(ctx, backoffTo.Add(time.Second), "w3", time.Minute)
	if err != nil {
		t.Fatalf("Claim after backoff: %v", err)
	}
	if again.ID != claimed.ID || again.LeaseID != "w3" {
		t.Fatalf("re-claim = id %q lease %q, want %q / w3", again.ID, again.LeaseID, claimed.ID)
	}
}

// Fail is a terminal, idempotent transition; the failed job is never re-claimed.
func testDeliveryFail(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	if _, err := repo.Enqueue(ctx, newDeliveryJob("key-F", 0, time.Now())); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	claimed, err := repo.Claim(ctx, time.Now(), "w1", time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := repo.Fail(ctx, claimed.ID, "w1", "permanent: invalid address", time.Now()); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if err := repo.Fail(ctx, claimed.ID, "w1", "permanent: invalid address", time.Now()); err != nil {
		t.Fatalf("idempotent Fail: %v", err)
	}
	if _, err := repo.Claim(ctx, time.Now(), "w2", time.Minute); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Claim after failure err=%v, want ErrNotFound", err)
	}
}

// A completer whose lease was reclaimed loses: it cannot clobber the new
// claimant's job (the late report is a conflict, not a silent success).
func testDeliveryReclaimedLease(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	if _, err := repo.Enqueue(ctx, newDeliveryJob("key-RL", 0, time.Now())); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	first, err := repo.Claim(ctx, time.Now(), "worker-slow", time.Minute)
	if err != nil {
		t.Fatalf("Claim #1: %v", err)
	}
	// The lease expires and a second worker reclaims the job.
	future := time.Now().Add(2 * time.Minute)
	if _, err := repo.Claim(ctx, future, "worker-fast", time.Minute); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	// The slow worker's late completion is rejected.
	if err := repo.Succeed(ctx, first.ID, "worker-slow", future); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale Succeed err=%v, want ErrConflict", err)
	}
	if err := repo.Fail(ctx, first.ID, "worker-slow", "x", future); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale Fail err=%v, want ErrConflict", err)
	}
}

// Cancel terminally cancels a non-terminal job; a second cancel is idempotent.
func testDeliveryCancel(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	job, err := repo.Enqueue(ctx, newDeliveryJob("key-X", 0, time.Now()))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := repo.Cancel(ctx, job.ID, time.Now()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := repo.Cancel(ctx, job.ID, time.Now()); err != nil {
		t.Fatalf("idempotent Cancel: %v", err)
	}
	if _, err := repo.Claim(ctx, time.Now(), "w1", time.Minute); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Claim after cancel err=%v, want ErrNotFound", err)
	}
	if err := repo.Cancel(ctx, "no-such-id", time.Now()); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Cancel unknown err=%v, want ErrNotFound", err)
	}
}

// PurgeTerminal removes at most limit terminal jobs and never a live one.
func testDeliveryPurge(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	for _, key := range []string{"p1", "p2", "p3"} {
		job, err := repo.Enqueue(ctx, newDeliveryJob(key, 0, time.Now()))
		if err != nil {
			t.Fatalf("Enqueue %s: %v", key, err)
		}
		if err := repo.Cancel(ctx, job.ID, time.Now()); err != nil {
			t.Fatalf("Cancel %s: %v", key, err)
		}
	}
	// One live (pending) job must never be purged.
	live, err := repo.Enqueue(ctx, newDeliveryJob("live", 0, time.Now()))
	if err != nil {
		t.Fatalf("Enqueue live: %v", err)
	}

	before := time.Now().Add(time.Minute)
	n, err := repo.PurgeTerminal(ctx, before, 2)
	if err != nil || n != 2 {
		t.Fatalf("PurgeTerminal(limit=2) = %d err=%v, want 2", n, err)
	}
	n, err = repo.PurgeTerminal(ctx, before, 10)
	if err != nil || n != 1 {
		t.Fatalf("PurgeTerminal(limit=10) = %d err=%v, want 1", n, err)
	}
	// The live job survived and is still claimable.
	claimed, err := repo.Claim(ctx, time.Now(), "w1", time.Minute)
	if err != nil || claimed.ID != live.ID {
		t.Fatalf("live job after purge = %q err=%v, want %q (never purged)", claimed.ID, err, live.ID)
	}
}

// Many workers race to claim one due job: exactly one wins.
func testDeliveryConcurrentClaim(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.DeliveryJobs

	if _, err := repo.Enqueue(ctx, newDeliveryJob("key-race", 0, time.Now())); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = repo.Claim(ctx, time.Now(), fmt.Sprintf("w%d", i), time.Minute)
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, sdk.ErrNotFound):
		default:
			t.Fatalf("unexpected Claim error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent Claim winners = %d, want exactly 1 (%v)", wins, errs)
	}
}

// --- UserIdentifiers (design §2.2) ---

// idNorm is the bundled strict normalizer; the storetest passes already-normalized
// values (lowercase emails, canonical E.164 phones) because ApplyVerifiedChange
// receives a pre-normalized value the same way login/recovery lookups do.
var idNorm = identifier.DefaultNormalizer{}

// loginRecoveryUses is a verified email's full authentication+contact role set.
var loginRecoveryUses = identifier.Uses{Login: true, Recovery: true, Notification: true}

// seedUserWithIdentifier creates a user whose first identifier claims identValue
// with the given uses/primary/verify state, through the atomic
// CreateWithPrimaryIdentifier. IDs are DB-generated (empty pre-create). The label
// argument names the user at the call site (identity lives entirely on the
// identifier now). It fatals on error.
func seedUserWithIdentifier(t *testing.T, repos auth.Repositories, label, identValue string, kind identifier.Kind, uses identifier.Uses, primary bool, verifiedAt time.Time) (user.User, identifier.Identifier) {
	t.Helper()
	_ = label
	ctx := context.Background()
	u := user.NewUser(dbIDs, "Test", suiteBase)
	ident, err := identifier.New(dbIDs, idNorm, "", kind, identValue, uses, primary, verifiedAt, suiteBase)
	if err != nil {
		t.Fatalf("identifier.New: %v", err)
	}
	cu, ci, err := repos.Users.CreateWithPrimaryIdentifier(ctx, u, ident)
	if err != nil {
		t.Fatalf("CreateWithPrimaryIdentifier: %v", err)
	}
	return cu, ci
}

// applyEmailChange drives ApplyVerifiedChange for an email add/change with the
// given uses/primary and prior-identifier replacement, returning the result.
func applyEmailChange(repos auth.Repositories, userID, newValue string, uses identifier.Uses, primary bool, replacesID string, expectedRev int64, verifiedAt time.Time) (identifier.Identifier, error) {
	return repos.Identifiers.ApplyVerifiedChange(context.Background(), identifier.ApplyVerifiedChangeInput{
		UserID:               userID,
		Kind:                 identifier.KindEmail,
		NormalizedValue:      newValue,
		LoginEnabled:         uses.Login,
		RecoveryEnabled:      uses.Recovery,
		NotificationEnabled:  uses.Notification,
		MakePrimary:          primary,
		ReplacesIdentifierID: replacesID,
	}, expectedRev, verifiedAt)
}

// case 1: CreateWithPrimaryIdentifier assigns DB-generated IDs to both rows,
// links the identifier to the user, and both are readable.
func testIdentifiersAtomicCreate(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	cu, ci := seedUserWithIdentifier(t, repos, "alice@example.com", "alice@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)
	if cu.ID == "" || ci.ID == "" {
		t.Fatalf("DB-generated IDs missing: user=%q identifier=%q", cu.ID, ci.ID)
	}
	if ci.UserID != cu.ID {
		t.Fatalf("identifier not linked to user: %q != %q", ci.UserID, cu.ID)
	}
	got, err := repos.Identifiers.Get(ctx, ci.ID)
	if err != nil || got.NormalizedValue != "alice@example.com" || !got.Verified() || !got.IsPrimary {
		t.Fatalf("Get(identifier): %+v err=%v", got, err)
	}
	if _, err := repos.Users.Get(ctx, cu.ID); err != nil {
		t.Fatalf("Get(user): %v", err)
	}
}

// case 2: a lost authentication claim rolls BOTH the user and the identifier back
// — no orphan user row, and the claim still resolves to the original owner.
func testIdentifiersCreateRollback(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	owner, _ := seedUserWithIdentifier(t, repos, "owner@example.com", "claim@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	// A concrete (non-DB-generated) user id so the no-orphan assertion can Get it
	// back after the aggregate rolls back.
	u2 := user.NewUser(ids, "Loser", suiteBase)
	if u2.ID == "" {
		t.Fatal("expected a concrete user id for the rollback probe")
	}
	// A second registration email claiming the same login value must lose.
	ident2, err := identifier.New(dbIDs, idNorm, "", identifier.KindEmail, "claim@example.com", loginRecoveryUses, true, suiteBase, suiteBase)
	if err != nil {
		t.Fatalf("identifier.New: %v", err)
	}
	if _, _, err := repos.Users.CreateWithPrimaryIdentifier(ctx, u2, ident2); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("colliding CreateWithPrimaryIdentifier: err=%v, want ErrAlreadyExists", err)
	}
	// No orphan user (the whole aggregate rolled back).
	if _, err := repos.Users.Get(ctx, u2.ID); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("orphan user survived rollback: err=%v, want ErrNotFound", err)
	}
	// The claim still belongs to the original owner.
	got, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "claim@example.com")
	if err != nil || got.UserID != owner.ID {
		t.Errorf("GetLogin after rollback: owner=%q err=%v, want %q", got.UserID, err, owner.ID)
	}
}

// case 3: after a replacement, active reads return only the active row; the
// retired row is still fetchable by ID for history.
func testIdentifiersActiveOnly(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, first := seedUserWithIdentifier(t, repos, "active@example.com", "active@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	second, err := applyEmailChange(repos, u.ID, "active2@example.com", loginRecoveryUses, true, first.ID, 0, suiteBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("ApplyVerifiedChange: %v", err)
	}
	list, err := repos.Identifiers.ListByUser(ctx, u.ID)
	if err != nil || len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("ListByUser active-only = %+v err=%v, want just the new row", list, err)
	}
	retired, err := repos.Identifiers.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get(retired): %v", err)
	}
	if retired.Active() {
		t.Errorf("retired identifier still active: %+v", retired)
	}
	if _, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "active@example.com"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin(retired value): err=%v, want ErrNotFound", err)
	}
}

// case 4: login and recovery lookups resolve a verified login+recovery
// identifier; a notification-only identifier is neither a login nor a recovery
// claim.
func testIdentifiersLoginRecoveryLookup(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, ci := seedUserWithIdentifier(t, repos, "look@example.com", "look@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	gotLogin, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "look@example.com")
	if err != nil || gotLogin.ID != ci.ID {
		t.Fatalf("GetLogin: id=%q err=%v, want %q", gotLogin.ID, err, ci.ID)
	}
	gotRec, err := repos.Identifiers.GetRecovery(ctx, string(identifier.KindEmail), "look@example.com")
	if err != nil || gotRec.ID != ci.ID {
		t.Fatalf("GetRecovery: id=%q err=%v, want %q", gotRec.ID, err, ci.ID)
	}
	if _, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "absent@example.com"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin(absent): err=%v, want ErrNotFound", err)
	}

	// A notification-only primary claims neither login nor recovery.
	notifyOnly := identifier.Uses{Notification: true}
	_, notifCI := seedUserWithIdentifier(t, repos, "notif@example.com", "shared@example.com", identifier.KindEmail, notifyOnly, true, time.Time{})
	if _, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "shared@example.com"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin(notification-only): err=%v, want ErrNotFound", err)
	}
	if got, err := repos.Identifiers.Get(ctx, notifCI.ID); err != nil || got.Verified() {
		t.Errorf("notification-only identifier: %+v err=%v, want present & unverified", got, err)
	}
	_ = u
}

// case 5: a user may hold multiple active identifiers of different kinds, each
// independently resolvable.
func testIdentifiersMultiple(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, _ := seedUserWithIdentifier(t, repos, "multi@example.com", "multi@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	phone, err := repos.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
		UserID:              u.ID,
		Kind:                identifier.KindPhone,
		NormalizedValue:     "+15551230000",
		LoginEnabled:        true,
		NotificationEnabled: true,
	}, 0, suiteBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("ApplyVerifiedChange(add phone): %v", err)
	}
	list, err := repos.Identifiers.ListByUser(ctx, u.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListByUser = %d rows err=%v, want 2", len(list), err)
	}
	gotPhone, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindPhone), "+15551230000")
	if err != nil || gotPhone.ID != phone.ID {
		t.Errorf("GetLogin(phone): id=%q err=%v, want %q", gotPhone.ID, err, phone.ID)
	}
	if _, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "multi@example.com"); err != nil {
		t.Errorf("GetLogin(email) after adding phone: err=%v, want nil", err)
	}
}

// case 6: a notification-only value may be shared across accounts — it is not an
// authentication claim, so both users hold it and it resolves to no login.
func testIdentifiersSharedNotification(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	a, _ := seedUserWithIdentifier(t, repos, "a@example.com", "a@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)
	b, _ := seedUserWithIdentifier(t, repos, "b@example.com", "b@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	const shared = "+15550009999"
	addNotifyPhone := func(userID string) error {
		_, err := repos.Identifiers.ApplyVerifiedChange(ctx, identifier.ApplyVerifiedChangeInput{
			UserID:              userID,
			Kind:                identifier.KindPhone,
			NormalizedValue:     shared,
			NotificationEnabled: true,
		}, 0, suiteBase.Add(time.Hour))
		return err
	}
	if err := addNotifyPhone(a.ID); err != nil {
		t.Fatalf("add shared phone to A: %v", err)
	}
	if err := addNotifyPhone(b.ID); err != nil {
		t.Fatalf("add shared phone to B (notification is not a claim): %v", err)
	}
	if _, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindPhone), shared); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin(shared notification phone): err=%v, want ErrNotFound", err)
	}
	if la, _ := repos.Identifiers.ListByUser(ctx, a.ID); len(la) != 2 {
		t.Errorf("ListByUser(A) = %d, want 2", len(la))
	}
	if lb, _ := repos.Identifiers.ListByUser(ctx, b.ID); len(lb) != 2 {
		t.Errorf("ListByUser(B) = %d, want 2", len(lb))
	}
}

// case 7: enabling login for a value already claimed by another user loses at
// apply time with the generic ErrAlreadyExists.
func testIdentifiersClaimCollision(t *testing.T, repos auth.Repositories) {
	a, _ := seedUserWithIdentifier(t, repos, "ca@example.com", "taken@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)
	b, _ := seedUserWithIdentifier(t, repos, "cb@example.com", "cb@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	_, err := applyEmailChange(repos, b.ID, "taken@example.com", loginRecoveryUses, false, "", 0, suiteBase.Add(time.Hour))
	if !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("claim collision: err=%v, want ErrAlreadyExists", err)
	}
	// The claim still belongs to A.
	got, err := repos.Identifiers.GetLogin(context.Background(), string(identifier.KindEmail), "taken@example.com")
	if err != nil || got.UserID != a.ID {
		t.Errorf("GetLogin after collision: owner=%q err=%v, want %q", got.UserID, err, a.ID)
	}
}

// case 8: making a new identifier primary retires the displaced same-kind primary,
// so at most one active primary per (user, kind) survives.
func testIdentifiersOnePrimary(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, first := seedUserWithIdentifier(t, repos, "p@example.com", "p1@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	second, err := applyEmailChange(repos, u.ID, "p2@example.com", loginRecoveryUses, true, "", 0, suiteBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("ApplyVerifiedChange(make primary): %v", err)
	}
	list, err := repos.Identifiers.ListByUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	primaries := 0
	for _, it := range list {
		if it.IsPrimary {
			primaries++
			if it.ID != second.ID {
				t.Errorf("unexpected primary %q, want %q", it.ID, second.ID)
			}
		}
	}
	if primaries != 1 {
		t.Errorf("active primaries = %d, want exactly 1", primaries)
	}
	if old, _ := repos.Identifiers.Get(ctx, first.ID); old.Active() {
		t.Errorf("displaced primary still active: %+v", old)
	}
}

// case 9: the applied identifier round-trips its uses, primary flag, verified
// time, and value byte-for-byte.
func testIdentifiersApplyRoundTrip(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, _ := seedUserWithIdentifier(t, repos, "rt@example.com", "rt@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	verifiedAt := suiteBase.Add(3 * time.Hour)
	uses := identifier.Uses{Login: true, Recovery: false, Notification: true}
	applied, err := applyEmailChange(repos, u.ID, "rt2@example.com", uses, true, "", 0, verifiedAt)
	if err != nil {
		t.Fatalf("ApplyVerifiedChange: %v", err)
	}
	got, err := repos.Identifiers.Get(ctx, applied.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.NormalizedValue != "rt2@example.com" {
		t.Errorf("value = %q, want rt2@example.com", got.NormalizedValue)
	}
	if !got.VerifiedAt.Equal(verifiedAt) {
		t.Errorf("VerifiedAt = %v, want %v", got.VerifiedAt, verifiedAt)
	}
	if !got.LoginEnabled || got.RecoveryEnabled || !got.NotificationEnabled || !got.IsPrimary {
		t.Errorf("uses/primary round-trip lost: %+v", got)
	}
}

// case 10: a replacement retires the prior identifier (history preserved) and the
// old value stops resolving; the new value resolves.
func testIdentifiersReplacementHistory(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	u, first := seedUserWithIdentifier(t, repos, "hist@example.com", "old@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	second, err := applyEmailChange(repos, u.ID, "new@example.com", loginRecoveryUses, true, first.ID, 0, suiteBase.Add(time.Hour))
	if err != nil {
		t.Fatalf("ApplyVerifiedChange: %v", err)
	}
	old, err := repos.Identifiers.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get(old): %v", err)
	}
	if old.Active() || old.ReplacedAt.IsZero() {
		t.Errorf("old identifier not retired: %+v", old)
	}
	if _, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "old@example.com"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("GetLogin(old): err=%v, want ErrNotFound", err)
	}
	got, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), "new@example.com")
	if err != nil || got.ID != second.ID {
		t.Errorf("GetLogin(new): id=%q err=%v, want %q", got.ID, err, second.ID)
	}
}

// case 11: ApplyVerifiedChange is revision-CAS — a second apply at the stale
// expected revision conflicts.
func testIdentifiersRevisionConflict(t *testing.T, repos auth.Repositories) {
	u, _ := seedUserWithIdentifier(t, repos, "rev@example.com", "rev@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	if _, err := applyEmailChange(repos, u.ID, "rev2@example.com", loginRecoveryUses, true, "", 0, suiteBase.Add(time.Hour)); err != nil {
		t.Fatalf("first ApplyVerifiedChange(rev=0): %v", err)
	}
	// The revision advanced to 1; a second apply at the stale expected 0 conflicts.
	if _, err := applyEmailChange(repos, u.ID, "rev3@example.com", loginRecoveryUses, true, "", 0, suiteBase.Add(2*time.Hour)); !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("stale ApplyVerifiedChange: err=%v, want ErrConflict", err)
	}
	// The conflicted change was not applied.
	if _, err := repos.Identifiers.GetLogin(context.Background(), string(identifier.KindEmail), "rev3@example.com"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("conflicted value leaked: err=%v, want ErrNotFound", err)
	}
}

// case 12: a lost authentication claim inside ApplyVerifiedChange rolls the whole
// mutation back — the revision does not advance (a fresh apply at the same
// expected revision still succeeds) and no partial row lands.
func testIdentifiersApplyRollback(t *testing.T, repos auth.Repositories) {
	seedUserWithIdentifier(t, repos, "rb-owner@example.com", "held@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)
	b, _ := seedUserWithIdentifier(t, repos, "rb-b@example.com", "rb-b@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	if _, err := applyEmailChange(repos, b.ID, "held@example.com", loginRecoveryUses, true, "", 0, suiteBase.Add(time.Hour)); !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("colliding ApplyVerifiedChange: err=%v, want ErrAlreadyExists", err)
	}
	// The revision did not advance: a fresh apply at expected 0 still commits.
	if _, err := applyEmailChange(repos, b.ID, "rb-b2@example.com", loginRecoveryUses, true, "", 0, suiteBase.Add(2*time.Hour)); err != nil {
		t.Fatalf("apply after rolled-back conflict (rev must still be 0): %v", err)
	}
}

// case 13: two accounts concurrently claim the same login value — exactly one
// wins, and the value resolves to that single user.
func testIdentifiersConcurrentClaim(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	a, _ := seedUserWithIdentifier(t, repos, "cc-a@example.com", "cc-a@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)
	b, _ := seedUserWithIdentifier(t, repos, "cc-b@example.com", "cc-b@example.com", identifier.KindEmail, loginRecoveryUses, true, suiteBase)

	const contested = "contested@example.com"
	userIDs := []string{a.ID, b.ID}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range userIDs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = applyEmailChange(repos, userIDs[i], contested, loginRecoveryUses, false, "", 0, suiteBase.Add(time.Hour))
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, sdk.ErrAlreadyExists):
		default:
			t.Fatalf("unexpected ApplyVerifiedChange error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent claim winners = %d, want exactly 1 (%v)", wins, errs)
	}
	if _, err := repos.Identifiers.GetLogin(ctx, string(identifier.KindEmail), contested); err != nil {
		t.Errorf("contested value must resolve to the single winner: err=%v", err)
	}
}

// --- ContactChanges (design §2.4) ---

// newEmailChange builds an email PendingChange for user with the given uses and a
// ttl offset from the real clock (the reference/store expiry read against now).
func newEmailChange(userID, newValue string, uses identifier.Uses, primary bool, replacesID string, ttl time.Duration) contactchange.PendingChange {
	return contactchange.New(userID, identifier.KindEmail, newValue, uses, primary, replacesID, ttl, time.Now())
}

// case 1: Create is an atomic replace per (user, kind): a second create for the
// same pair supersedes the first, while a different kind coexists.
func testContactChangeReplace(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ContactChanges
	if _, err := repo.Create(ctx, newEmailChange("u1", "first@example.com", loginRecoveryUses, true, "", time.Hour)); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if _, err := repo.Create(ctx, newEmailChange("u1", "second@example.com", loginRecoveryUses, true, "", time.Hour)); err != nil {
		t.Fatalf("Create second (replace): %v", err)
	}
	// A phone change for the same user is a different (user, kind) pair and coexists.
	if _, err := repo.Create(ctx, contactchange.New("u1", identifier.KindPhone, "+15551112222", identifier.Uses{Login: true, Notification: true}, true, "", time.Hour, time.Now())); err != nil {
		t.Fatalf("Create phone (different kind): %v", err)
	}
	// Consuming the email change returns only the surviving (second) value.
	got, err := repo.Consume(ctx, "u1", identifier.KindEmail)
	if err != nil {
		t.Fatalf("Consume email: %v", err)
	}
	if got.NewValue != "second@example.com" {
		t.Errorf("Consume email value = %q, want second@example.com (first should have been replaced)", got.NewValue)
	}
	// The replaced-and-consumed email pair is now empty.
	if _, err := repo.Consume(ctx, "u1", identifier.KindEmail); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("second Consume email: err=%v, want ErrNotFound", err)
	}
	// The phone change is untouched.
	ph, err := repo.Consume(ctx, "u1", identifier.KindPhone)
	if err != nil || ph.NewValue != "+15551112222" {
		t.Errorf("Consume phone = %+v err=%v, want the coexisting phone change", ph, err)
	}
}

// case 2: the stored pending change round-trips its value, kind, uses,
// primary/replacement intent, and a DB-assigned ID byte-for-byte.
func testContactChangeRoundTrip(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ContactChanges
	uses := identifier.Uses{Login: true, Recovery: false, Notification: true}
	created, err := repo.Create(ctx, contactchange.New("u-rt", identifier.KindPhone, "+15559998888", uses, true, "old-ident-id", time.Hour, time.Now()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Errorf("DB-generated ID missing on returned row: %+v", created)
	}
	got, err := repo.Consume(ctx, "u-rt", identifier.KindPhone)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.NewValue != "+15559998888" || got.Kind != identifier.KindPhone {
		t.Errorf("value/kind round-trip lost: %+v", got)
	}
	if !got.LoginEnabled || got.RecoveryEnabled || !got.NotificationEnabled {
		t.Errorf("use flags round-trip lost: %+v", got)
	}
	if !got.MakePrimary || got.ReplacesIdentifierID != "old-ident-id" {
		t.Errorf("primary/replacement intent round-trip lost: %+v", got)
	}
}

// case 3: Consume is single-use — the second Consume of a consumed pair is NotFound.
func testContactChangeSingleUse(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ContactChanges
	if _, err := repo.Create(ctx, newEmailChange("u-su", "su@example.com", loginRecoveryUses, false, "", time.Hour)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Consume(ctx, "u-su", identifier.KindEmail); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if _, err := repo.Consume(ctx, "u-su", identifier.KindEmail); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("second Consume: err=%v, want ErrNotFound", err)
	}
}

// case 4: consuming an expired pending change deletes the row and returns
// ErrExpired; the row is gone afterward.
func testContactChangeExpired(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ContactChanges
	if _, err := repo.Create(ctx, newEmailChange("u-exp", "exp@example.com", loginRecoveryUses, false, "", -time.Minute)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := repo.Consume(ctx, "u-exp", identifier.KindEmail); !errors.Is(err, sdk.ErrExpired) {
		t.Fatalf("Consume expired: err=%v, want ErrExpired", err)
	}
	if _, err := repo.Consume(ctx, "u-exp", identifier.KindEmail); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Consume after expiry-delete: err=%v, want ErrNotFound (row gone)", err)
	}
}

// case 5: consuming a (user, kind) with no pending change is NotFound.
func testContactChangeMissing(t *testing.T, repos auth.Repositories) {
	if _, err := repos.ContactChanges.Consume(context.Background(), "nobody", identifier.KindEmail); !errors.Is(err, sdk.ErrNotFound) {
		t.Fatalf("Consume missing: err=%v, want ErrNotFound", err)
	}
}

// case 6: many goroutines race to Consume one pending change — exactly one wins
// and the rest see NotFound (single-use under concurrency).
func testContactChangeConcurrentConsume(t *testing.T, repos auth.Repositories) {
	ctx := context.Background()
	repo := repos.ContactChanges
	if _, err := repo.Create(ctx, newEmailChange("u-cc", "cc@example.com", loginRecoveryUses, false, "", time.Hour)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	const n = 8
	results := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = repo.Consume(ctx, "u-cc", identifier.KindEmail)
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, sdk.ErrNotFound):
		default:
			t.Fatalf("unexpected Consume error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("concurrent Consume winners = %d, want exactly 1 (%v)", wins, results)
	}
}
