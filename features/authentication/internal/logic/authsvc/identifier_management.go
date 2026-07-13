package authsvc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/gopernicus/gopernicus/features/authentication/domain/authgrant"
	"github.com/gopernicus/gopernicus/features/authentication/domain/challenge"
	"github.com/gopernicus/gopernicus/features/authentication/domain/contactchange"
	"github.com/gopernicus/gopernicus/features/authentication/domain/credential"
	"github.com/gopernicus/gopernicus/features/authentication/domain/identifier"
	"github.com/gopernicus/gopernicus/features/authentication/domain/securityevent"
	"github.com/gopernicus/gopernicus/features/authentication/internal/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/ratelimiter"
)

const (
	// contactChangeTTL bounds an in-flight identifier add/change (design §2.4). It
	// matches the change_email/change_phone challenge TTL so the pending value and its
	// proof code expire together — a confirm always sees both live or neither.
	contactChangeTTL = 15 * time.Minute
	// identifierChangeStartsPerUserPerMinute bounds how many identifier add/change
	// starts one user may launch per minute (design §5.5 delta gate: a per-account
	// flood arm).
	identifierChangeStartsPerUserPerMinute = 5
	// identifierChangeStartsPerTargetPerMinute bounds how many identifier add/change
	// proofs one target address may receive per minute (design §5.5 delta gate: the
	// victim-address flood-amplifier arm). Deliberately tighter than the per-user arm.
	identifierChangeStartsPerTargetPerMinute = 3
)

// Stable identifier-management errors (design §5.5/§5.8). Each wraps an sdk kind so
// the transport maps it; callers detect them with errors.Is.
var (
	// ErrIdentifierChangeUnavailable is returned when the pending-value flow-state
	// rail is not wired (nil ContactChanges). A wiring fault; the flow fails CLOSED
	// rather than bypassing the pending-value contract. Wraps sdk.ErrForbidden (→ 403).
	ErrIdentifierChangeUnavailable = fmt.Errorf("identifier-change subsystem not wired: %w", sdk.ErrForbidden)
	// ErrKindNotSupported is returned by an add/change start when no transport is
	// wired for the target kind (design §5.5: "no phone notifier → ErrKindNotSupported")
	// — checked BEFORE any pending row or proof secret is created. Wraps
	// sdk.ErrInvalidInput (→ 400).
	ErrKindNotSupported = fmt.Errorf("identifier kind not supported by any wired transport: %w", sdk.ErrInvalidInput)
	// ErrIdentifierChangeRateLimited is returned when a per-user or per-target add/
	// change budget is exhausted (design §5.5 delta gate). Distinct from the
	// credential errors so the transport maps it to 429. Checked with errors.Is.
	ErrIdentifierChangeRateLimited = errors.New("too many identifier-change requests")
)

// changeBinding is the challenge stored-context that pins an add/change proof code
// to one pending change (design §2.4): the confirm step checks the consumed
// challenge's bound pending-change ID matches the consumed pending change, so a code
// minted for one proposed value can never confirm a different one (the concurrent-
// start race where a live challenge and a live pending row disagree).
type changeBinding struct {
	PendingID string `json:"pending_id"`
}

// IdentifierChangeStart is the input to StartIdentifierChange: the live session and
// user, the target kind and raw value, the requested uses, and whether the new
// identifier becomes primary. The value is normalized through the shared policy
// before any flow state is created; no start-time existence lookup runs (claim only
// at apply, design §5.5).
type IdentifierChangeStart struct {
	SessionID   string
	UserID      string
	Kind        identifier.Kind
	Value       string
	Uses        identifier.Uses
	MakePrimary bool
}

// IdentifierChangeConfirm is the input to ConfirmIdentifierChange: the live session
// and user, the kind whose pending change is being confirmed, and the proof code
// delivered to the proposed new address.
type IdentifierChangeConfirm struct {
	SessionID string
	UserID    string
	Kind      identifier.Kind
	Code      string
}

// IdentifierRemoveInput is the input to RemoveIdentifier: the live session and user,
// the identifier to retire, and an optional replacement to promote to primary when
// the retired identifier was primary (empty → the service selects one).
type IdentifierRemoveInput struct {
	SessionID     string
	UserID        string
	IdentifierID  string
	ReplacementID string
}

// IdentifierUsesInput is the input to SetIdentifierUses: the live session and user,
// the identifier whose use flags change, the new uses, and whether it becomes primary.
type IdentifierUsesInput struct {
	SessionID    string
	UserID       string
	IdentifierID string
	Uses         identifier.Uses
	MakePrimary  bool
}

// StartIdentifierChange begins an identifier add/change (design §5.5). It requires a
// live session plus an existing-method recent-authentication grant (the proposed new
// address can NEVER satisfy this — §5.0), rejects a kind with no wired transport
// (ErrKindNotSupported), normalizes the target, applies the per-user AND per-target
// flood budgets, then — in the pinned order — creates the pending-value row first and
// issues the proof challenge bound to that row's ID, delivering the code to the
// proposed NEW address over the durable outbox (a delivery failure surfaces through
// the receipt). No start-time existence lookup runs; the claim is arbitrated only at
// confirm. Records email_change_code_sent / phone_change_code_sent.
func (s *Service) StartIdentifierChange(ctx context.Context, in IdentifierChangeStart) (StepUpReceipt, error) {
	if s.contactChanges == nil {
		return StepUpReceipt{}, ErrIdentifierChangeUnavailable
	}
	if s.challenges == nil || s.protector == nil {
		return StepUpReceipt{}, ErrStepUpUnavailable
	}
	grantPurpose, challengePurpose, ok := changePurposes(in.Kind)
	if !ok {
		return StepUpReceipt{}, fmt.Errorf("identifier: %q: %w", in.Kind, identifier.ErrUnknownKind)
	}
	// A transport for the kind must exist before a secret is delivered to it (design
	// §5.5). Checked before any flow state so a rejected kind leaves nothing behind.
	if s.deliver == nil || !s.deliver.Supports(string(in.Kind)) {
		return StepUpReceipt{}, ErrKindNotSupported
	}
	// Normalize through the single injected policy so the confirm-time claim and any
	// future lookup speak the stored value; a rejected value wraps sdk.ErrInvalidInput.
	normalized, err := s.normalizer.Normalize(string(in.Kind), in.Value)
	if err != nil {
		return StepUpReceipt{}, err
	}
	// Existing-method step-up: consume the operation-bound grant immediately before
	// the flow starts (design §5.0/§5.5).
	if _, err := s.RequireRecentAuthentication(ctx, in.SessionID, in.UserID, grantPurpose, "", RecentAuthPolicy{}); err != nil {
		return StepUpReceipt{}, err
	}
	// Per-user AND per-target budgets (design §5.5 delta gate): a start delivers a
	// secret to a caller-supplied address, so both a per-account and a victim-address
	// flood are bounded before any code is minted.
	if err := s.identifierChangeBudget(ctx, in.UserID, string(in.Kind), normalized); err != nil {
		return StepUpReceipt{}, err
	}
	// Pinned order (design §2.4): create the pending row FIRST (obtaining its ID), then
	// issue the challenge bound to that ID.
	pending, err := s.contactChanges.Create(ctx, contactchange.New(
		in.UserID, in.Kind, normalized, in.Uses, in.MakePrimary, "", contactChangeTTL, s.now()))
	if err != nil {
		return StepUpReceipt{}, err
	}
	code, err := s.IssueChallenge(ctx, in.UserID, challengePurpose,
		WithStoredContext(changeBinding{PendingID: pending.ID}))
	if err != nil {
		return StepUpReceipt{}, err
	}
	// Deliver the ownership-proof code to the proposed NEW address (proving control of
	// it). A change-start is a caller-driven (re)send, so it supersedes any prior
	// pending job for the address rather than deduping onto a stale code.
	key := s.idempotencyKey(string(in.Kind), normalized, delivery.PurposeIdentifierChangeProof)
	if err := s.enqueueRenderedReplace(ctx, delivery.PurposeIdentifierChangeProof, key, delivery.Request{
		Kind:            string(in.Kind),
		Purpose:         delivery.PurposeIdentifierChangeProof,
		Destination:     normalized,
		ResolutionInput: normalized,
		Secret:          code,
		Data:            map[string]any{"IdentifierKind": string(in.Kind)},
	}); err != nil {
		return StepUpReceipt{}, err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: in.UserID,
		Type:   changeCodeSentEvent(in.Kind),
		Status: securityevent.StatusSuccess,
	})
	return StepUpReceipt{Delivered: true, Receipt: key}, nil
}

// ConfirmIdentifierChange completes an identifier add/change (design §5.5). In the
// pinned confirm order it consumes the proof CHALLENGE first (a wrong code counts an
// attempt and keeps the challenge alive, leaving the pending value intact for a
// retry), then consumes the pending change, then validates that the consumed
// challenge's bound pending-change ID matches the consumed pending row (a mismatch
// spends the code and rejects — the concurrent-start race stop). It then reloads the
// method set, evaluates the credential policy against the proposed post-change set,
// and applies the verified change atomically under the user's auth_revision,
// re-evaluating on a concurrent conflict. A lost authentication-claim race on the new
// value surfaces the generic sdk.ErrAlreadyExists. Records email_changed /
// phone_changed and enqueues an independent notice to the previously verified
// channels.
func (s *Service) ConfirmIdentifierChange(ctx context.Context, in IdentifierChangeConfirm) error {
	if s.contactChanges == nil {
		return ErrIdentifierChangeUnavailable
	}
	if s.credentialMutations == nil {
		return ErrCredentialMutationUnavailable
	}
	_, challengePurpose, ok := changePurposes(in.Kind)
	if !ok {
		return fmt.Errorf("identifier: %q: %w", in.Kind, identifier.ErrUnknownKind)
	}
	// CHALLENGE first (design §2.4): a wrong/expired/locked-out code is the stable
	// challenge error and does NOT consume the pending value.
	consumed, err := s.ConsumeChallenge(ctx, in.UserID, challengePurpose, in.Code)
	if err != nil {
		return err
	}
	// Then the pending value.
	pending, err := s.contactChanges.Consume(ctx, in.UserID, in.Kind)
	if err != nil {
		return err
	}
	// Binding check (design §2.4): the code's bound pending ID must match the pending
	// row just consumed. The code is already spent, so a mismatch — a stale code from
	// a superseded start against a newer pending value — cannot be replayed.
	wantContext, err := contextDigest(changeBinding{PendingID: pending.ID})
	if err != nil {
		return err
	}
	if string(consumed.Context) != string(wantContext) {
		return ErrChallengeInvalid
	}
	// Capture the previously verified notice recipients BEFORE the apply, so a primary
	// replacement still notifies the old primary it retires (design §5.5).
	recipients := s.verifiedContactChannels(ctx, in.UserID, pending.NewValue)
	if err := s.applyVerifiedIdentifierChange(ctx, in.UserID, pending); err != nil {
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: in.UserID,
		Type:   changedEvent(in.Kind),
		Status: securityevent.StatusSuccess,
	})
	// Independent notice to previously verified channels (never only the newly proved
	// address, design §5.5).
	s.enqueueIdentifierChangeNotices(ctx, in.Kind, recipients)
	return nil
}

// RemoveIdentifier retires an identifier through the revision-serialized credential
// rail (design §5.5). It requires a live session plus an existing-method grant bound
// to the identifier, and — when the retired identifier is primary — promotes a
// replacement to primary in the same atomic mutation (the caller's nominee, else the
// oldest remaining active identifier of the same kind). The credential policy guards
// the proposed set immediately before the revision-CAS Apply, re-evaluating on a
// concurrent conflict; a removal that would leave no acceptable method set is the
// policy's stable rejection. Records email_removed / phone_removed and notifies the
// previously verified channels.
func (s *Service) RemoveIdentifier(ctx context.Context, in IdentifierRemoveInput) error {
	if s.credentialMutations == nil {
		return ErrCredentialMutationUnavailable
	}
	target, err := s.ownedActiveIdentifier(ctx, in.UserID, in.IdentifierID)
	if err != nil {
		return err
	}
	if _, err := s.RequireRecentAuthentication(ctx, in.SessionID, in.UserID, authgrant.PurposeRemoveIdentifier, in.IdentifierID, RecentAuthPolicy{}); err != nil {
		return err
	}
	replacement := in.ReplacementID
	if target.IsPrimary && replacement == "" {
		replacement = s.selectReplacementPrimary(ctx, in.UserID, target)
	}
	// Capture recipients before the removal so the remaining verified channels are
	// notified (never the just-removed address).
	recipients := s.verifiedContactChannels(ctx, in.UserID, target.NormalizedValue)
	if err := s.applyCredentialMutation(ctx, in.UserID, credential.RetireIdentifier{
		IdentifierID:         in.IdentifierID,
		ReplacementPrimaryID: replacement,
	}); err != nil {
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: in.UserID,
		Type:   removedEvent(target.Kind),
		Status: securityevent.StatusSuccess,
	})
	s.enqueueIdentifierChangeNotices(ctx, target.Kind, recipients)
	return nil
}

// SetIdentifierUses changes an identifier's use flags (and optionally promotes it to
// primary) through the revision-serialized credential rail (design §5.5). It requires
// a live session plus an existing-method grant bound to the identifier, refuses
// enabling login/recovery on an unverified identifier (the §2.3 verification
// invariant), and routes the mutation through the policy-guarded revision-CAS Apply.
// Records identifier_uses_changed.
func (s *Service) SetIdentifierUses(ctx context.Context, in IdentifierUsesInput) error {
	if s.credentialMutations == nil {
		return ErrCredentialMutationUnavailable
	}
	target, err := s.ownedActiveIdentifier(ctx, in.UserID, in.IdentifierID)
	if err != nil {
		return err
	}
	// Enabling an authentication-bearing use requires a proven address (design §2.3).
	if (in.Uses.Login || in.Uses.Recovery) && !target.Verified() {
		return identifier.ErrVerificationRequired
	}
	if _, err := s.RequireRecentAuthentication(ctx, in.SessionID, in.UserID, authgrant.PurposeChangeIdentifierUses, in.IdentifierID, RecentAuthPolicy{}); err != nil {
		return err
	}
	if err := s.applyCredentialMutation(ctx, in.UserID, credential.ChangeIdentifierUses{
		IdentifierID: in.IdentifierID,
		Uses:         credential.IdentifierUses{Login: in.Uses.Login, Recovery: in.Uses.Recovery, Notification: in.Uses.Notification},
		MakePrimary:  in.MakePrimary,
	}); err != nil {
		return err
	}
	s.recordSecurityEvent(ctx, securityEventInput{
		UserID: in.UserID,
		Type:   securityevent.TypeIdentifierUsesChanged,
		Status: securityevent.StatusSuccess,
	})
	return nil
}

// applyVerifiedIdentifierChange evaluates the credential policy against the proposed
// post-change method set and applies the verified change atomically under the user's
// auth_revision (design §5.5/§5.6). It reads the current MethodSet, computes the
// proposed set the ApplyVerifiedChange would produce, evaluates policy immediately
// before the revision-CAS apply, and on an sdk.ErrConflict reloads and re-evaluates
// rather than committing a stale set. A lost authentication-claim race on the new
// value surfaces as sdk.ErrAlreadyExists (the generic collision).
func (s *Service) applyVerifiedIdentifierChange(ctx context.Context, userID string, pending contactchange.PendingChange) error {
	input := identifier.ApplyVerifiedChangeInput{
		UserID:               userID,
		Kind:                 pending.Kind,
		NormalizedValue:      pending.NewValue,
		LoginEnabled:         pending.LoginEnabled,
		RecoveryEnabled:      pending.RecoveryEnabled,
		NotificationEnabled:  pending.NotificationEnabled,
		MakePrimary:          pending.MakePrimary,
		ReplacesIdentifierID: pending.ReplacesIdentifierID,
	}
	var lastErr error
	for attempt := 0; attempt < adoptionRevisionRetries; attempt++ {
		snap, err := s.credentialMutations.Snapshot(ctx, userID)
		if err != nil {
			return err
		}
		if err := s.credentialPolicy.EvaluateMutation(ctx, snap, proposedSetForChange(snap, pending)); err != nil {
			return err
		}
		_, err = s.identifiers.ApplyVerifiedChange(ctx, input, snap.AuthRevision, s.now())
		if err == nil {
			return nil
		}
		if !errors.Is(err, sdk.ErrConflict) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// proposedSetForChange builds the method set ApplyVerifiedChange would produce so the
// policy can judge it before the store commits (design §5.6). It mirrors the store's
// effects: an explicitly replaced identifier is retired, a make-primary retires the
// displaced primary of the same kind, and the new verified identifier is added
// (primary when requested). The new identifier carries no ID (the store assigns it),
// which the policy does not key on.
func proposedSetForChange(snap credential.MethodSet, p contactchange.PendingChange) credential.MethodSet {
	proposed := snap
	if p.ReplacesIdentifierID != "" {
		proposed = proposed.With(credential.RetireIdentifier{IdentifierID: p.ReplacesIdentifierID})
	}
	if p.MakePrimary {
		if pid, ok := primaryIdentifierID(proposed, string(p.Kind)); ok {
			proposed = proposed.With(credential.RetireIdentifier{IdentifierID: pid})
		}
	}
	added := make([]credential.IdentifierMethod, len(proposed.Identifiers), len(proposed.Identifiers)+1)
	copy(added, proposed.Identifiers)
	proposed.Identifiers = append(added, credential.IdentifierMethod{
		Kind:     string(p.Kind),
		Uses:     credential.IdentifierUses{Login: p.LoginEnabled, Recovery: p.RecoveryEnabled, Notification: p.NotificationEnabled},
		Verified: true,
		Primary:  p.MakePrimary,
	})
	return proposed
}

// primaryIdentifierID returns the active primary identifier of kind in the set, if any.
func primaryIdentifierID(set credential.MethodSet, kind string) (string, bool) {
	for _, m := range set.Identifiers {
		if m.Kind == kind && m.Primary {
			return m.ID, true
		}
	}
	return "", false
}

// ownedActiveIdentifier loads an identifier and confirms it is active and owned by
// userID, returning sdk.ErrNotFound otherwise so a caller can never mutate another
// user's identifier or a retired row (design §5.5).
func (s *Service) ownedActiveIdentifier(ctx context.Context, userID, identifierID string) (identifier.Identifier, error) {
	if identifierID == "" {
		return identifier.Identifier{}, fmt.Errorf("identifier: %w", sdk.ErrNotFound)
	}
	it, err := s.identifiers.Get(ctx, identifierID)
	if err != nil {
		return identifier.Identifier{}, err
	}
	if it.UserID != userID || !it.Active() {
		return identifier.Identifier{}, fmt.Errorf("identifier %q not owned or inactive: %w", identifierID, sdk.ErrNotFound)
	}
	return it, nil
}

// selectReplacementPrimary picks the identifier promoted to primary when a primary is
// retired and the caller named no replacement (design §5.5): the oldest remaining
// active identifier of the same kind. Returns "" when none exists — the retired
// primary then simply leaves no primary of that kind, and the credential policy
// decides whether the removal is safe at all.
func (s *Service) selectReplacementPrimary(ctx context.Context, userID string, target identifier.Identifier) string {
	idents, err := s.identifiers.ListByUser(ctx, userID)
	if err != nil {
		return ""
	}
	candidates := make([]identifier.Identifier, 0, len(idents))
	for _, it := range idents {
		if it.ID != target.ID && it.Active() && it.Kind == target.Kind {
			candidates = append(candidates, it)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	return candidates[0].ID
}

// verifiedContactChannels returns the user's active, verified, contactable (recovery-
// or notification-enabled) identifiers, excluding excludeValue (the newly bound or
// just-removed address). It is the pre-mutation snapshot of the independent notice
// recipients (design §5.5), captured before an apply retires a replaced channel.
func (s *Service) verifiedContactChannels(ctx context.Context, userID, excludeValue string) []identifier.Identifier {
	if s.identifiers == nil {
		return nil
	}
	idents, err := s.identifiers.ListByUser(ctx, userID)
	if err != nil {
		s.logger.Warn("identifier-change notice listing failed", "error_kind", errKind(err))
		return nil
	}
	out := make([]identifier.Identifier, 0, len(idents))
	for _, it := range idents {
		if !it.Active() || !it.Verified() || it.NormalizedValue == excludeValue {
			continue
		}
		if !it.RecoveryEnabled && !it.NotificationEnabled {
			continue // not a contactable channel
		}
		out = append(out, it)
	}
	return out
}

// enqueueIdentifierChangeNotices enqueues an independent security notice to each
// previously verified channel in recipients (design §5.5): the notice never rides only
// the channel just proved, so a hijacked binding is still reported to a channel the
// attacker does not control. It is best-effort over the durable outbox — the mutation
// has already committed — so a notifier outage never rolls the change back; a failed
// enqueue is a coarse WARN. When recipients is empty (the risky "only the new channel"
// posture, design §5.5 stop condition), no notice is sent: there is nowhere safe to
// send it, and blocking the legitimate first-verified-identifier add is worse; a host
// redress workflow is the documented mitigation.
func (s *Service) enqueueIdentifierChangeNotices(ctx context.Context, changedKind identifier.Kind, recipients []identifier.Identifier) {
	if s.queue == nil || s.deliver == nil {
		return
	}
	info := clientInfoFromContext(ctx)
	when := s.now().UTC().Format(time.RFC3339)
	for _, it := range recipients {
		if !s.deliver.Supports(string(it.Kind)) {
			continue // no transport for this channel's kind
		}
		key := s.idempotencyKey(string(it.Kind), it.NormalizedValue, delivery.PurposeIdentifierChangeNotice) + ":" + string(changedKind) + ":" + when
		if err := s.enqueueRendered(ctx, delivery.PurposeIdentifierChangeNotice, key, delivery.Request{
			Kind:            string(it.Kind),
			Purpose:         delivery.PurposeIdentifierChangeNotice,
			Destination:     it.NormalizedValue,
			ResolutionInput: it.NormalizedValue,
			Data: map[string]any{
				"IdentifierKind": string(changedKind),
				"ChangedAt":      when,
				"ClientIP":       info.ip,
			},
		}); err != nil {
			s.logger.Warn("identifier-change notice enqueue failed", "error_kind", errKind(err))
		}
	}
}

// identifierChangeBudget enforces the per-user and per-target add/change flood budgets
// (design §5.5). The per-target key uses the PII-free identifier digest so a raw
// address never enters a limiter key (design §4.4). Either budget's exhaustion is
// ErrIdentifierChangeRateLimited.
func (s *Service) identifierChangeBudget(ctx context.Context, userID, kind, normalizedValue string) error {
	perUser, err := s.limiter.Allow(ctx, "identifier_change:user:"+userID, ratelimiter.PerMinute(identifierChangeStartsPerUserPerMinute))
	if err != nil {
		return err
	}
	if !perUser.Allowed {
		return ErrIdentifierChangeRateLimited
	}
	perTarget, err := s.limiter.Allow(ctx, "identifier_change:target:"+s.identifierDigest(kind, normalizedValue), ratelimiter.PerMinute(identifierChangeStartsPerTargetPerMinute))
	if err != nil {
		return err
	}
	if !perTarget.Allowed {
		return ErrIdentifierChangeRateLimited
	}
	return nil
}

// changePurposes maps a change kind to its recent-auth grant purpose and its proof
// challenge purpose. The second bool is false for a kind outside the closed
// vocabulary, so an unknown kind never drives the rail.
func changePurposes(kind identifier.Kind) (grantPurpose, challengePurpose string, ok bool) {
	switch kind {
	case identifier.KindEmail:
		return authgrant.PurposeChangeEmail, challenge.PurposeChangeEmail, true
	case identifier.KindPhone:
		return authgrant.PurposeChangePhone, challenge.PurposeChangePhone, true
	default:
		return "", "", false
	}
}

// changeCodeSentEvent / changedEvent / removedEvent map a kind to its per-kind audit
// event type (design §5.5). An unknown kind is unreachable (the callers gate on
// changePurposes / a loaded identifier), so a defensive empty type is never recorded.
func changeCodeSentEvent(kind identifier.Kind) string {
	if kind == identifier.KindPhone {
		return securityevent.TypePhoneChangeCodeSent
	}
	return securityevent.TypeEmailChangeCodeSent
}

func changedEvent(kind identifier.Kind) string {
	if kind == identifier.KindPhone {
		return securityevent.TypePhoneChanged
	}
	return securityevent.TypeEmailChanged
}

func removedEvent(kind identifier.Kind) string {
	if kind == identifier.KindPhone {
		return securityevent.TypePhoneRemoved
	}
	return securityevent.TypeEmailRemoved
}
