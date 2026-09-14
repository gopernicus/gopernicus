package firestore

import (
	firestoredb "github.com/gopernicus/gopernicus/integrations/datastores/firestore"
)

// The collections. Names mirror the SQL table names (milestone convention) so
// the two documentation trees and the operator vocabulary stay shared; the seven
// CLAIM collections have no SQL table because they reproduce an INDEX
// (SCHEMA.md §5). All are top-level collections — no subcollections, so every
// index in the manifest is COLLECTION scope.
const (
	collectionUsers           = "users"
	collectionPasswords       = "user_passwords"
	collectionIdentifiers     = "user_identifiers"
	collectionSessions        = "sessions"
	collectionOAuthAccounts   = "oauth_accounts"
	collectionOAuthStates     = "oauth_states"
	collectionServiceAccounts = "service_accounts"
	collectionAPIKeys         = "api_keys"
	collectionSecurityEvents  = "security_events"
	collectionInvitations     = "invitations"
	collectionChallenges      = "challenges"
	collectionContactChanges  = "contact_changes"
	collectionAuthGrants      = "authentication_grants"

	collectionIdentifierClaims    = "identifier_claims"
	collectionIdentifierPrimaries = "identifier_primaries"
	collectionRefreshHashClaims   = "session_refresh_hashes"
	collectionAPIKeyHashClaims    = "api_key_hashes"
	collectionInvitationTokens    = "invitation_token_hashes"
	collectionInvitationPending   = "invitation_pending"
	collectionChallengeDigests    = "challenge_digests"
)

// The document ids. Every one is a connector KeyHash over the row's SQL PRIMARY
// KEY (or, for the two replacement-keyed collections, over the unique tuple a
// replacement is keyed by), in the SQL's own column order.
//
// The hash is mandatory rather than decorative (SCHEMA.md §4.2): a document id
// must be valid UTF-8, at most 1500 bytes, must contain no '/', and must not be
// '.', '..', or match __.*__ — and this pocket bounds NO port input, so a
// host-minted id, an email address, or an opaque token can violate all three.
// The original value is always kept in a FIELD and returned verbatim; a KeyHash
// is an identity, never a projection and never a sort key.

func userDocID(userID string) string { return firestoredb.KeyHash(userID) }

// passwordDocID keys user_passwords by its user_id PRIMARY KEY — one credential
// row per user, so Set is a document-level upsert rather than an ON CONFLICT.
func passwordDocID(userID string) string { return firestoredb.KeyHash(userID) }

func identifierDocID(identifierID string) string { return firestoredb.KeyHash(identifierID) }

func sessionDocID(sessionID string) string { return firestoredb.KeyHash(sessionID) }

// oauthAccountDocID is the (provider, provider_user_id) COMPOSITE primary key,
// which is also the table's only uniqueness rule: a provider identity belongs to
// at most one local user, so a duplicate Create fails AlreadyExists at the
// server and no claim document is needed.
func oauthAccountDocID(provider, providerUserID string) string {
	return firestoredb.KeyHash(provider, providerUserID)
}

func oauthStateDocID(token string) string { return firestoredb.KeyHash(token) }

func serviceAccountDocID(id string) string { return firestoredb.KeyHash(id) }

func apiKeyDocID(id string) string { return firestoredb.KeyHash(id) }

func securityEventDocID(id string) string { return firestoredb.KeyHash(id) }

func invitationDocID(id string) string { return firestoredb.KeyHash(id) }

// challengeDocID keys challenges by (subject_key, purpose) — migration 0015's
// unique index — rather than by the row's surrogate id, because Replace is a
// REPLACEMENT: one live challenge per subject and purpose, written with Set so
// the prior row is displaced atomically instead of deleted-then-inserted
// (SCHEMA.md §5.7).
func challengeDocID(subjectKey, purpose string) string {
	return firestoredb.KeyHash(subjectKey, purpose)
}

// contactChangeDocID keys contact_changes by (user_id, kind) for the same
// reason: one pending change per user and kind, replacement by Set.
func contactChangeDocID(userID, kind string) string {
	return firestoredb.KeyHash(userID, kind)
}

func authGrantDocID(id string) string { return firestoredb.KeyHash(id) }

// The claim document ids. Each one IS the unique key of the SQL index it
// reproduces, so creating the claim in the same transaction as its row is what
// makes a lost race an sdk.ErrAlreadyExists instead of a duplicate (ruling R3).
// A claim exists only while its row satisfies the index's stored predicate; the
// helper pair that owns the row releases it the moment it does not.

// identifierClaimDocID reproduces idx_user_identifiers_auth_claim:
// (kind, normalized_value) WHERE replaced_at IS NULL AND (login_enabled OR
// recovery_enabled).
func identifierClaimDocID(kind, normalizedValue string) string {
	return firestoredb.KeyHash(kind, normalizedValue)
}

// identifierPrimaryDocID reproduces idx_user_identifiers_primary:
// (user_id, kind) WHERE replaced_at IS NULL AND is_primary.
func identifierPrimaryDocID(userID, kind string) string {
	return firestoredb.KeyHash(userID, kind)
}

// refreshHashClaimDocID reproduces idx_sessions_refresh_token_hash, which covers
// the CURRENT hash only. The rotated-away (grace) hash has a non-unique partial
// index in SQL and therefore takes no claim here — it stays queryable on the
// session document's own field, before AND after the grace slot is consumed.
func refreshHashClaimDocID(hash string) string { return firestoredb.KeyHash(hash) }

// apiKeyHashClaimDocID reproduces idx_api_keys_key_hash. It doubles as the
// GetByHash access path: the claim resolves the hash to its key's id.
func apiKeyHashClaimDocID(keyHash string) string { return firestoredb.KeyHash(keyHash) }

// invitationTokenClaimDocID reproduces idx_invitations_token_hash and doubles as
// the GetByTokenHash access path.
func invitationTokenClaimDocID(tokenHash string) string { return firestoredb.KeyHash(tokenHash) }

// invitationPendingClaimDocID reproduces idx_invitations_pending_tuple:
// (resource_type, resource_id, identifier_kind, identifier, relation) WHERE
// status = 'pending'. RELATION is part of the tuple — two invitations to the
// same address for the same resource under different relations coexist — and
// the predicate is the STORED status, never the read clock: an expired but still
// pending row keeps its claim.
func invitationPendingClaimDocID(resourceType, resourceID, identifierKind, identifierValue, relation string) string {
	return firestoredb.KeyHash(resourceType, resourceID, identifierKind, identifierValue, relation)
}

// challengeDigestClaimDocID reproduces idx_challenges_purpose_secret_digest and
// doubles as the ConsumeToken access path: the claim resolves
// (purpose, digest) to the owning challenge document.
func challengeDigestClaimDocID(purpose, secretDigest string) string {
	return firestoredb.KeyHash(purpose, secretDigest)
}

// The derived equality keys. Firestore charges for query complexity, not for
// fields: collapsing a multi-column SQL filter into ONE indexed equality keeps
// the composite manifest small and — the load-bearing half — keeps the filter
// off raw, unbounded port input, whose index entry would TRUNCATE past 1500
// bytes and could then match a different value (SCHEMA.md §4.2). The originals
// stay in their own fields and are what the port returns.

// invitationResourceKey is the (resource_type, resource_id) equality key for
// ListByResource.
func invitationResourceKey(resourceType, resourceID string) string {
	return firestoredb.KeyHash(resourceType, resourceID)
}

// invitationSubjectKey is the (identifier_kind, identifier) equality key for
// ListBySubject. The invitee address is arbitrary port input, which is exactly
// the truncation hazard above.
func invitationSubjectKey(identifierKind, identifierValue string) string {
	return firestoredb.KeyHash(identifierKind, identifierValue)
}

// grantConsumeKey is the (session_id, purpose, context_digest) equality key
// authgrant.Consume selects on — the SQL index
// idx_authentication_grants_session_purpose_context, collapsed to one clause.
func grantConsumeKey(sessionID, purpose, contextDigest string) string {
	return firestoredb.KeyHash(sessionID, purpose, contextDigest)
}
