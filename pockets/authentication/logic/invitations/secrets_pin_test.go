package invitations

import (
	"context"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
)

func isInvitationSecret(s string) bool {
	return len(s) == tokenSecretLen && strings.Trim(s, sdk.DefaultIDAlphabet) == ""
}

// TestInvitationSecretsIgnoreTheEntityIDs pins the invitation secret and the
// direct-add operation id to the unconditional secret generator: under
// WithIDs(sdk.DatabaseID) the pending row's id is left to the store, but the
// secret behind its token hash and the operation id are minted in full.
func TestInvitationSecretsIgnoreTheEntityIDs(t *testing.T) {
	for _, s := range []string{mintSecret(), mintOperationID()} {
		if !isInvitationSecret(s) {
			t.Errorf("minted %q, want %d characters of the default alphabet", s, tokenSecretLen)
		}
	}
	if mintSecret() == mintSecret() || mintOperationID() == mintOperationID() {
		t.Error("two mints repeated")
	}

	ctx := context.Background()
	databaseIDs := sdk.NewIDGenerator(sdk.DatabaseID)
	repo := newFakeInvRepo()
	pending := newSvc(t, repo, &fakeGranter{}, constructorConfig{
		IDs:        databaseIDs,
		UserLookup: func(context.Context, string) (string, bool, error) { return "", false, nil },
	})
	res, err := pending.Create(ctx, CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "new@x.com", InvitedBy: "inviter"})
	if err != nil {
		t.Fatalf("Create (pending): %v", err)
	}
	if res.Invitation.ID != "" {
		t.Errorf("pending id = %q, want the DatabaseID empty id", res.Invitation.ID)
	}
	if len(res.Invitation.TokenHash) != 64 {
		t.Errorf("token hash %q is not the SHA-256 of a minted secret", res.Invitation.TokenHash)
	}

	granter := &fakeGranter{}
	direct := newSvc(t, newFakeInvRepo(), granter, constructorConfig{
		IDs:        databaseIDs,
		UserLookup: func(context.Context, string) (string, bool, error) { return "user-known", true, nil },
	})
	if _, err := direct.Create(ctx, CreateInput{ResourceType: "project", ResourceID: "p1", Relation: "member", Identifier: "known@x.com", InvitedBy: "inviter", AutoAccept: true}); err != nil {
		t.Fatalf("Create (direct add): %v", err)
	}
	if len(granter.ops) != 1 || !isInvitationSecret(granter.ops[0]) {
		t.Errorf("direct-add operation ids = %q, want one full-entropy id", granter.ops)
	}
}
