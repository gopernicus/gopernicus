package firestore

import (
	gcfs "cloud.google.com/go/firestore"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
)

// The DIRECTORY PROJECTION (N-D3, SCHEMA.md §6) and the only code allowed to
// name its two fields.
//
// user.Summary carries the normalized value of the subject's ACTIVE PRIMARY
// EMAIL identifier and whether that identifier is proven. Both SQL adapters
// resolve the pair with a LEFT JOIN in the SAME statement as the page. Firestore
// has no join, and reading one identifier per listed user would make the
// operator directory O(page) round trips — the port explicitly forbids that
// ("an implementation must not issue one identifier read per user"). So the pair
// is PERSISTED on the users document and answered by the query the directory
// already issues.
//
// user_identifiers stays AUTHORITATIVE. This is a projection, and keeping it
// true is a transactional obligation of every writer that can change it: the
// projection is recomputed from the identifier rows the transaction read or is
// about to write, in that same transaction, and never assumed.
//
// Two invariants make it safe, and both are enforced by ownership rather than by
// review. First, THE USERS DOCUMENT IS NEVER WRITTEN WITH A WHOLE-DOCUMENT Set
// BUILT FROM A DOMAIN user.User: user.User has no email fields, so such a write
// would silently blank the projection and the directory would start answering
// empty addresses. Every non-identifier writer uses FIELD updates. Second, the
// two field names appear ONLY in this file and in documents.go's struct tags, so
// a future write path cannot set them from a partial view of the change —
// ownership_test.go fails the build if it tries.
const (
	fieldPrimaryEmail  = "primary_email"
	fieldEmailVerified = "email_verified"
)

// emailProjection is the pair as a value. Its zero value is the ABSENCE model
// and it is a real state, not a missing one: empty means "no active primary
// email on file" — the same fact the SQL LEFT JOIN reports as NULL — and
// verification is false whenever the address is empty, which is why the two
// fields are only ever written together.
type emailProjection struct {
	Address  string
	Verified bool
}

// projectionOf returns the projection one identifier row implies. Only an
// ACTIVE, PRIMARY, EMAIL row projects anything; every other row — a retired
// primary, a non-primary email, a phone — projects the empty pair, which is what
// makes "recompute from the rows in hand" a total function.
//
// The predicate is the SQL join predicate of userSummarySelect (kind = 'email'
// AND is_primary AND replaced_at IS NULL), NOT the authentication claim: a
// notification-only primary email is still the address the directory shows,
// unverified.
func projectionOf(row identifierDoc) (emailProjection, error) {
	if !row.Active || !row.IsPrimary || row.Kind != string(identifier.KindEmail) {
		return emailProjection{}, nil
	}
	verifiedAt, err := row.verifiedAt()
	if err != nil {
		return emailProjection{}, err
	}
	return emailProjection{Address: row.NormalizedValue, Verified: !verifiedAt.IsZero()}, nil
}

// resolveEmailProjection recomputes a user's projection from the identifier rows
// a transaction has in hand. Candidates are given in PRECEDENCE order — the rows
// as READ first, then the rows the transaction is about to WRITE — and a later
// candidate supersedes an earlier one with the same identifier id, so a
// retirement overrides the row's pre-change state.
//
// At most one candidate can be an active primary email (the identifier_primaries
// claim is what guarantees it), so the scan needs no tie-break; if none is, the
// answer is the empty projection and BOTH fields are cleared. That is the
// clearing half of SCHEMA §6.3 rows 3 and 6, and it is why every writer calls
// this rather than only writing when it has an address.
func resolveEmailProjection(candidates ...identifierDoc) (emailProjection, error) {
	latest := make(map[string]identifierDoc, len(candidates))
	order := make([]string, 0, len(candidates))
	for _, row := range candidates {
		if _, seen := latest[row.ID]; !seen {
			order = append(order, row.ID)
		}
		latest[row.ID] = row
	}
	for _, id := range order {
		p, err := projectionOf(latest[id])
		if err != nil {
			return emailProjection{}, err
		}
		if p.Address != "" {
			return p, nil
		}
	}
	return emailProjection{}, nil
}

// projectionOfUser reads the pair back off a users document.
func projectionOfUser(row userDoc) emailProjection {
	return emailProjection{Address: row.PrimaryEmail, Verified: row.EmailVerified}
}

// apply stamps the pair onto a users document being CREATED. Creation is the one
// path that writes the whole document, and it is safe there precisely because
// the document is built here rather than from a domain user.User.
func (p emailProjection) apply(row *userDoc) {
	row.PrimaryEmail = p.Address
	row.EmailVerified = p.Verified
}

// updates renders the pair as field updates for a users document being CHANGED.
// Both fields are always emitted: writing only the address would leave a stale
// verification flag behind when a verified primary is replaced by an unverified
// one.
func (p emailProjection) updates() []gcfs.Update {
	return []gcfs.Update{
		{Path: fieldPrimaryEmail, Value: p.Address},
		{Path: fieldEmailVerified, Value: p.Verified},
	}
}

// fill copies the pair into a directory summary.
func (p emailProjection) fill(s *user.Summary) {
	s.PrimaryEmail = p.Address
	s.EmailVerified = p.Verified
}
