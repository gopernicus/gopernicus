package authorization

import (
	"testing"

	"github.com/gopernicus/gopernicus/features/authorization/memstore"
)

// TestGetSchemaReturnsSnapshotAndDigest proves the host-facing GetSchema returns a
// deep read-only snapshot (mutating it cannot reach the runtime policy) and that
// SchemaDigest is a stable identifier for equivalent schemas.
func TestGetSchemaReturnsSnapshotAndDigest(t *testing.T) {
	comps, err := NewService(Repositories{Relationships: memstore.NewRelationships()}, Config{Model: validModel()})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	svc := comps.Service

	snap, err := svc.GetSchema()
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
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

	digest, err := svc.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest: %v", err)
	}
	if digest == "" || digest != snap.Digest() {
		t.Fatalf("SchemaDigest %q disagrees with snapshot digest %q", digest, snap.Digest())
	}

	// An independently constructed service over an equivalent schema reports the
	// same digest.
	other, err := NewService(Repositories{Relationships: memstore.NewRelationships()}, Config{Model: validModel()})
	if err != nil {
		t.Fatalf("NewService (other): %v", err)
	}
	otherDigest, err := other.Service.SchemaDigest()
	if err != nil {
		t.Fatalf("SchemaDigest (other): %v", err)
	}
	if otherDigest != digest {
		t.Fatalf("equivalent schemas produced different digests: %q vs %q", otherDigest, digest)
	}
}
