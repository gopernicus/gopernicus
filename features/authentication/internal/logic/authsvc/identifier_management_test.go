package authsvc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/domain/session"
	"github.com/gopernicus/gopernicus/features/authentication/domain/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/notify"
	"github.com/gopernicus/gopernicus/sdk/foundation/identity"
)

var _ contactchange.Repository = (*fakeContactChanges)(nil)

// fakeContactChanges is the authsvc-test contactchange.Repository: an atomic
// replace-per-(user, kind) Create and a single-use get-and-delete Consume that
// deletes regardless of expiry (the reference contract, design §2.4).
type fakeContactChanges struct {
	mu  sync.Mutex
	m   map[string]contactchange.PendingChange
	seq int
}

func newFakeContactChanges() *fakeContactChanges {
	return &fakeContactChanges{m: map[string]contactchange.PendingChange{}}
}

func ccKey(userID string, kind identifier.Kind) string { return userID + "|" + string(kind) }

func (f *fakeContactChanges) Create(_ context.Context, p contactchange.PendingChange) (contactchange.PendingChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	p.ID = "cc" + itoa(f.seq)
	f.m[ccKey(p.UserID, p.Kind)] = p
	return p, nil
}

func (f *fakeContactChanges) Consume(_ context.Context, userID string, kind identifier.Kind) (contactchange.PendingChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := ccKey(userID, kind)
	p, ok := f.m[k]
	if !ok {
		return contactchange.PendingChange{}, sdk.ErrNotFound
	}
	delete(f.m, k)
	if p.Expired(time.Now()) {
		return contactchange.PendingChange{}, sdk.ErrExpired
	}
	return p, nil
}

// recordingNotifier captures every Notify (the phone rail) so a phone add/change and
// its SMS proof code can be driven end to end.
type recordingNotifier struct {
	mu   sync.Mutex
	sent []recordedNotice
}

type recordedNotice struct {
	to   identity.Address
	body string
}

func (n *recordingNotifier) Kind() string { return identity.KindPhone }

func (n *recordingNotifier) Notify(_ context.Context, to identity.Address, msg notify.Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, recordedNotice{to: to, body: msg.Body})
	return nil
}

// codeFor returns the last SMS code delivered to value.
func (n *recordingNotifier) codeFor(t *testing.T, value string) string {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := len(n.sent) - 1; i >= 0; i-- {
		if n.sent[i].to.Value == value {
			return extractCode(t, n.sent[i].body)
		}
	}
	t.Fatalf("no SMS delivered to %s", value)
	return ""
}

// enablePhone re-wires the harness delivery with a phone notifier so phone add/change
// flows deliver their SMS proof to a recorder.
func (h *harness) enablePhone(t *testing.T) {
	t.Helper()
	h.phone = &recordingNotifier{}
	h.deliveryRepo = wireSyncDelivery(t, h.svc, h.mailer, map[string]notify.Notifier{identity.KindPhone: h.phone})
}

// extractCode pulls the first six-digit run appearing after a "code is" prefix in a
// rendered proof message (email text or SMS body).
func extractCode(t *testing.T, text string) string {
	t.Helper()
	i := strings.LastIndex(text, "code is")
	if i < 0 {
		t.Fatalf("no code prefix in %q", text)
	}
	digits := make([]rune, 0, 6)
	for _, r := range text[i:] {
		if r >= '0' && r <= '9' {
			digits = append(digits, r)
			if len(digits) == 6 {
				return string(digits)
			}
		} else if len(digits) > 0 {
			break
		}
	}
	t.Fatalf("no six-digit code after prefix in %q", text)
	return ""
}

// proofCodeFor returns the add/change proof code delivered by email to normEmail.
func (m *recordingMailer) proofCodeFor(t *testing.T, normEmail string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		msg := m.sent[i]
		if len(msg.To) == 1 && msg.To[0] == normEmail {
			return extractCode(t, msg.Text)
		}
	}
	t.Fatalf("no proof mail for %s", normEmail)
	return ""
}

// noticeToCount counts delivered identifier-change notices addressed to value.
func (m *recordingMailer) noticeToCount(value string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, msg := range m.sent {
		if len(msg.To) == 1 && msg.To[0] == value && strings.Contains(msg.Subject, "was changed") {
			n++
		}
	}
	return n
}

// --- add / change ---

func TestIdentifierAddEmailSuccess(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "owner@example.com", "password123456789")

	receipt, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail,
		Value: "Second@Example.com", Uses: identifier.Uses{Login: true, Recovery: true, Notification: true},
	})
	if err != nil {
		t.Fatalf("StartIdentifierChange: %v", err)
	}
	if !receipt.Delivered {
		t.Fatal("start reported no delivery")
	}
	if !hasEvent(h.events, securityevent.TypeEmailChangeCodeSent, securityevent.StatusSuccess) {
		t.Fatal("no email_change_code_sent event")
	}
	code := h.mailer.proofCodeFor(t, "second@example.com")

	if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Code: code,
	}); err != nil {
		t.Fatalf("ConfirmIdentifierChange: %v", err)
	}
	it, err := h.idents.GetLogin(ctx, string(identifier.KindEmail), "second@example.com")
	if err != nil {
		t.Fatalf("new identifier not claimed: %v", err)
	}
	if !it.Verified() || it.UserID != userID || !it.RecoveryEnabled {
		t.Errorf("new identifier = %+v, want verified login+recovery for %s", it, userID)
	}
	if !hasEvent(h.events, securityevent.TypeEmailChanged, securityevent.StatusSuccess) {
		t.Fatal("no email_changed event")
	}
}

func TestIdentifierEmailChangePrimaryReplacement(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, primary, sessionID := h.mustVerifiedLogin(t, "old@example.com", "password123456789")
	oldPrimary, err := h.idents.GetLogin(ctx, string(identifier.KindEmail), primary)
	if err != nil {
		t.Fatalf("resolve old primary: %v", err)
	}

	if err := h.startAndConfirmEmail(t, sessionID, userID, "new@example.com",
		identifier.Uses{Login: true, Recovery: true, Notification: true}, true); err != nil {
		t.Fatalf("primary replacement: %v", err)
	}
	// The new address is the verified primary; the old primary is retired.
	newIt, err := h.idents.GetLogin(ctx, string(identifier.KindEmail), "new@example.com")
	if err != nil || !newIt.IsPrimary {
		t.Fatalf("new primary not set: %+v err=%v", newIt, err)
	}
	if retired, _ := h.idents.Get(ctx, oldPrimary.ID); retired.Active() {
		t.Error("displaced primary not retired")
	}
	// The retired old primary — a previously verified channel — still receives the
	// independent notice (recipients are captured before the apply).
	if h.mailer.noticeToCount(primary) == 0 {
		t.Error("no independent notice to the replaced old primary")
	}
}

func TestIdentifierChangeWrongCodeRetainsPendingValue(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "retain@example.com", "password123456789")
	if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail,
		Value: "add@example.com", Uses: identifier.Uses{Notification: true},
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	// A wrong code is rejected and does NOT consume the pending value.
	if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Code: "000000",
	}); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("wrong code = %v, want ErrChallengeInvalid", err)
	}
	// The correct code still confirms — the pending value survived the wrong attempt.
	code := h.mailer.proofCodeFor(t, "add@example.com")
	if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Code: code,
	}); err != nil {
		t.Fatalf("confirm after wrong attempt: %v", err)
	}
}

func TestIdentifierConfirmWrongContextConsumesCode(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "ctx@example.com", "password123456789")
	// Start binds the challenge to pending P_A.
	if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail,
		Value: "a@example.com", Uses: identifier.Uses{Notification: true},
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	code := h.mailer.proofCodeFor(t, "a@example.com")
	// A superseding pending row P_B replaces P_A WITHOUT re-issuing the challenge, so
	// the live challenge is bound to P_A but the live pending is P_B (the mismatch the
	// concurrent-start race can produce).
	if _, err := h.contacts.Create(ctx, contactchange.New(userID, identifier.KindEmail, "b@example.com",
		identifier.Uses{Notification: true}, false, "", contactChangeTTL, time.Now())); err != nil {
		t.Fatalf("superseding pending: %v", err)
	}
	if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Code: code,
	}); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("context mismatch = %v, want ErrChallengeInvalid", err)
	}
	// The code was spent: a replay finds no live challenge.
	if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Code: code,
	}); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("replay after mismatch = %v, want ErrChallengeInvalid (spent)", err)
	}
}

func TestIdentifierStartRequiresStepUp(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "stepup@example.com", "password123456789")
	// Push the login out of the recent window and provide no grant.
	h.backdateLogin(sessionID, time.Hour)
	_, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail,
		Value: "x@example.com", Uses: identifier.Uses{Notification: true},
	})
	if !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("start without recent auth = %v, want ErrStepUpRequired", err)
	}
}

func TestIdentifierStartRateLimited(t *testing.T) {
	h := newHarness(t, denyLimiter{})
	ctx := context.Background()
	// denyLimiter fails Login's own budget, so seed a verified user + a live session
	// directly and rely on the recent-login shortcut for step-up.
	userID := "u-rl"
	now := time.Now().UTC()
	h.users.mu.Lock()
	h.users.byID[userID] = user.User{ID: userID}
	h.users.mu.Unlock()
	h.idents.insert(identifier.Identifier{
		ID: "id-rl", UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "rl@example.com",
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		IsPrimary: true, CreatedAt: now, UpdatedAt: now,
	})
	pair, err := h.svc.mintSession(ctx, userID, h.svc.primaryAuthentication(session.MethodPassword))
	if err != nil {
		t.Fatalf("mintSession: %v", err)
	}
	_ = pair
	sessionID := h.sessionIDFor(t, userID)
	_, err = h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail,
		Value: "target@example.com", Uses: identifier.Uses{Notification: true},
	})
	if !errors.Is(err, ErrIdentifierChangeRateLimited) {
		t.Fatalf("rate-limited start = %v, want ErrIdentifierChangeRateLimited", err)
	}
}

func TestIdentifierStartUnsupportedKind(t *testing.T) {
	h := newHarness(t, nil) // no phone notifier wired
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "nophone@example.com", "password123456789")
	_, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindPhone,
		Value: "+15551234567", Uses: identifier.Uses{Notification: true},
	})
	if !errors.Is(err, ErrKindNotSupported) {
		t.Fatalf("phone start with no notifier = %v, want ErrKindNotSupported", err)
	}
}

func TestPhoneChangeAddSuccess(t *testing.T) {
	h := newHarness(t, nil)
	h.enablePhone(t)
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "phoneowner@example.com", "password123456789")

	if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindPhone,
		Value: "+15551234567", Uses: identifier.Uses{Notification: true},
	}); err != nil {
		t.Fatalf("phone start: %v", err)
	}
	if !hasEvent(h.events, securityevent.TypePhoneChangeCodeSent, securityevent.StatusSuccess) {
		t.Fatal("no phone_change_code_sent event")
	}
	code := h.phone.codeFor(t, "+15551234567")
	if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindPhone, Code: code,
	}); err != nil {
		t.Fatalf("phone confirm: %v", err)
	}
	idents, _ := h.idents.ListByUser(ctx, userID)
	found := false
	for _, it := range idents {
		if it.Kind == identifier.KindPhone && it.NormalizedValue == "+15551234567" && it.Verified() {
			found = true
		}
	}
	if !found {
		t.Errorf("verified phone not added: %+v", idents)
	}
	if !hasEvent(h.events, securityevent.TypePhoneChanged, securityevent.StatusSuccess) {
		t.Fatal("no phone_changed event")
	}
}

func TestPhoneChangeSharedNotificationOnly(t *testing.T) {
	h := newHarness(t, nil)
	h.enablePhone(t)
	ctx := context.Background()
	// Another user already holds the same phone as a notification-only identifier.
	now := time.Now().UTC()
	h.users.mu.Lock()
	h.users.byID["other"] = user.User{ID: "other"}
	h.users.mu.Unlock()
	h.idents.insert(identifier.Identifier{
		ID: "id-other-phone", UserID: "other", Kind: identifier.KindPhone, NormalizedValue: "+15559998888",
		VerifiedAt: now, NotificationEnabled: true, CreatedAt: now, UpdatedAt: now,
	})
	userID, _, sessionID := h.mustVerifiedLogin(t, "sharer@example.com", "password123456789")
	if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindPhone,
		Value: "+15559998888", Uses: identifier.Uses{Notification: true},
	}); err != nil {
		t.Fatalf("shared-phone start: %v", err)
	}
	code := h.phone.codeFor(t, "+15559998888")
	// A notification-only phone is not an authentication claim, so the shared value is
	// added without an ErrAlreadyExists collision.
	if err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindPhone, Code: code,
	}); err != nil {
		t.Fatalf("shared notification-only phone confirm: %v", err)
	}
}

func TestIdentifierUniqueRaceCollisionIsGeneric(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	// Another user already claims taken@example.com as a login identifier.
	now := time.Now().UTC()
	h.users.mu.Lock()
	h.users.byID["holder"] = user.User{ID: "holder"}
	h.users.mu.Unlock()
	h.idents.insert(identifier.Identifier{
		ID: "id-taken", UserID: "holder", Kind: identifier.KindEmail, NormalizedValue: "taken@example.com",
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, CreatedAt: now, UpdatedAt: now,
	})
	userID, _, sessionID := h.mustVerifiedLogin(t, "racer@example.com", "password123456789")
	if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail,
		Value: "taken@example.com", Uses: identifier.Uses{Login: true, Recovery: true},
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	code := h.mailer.proofCodeFor(t, "taken@example.com")
	err := h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Code: code,
	})
	if !errors.Is(err, sdk.ErrAlreadyExists) {
		t.Fatalf("lost claim race = %v, want sdk.ErrAlreadyExists", err)
	}
}

// --- independent notice ---

func TestIdentifierChangeSendsIndependentNotice(t *testing.T) {
	h := newHarness(t, nil)
	userID, primary, sessionID := h.mustVerifiedLogin(t, "primary@example.com", "password123456789")

	if err := h.startAndConfirmEmail(t, sessionID, userID, "brand-new@example.com",
		identifier.Uses{Notification: true}, false); err != nil {
		t.Fatalf("add: %v", err)
	}
	// The previously verified primary receives an independent notice; the newly bound
	// address never receives the notice.
	if h.mailer.noticeToCount(primary) == 0 {
		t.Error("no independent notice to the previously verified channel")
	}
	if h.mailer.noticeToCount("brand-new@example.com") != 0 {
		t.Error("notice sent to the newly bound address (must go to an independent channel)")
	}
}

// --- remove ---

func TestIdentifierRemoveNonPrimarySuccess(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "rmowner@example.com", "password123456789")
	// A second verified login+recovery email so removing it leaves a login+recovery set.
	now := time.Now().UTC()
	second := h.idents.insert(identifier.Identifier{
		UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "secondary@example.com",
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err := h.svc.RemoveIdentifier(ctx, IdentifierRemoveInput{
		SessionID: sessionID, UserID: userID, IdentifierID: second.ID,
	}); err != nil {
		t.Fatalf("RemoveIdentifier: %v", err)
	}
	if it, _ := h.idents.Get(ctx, second.ID); it.Active() {
		t.Error("removed identifier still active")
	}
	if !hasEvent(h.events, securityevent.TypeEmailRemoved, securityevent.StatusSuccess) {
		t.Fatal("no email_removed event")
	}
}

func TestIdentifierRemoveLastLoginMethodRejected(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-onlyid"
	now := time.Now().UTC()
	h.users.mu.Lock()
	h.users.byID[userID] = user.User{ID: userID}
	h.users.mu.Unlock()
	// The sole login+recovery method, no password: removing it leaves no login method.
	sole := h.idents.insert(identifier.Identifier{
		UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "sole@example.com",
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		IsPrimary: true, CreatedAt: now, UpdatedAt: now,
	})
	pair, err := h.svc.mintSession(ctx, userID, h.svc.primaryAuthentication(session.MethodPassword))
	if err != nil {
		t.Fatalf("mintSession: %v", err)
	}
	_ = pair
	sessionID := h.sessionIDFor(t, userID)
	err = h.svc.RemoveIdentifier(ctx, IdentifierRemoveInput{
		SessionID: sessionID, UserID: userID, IdentifierID: sole.ID,
	})
	if !errors.Is(err, credential.ErrNoLoginMethod) {
		t.Fatalf("remove last login method = %v, want credential.ErrNoLoginMethod", err)
	}
	if it, _ := h.idents.Get(ctx, sole.ID); !it.Active() {
		t.Error("identifier retired despite policy rejection")
	}
}

func TestIdentifierRemovePrimaryPromotesReplacement(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, primaryValue, sessionID := h.mustVerifiedLogin(t, "primaryrm@example.com", "password123456789")
	primary, err := h.idents.GetLogin(ctx, string(identifier.KindEmail), primaryValue)
	if err != nil {
		t.Fatalf("resolve primary: %v", err)
	}
	now := time.Now().UTC()
	second := h.idents.insert(identifier.Identifier{
		UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "backup@example.com",
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err := h.svc.RemoveIdentifier(ctx, IdentifierRemoveInput{
		SessionID: sessionID, UserID: userID, IdentifierID: primary.ID,
	}); err != nil {
		t.Fatalf("RemoveIdentifier(primary): %v", err)
	}
	if it, _ := h.idents.Get(ctx, primary.ID); it.Active() {
		t.Error("primary not retired")
	}
	if promoted, _ := h.idents.Get(ctx, second.ID); !promoted.IsPrimary {
		t.Error("replacement not promoted to primary")
	}
}

// --- change uses ---

func TestIdentifierSetUsesSuccess(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, primaryValue, sessionID := h.mustVerifiedLogin(t, "uses@example.com", "password123456789")
	primary, _ := h.idents.GetLogin(ctx, string(identifier.KindEmail), primaryValue)
	// Drop notification while keeping login+recovery (still verified).
	if err := h.svc.SetIdentifierUses(ctx, IdentifierUsesInput{
		SessionID: sessionID, UserID: userID, IdentifierID: primary.ID,
		Uses: identifier.Uses{Login: true, Recovery: true, Notification: false},
	}); err != nil {
		t.Fatalf("SetIdentifierUses: %v", err)
	}
	it, _ := h.idents.Get(ctx, primary.ID)
	if it.NotificationEnabled {
		t.Error("notification use not cleared")
	}
	if !hasEvent(h.events, securityevent.TypeIdentifierUsesChanged, securityevent.StatusSuccess) {
		t.Fatal("no identifier_uses_changed event")
	}
}

func TestIdentifierSetUsesRequiresVerification(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	userID, _, sessionID := h.mustVerifiedLogin(t, "unverified-use@example.com", "password123456789")
	// An unverified notification-only identifier cannot gain login use.
	now := time.Now().UTC()
	unverified := h.idents.insert(identifier.Identifier{
		UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "pending@example.com",
		NotificationEnabled: true, CreatedAt: now, UpdatedAt: now,
	})
	err := h.svc.SetIdentifierUses(ctx, IdentifierUsesInput{
		SessionID: sessionID, UserID: userID, IdentifierID: unverified.ID,
		Uses: identifier.Uses{Login: true},
	})
	if !errors.Is(err, identifier.ErrVerificationRequired) {
		t.Fatalf("enable login on unverified = %v, want ErrVerificationRequired", err)
	}
}

// --- concurrent final-method mutation ---

func TestIdentifierRemoveConcurrentReevaluatesPolicy(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	const userID = "u-concurrent"
	now := time.Now().UTC()
	h.users.mu.Lock()
	h.users.byID[userID] = user.User{ID: userID}
	h.users.mu.Unlock()
	// Two login+recovery emails; removing B is safe at first snapshot, but a competing
	// removal of A commits first, so the retry must re-evaluate and reject.
	a := h.idents.insert(identifier.Identifier{
		UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "a@example.com",
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		IsPrimary: true, CreatedAt: now, UpdatedAt: now,
	})
	b := h.idents.insert(identifier.Identifier{
		UserID: userID, Kind: identifier.KindEmail, NormalizedValue: "b@example.com",
		VerifiedAt: now, LoginEnabled: true, RecoveryEnabled: true, NotificationEnabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	pair, err := h.svc.mintSession(ctx, userID, h.svc.primaryAuthentication(session.MethodPassword))
	if err != nil {
		t.Fatalf("mintSession: %v", err)
	}
	_ = pair
	sessionID := h.sessionIDFor(t, userID)

	h.creds.beforeApply = func() {
		u, _ := h.users.Get(ctx, userID)
		h.idents.mu.Lock()
		it := h.idents.byID[a.ID]
		it.Retire(time.Now())
		h.idents.byID[a.ID] = it
		h.idents.mu.Unlock()
		_ = h.users.applyRevision(userID, u.AuthRevision)
	}
	err = h.svc.RemoveIdentifier(ctx, IdentifierRemoveInput{
		SessionID: sessionID, UserID: userID, IdentifierID: b.ID,
	})
	if !errors.Is(err, credential.ErrNoLoginMethod) {
		t.Fatalf("concurrent last-method removal = %v, want credential.ErrNoLoginMethod", err)
	}
	if it, _ := h.idents.Get(ctx, b.ID); !it.Active() {
		t.Error("identifier retired despite re-evaluated rejection")
	}
}

// --- helpers ---

// startAndConfirmEmail runs a full email add/change to completion.
func (h *harness) startAndConfirmEmail(t *testing.T, sessionID, userID, newEmail string, uses identifier.Uses, makePrimary bool) error {
	t.Helper()
	ctx := context.Background()
	if _, err := h.svc.StartIdentifierChange(ctx, IdentifierChangeStart{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail,
		Value: newEmail, Uses: uses, MakePrimary: makePrimary,
	}); err != nil {
		return err
	}
	code := h.mailer.proofCodeFor(t, normalizeTestEmail(newEmail))
	return h.svc.ConfirmIdentifierChange(ctx, IdentifierChangeConfirm{
		SessionID: sessionID, UserID: userID, Kind: identifier.KindEmail, Code: code,
	})
}
