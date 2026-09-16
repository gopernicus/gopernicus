package invitations

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
)

func preparedInput() CreateInput {
	return CreateInput{ResourceType: " project ", ResourceID: " p1 ", Relation: " member ", Identifier: " Known@X.com ", IdentifierKind: " email ", InvitedBy: " inviter ", Metadata: map[string]string{"route": "original"}}
}

func TestPreparedCreateNormalizesOnceAndIsImmutable(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprint(direct), func(t *testing.T) {
			repo := newFakeInvRepo()
			granter := &fakeGranter{}
			lookups := 0
			svc := newSvc(t, repo, granter, constructorConfig{UserLookup: func(_ context.Context, email string) (string, bool, error) {
				lookups++
				if email != "known@x.com" {
					t.Fatalf("lookup identifier=%q", email)
				}
				return "verified-owner", true, nil
			}})
			in := preparedInput()
			in.AutoAccept = direct
			prepared, err := svc.PrepareCreate(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			inspected := prepared.Input()
			if inspected.ResourceType != "project" || inspected.ResourceID != "p1" || inspected.Relation != "member" || inspected.InvitedBy != "inviter" || inspected.Identifier != "known@x.com" || inspected.IdentifierKind != "email" || prepared.ResolvedSubjectID() != "verified-owner" {
				t.Fatalf("prepared=%+v subject=%s", inspected, prepared.ResolvedSubjectID())
			}
			if lookups != 1 || len(repo.byID) != 0 || len(granter.calls) != 0 {
				t.Fatal("preparation changed state or repeated lookup")
			}
			in.Metadata["route"] = "caller mutation"
			inspected.Metadata["route"] = "policy mutation"
			inspected.Relation = "owner"
			inspected.Identifier = "other@x.com"
			result, err := svc.CreatePrepared(t.Context(), prepared)
			if err != nil {
				t.Fatal(err)
			}
			if lookups != 1 {
				t.Fatalf("execute repeated lookup %d", lookups)
			}
			if direct {
				if !result.DirectlyAdded || len(granter.calls) != 1 {
					t.Fatalf("direct=%+v grants=%v", result, granter.calls)
				}
				call := granter.calls[0]
				if call.subjectID != "verified-owner" || call.relation != "member" || call.resourceID != "p1" || !maps.Equal(granter.metas[0], map[string]string{"route": "original"}) {
					t.Fatalf("grant changed checked command=%+v", call)
				}
			} else if result.Invitation.Identifier != "known@x.com" || result.Invitation.ResolvedSubjectID != "verified-owner" || result.Invitation.Relation != "member" || !maps.Equal(result.Invitation.Metadata, map[string]string{"route": "original"}) {
				t.Fatalf("persisted changed command=%+v", result.Invitation)
			}
			// Mutating one inspection or one execution's output cannot change future inspection.
			result.Invitation.Metadata = map[string]string{"route": "changed"}
			if prepared.Input().Metadata["route"] != "original" {
				t.Fatal("prepared input aliased caller state")
			}
		})
	}
}
func TestPreparedCreateSubjectResolutionKinds(t *testing.T) {
	for _, tc := range []struct {
		identifier, kind, want, subject string
		lookups                         int
	}{{"known@x.com", "", "known@x.com", "known", 1}, {"unknown@x.com", "", "unknown@x.com", "", 1}, {"+1 (555) 010-2345", sdk.AddressKindPhone, "+15550102345", "", 0}} {
		calls := 0
		svc := newSvc(t, newFakeInvRepo(), &fakeGranter{}, constructorConfig{UserLookup: func(_ context.Context, address string) (string, bool, error) {
			calls++
			return "known", address == "known@x.com", nil
		}, BodySenders: map[string]delivery.BodySender{sdk.AddressKindPhone: &fakeNotifier{}}})
		in := preparedInput()
		in.Identifier = tc.identifier
		in.IdentifierKind = tc.kind
		prepared, err := svc.PrepareCreate(t.Context(), in)
		if err != nil || prepared.Input().Identifier != tc.want || prepared.ResolvedSubjectID() != tc.subject || calls != tc.lookups {
			t.Fatalf("prepared=%+v subject=%s calls=%d err=%v", prepared.Input(), prepared.ResolvedSubjectID(), calls, err)
		}
	}
}
func TestPreparedCreateRejectsZeroForeignAndCanceledCommands(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, constructorConfig{})
	other := newSvc(t, repo, granter, constructorConfig{})
	prepared, err := other.PrepareCreate(t.Context(), preparedInput())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []PreparedCreate{{}, prepared} {
		if _, err := svc.CreatePrepared(t.Context(), p); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("foreign/zero accepted=%v", err)
		}
	}
	prepared, err = svc.PrepareCreate(t.Context(), preparedInput())
	if err != nil {
		t.Fatal(err)
	}
	copied := *svc
	if _, err := copied.CreatePrepared(t.Context(), prepared); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("foreign copied service accepted=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := svc.CreatePrepared(ctx, prepared); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled execute=%v", err)
	}
	if _, err := svc.PrepareCreate(ctx, preparedInput()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled prepare=%v", err)
	}
	if len(repo.byID) != 0 || len(granter.calls) != 0 {
		t.Fatal("invalid prepared command wrote state")
	}
}
func TestPreparedCreateRejectsInvalidInputBeforeLookup(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	calls := 0
	svc := newSvc(t, repo, granter, constructorConfig{UserLookup: func(context.Context, string) (string, bool, error) { calls++; return "known", true, nil }})
	for _, change := range []func(*CreateInput){func(in *CreateInput) { in.ResourceType = " " }, func(in *CreateInput) { in.ResourceID = "" }, func(in *CreateInput) { in.Relation = " " }, func(in *CreateInput) { in.InvitedBy = "" }, func(in *CreateInput) { in.Identifier = "" }, func(in *CreateInput) { in.IdentifierKind = "unsupported" }, func(in *CreateInput) {
		in.Metadata = map[string]string{"x": strings.Repeat("x", MetadataMaxValueBytes+1)}
	}} {
		in := preparedInput()
		in.AutoAccept = true
		change(&in)
		if _, err := svc.PrepareCreate(t.Context(), in); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("invalid prepare=%v", err)
		}
	}
	if calls != 0 || len(repo.byID) != 0 || len(granter.calls) != 0 {
		t.Fatal("invalid preparation caused I/O")
	}
}
