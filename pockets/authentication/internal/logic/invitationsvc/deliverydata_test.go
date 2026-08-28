package invitationsvc

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/pockets/authentication/internal/logic/delivery"
)

// hookCall is one recorded DataHook invocation.
type hookCall struct {
	purpose string
	data    map[string]any
}

// recordingHook is a concurrent-safe DataHook that records every call, returns
// extra as its additions, and fails with err when set.
type recordingHook struct {
	mu    sync.Mutex
	calls []hookCall
	extra map[string]any
	err   error
}

func (h *recordingHook) hook(_ context.Context, purpose string, data map[string]any) (map[string]any, error) {
	h.mu.Lock()
	h.calls = append(h.calls, hookCall{purpose: purpose, data: data})
	h.mu.Unlock()
	if h.err != nil {
		return nil, h.err
	}
	return h.extra, nil
}

func (h *recordingHook) only(t *testing.T, purpose string) hookCall {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.calls) != 1 {
		t.Fatalf("hook ran %d times, want 1: %+v", len(h.calls), h.calls)
	}
	if h.calls[0].purpose != purpose {
		t.Fatalf("hook purpose = %q, want %q", h.calls[0].purpose, purpose)
	}
	return h.calls[0]
}

// assertDeliveryData pins the per-purpose data contract the README documents: the
// tuple and provenance fields, the empty name-like defaults, a Link, a non-nil
// Metadata copy, and never a Secret or Subject.
func assertDeliveryData(t *testing.T, c hookCall, invitationID, operationID, invitedBy string, metadata map[string]string) {
	t.Helper()
	want := map[string]any{
		"InvitationID": invitationID, "OperationID": operationID,
		"ResourceType": "project", "ResourceID": "p1", "ResourceName": "", "ResourceKind": "",
		"Relation": "member", "RelationLabel": "",
		"InvitedBy": invitedBy, "InviterName": "",
	}
	for k, v := range want {
		if c.data[k] != v {
			t.Errorf("data[%s] = %#v, want %#v", k, c.data[k], v)
		}
	}
	for _, k := range []string{"Secret", "Subject"} {
		if _, ok := c.data[k]; ok {
			t.Errorf("hook received reserved %s", k)
		}
	}
	if link, _ := c.data["Link"].(string); link == "" {
		t.Errorf("data[Link] missing: %v", c.data)
	}
	md, ok := c.data["Metadata"].(map[string]string)
	if !ok || md == nil {
		t.Fatalf("data[Metadata] = %#v, want non-nil map[string]string", c.data["Metadata"])
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	if !reflect.DeepEqual(md, metadata) {
		t.Errorf("data[Metadata] = %v, want %v", md, metadata)
	}
}

func unknownUser(_ context.Context, _ string) (string, bool, error) { return "", false, nil }
func knownUser(_ context.Context, _ string) (string, bool, error)   { return "user-known", true, nil }

// A pending invitation renders with InvitationID == OperationID == the persisted
// row ID, the inviter, a defensive Metadata copy, and a token-carrying Link.
func TestCreatePendingDeliveryData(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	hook := &recordingHook{}
	svc := newSvc(t, repo, granter, Deps{UserLookup: unknownUser})
	wireSyncDeliveryHook(t, svc, &recordingMailer{}, nil, hook.hook)

	md := map[string]string{"routing_key": "org-42"}
	res, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "invitee@x.com", InvitedBy: "inviter", Metadata: md,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	c := hook.only(t, delivery.PurposeInvitation)
	assertDeliveryData(t, c, res.Invitation.ID, res.Invitation.ID, "inviter", md)
	if !strings.Contains(c.data["Link"].(string), "token=") {
		t.Errorf("invitation Link carries no token: %v", c.data["Link"])
	}
	// The hook's Metadata is a copy: mutating it never reaches the persisted row.
	c.data["Metadata"].(map[string]string)["routing_key"] = "tampered"
	stored, _ := repo.Get(context.Background(), res.Invitation.ID)
	if stored.Metadata["routing_key"] != "org-42" {
		t.Errorf("hook mutated persisted metadata: %v", stored.Metadata)
	}
}

// A direct add has no invitation row: InvitationID is empty and OperationID is the
// minted grant operation ID the Granter received.
func TestDirectAddDeliveryData(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	hook := &recordingHook{}
	svc := newSvc(t, repo, granter, Deps{UserLookup: knownUser})
	wireSyncDeliveryHook(t, svc, &recordingMailer{}, nil, hook.hook)

	md := map[string]string{"plan": "pro"}
	res, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "known@x.com", InvitedBy: "inviter", AutoAccept: true, Metadata: md,
	})
	if err != nil || !res.DirectlyAdded {
		t.Fatalf("Create: res=%+v err=%v", res, err)
	}
	if len(granter.ops) != 1 || granter.ops[0] == "" {
		t.Fatalf("grant ops = %v, want one non-empty operation id", granter.ops)
	}
	c := hook.only(t, delivery.PurposeMemberAdded)
	assertDeliveryData(t, c, "", granter.ops[0], "inviter", md)
}

// An accepted invitation's member-added notice carries the row ID as both
// InvitationID and OperationID, plus the stored inviter and metadata.
func TestAcceptDeliveryData(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	hook := &recordingHook{}
	svc := newSvc(t, repo, granter, Deps{})
	wireSyncDeliveryHook(t, svc, &recordingMailer{}, nil, hook.hook)

	md := map[string]string{"routing_key": "org-7"}
	inv := seedInviteMeta(t, repo, "project", "p1", "member", "invitee@x.com", "secret-a", false, time.Now().Add(time.Hour), md)
	if _, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-a", SubjectType: "user", SubjectID: "user-9", Identifier: "invitee@x.com"}); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	c := hook.only(t, delivery.PurposeMemberAdded)
	assertDeliveryData(t, c, inv.ID, inv.ID, "inviter", md)
	if c.data["OperationID"] != granter.ops[0] {
		t.Errorf("OperationID %v != granted operation %v", c.data["OperationID"], granter.ops[0])
	}
}

// A legacy row with no metadata still hands the hook a non-nil empty map.
func TestAcceptDeliveryDataLegacyMetadata(t *testing.T) {
	repo := newFakeInvRepo()
	hook := &recordingHook{}
	svc := newSvc(t, repo, &fakeGranter{}, Deps{})
	wireSyncDeliveryHook(t, svc, &recordingMailer{}, nil, hook.hook)

	inv := seedInvite(t, repo, "project", "p1", "member", "legacy@x.com", "inviter", "secret-b", false, time.Now().Add(time.Hour))
	if _, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-b", SubjectType: "user", SubjectID: "user-1", Identifier: "legacy@x.com"}); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	assertDeliveryData(t, hook.only(t, delivery.PurposeMemberAdded), inv.ID, inv.ID, "inviter", nil)
}

// A hook that sets ResourceName alone changes the delivered mail (the bundled body
// renders {{or .ResourceName .ResourceID}}); the accept link is untouched.
func TestHookEnrichmentReachesDeliveredMail(t *testing.T) {
	repo := newFakeInvRepo()
	mailer := &recordingMailer{}
	hook := &recordingHook{extra: map[string]any{"ResourceName": "Apollo"}}
	svc := newSvc(t, repo, &fakeGranter{}, Deps{UserLookup: unknownUser})
	wireSyncDeliveryHook(t, svc, mailer, nil, hook.hook)

	if _, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "invitee@x.com", InvitedBy: "inviter",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d mails, want 1", len(mailer.sent))
	}
	msg := mailer.sent[0]
	if !strings.Contains(msg.Text, "You were invited to project Apollo as member.") || !strings.Contains(msg.HTML, "project Apollo as member") {
		t.Errorf("delivered mail not enriched: text=%q", msg.Text)
	}
	if !strings.Contains(msg.Text, "token=") {
		t.Errorf("delivered mail lost the accept link: %q", msg.Text)
	}
}

// Create (pending): a hook error is returned to the caller together with the
// already-persisted invitation, and nothing is enqueued or sent.
func TestCreatePendingHookErrorReturnsPersistedInvitation(t *testing.T) {
	repo := newFakeInvRepo()
	mailer := &recordingMailer{}
	hookErr := errors.New("name lookup down")
	hook := &recordingHook{err: hookErr}
	svc := newSvc(t, repo, &fakeGranter{}, Deps{UserLookup: unknownUser})
	wireSyncDeliveryHook(t, svc, mailer, nil, hook.hook)

	res, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "invitee@x.com", InvitedBy: "inviter",
	})
	if !errors.Is(err, hookErr) {
		t.Fatalf("Create err=%v, want the hook error", err)
	}
	if res.Invitation.ID == "" {
		t.Fatalf("Create returned no invitation alongside the error: %+v", res)
	}
	if _, err := repo.Get(context.Background(), res.Invitation.ID); err != nil {
		t.Errorf("invitation not persisted: %v", err)
	}
	if len(mailer.sent) != 0 {
		t.Errorf("mail sent despite hook error: %+v", mailer.sent)
	}
}

// Resend: a hook error is returned so the owner can retry; the row stays pending.
func TestResendHookErrorReturned(t *testing.T) {
	repo := newFakeInvRepo()
	mailer := &recordingMailer{}
	hookErr := errors.New("name lookup down")
	hook := &recordingHook{err: hookErr}
	svc := newSvc(t, repo, &fakeGranter{}, Deps{})
	wireSyncDeliveryHook(t, svc, mailer, nil, hook.hook)

	inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "inviter", "secret-a", false, time.Now().Add(time.Hour))
	if _, err := svc.Resend(context.Background(), inv.ID, "inviter", ""); !errors.Is(err, hookErr) {
		t.Fatalf("Resend err=%v, want the hook error", err)
	}
	stored, _ := repo.Get(context.Background(), inv.ID)
	if stored.Status != invitation.StatusPending {
		t.Errorf("status after failed resend = %q, want pending", stored.Status)
	}
	if len(mailer.sent) != 0 {
		t.Errorf("mail sent despite hook error: %+v", mailer.sent)
	}
	// Recovery: once the hook succeeds, the same owner can resend.
	hook.err = nil
	if _, err := svc.Resend(context.Background(), inv.ID, "inviter", ""); err != nil {
		t.Fatalf("Resend after recovery: %v", err)
	}
	if len(mailer.sent) != 1 {
		t.Errorf("recovered resend sent %d mails, want 1", len(mailer.sent))
	}
}

// Member-added (direct add and accept): the grant has already committed, so a hook
// error is logged and never surfaced — the operation succeeds, no mail is sent.
func TestMemberAddedHookErrorIsBestEffort(t *testing.T) {
	t.Run("direct add", func(t *testing.T) {
		repo := newFakeInvRepo()
		granter := &fakeGranter{}
		mailer := &recordingMailer{}
		svc := newSvc(t, repo, granter, Deps{UserLookup: knownUser})
		wireSyncDeliveryHook(t, svc, mailer, nil, (&recordingHook{err: errors.New("down")}).hook)

		res, err := svc.Create(context.Background(), CreateInput{
			ResourceType: "project", ResourceID: "p1", Relation: "member",
			Identifier: "known@x.com", InvitedBy: "inviter", AutoAccept: true,
		})
		if err != nil || !res.DirectlyAdded {
			t.Fatalf("Create: res=%+v err=%v", res, err)
		}
		if len(granter.calls) != 1 {
			t.Errorf("grant calls = %d, want 1", len(granter.calls))
		}
		if len(mailer.sent) != 0 {
			t.Errorf("mail sent despite hook error: %+v", mailer.sent)
		}
	})
	t.Run("accept", func(t *testing.T) {
		repo := newFakeInvRepo()
		granter := &fakeGranter{}
		mailer := &recordingMailer{}
		svc := newSvc(t, repo, granter, Deps{})
		wireSyncDeliveryHook(t, svc, mailer, nil, (&recordingHook{err: errors.New("down")}).hook)

		inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "inviter", "secret-a", false, time.Now().Add(time.Hour))
		if _, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-a", SubjectType: "user", SubjectID: "user-9", Identifier: "invitee@x.com"}); err != nil {
			t.Fatalf("Accept: %v", err)
		}
		stored, _ := repo.Get(context.Background(), inv.ID)
		if stored.Status != invitation.StatusAccepted || len(granter.calls) != 1 {
			t.Errorf("accept did not commit: status=%q grants=%d", stored.Status, len(granter.calls))
		}
		if len(mailer.sent) != 0 {
			t.Errorf("mail sent despite hook error: %+v", mailer.sent)
		}
	})
}
