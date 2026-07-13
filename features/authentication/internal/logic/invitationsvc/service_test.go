package invitationsvc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/invitation"
	"github.com/gopernicus/gopernicus/features/authentication/internal/redirect"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/email"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/crud"
	"github.com/gopernicus/gopernicus/sdk/foundation/cryptids"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

// --- fakes ---

type grantCall struct {
	resourceType, resourceID, relation, subjectType, subjectID string
}

type fakeGranter struct {
	calls  []grantCall
	err    error  // blanket failure
	failOn string // fail only when resourceID == failOn
}

func (g *fakeGranter) Grant(_ context.Context, rt, rid, rel, st, sid string) error {
	g.calls = append(g.calls, grantCall{rt, rid, rel, st, sid})
	if g.err != nil {
		return g.err
	}
	if g.failOn != "" && rid == g.failOn {
		return errors.New("grant rejected")
	}
	return nil
}

type recordingMailer struct{ sent []email.Message }

func (m *recordingMailer) Send(_ context.Context, msg email.Message) error {
	m.sent = append(m.sent, msg)
	return nil
}

// delivered records one notify.Notifier delivery for the delivery-seam tests.
type delivered struct {
	to  identity.Address
	msg notify.Message
}

// fakeNotifier is an in-package notify.Notifier for the delivery-fork tests: it
// declares one kind and records every delivery.
type fakeNotifier struct {
	kind string
	got  []delivered
}

func (n *fakeNotifier) Kind() string { return n.kind }

func (n *fakeNotifier) Notify(_ context.Context, to identity.Address, msg notify.Message) error {
	n.got = append(n.got, delivered{to: to, msg: msg})
	return nil
}

// fakeInvRepo is a minimal in-memory invitation repository for service tests. It
// enforces the pending-tuple uniqueness the service relies on and the read-time
// token expiry, but pages everything in one shot (tests use small populations).
type fakeInvRepo struct {
	byID map[string]invitation.Invitation
}

func newFakeInvRepo() *fakeInvRepo { return &fakeInvRepo{byID: map[string]invitation.Invitation{}} }

func (r *fakeInvRepo) Create(_ context.Context, inv invitation.Invitation) (invitation.Invitation, error) {
	for _, ex := range r.byID {
		if ex.Status == invitation.StatusPending &&
			ex.ResourceType == inv.ResourceType && ex.ResourceID == inv.ResourceID &&
			ex.Identifier == inv.Identifier && ex.Relation == inv.Relation {
			return invitation.Invitation{}, sdk.ErrAlreadyExists
		}
	}
	r.byID[inv.ID] = inv
	return inv, nil
}

func (r *fakeInvRepo) Get(_ context.Context, id string) (invitation.Invitation, error) {
	inv, ok := r.byID[id]
	if !ok {
		return invitation.Invitation{}, sdk.ErrNotFound
	}
	return inv, nil
}

func (r *fakeInvRepo) GetByTokenHash(_ context.Context, tokenHash string) (invitation.Invitation, error) {
	for _, inv := range r.byID {
		if inv.TokenHash == tokenHash {
			if inv.Expired(time.Now()) {
				return invitation.Invitation{}, sdk.ErrExpired
			}
			return inv, nil
		}
	}
	return invitation.Invitation{}, sdk.ErrNotFound
}

func (r *fakeInvRepo) ListByResource(_ context.Context, resourceType, resourceID string, _ crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	var items []invitation.Invitation
	for _, inv := range r.byID {
		if inv.ResourceType == resourceType && inv.ResourceID == resourceID {
			items = append(items, inv)
		}
	}
	return crud.Page[invitation.Invitation]{Items: items}, nil
}

func (r *fakeInvRepo) ListBySubject(_ context.Context, kind, identifier string, _ crud.ListRequest) (crud.Page[invitation.Invitation], error) {
	var items []invitation.Invitation
	for _, inv := range r.byID {
		if inv.IdentifierKind == kind && inv.Identifier == identifier {
			items = append(items, inv)
		}
	}
	return crud.Page[invitation.Invitation]{Items: items}, nil
}

func (r *fakeInvRepo) UpdateStatus(_ context.Context, id string, upd invitation.StatusUpdate) (invitation.Invitation, error) {
	inv, ok := r.byID[id]
	if !ok {
		return invitation.Invitation{}, sdk.ErrNotFound
	}
	inv.Status = upd.Status
	inv.TokenHash = upd.TokenHash
	inv.ExpiresAt = upd.ExpiresAt
	inv.AcceptedAt = upd.AcceptedAt
	inv.ResolvedSubjectID = upd.ResolvedSubjectID
	inv.UpdatedAt = upd.UpdatedAt
	r.byID[id] = inv
	return inv, nil
}

// --- helpers ---

func hashOf(t *testing.T, secret string) string {
	t.Helper()
	h, err := cryptids.NewSHA256Hasher().Hash(secret)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return h
}

// seedInvite builds a pending EMAIL-kind invitation with a known token hash and
// expiry and puts it straight into the repo (bypassing Create's minting).
// Kind-specific cases seed with seedInviteKind.
func seedInvite(t *testing.T, repo *fakeInvRepo, rt, rid, rel, ident, invitedBy, secret string, autoAccept bool, expiresAt time.Time) invitation.Invitation {
	t.Helper()
	return seedInviteKind(t, repo, rt, rid, rel, ident, identity.KindEmail, invitedBy, secret, autoAccept, expiresAt)
}

// seedInviteKind is seedInvite with an explicit identifier kind.
func seedInviteKind(t *testing.T, repo *fakeInvRepo, rt, rid, rel, ident, kind, invitedBy, secret string, autoAccept bool, expiresAt time.Time) invitation.Invitation {
	t.Helper()
	inv, err := invitation.New(cryptids.IDGenerator{}, rt, rid, rel, ident, kind, invitedBy, hashOf(t, secret), autoAccept, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inv.ExpiresAt = expiresAt
	repo.byID[inv.ID] = inv
	return inv
}

func newSvc(t *testing.T, repo *fakeInvRepo, granter Granter, d Deps) *Service {
	t.Helper()
	d.Invitations = repo
	d.Granter = granter
	if d.Mailer == nil {
		d.Mailer = &recordingMailer{}
	}
	d.Redirects = redirect.New(nil)
	svc := New(d)
	// Wire the durable outbox with a synchronous drain so the invitation/member-added
	// send sites enqueue and the worker delivers to the wired transports within the
	// call, keeping the delivery assertions direct.
	wireSyncDelivery(t, svc, d.Mailer, d.Notifiers)
	return svc
}

// --- tests ---

// TestAcceptGrantsWithTupleArgs is the pinned fake-Granter assertion: Grant is
// called with EXACTLY the invitation's tuple-shaped args, and the invitation is
// marked accepted with the resolved subject id.
func TestAcceptGrantsWithTupleArgs(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{})

	inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "inviter", "secret-a", false, time.Now().Add(time.Hour))

	res, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-a", SubjectType: "user", SubjectID: "user-9", Identifier: "Invitee@x.com"})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if res.ResourceType != "project" || res.ResourceID != "p1" || res.Relation != "member" {
		t.Errorf("AcceptResult = %+v", res)
	}
	want := grantCall{"project", "p1", "member", "user", "user-9"}
	if len(granter.calls) != 1 || granter.calls[0] != want {
		t.Fatalf("Grant calls = %+v, want exactly [%+v]", granter.calls, want)
	}
	got, _ := repo.Get(context.Background(), inv.ID)
	if got.Status != invitation.StatusAccepted || got.ResolvedSubjectID != "user-9" || got.AcceptedAt.IsZero() {
		t.Errorf("invitation not marked accepted+resolved: %+v", got)
	}
}

func TestAcceptIdentifierMismatch(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{})
	seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "inviter", "secret-a", false, time.Now().Add(time.Hour))

	_, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-a", SubjectType: "user", SubjectID: "user-9", Identifier: "someone-else@x.com"})
	if !errors.Is(err, sdk.ErrForbidden) {
		t.Errorf("Accept(mismatch): err=%v, want ErrForbidden", err)
	}
	if len(granter.calls) != 0 {
		t.Errorf("Grant called on identifier mismatch: %+v", granter.calls)
	}
}

func TestAcceptExpiredToken(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{})
	seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "inviter", "secret-a", false, time.Now().Add(-time.Hour))

	_, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-a", SubjectType: "user", SubjectID: "user-9", Identifier: "invitee@x.com"})
	if !errors.Is(err, sdk.ErrExpired) {
		t.Errorf("Accept(expired): err=%v, want ErrExpired", err)
	}
	if len(granter.calls) != 0 {
		t.Errorf("Grant called for an expired token: %+v", granter.calls)
	}
}

// TestCreateDirectAddGrantsKnownUser: AutoAccept + a known invitee → an
// immediate grant with the tuple args and no pending record.
func TestCreateDirectAddGrantsKnownUser(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{
		UserLookup: func(_ context.Context, _ string) (string, bool, error) { return "user-known", true, nil },
	})

	res, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "known@x.com", InvitedBy: "inviter", AutoAccept: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !res.DirectlyAdded {
		t.Errorf("DirectlyAdded = false, want true")
	}
	want := grantCall{"project", "p1", "member", "user", "user-known"}
	if len(granter.calls) != 1 || granter.calls[0] != want {
		t.Fatalf("Grant calls = %+v, want [%+v]", granter.calls, want)
	}
	if len(repo.byID) != 0 {
		t.Errorf("direct add created a pending record: %d rows", len(repo.byID))
	}
}

// TestCreateMemberCheckDupPath: a known invitee that MemberCheck reports is
// already a member → ErrAlreadyMember, no grant.
func TestCreateMemberCheckDupPath(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{
		UserLookup:  func(_ context.Context, _ string) (string, bool, error) { return "user-known", true, nil },
		MemberCheck: func(_ context.Context, _, _, _, _ string) (bool, error) { return true, nil },
	})

	_, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "known@x.com", InvitedBy: "inviter", AutoAccept: true,
	})
	if !errors.Is(err, sdk.ErrConflict) {
		t.Errorf("Create(already member): err=%v, want ErrConflict (ErrAlreadyMember)", err)
	}
	if len(granter.calls) != 0 {
		t.Errorf("Grant called despite an existing member: %+v", granter.calls)
	}
}

// TestCreatePendingUnknownUser: an unknown invitee → a pending record + mail, no
// grant; a second create for the same tuple → ErrPendingInvitationExists.
func TestCreatePendingUnknownUser(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	mailer := &recordingMailer{}
	svc := newSvc(t, repo, granter, Deps{
		Mailer:     mailer,
		UserLookup: func(_ context.Context, _ string) (string, bool, error) { return "", false, nil },
	})

	res, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "new@x.com", InvitedBy: "inviter", AutoAccept: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.DirectlyAdded || res.Invitation.Status != invitation.StatusPending {
		t.Errorf("expected a pending invitation, got %+v", res)
	}
	if len(granter.calls) != 0 {
		t.Errorf("Grant called for an unknown invitee: %+v", granter.calls)
	}
	if len(mailer.sent) != 1 || len(mailer.sent[0].To) != 1 || mailer.sent[0].To[0] != "new@x.com" {
		t.Errorf("invite-sent mail = %+v, want one to new@x.com", mailer.sent)
	}

	// A second pending invite for the same tuple collides.
	_, err = svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "new@x.com", InvitedBy: "inviter", AutoAccept: true,
	})
	if !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Errorf("second Create same tuple: err=%v, want ErrAlreadyExists", err)
	}
}

// TestResolveInvitationsBestEffort: resolve-on-registration grants every pending
// auto-accept invite for the email, skips non-auto-accept and expired ones, and
// a single failed grant never aborts the rest (best-effort).
func TestResolveInvitationsBestEffort(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{failOn: "B"} // the grant on resource B fails
	svc := newSvc(t, repo, granter, Deps{})

	future := time.Now().Add(time.Hour)
	invA := seedInvite(t, repo, "project", "A", "member", "sub@x.com", "inviter", "s-a", true, future)
	invB := seedInvite(t, repo, "project", "B", "member", "sub@x.com", "inviter", "s-b", true, future)
	invC := seedInvite(t, repo, "project", "C", "member", "sub@x.com", "inviter", "s-c", true, future)
	manual := seedInvite(t, repo, "project", "D", "member", "sub@x.com", "inviter", "s-d", false, future) // not auto-accept
	expired := seedInvite(t, repo, "project", "E", "member", "sub@x.com", "inviter", "s-e", true, time.Now().Add(-time.Hour))
	other := seedInvite(t, repo, "project", "F", "member", "other@x.com", "inviter", "s-f", true, future) // different email

	n, err := svc.ResolveInvitations(context.Background(), "Sub@x.com", "user", "user-7")
	if err != nil {
		t.Fatalf("ResolveInvitations: %v", err)
	}
	if n != 2 { // A and C succeed; B's grant failed
		t.Errorf("resolved count = %d, want 2 (A,C; B failed)", n)
	}

	assertStatus := func(id, want string) {
		got, _ := repo.Get(context.Background(), id)
		if got.Status != want {
			t.Errorf("invitation %s status = %q, want %q", id, got.Status, want)
		}
	}
	assertStatus(invA.ID, invitation.StatusAccepted)
	assertStatus(invC.ID, invitation.StatusAccepted)
	assertStatus(invB.ID, invitation.StatusPending) // grant failed → stays pending
	assertStatus(manual.ID, invitation.StatusPending)
	assertStatus(expired.ID, invitation.StatusPending)
	assertStatus(other.ID, invitation.StatusPending)

	// Grant was attempted for the three auto-accept, non-expired, matching invites.
	if len(granter.calls) != 3 {
		t.Errorf("Grant call count = %d, want 3 (A,B,C)", len(granter.calls))
	}
}

func TestCancelOwnership(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{})
	inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "owner", "s-a", false, time.Now().Add(time.Hour))

	if err := svc.Cancel(context.Background(), inv.ID, "not-owner"); !errors.Is(err, sdk.ErrForbidden) {
		t.Errorf("Cancel(non-owner): err=%v, want ErrForbidden", err)
	}
	if err := svc.Cancel(context.Background(), inv.ID, "owner"); err != nil {
		t.Fatalf("Cancel(owner): %v", err)
	}
	got, _ := repo.Get(context.Background(), inv.ID)
	if got.Status != invitation.StatusCancelled {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
}

func TestResendOwnership(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	mailer := &recordingMailer{}
	svc := newSvc(t, repo, granter, Deps{Mailer: mailer})
	inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "owner", "s-a", false, time.Now().Add(time.Hour))
	originalHash := inv.TokenHash

	if _, err := svc.Resend(context.Background(), inv.ID, "not-owner", ""); !errors.Is(err, sdk.ErrForbidden) {
		t.Errorf("Resend(non-owner): err=%v, want ErrForbidden", err)
	}
	updated, err := svc.Resend(context.Background(), inv.ID, "owner", "")
	if err != nil {
		t.Fatalf("Resend(owner): %v", err)
	}
	if updated.TokenHash == originalHash {
		t.Errorf("Resend did not regenerate the token hash")
	}
	if updated.Status != invitation.StatusPending {
		t.Errorf("status = %q, want pending", updated.Status)
	}
	if len(mailer.sent) != 1 {
		t.Errorf("resend mail count = %d, want 1", len(mailer.sent))
	}
}

func TestDeclineTokenAuthorized(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{})
	inv := seedInvite(t, repo, "project", "p1", "member", "invitee@x.com", "owner", "s-a", false, time.Now().Add(time.Hour))

	// A wrong token leaks nothing.
	if err := svc.Decline(context.Background(), inv.ID, "wrong-token"); !errors.Is(err, sdk.ErrNotFound) {
		t.Errorf("Decline(wrong token): err=%v, want ErrNotFound", err)
	}
	// The invitee's token declines it.
	if err := svc.Decline(context.Background(), inv.ID, "s-a"); err != nil {
		t.Fatalf("Decline(correct token): %v", err)
	}
	got, _ := repo.Get(context.Background(), inv.ID)
	if got.Status != invitation.StatusDeclined {
		t.Errorf("status = %q, want declined", got.Status)
	}
	if len(granter.calls) != 0 {
		t.Errorf("Decline granted something: %+v", granter.calls)
	}
}

// --- delivery-seam (kind-aware, delta-fold 1/4/6) ---

// TestCreateUnsupportedKind: a non-email kind with no wired notifier of that kind
// → the loud ErrKindNotSupported (wrapping ErrInvalidInput → 400), and NO record
// is created (deny-by-absence, ruling 6). The email Mailer being wired does not
// make phone supported.
func TestCreateUnsupportedKind(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{}) // Mailer defaulted; no notifiers

	_, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "+15550100", IdentifierKind: identity.KindPhone, InvitedBy: "inviter",
	})
	if !errors.Is(err, ErrKindNotSupported) {
		t.Errorf("Create(unsupported kind): err=%v, want ErrKindNotSupported", err)
	}
	if !errors.Is(err, sdk.ErrInvalidInput) {
		t.Errorf("ErrKindNotSupported must wrap ErrInvalidInput (400); err=%v", err)
	}
	if len(repo.byID) != 0 {
		t.Errorf("unsupported kind created a record: %d rows", len(repo.byID))
	}
}

// TestCreateKindDeliveredViaNotifier: a supported non-email kind is minted pending
// and the TOKEN is DELIVERED to the invited address via the wired notifier — the
// only channel (no plaintext hand-back; CreateResult carries no token). The email
// Mailer is not touched.
func TestCreateKindDeliveredViaNotifier(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	mailer := &recordingMailer{}
	notifier := &fakeNotifier{kind: identity.KindPhone}
	svc := newSvc(t, repo, granter, Deps{
		Mailer:    mailer,
		Notifiers: map[string]notify.Notifier{identity.KindPhone: notifier},
	})

	res, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "+15550100", IdentifierKind: identity.KindPhone, InvitedBy: "inviter",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.DirectlyAdded || res.Invitation.Status != invitation.StatusPending || res.Invitation.IdentifierKind != identity.KindPhone {
		t.Errorf("expected a pending phone invitation, got %+v", res)
	}
	if len(notifier.got) != 1 {
		t.Fatalf("notifier deliveries = %d, want 1", len(notifier.got))
	}
	d := notifier.got[0]
	if d.to.Kind != identity.KindPhone || d.to.Value != "+15550100" {
		t.Errorf("delivered to = %+v, want {phone, +15550100}", d.to)
	}
	if !strings.Contains(d.msg.Body, "token=") {
		t.Errorf("notifier body missing the delivered token link: %q", d.msg.Body)
	}
	if len(mailer.sent) != 0 {
		t.Errorf("a non-email invite rode the email Mailer: %+v", mailer.sent)
	}
}

// TestCreateEmailNoNotifierRidesMailer: an email invite with NO email-kind
// notifier keeps riding Config.Mailer byte-for-byte, even when other-kind
// notifiers are wired (the regression guard on delta-fold 1).
func TestCreateEmailNoNotifierRidesMailer(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	mailer := &recordingMailer{}
	phone := &fakeNotifier{kind: identity.KindPhone}
	svc := newSvc(t, repo, granter, Deps{
		Mailer:     mailer,
		Notifiers:  map[string]notify.Notifier{identity.KindPhone: phone},
		UserLookup: func(_ context.Context, _ string) (string, bool, error) { return "", false, nil },
	})

	_, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "new@x.com", InvitedBy: "inviter", // empty kind → email
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(mailer.sent) != 1 || len(mailer.sent[0].To) != 1 || mailer.sent[0].To[0] != "new@x.com" {
		t.Errorf("email invite did not ride the Mailer: %+v", mailer.sent)
	}
	if len(phone.got) != 0 {
		t.Errorf("phone notifier received an email invite: %+v", phone.got)
	}
}

// TestAcceptPhoneKindMatchesVerifiedPhone covers V11 (a phone invitation is
// accepted only by the subject whose active VERIFIED phone equals the invited
// address — resolved through the injected accessor) AND delta-fold 4 (the member-
// added notice follows the kind fork, routing through the wired phone notifier).
func TestAcceptPhoneKindMatchesVerifiedPhone(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	notifier := &fakeNotifier{kind: identity.KindPhone}
	svc := newSvc(t, repo, granter, Deps{
		Notifiers: map[string]notify.Notifier{identity.KindPhone: notifier},
		CallerIdentifiers: func(_ context.Context, userID, kind string) (string, error) {
			if userID == "user-9" && kind == identity.KindPhone {
				return "+15550100", nil
			}
			return "", sdk.ErrNotFound
		},
	})
	inv := seedInviteKind(t, repo, "project", "p1", "member", "+15550100", identity.KindPhone, "inviter", "secret-p", false, time.Now().Add(time.Hour))

	// The acceptor's own verified phone matches the invited address, so the accept
	// succeeds even though in.Identifier (the email claim) is unrelated.
	res, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-p", SubjectType: "user", SubjectID: "user-9", Identifier: "acceptor@x.com"})
	if err != nil {
		t.Fatalf("Accept(phone kind): %v", err)
	}
	if res.Relation != "member" {
		t.Errorf("AcceptResult = %+v", res)
	}
	if len(granter.calls) != 1 {
		t.Errorf("Grant calls = %+v, want 1", granter.calls)
	}
	got, _ := repo.Get(context.Background(), inv.ID)
	if got.Status != invitation.StatusAccepted {
		t.Errorf("status = %q, want accepted", got.Status)
	}
	// member-added rode the phone notifier (delta-fold 4), not the email Mailer.
	if len(notifier.got) != 1 || notifier.got[0].to.Value != "+15550100" {
		t.Errorf("member-added did not ride the phone notifier: %+v", notifier.got)
	}
}

// TestAcceptPhoneKindMismatch proves a phone invitation is refused when the
// caller's active verified phone is a DIFFERENT number — possession of the
// delivered token alone no longer accepts (V11). No grant happens.
func TestAcceptPhoneKindMismatch(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{
		Notifiers: map[string]notify.Notifier{identity.KindPhone: &fakeNotifier{kind: identity.KindPhone}},
		CallerIdentifiers: func(_ context.Context, _ string, _ string) (string, error) {
			return "+15559999", nil // the caller owns a different verified phone
		},
	})
	seedInviteKind(t, repo, "project", "p1", "member", "+15550100", identity.KindPhone, "inviter", "secret-p", false, time.Now().Add(time.Hour))

	_, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-p", SubjectType: "user", SubjectID: "user-9", Identifier: "acceptor@x.com"})
	if !errors.Is(err, ErrIdentifierMismatch) {
		t.Errorf("Accept(phone mismatch): err=%v, want ErrIdentifierMismatch", err)
	}
	if len(granter.calls) != 0 {
		t.Errorf("mismatch granted anyway: %+v", granter.calls)
	}
}

// TestAcceptPhoneKindNoVerifiedPhone proves the phone accept fails CLOSED when the
// caller has no active verified phone (the accessor returns sdk.ErrNotFound) —
// including the replaced/unverified case, which the identifier rail reports as
// no-such-verified-identifier.
func TestAcceptPhoneKindNoVerifiedPhone(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	svc := newSvc(t, repo, granter, Deps{
		Notifiers: map[string]notify.Notifier{identity.KindPhone: &fakeNotifier{kind: identity.KindPhone}},
		CallerIdentifiers: func(_ context.Context, _ string, _ string) (string, error) {
			return "", sdk.ErrNotFound
		},
	})
	seedInviteKind(t, repo, "project", "p1", "member", "+15550100", identity.KindPhone, "inviter", "secret-p", false, time.Now().Add(time.Hour))

	_, err := svc.Accept(context.Background(), AcceptInput{Token: "secret-p", SubjectType: "user", SubjectID: "user-9", Identifier: "acceptor@x.com"})
	if !errors.Is(err, ErrIdentifierMismatch) {
		t.Errorf("Accept(no verified phone): err=%v, want ErrIdentifierMismatch", err)
	}
	if len(granter.calls) != 0 {
		t.Errorf("unverified caller granted anyway: %+v", granter.calls)
	}
}

// TestCreatePhoneNormalizesE164 proves a phone invitation is normalized strict
// E.164 AT CREATE through the injected normalizer (design §2.2/§7): the stored
// identifier is the separator-stripped canonical form, which is the value the V11
// accept-time match compares against.
func TestCreatePhoneNormalizesE164(t *testing.T) {
	repo := newFakeInvRepo()
	granter := &fakeGranter{}
	notifier := &fakeNotifier{kind: identity.KindPhone}
	svc := newSvc(t, repo, granter, Deps{
		Notifiers: map[string]notify.Notifier{identity.KindPhone: notifier},
	})

	res, err := svc.Create(context.Background(), CreateInput{
		ResourceType: "project", ResourceID: "p1", Relation: "member",
		Identifier: "+1 (555) 010-2345", IdentifierKind: identity.KindPhone, InvitedBy: "inviter",
	})
	if err != nil {
		t.Fatalf("Create(phone): %v", err)
	}
	if res.Invitation.Identifier != "+15550102345" {
		t.Errorf("stored phone = %q, want strict E.164 %q", res.Invitation.Identifier, "+15550102345")
	}
}
