package firestore

import (
	"regexp"
	"strings"
	"testing"

	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
)

// hexID is the shape every natural document id must have: 64 lowercase hex
// characters. That is what makes a port-legal natural key — up to 1536 bytes,
// possibly containing "/", possibly a reserved name — a legal Firestore
// document id at all (connector compatibility note N1).
var hexID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// sqlConcat is the transcription of the SQL adapters' `a || char(1) || b || …`
// derived-key expressions: turso spells the separator char(1) and pgx spells it
// chr(1); both emit the single byte U+0001. It exists so the parity tests below
// compare against the SQL EXPRESSION rather than against the Go implementation
// they are meant to check.
func sqlConcat(parts ...string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString("\x01") // char(1) / chr(1)
		}
		b.WriteString(p)
	}
	return b.String()
}

func TestTupleSortPartsPreserveFullNaturalOrderWithinIndexLimits(t *testing.T) {
	values := []string{"a", "a!", "a/b", "aa", "~", "é", "😀", strings.Repeat("x", 256), strings.Repeat("😀", 64)}
	var rows []relationshipDoc
	for _, value := range values {
		for field := 0; field < 6; field++ {
			parts := []string{"doc", "d", "viewer", "group", "g", ""}
			parts[field] = value
			rows = append(rows, relationshipDoc{ResourceType: parts[0], ResourceID: parts[1], Relation: parts[2], SubjectType: parts[3], SubjectID: parts[4], SubjectRelation: parts[5]})
		}
		rows = append(rows, relationshipDoc{ResourceType: value, ResourceID: value, Relation: value, SubjectType: value, SubjectID: value, SubjectRelation: value})
	}
	full := func(row relationshipDoc) string {
		return sqlConcat(row.ResourceType, row.ResourceID, row.Relation, row.SubjectType, row.SubjectID, row.SubjectRelation)
	}
	for _, left := range rows {
		prefix, suffix := tupleSortKeys(left)
		if len(prefix) > 1284 || len(suffix) > 257 || suffix == "" || prefix+suffix != full(left) {
			t.Fatalf("invalid tuple ordering parts: %d/%d bytes for %+v", len(prefix), len(suffix), left)
		}
		for _, right := range rows {
			rp, rs := tupleSortKeys(right)
			order := strings.Compare(prefix, rp)
			if order == 0 {
				order = strings.Compare(suffix, rs)
			}
			if want := strings.Compare(full(left), full(right)); order != want {
				t.Fatalf("split order differs from full tuple order: %+v / %+v", left, right)
			}
		}
	}
}

// TestRoleKeyMatchesTheSQLExpression pins role_key byte-for-byte against
// stores/turso/roles.go's roleKeyExpr:
//
//	subject_type || char(1) || subject_id || char(1) || role
//	  || char(1) || resource_type || char(1) || resource_id
//
// It is a CONTRACTUAL sort key — the listings' keyset tiebreak and cursor PK —
// so a divergence here is a paging divergence between the families, not a
// cosmetic one.
func TestRoleKeyMatchesTheSQLExpression(t *testing.T) {
	cases := []struct {
		name                                         string
		subjectType, subjectID, role, resType, resID string
		want                                         string
	}{
		{
			name:        "scoped grant",
			subjectType: "user", subjectID: "u1", role: "admin", resType: "tenant", resID: "t1",
			want: "user\x01u1\x01admin\x01tenant\x01t1",
		},
		{
			name:        "global grant keeps both empty scope components",
			subjectType: "user", subjectID: "u1", role: "admin", resType: "", resID: "",
			want: "user\x01u1\x01admin\x01\x01",
		},
		{
			name:        "slashes are ordinary bytes in a sort key",
			subjectType: "user", subjectID: "a/b", role: "r", resType: "t/y", resID: "i/d",
			want: "user\x01a/b\x01r\x01t/y\x01i/d",
		},
		{
			name:        "unicode is preserved verbatim",
			subjectType: "user", subjectID: "ünïcødé", role: "röle", resType: "資源", resID: "🔑",
			want: "user\x01ünïcødé\x01röle\x01資源\x01🔑",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := roleKey(tc.subjectType, tc.subjectID, tc.role, tc.resType, tc.resID)
			if got != tc.want {
				t.Fatalf("roleKey = %q, want the golden %q", got, tc.want)
			}
			if sql := sqlConcat(tc.subjectType, tc.subjectID, tc.role, tc.resType, tc.resID); got != sql {
				t.Fatalf("roleKey = %q, but the SQL roleKeyExpr yields %q", got, sql)
			}
		})
	}
}

// TestGrantKeyMatchesTheSQLExpression pins grant_key against
// effectiveGrantKeyExpr — `subject_type || char(1) || subject_id || char(1) ||
// role` — the effective listing's ORDER field and cursor PK.
func TestGrantKeyMatchesTheSQLExpression(t *testing.T) {
	cases := []struct {
		name                         string
		subjectType, subjectID, role string
		want                         string
	}{
		{"plain", "user", "u1", "admin", "user\x01u1\x01admin"},
		{"slash", "user", "a/b", "r/w", "user\x01a/b\x01r/w"},
		{"unicode", "user", "ü", "röle", "user\x01ü\x01röle"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := grantKey(tc.subjectType, tc.subjectID, tc.role)
			if got != tc.want {
				t.Fatalf("grantKey = %q, want the golden %q", got, tc.want)
			}
			if sql := sqlConcat(tc.subjectType, tc.subjectID, tc.role); got != sql {
				t.Fatalf("grantKey = %q, but the SQL effectiveGrantKeyExpr yields %q", got, sql)
			}
		})
	}
}

// TestGrantKeyIsRoleKeysPrefix records the relationship the two derived keys
// have in SQL and must keep here: grant_key is role_key's first three
// components, so a subject's grants group the same way under both.
func TestGrantKeyIsRoleKeysPrefix(t *testing.T) {
	rk := roleKey("user", "u1", "admin", "tenant", "t1")
	gk := grantKey("user", "u1", "admin")
	if !strings.HasPrefix(rk, gk+"\x01") {
		t.Fatalf("role_key %q does not extend grant_key %q", rk, gk)
	}
}

// TestSortKeyOrderIsRawByteOrder proves the ordering property the three
// families share: Go string comparison over these keys is UTF-8 byte order,
// which is what SQLite's BINARY collation, postgres's COLLATE "C", and
// Firestore's index order all give. The separator sorting BELOW every legal
// component byte is what keeps a shorter component from sorting inside a longer
// one (ValidateRefField rejects control characters, so no component can contain
// U+0001 itself).
func TestSortKeyOrderIsRawByteOrder(t *testing.T) {
	ordered := []string{
		grantKey("user", "u1", "admin"),
		grantKey("user", "u1", "viewer"),
		grantKey("user", "u10", "admin"),
		grantKey("user", "u2", "admin"),
	}
	for i := 1; i < len(ordered); i++ {
		if !(ordered[i-1] < ordered[i]) {
			t.Fatalf("grant keys are not in ascending byte order: %q >= %q", ordered[i-1], ordered[i])
		}
	}
}

// TestDocumentIDsAreLegalFirestoreIDs is compatibility note N1's assertion at
// this store: every natural key — including the six-component tuple at the
// port's maximum component size (1536 bytes, past the 1500-byte id limit), one
// carrying slashes, and one spelling a reserved name — hashes to a legal id.
func TestDocumentIDsAreLegalFirestoreIDs(t *testing.T) {
	max := strings.Repeat("x", authmodel.MaxRefFieldLen)
	cases := map[string]string{
		"plain":              relationshipDocID("doc", "d1", "owner", "user", "u1", ""),
		"userset":            relationshipDocID("doc", "d1", "viewer", "group", "eng", "member"),
		"slashes":            relationshipDocID("a/b", "c/d", "e/f", "g/h", "i/j", "k/l"),
		"reserved name":      relationshipDocID("__name__", "..", ".", "__id__", "__x__", ""),
		"maximum components": relationshipDocID(max, max, max, max, max, max),
		"unicode":            relationshipDocID("dôc", "🗂", "ownër", "üser", "u¹", "membér"),
		"subject claim":      subjectClaimDocID(max, max, max, max, max),
		"role":               roleDocID(max, max, max, "", ""),
	}

	for name, id := range cases {
		if !hexID.MatchString(id) {
			t.Errorf("%s: document id %q is not 64 lowercase hex characters", name, id)
		}
		if strings.Contains(id, "/") {
			t.Errorf("%s: document id %q contains a slash", name, id)
		}
	}
	if got := len(cases["maximum components"]); got != 64 {
		t.Fatalf("a 1536-byte natural key must still hash to 64 characters, got %d", got)
	}
}

// TestDocumentIDsDoNotAliasAcrossComponentBoundaries is why the ids are
// length-prefixed (KeyHash) rather than joined: two tuples that differ only in
// where a component boundary falls must never share a document, or one write
// would silently no-op over an unrelated row.
func TestDocumentIDsDoNotAliasAcrossComponentBoundaries(t *testing.T) {
	pairs := [][2]string{
		{
			relationshipDocID("doc", "d1", "owner", "user", "u1", ""),
			relationshipDocID("do", "cd1", "owner", "user", "u1", ""),
		},
		{
			// the userset relation is load-bearing: group:eng and group:eng#member
			// are different subjects, so they are different tuples.
			relationshipDocID("doc", "d1", "viewer", "group", "eng", ""),
			relationshipDocID("doc", "d1", "viewer", "group", "eng", "member"),
		},
		{
			// a global role grant is the EMPTY pair, not an absent one.
			roleDocID("user", "u1", "admin", "", ""),
			roleDocID("user", "u1", "admin", "", "x"),
		},
		{
			// the two subject_key shapes have different arity by design.
			subjectKey("user", "u1", ""),
			roleSubjectKey("user", "u1"),
		},
		{
			resourceKey("a", "bc"),
			resourceKey("ab", "c"),
		},
	}

	for i, p := range pairs {
		if p[0] == p[1] {
			t.Errorf("pair %d: distinct keys collided on %q", i, p[0])
		}
	}
}

// TestDerivedKeysAreDeterministic — the same tuple must always land on the same
// document, across processes and releases; the claim documents depend on it.
func TestDerivedKeysAreDeterministic(t *testing.T) {
	if a, b := relationshipDocID("doc", "d1", "owner", "user", "u1", ""), relationshipDocID("doc", "d1", "owner", "user", "u1", ""); a != b {
		t.Fatalf("relationshipDocID is not deterministic: %q != %q", a, b)
	}
	// The golden ids. Changing one is a SCHEMA change — every stored document
	// moves and every claim is orphaned — never a refactor.
	goldens := map[string]struct{ got, want string }{
		"tuple":   {relationshipDocID("doc", "d1", "owner", "user", "u1", ""), "fa8297b15d943380dcbd7cd3da788ea02dccd738d201a39896a3f68671cb3100"},
		"subject": {subjectClaimDocID("doc", "d1", "user", "u1", ""), "0ef3b055cf09ef927cdedcac3ba13feaccf527957f016bd096e9c49b2dafac19"},
		"role":    {roleDocID("user", "u1", "admin", "tenant", "t1"), "62e6d75d2e1ded919ecb86b9963428656cc1d8a90600de1569752b1339229bd7"},
	}
	for name, g := range goldens {
		if g.got != g.want {
			t.Errorf("%s document id = %q, want %q — the document layout moved", name, g.got, g.want)
		}
	}
}

// TestClaimIDsSeparateTheirNamespaces — the subject claim is the tuple id minus
// `relation`, so a five-part and a six-part hash must not collide even when the
// components read alike.
func TestClaimIDsSeparateTheirNamespaces(t *testing.T) {
	tuple := relationshipDocID("doc", "d1", "owner", "user", "u1", "")
	claim := subjectClaimDocID("doc", "d1", "user", "u1", "")
	if tuple == claim {
		t.Fatalf("the tuple id and its subject claim id collided on %q", tuple)
	}
}
