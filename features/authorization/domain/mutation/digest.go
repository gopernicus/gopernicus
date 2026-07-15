package mutation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"sort"
)

// MutationEncodingVersion identifies the canonical encoding hashed into a
// command's payload digest. The digest hashes this prefix plus the canonical
// bytes, so bumping it changes every digest — a deliberate, visible break. It is
// published beside the digest ([Command.PayloadEncoding], [Receipt.PayloadEncoding])
// so a stored digest can be interpreted against the encoding that produced it.
// It mirrors the schema-digest precedent (AZ3-0.2's SchemaEncodingVersion).
const MutationEncodingVersion = "gopernicus.authorization.mutation/1"

// PayloadEncoding returns the canonical encoding version [Command.PayloadDigest]
// is computed under (MutationEncodingVersion), published so a stored digest is
// interpretable.
func (c Command) PayloadEncoding() string { return MutationEncodingVersion }

// PayloadDigest returns the command's stable, actor-INDEPENDENT payload digest:
// SHA-256 over the version prefix plus the canonical bytes of the operation,
// scope, and the sorted, duplicate-order-independent row set. It deliberately
// EXCLUDES the MutationID (the digest is what distinguishes a replay of the same
// id from a reuse of that id under a different payload), the ExpectedRevision (a
// first-application precondition, not payload), and any actor. Two commands with
// the same operation, scope, and rows — regardless of slice order — yield the
// same digest; any change to the requested state yields a different one.
//
// A store persists this digest in the receipt; [MutationRepository.Apply]
// compares an incoming command's digest against the stored one to return the
// stable [MutationID] payload-mismatch command error on reuse, or an exact
// replay on a match.
func (c Command) PayloadDigest() string {
	h := sha256.New()
	io.WriteString(h, MutationEncodingVersion)
	h.Write([]byte{0}) // separate the version prefix from the canonical bytes

	writeMutString(h, string(c.Operation))
	writeMutString(h, string(c.Scope.Kind))
	writeMutString(h, c.Scope.Type)
	writeMutString(h, c.Scope.ID)

	rels := sortedRelationshipRows(c.Relationships)
	writeMutUint(h, len(rels))
	for _, r := range rels {
		writeMutString(h, r.Relation)
		writeMutString(h, r.Subject.Type)
		writeMutString(h, r.Subject.ID)
		writeMutString(h, r.Subject.Relation)
	}

	roles := sortedRoleRows(c.Roles)
	writeMutUint(h, len(roles))
	for _, r := range roles {
		writeMutString(h, r.SubjectType)
		writeMutString(h, r.SubjectID)
		writeMutString(h, r.Role)
	}

	return hex.EncodeToString(h.Sum(nil))
}

func sortedRelationshipRows(in []RelationshipRow) []RelationshipRow {
	out := append([]RelationshipRow(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Relation != b.Relation {
			return a.Relation < b.Relation
		}
		if a.Subject.Type != b.Subject.Type {
			return a.Subject.Type < b.Subject.Type
		}
		if a.Subject.ID != b.Subject.ID {
			return a.Subject.ID < b.Subject.ID
		}
		return a.Subject.Relation < b.Subject.Relation
	})
	return out
}

func sortedRoleRows(in []RoleRow) []RoleRow {
	out := append([]RoleRow(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.SubjectType != b.SubjectType {
			return a.SubjectType < b.SubjectType
		}
		if a.SubjectID != b.SubjectID {
			return a.SubjectID < b.SubjectID
		}
		return a.Role < b.Role
	})
	return out
}

func writeMutString(h io.Writer, s string) {
	writeMutUint(h, len(s))
	io.WriteString(h, s)
}

func writeMutUint(h io.Writer, v int) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	h.Write(b[:])
}
