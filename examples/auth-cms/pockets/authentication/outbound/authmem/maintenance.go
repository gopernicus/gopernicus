package authmem

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"time"

	authgrant "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	contactchange "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/contactchange"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauth2"
	oauthstate "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/oauthstate"
	securityevent "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	session "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	invitation "github.com/gopernicus/gopernicus/pockets/authentication/logic/invitations"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/maintenance"
	"github.com/gopernicus/gopernicus/sdk"
)

// memFloor is this store's retention floor for the rail: the Go minimum less
// the day of clock-skew margin, measured on time.Now — the store's own clock.
const memFloor = maintenance.MinSecurityEventRetention - 24*time.Hour

// neverPurged is the purge instant of a row maintenance never removes (an
// accepting claim).
var neverPurged = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

var _ maintenance.Repository = maintenanceRepo{}

// maintenanceRepo is the retention port (SOC 2 S3). Each call is one critical
// section under the shared mutex; the rail purge appends its
// security_events_purged row in the same one, as the port contract requires.
type maintenanceRepo struct{ *data }

func memLimit(limit int) error {
	if limit <= 0 {
		return fmt.Errorf("authmem: limit %d: %w", limit, sdk.ErrInvalidInput)
	}
	return nil
}

// purgeExpired deletes up to limit entries of m whose instant is at or before
// before, oldest first. Callers hold the mutex.
func purgeExpired[V any](m map[string]V, at func(V) time.Time, before time.Time, limit int) int {
	type candidate struct {
		key string
		at  time.Time
	}
	var cands []candidate
	for k, v := range m {
		if t := at(v); !t.After(before) {
			cands = append(cands, candidate{k, t})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if !cands[i].at.Equal(cands[j].at) {
			return cands[i].at.Before(cands[j].at)
		}
		return cands[i].key < cands[j].key
	})
	n := min(limit, len(cands))
	for _, c := range cands[:n] {
		delete(m, c.key)
	}
	return n
}

func (r maintenanceRepo) PurgeExpiredSessions(_ context.Context, before time.Time, limit int) (int, error) {
	if err := memLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return purgeExpired(r.sessions, func(s session.Session) time.Time { return s.ExpiresAt }, before, limit), nil
}

func (r maintenanceRepo) PurgeExpiredOAuthStates(_ context.Context, before time.Time, limit int) (int, error) {
	if err := memLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return purgeExpired(r.oauthStates, func(s oauthstate.State) time.Time { return s.ExpiresAt }, before, limit), nil
}

func (r maintenanceRepo) PurgeExpiredGrants(_ context.Context, before time.Time, limit int) (int, error) {
	if err := memLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return purgeExpired(r.authGrants, func(g authgrant.Grant) time.Time { return g.ExpiresAt }, before, limit), nil
}

func (r maintenanceRepo) PurgeExpiredContactChanges(_ context.Context, before time.Time, limit int) (int, error) {
	if err := memLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return purgeExpired(r.contactChanges, func(p contactchange.PendingChange) time.Time { return p.ExpiresAt }, before, limit), nil
}

// invitationPurgeAt is when an invitation becomes purgeable: a terminal row at
// its last update, a pending row at its expiry, an accepting claim never.
func invitationPurgeAt(inv invitation.Invitation) time.Time {
	switch inv.Status {
	case invitation.StatusAccepted, invitation.StatusDeclined, invitation.StatusCancelled:
		return inv.UpdatedAt
	case invitation.StatusPending:
		return inv.ExpiresAt
	}
	return neverPurged
}

func (r maintenanceRepo) PurgeInvitations(_ context.Context, before time.Time, limit int) (int, error) {
	if err := memLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return purgeExpired(r.invitations, invitationPurgeAt, before, limit), nil
}

func (r maintenanceRepo) CountStaleAcceptingInvitations(_ context.Context, before time.Time) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, inv := range r.invitations {
		if inv.Status == invitation.StatusAccepting && !inv.UpdatedAt.After(before) {
			n++
		}
	}
	return n, nil
}

func (r maintenanceRepo) PruneOAuth2(_ context.Context, now time.Time, limit int) (int, error) {
	if err := memLimit(limit); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := purgeExpired(r.oauthCodes, func(c oauth2.Code) time.Time { return c.ExpiresAt }, now, limit)
	n += purgeExpired(r.oauthClients, func(c oauth2.Client) time.Time { return c.ExpiresAt }, now, limit)
	n += purgeExpired(r.oauthRefresh, func(h oauthRefreshRecord) time.Time { return h.ExpiresAt }, now, limit)
	return n, nil
}

// railLocked returns the rail in ascending (created_at, id) order. Callers hold
// the mutex.
func (d *data) railLocked() []securityevent.SecurityEvent {
	rows := slices.Clone(d.securityEvents)
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.Before(rows[j].CreatedAt)
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

func (r maintenanceRepo) ExportSecurityEvents(_ context.Context, before time.Time, after maintenance.ExportCursor, limit int) ([]maintenance.ArchivedEvent, maintenance.ExportCursor, error) {
	if err := memLimit(limit); err != nil {
		return nil, after, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []maintenance.ArchivedEvent
	for _, evt := range r.railLocked() {
		if evt.CreatedAt.After(before) {
			break
		}
		if !after.CreatedAt.IsZero() && (evt.CreatedAt.Before(after.CreatedAt) || (evt.CreatedAt.Equal(after.CreatedAt) && evt.ID <= after.ID)) {
			continue
		}
		if len(out) >= limit && !evt.CreatedAt.Equal(out[len(out)-1].CreatedAt) {
			break
		}
		evt.Details = maps.Clone(evt.Details)
		out = append(out, maintenance.ArchivedEvent{SecurityEvent: evt})
	}
	if len(out) == 0 {
		return nil, after, nil
	}
	last := out[len(out)-1]
	return out, maintenance.ExportCursor{CreatedAt: last.CreatedAt, ID: last.ID}, nil
}

func (r maintenanceRepo) PurgeSecurityEventsBefore(_ context.Context, before time.Time, limit int) (maintenance.PurgeResult, error) {
	if err := memLimit(limit); err != nil {
		return maintenance.PurgeResult{}, err
	}
	now := time.Now().UTC()
	if before.After(now.Add(-memFloor)) {
		return maintenance.PurgeResult{}, fmt.Errorf("authmem: cutoff %s: %w", before.Format(time.RFC3339Nano), maintenance.ErrBelowRetentionFloor)
	}
	limit = min(limit, maintenance.MaxBatchSize)
	r.mu.Lock()
	defer r.mu.Unlock()
	doomed := map[string]bool{}
	for _, evt := range r.railLocked() {
		if len(doomed) == limit || evt.CreatedAt.After(before) {
			break
		}
		doomed[evt.ID] = true
	}
	kept := r.securityEvents[:0:0]
	for _, evt := range r.securityEvents {
		if !doomed[evt.ID] {
			kept = append(kept, evt)
		}
	}
	r.securityEvents = append(kept, securityevent.SecurityEvent{
		ID:          ids.MustGenerate(),
		Actor:       securityevent.Principal{Type: "system", ID: "maintenance"},
		EventType:   securityevent.TypeSecurityEventsPurged,
		EventStatus: securityevent.StatusSuccess,
		Details: map[string]any{
			"cutoff":     before.UTC().Format("2006-01-02T15:04:05.000000Z"),
			"count":      len(doomed),
			"floor_days": int(memFloor / (24 * time.Hour)),
			"max_rows":   limit,
		},
		CreatedAt: now,
	})
	return maintenance.PurgeResult{Cutoff: before, Count: len(doomed)}, nil
}
