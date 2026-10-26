package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/apikey"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/serviceaccount"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// fakeAccountStatus is the service tests' status table over the machine fakes.
type fakeAccountStatus struct {
	keys    *fakeAPIKeys
	m       map[string]serviceaccount.Status
	readErr error
	deleted []string
}

func (s *fakeAccountStatus) GetStatus(_ context.Context, id string) (serviceaccount.Status, error) {
	if s.readErr != nil {
		return "", s.readErr
	}
	if st, ok := s.m[id]; ok {
		return st, nil
	}
	return serviceaccount.StatusActive, nil
}
func (s *fakeAccountStatus) Statuses(ctx context.Context, ids []string) (map[string]serviceaccount.Status, error) {
	out := map[string]serviceaccount.Status{}
	for _, id := range ids {
		out[id], _ = s.GetStatus(ctx, id)
	}
	return out, nil
}
func (s *fakeAccountStatus) Disable(_ context.Context, id, _ string, now time.Time) (serviceaccount.StatusChange, error) {
	if s.m[id] == serviceaccount.StatusDisabled {
		return serviceaccount.StatusChange{}, nil
	}
	s.m[id] = serviceaccount.StatusDisabled
	change := serviceaccount.StatusChange{Changed: true}
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	for kid, k := range s.keys.m {
		if k.ServiceAccountID == id && !k.Revoked() {
			k.RevokedAt = now
			s.keys.m[kid] = k
			change.KeysRevoked++
		}
	}
	return change, nil
}
func (s *fakeAccountStatus) Enable(_ context.Context, id, _ string, _ time.Time) (serviceaccount.StatusChange, error) {
	if s.m[id] != serviceaccount.StatusDisabled {
		return serviceaccount.StatusChange{}, nil
	}
	s.m[id] = serviceaccount.StatusActive
	return serviceaccount.StatusChange{Changed: true}, nil
}
func (s *fakeAccountStatus) Delete(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	delete(s.m, id)
	return nil
}
func (s *fakeAccountStatus) ProbeSchema(context.Context) error { return nil }

func TestMintAPIKeyMaxTTL(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	h.svc.now = func() time.Time { return at }
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")

	// Unbounded: an omitted expiry never expires, a past one is refused.
	k, _, err := h.svc.MintAPIKey(ctx, sa.ID, "forever", time.Time{})
	if err != nil || !k.ExpiresAt.IsZero() {
		t.Fatalf("unbounded mint = %+v, %v; want never-expiring", k, err)
	}
	if _, _, err := h.svc.MintAPIKey(ctx, sa.ID, "past", at); !errors.Is(err, ErrAPIKeyExpiryPast) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("expires_at at now = %v, want ErrAPIKeyExpiryPast", err)
	}

	h.svc.apiKeyMaxTTL = 90 * 24 * time.Hour
	k, _, err = h.svc.MintAPIKey(ctx, sa.ID, "defaulted", time.Time{})
	if err != nil || !k.ExpiresAt.Equal(at.Add(90*24*time.Hour)) {
		t.Fatalf("defaulted mint = %+v, %v; want now + 90d", k.ExpiresAt, err)
	}
	var minted []securityevent.SecurityEvent
	for _, e := range h.events.recorded() {
		if e.EventType == securityevent.TypeAPIKeyMinted {
			minted = append(minted, e)
		}
	}
	if minted[0].Details["expires_at"] != "never" || minted[0].Details["expires_at_defaulted"] != false {
		t.Errorf("unbounded mint row = %v", minted[0].Details)
	}
	if minted[1].Details["expires_at"] != "2027-01-24T12:00:00Z" || minted[1].Details["expires_at_defaulted"] != true {
		t.Errorf("defaulted mint row = %v", minted[1].Details)
	}

	_, _, err = h.svc.MintAPIKey(ctx, sa.ID, "far", at.Add(91*24*time.Hour))
	var exceeded *APIKeyTTLExceededError
	if !errors.As(err, &exceeded) || !errors.Is(err, ErrAPIKeyTTLExceeded) || exceeded.MaxTTL != 90*24*time.Hour || !exceeded.MaxExpiresAt.Equal(at.Add(90*24*time.Hour)) {
		t.Fatalf("beyond the bound = %v (%+v)", err, exceeded)
	}
	if _, _, err := h.svc.MintAPIKey(ctx, sa.ID, "inside", at.Add(24*time.Hour)); err != nil {
		t.Fatalf("an expiry inside the bound: %v", err)
	}
}

func TestServiceAccountDisableEnable(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	actor := Principal{Type: PrincipalUser, ID: "admin-1"}
	if _, _, err := h.svc.DisableServiceAccount(ctx, actor, "x"); !errors.Is(err, ErrServiceAccountStatusUnavailable) {
		t.Fatalf("unwired disable = %v, want ErrServiceAccountStatusUnavailable", err)
	}
	status := &fakeAccountStatus{keys: h.keys, m: map[string]serviceaccount.Status{}}
	h.svc.accountStatus = status
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})

	_, change, err := h.svc.DisableServiceAccount(ctx, actor, sa.ID)
	if err != nil || !change.Changed || change.KeysRevoked != 1 {
		t.Fatalf("disable = %+v, %v", change, err)
	}
	row := requireEvent(t, h.events, securityevent.TypeServiceAccountDisabled, securityevent.StatusSuccess)
	if row.Actor.ID != "admin-1" || row.Details["keys_revoked"] != 1 || row.Details["service_account_id"] != sa.ID {
		t.Fatalf("disabled row = %+v", row)
	}
	if _, _, err := h.svc.MintAPIKey(ctx, sa.ID, "k2", time.Time{}); !errors.Is(err, ErrServiceAccountDisabled) || !errors.Is(err, sdk.ErrConflict) {
		t.Fatalf("mint on a disabled account = %v, want ErrServiceAccountDisabled", err)
	}
	if _, _, ok, err := h.svc.verifyAPIKeyCredential(ctx, raw); ok || err != nil {
		t.Fatalf("a revoked key of a disabled account = ok %v err %v, want denied", ok, err)
	}
	if _, change, _ := h.svc.DisableServiceAccount(ctx, actor, sa.ID); change.Changed {
		t.Fatal("a repeat disable reported a change")
	}
	if _, change, err := h.svc.EnableServiceAccount(ctx, actor, sa.ID); err != nil || !change.Changed {
		t.Fatalf("enable = %+v, %v", change, err)
	}
	requireEvent(t, h.events, securityevent.TypeServiceAccountEnabled, securityevent.StatusSuccess)
	if _, _, err := h.svc.MintAPIKey(ctx, sa.ID, "k3", time.Time{}); err != nil {
		t.Fatalf("mint after enable: %v", err)
	}
	if _, err := h.svc.DeleteServiceAccount(ctx, actor, sa.ID); err != nil || len(status.deleted) != 1 {
		t.Fatalf("delete = %v, status rows deleted %v; want the status row gone too", err, status.deleted)
	}
}

// TestDisabledAccountKeyIsBlocked proves the status read gates a still-live
// key — a disable racing a mint, or a row a host wrote directly — with a
// blocked apikey_auth reason, and an unreadable status answers unavailable.
func TestDisabledAccountKeyIsBlocked(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	status := &fakeAccountStatus{keys: h.keys, m: map[string]serviceaccount.Status{}}
	h.svc.accountStatus = status
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	_, raw, _ := h.svc.MintAPIKey(ctx, sa.ID, "k", time.Time{})
	status.m[sa.ID] = serviceaccount.StatusDisabled
	if _, _, ok, err := h.svc.verifyAPIKeyCredential(ctx, raw); ok || err != nil {
		t.Fatalf("a disabled account's key = ok %v err %v", ok, err)
	}
	row := requireEvent(t, h.events, securityevent.TypeAPIKeyAuth, securityevent.StatusBlocked)
	if row.Details["reason"] != securityevent.ReasonServiceAccountDisabled || row.Actor.ID != sa.ID {
		t.Fatalf("apikey_auth row = %+v", row)
	}
	status.readErr = errors.New("db down")
	if _, _, ok, err := h.svc.verifyAPIKeyCredential(ctx, raw); ok || !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("an unreadable status = ok %v err %v, want ErrUnavailable", ok, err)
	}
	if _, err := h.svc.Authenticate(ctx, CredentialAPIKey, TransportHeader, raw); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("Authenticate over an unreadable status = %v, want ErrUnavailable", err)
	}
}

func TestListAPIKeysFiltered(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	if h.svc.APIKeyFilterSupported() {
		t.Fatal("the fake store reported the optional lister")
	}
	if _, err := h.svc.ListAPIKeysFiltered(ctx, "sa", apikey.Filter{Status: apikey.StatusActive}, list.Request{}); !errors.Is(err, ErrAPIKeyFilterUnsupported) {
		t.Fatalf("a store without the lister = %v", err)
	}
	if _, err := h.svc.ListAPIKeysFiltered(ctx, "sa", apikey.Filter{Status: "bogus"}, list.Request{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("an unknown status = %v", err)
	}
	if _, err := h.svc.ListAPIKeysFiltered(ctx, "sa", apikey.Filter{Status: apikey.StatusExpired, NeverExpires: true}, list.Request{}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("a contradiction = %v", err)
	}
}

// probingStatus answers ProbeSchema with err.
type probingStatus struct {
	fakeAccountStatus
	err error
}

func (p *probingStatus) ProbeSchema(context.Context) error { return p.err }

func TestServiceAccountStatusWiring(t *testing.T) {
	machine := constructorConfig{ServiceAccounts: newFakeServiceAccounts(), APIKeys: newFakeAPIKeys(), ServiceAccountStatus: true}
	if err := validateServiceAccountStatus(machine); !errors.Is(err, ErrServiceAccountStatusRequires) {
		t.Fatalf("the flag without the repository = %v", err)
	}
	for _, tc := range []struct {
		probe error
		want  error
	}{
		{nil, nil},
		{sdk.ErrNotFound, ErrServiceAccountStatusSchemaMissing},
		{errors.New("connection refused"), sdk.ErrUnavailable},
	} {
		d := machine
		d.ServiceAccountStatuses = &probingStatus{err: tc.probe}
		err := validateServiceAccountStatus(d)
		if (tc.want == nil && err != nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("probe %v = %v, want %v", tc.probe, err, tc.want)
		}
	}
	unwired := constructorConfig{ServiceAccountStatuses: &probingStatus{err: sdk.ErrNotFound}}
	if err := validateServiceAccountStatus(unwired); err != nil {
		t.Fatalf("an unwired store was probed: %v", err)
	}
	if svc := newService(unwired); svc.ServiceAccountStatusEnabled() {
		t.Fatal("the repository alone enabled the status")
	}
}

// racingStatus answers the mint's first read active and lets a disable commit
// right after it, before the key is inserted.
type racingStatus struct {
	*fakeAccountStatus
	reads int
}

func (r *racingStatus) GetStatus(ctx context.Context, id string) (serviceaccount.Status, error) {
	r.reads++
	st, err := r.fakeAccountStatus.GetStatus(ctx, id)
	if r.reads == 1 {
		_, _ = r.fakeAccountStatus.Disable(ctx, id, "user:admin", time.Now())
	}
	return st, err
}

// TestMintRacingADisableLeavesNoLiveKey is the M1 race: a disable that commits
// between the mint's status read and its insert still leaves no unrevoked key.
func TestMintRacingADisableLeavesNoLiveKey(t *testing.T) {
	h := newMachineHarness(t)
	ctx := context.Background()
	race := &racingStatus{fakeAccountStatus: &fakeAccountStatus{keys: h.keys, m: map[string]serviceaccount.Status{}}}
	h.svc.accountStatus = race
	sa, _ := h.svc.CreateServiceAccount(ctx, "admin", "bot", "", false, "")
	if _, _, err := h.svc.MintAPIKey(ctx, sa.ID, "racer", time.Time{}); !errors.Is(err, ErrServiceAccountDisabled) {
		t.Fatalf("a mint that lost the race = %v, want ErrServiceAccountDisabled", err)
	}
	h.keys.mu.Lock()
	defer h.keys.mu.Unlock()
	if len(h.keys.m) != 1 {
		t.Fatalf("keys = %d, want the one inserted", len(h.keys.m))
	}
	for _, k := range h.keys.m {
		if !k.Revoked() {
			t.Fatalf("the raced key %+v is live; an enable would resurrect it", k)
		}
	}
	if _, err := race.fakeAccountStatus.Enable(ctx, sa.ID, "user:admin", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, k := range h.keys.m {
		if !k.Revoked() {
			t.Fatal("enable resurrected the raced key")
		}
	}
}
