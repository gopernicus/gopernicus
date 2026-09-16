package decisions_test

import (
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"

	"github.com/gopernicus/gopernicus/pockets/authorization"

	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

// TestGetSchemaReturnsSnapshotAndDigest proves the host-facing GetSchema returns a
// deep read-only snapshot (mutating it cannot reach the runtime policy) and that
// SchemaDigest is a stable identifier for equivalent schemas.
func TestGetSchemaReturnsSnapshotAndDigest(t *testing.T) {
	comps, err := authorization.New(authorization.Repositories{Tuples: memory.NewTuples()}, authorization.WithModel(validModel()))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps

	snap := svc.Decisions.GetSchema()
	if got := snap.ResourceTypes(); len(got) != 1 || got[0] != "post" {
		t.Fatalf("snapshot resource types = %v, want [post]", got)
	}

	// A snapshot accessor hands out copies; tampering cannot bleed back.
	subs := snap.AllowedSubjects("post", "owner")
	if len(subs) != 1 || subs[0].Type != "user" {
		t.Fatalf("AllowedSubjects(post, owner) = %v", subs)
	}
	subs[0].Type = "tampered"
	if again := snap.AllowedSubjects("post", "owner"); again[0].Type != "user" {
		t.Fatalf("snapshot accessor aliased its backing slice: %v", again)
	}

	digest := svc.Decisions.ModelDigest()
	if digest == "" || digest != snap.Digest() {
		t.Fatalf("SchemaDigest %q disagrees with snapshot digest %q", digest, snap.Digest())
	}

	// An independently constructed service over an equivalent schema reports the
	// same digest.
	other, err := authorization.New(authorization.Repositories{Tuples: memory.NewTuples()}, authorization.WithModel(validModel()))
	if err != nil {
		t.Fatalf("NewService (other): %v", err)
	}
	otherDigest := other.Decisions.ModelDigest()
	if otherDigest != digest {
		t.Fatalf("equivalent schemas produced different digests: %q vs %q", otherDigest, digest)
	}
}

func validModel() decisions.Model {
	return decisions.NewSchema([]decisions.ResourceSchema{{
		Name: "post",
		Def: decisions.ResourceTypeDef{
			Relations:   map[string]decisions.RelationDef{"owner": {AllowedSubjects: []decisions.SubjectTypeRef{{Type: "user"}}}},
			Permissions: map[string]decisions.Expression{"delete": decisions.AnyOf(decisions.Direct("owner"))},
		},
	}})
}
