// Package memstore is the in-memory store behind this external-only host: the
// smallest set of authentication ports the external engine reads and writes —
// users, identifiers, the external subject mirror and the audit rail. It holds
// no passwords, sessions, challenges or API keys, because this host has none.
//
// Every user read and audit row is logged, so the proof can count the local
// status reads a request makes and see each provisioning event. State is lost
// on restart.
package memstore

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

const eventOrderField = "created_at"

var (
	// ErrContactChangeUnsupported answers ApplyVerifiedChange: contact changes
	// are a native flow this external-only host does not run.
	ErrContactChangeUnsupported = fmt.Errorf("memstore: contact changes are not supported: %w", sdk.ErrInvalidInput)

	_ user.UserRepository                   = Users{}
	_ identifier.IdentifierRepository       = Identifiers{}
	_ externalsubject.Repository            = ExternalSubjects{}
	_ securityevent.SecurityEventRepository = SecurityEvents{}
)

// Store is the shared state behind every repository. One mutex makes Provision
// atomic across users, identifiers and the mirror.
type Store struct {
	mu          sync.RWMutex
	ids         sdk.IDGenerator
	log         *slog.Logger
	users       map[string]user.User
	identifiers map[string]identifier.Identifier
	subjects    map[subjectKey]externalsubject.ExternalSubject
	events      []securityevent.SecurityEvent
}

type subjectKey struct{ provider, subject string }

// Users implements user.UserRepository.
type Users struct{ *Store }

// Identifiers implements identifier.IdentifierRepository.
type Identifiers struct{ *Store }

// ExternalSubjects implements externalsubject.Repository.
type ExternalSubjects struct{ *Store }

// SecurityEvents implements securityevent.SecurityEventRepository.
type SecurityEvents struct{ *Store }

// New returns an empty store. A nil logger discards.
func New(log *slog.Logger) *Store {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Store{
		log:         log,
		users:       map[string]user.User{},
		identifiers: map[string]identifier.Identifier{},
		subjects:    map[subjectKey]externalsubject.ExternalSubject{},
	}
}

// Repositories returns the external engine's repository set.
func (s *Store) Repositories() authlogic.ExternalRepositories {
	return authlogic.ExternalRepositories{
		Users:            Users{s},
		Identifiers:      Identifiers{s},
		ExternalSubjects: ExternalSubjects{s},
		SecurityEvents:   SecurityEvents{s},
	}
}

// Provision writes the user, its first identifier and the mirror row together,
// or none of them.
func (r Users) Provision(ctx context.Context, u user.User, ident identifier.Identifier, credentials user.InitialCredentials) (user.User, identifier.Identifier, error) {
	if err := ctx.Err(); err != nil {
		return user.User{}, identifier.Identifier{}, err
	}
	if credentials.PasswordHash != "" || credentials.OAuth != nil {
		return user.User{}, identifier.Identifier{}, fmt.Errorf("memstore: native credentials are not stored here: %w", sdk.ErrInvalidInput)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if credentials.External != nil {
		if _, taken := r.subjects[subjectKey{credentials.External.Provider, credentials.External.Subject}]; taken {
			return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
		}
	}
	u, ident, err := r.insertLocked(u, ident)
	if err != nil {
		return user.User{}, identifier.Identifier{}, err
	}
	if credentials.External != nil {
		mirror := *credentials.External
		mirror.UserID = u.ID
		mirror.FirstSeenAt = mirror.FirstSeenAt.UTC()
		r.subjects[subjectKey{mirror.Provider, mirror.Subject}] = mirror
	}
	return u, ident, nil
}

// CreateWithPrimaryIdentifier writes the user and its first identifier together.
func (r Users) CreateWithPrimaryIdentifier(ctx context.Context, u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	if err := ctx.Err(); err != nil {
		return user.User{}, identifier.Identifier{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.insertLocked(u, ident)
}

func (s *Store) insertLocked(u user.User, ident identifier.Identifier) (user.User, identifier.Identifier, error) {
	if claimsAuthentication(ident) {
		if _, taken := s.activeClaimLocked(ident.Kind, ident.NormalizedValue, func(i identifier.Identifier) bool { return i.LoginEnabled || i.RecoveryEnabled }); taken {
			return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
		}
	}
	if u.ID == "" {
		u.ID = s.ids.MustGenerate()
	}
	if ident.ID == "" {
		ident.ID = s.ids.MustGenerate()
	}
	if _, exists := s.users[u.ID]; exists {
		return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
	}
	if _, exists := s.identifiers[ident.ID]; exists {
		return user.User{}, identifier.Identifier{}, sdk.ErrAlreadyExists
	}
	ident.UserID = u.ID
	s.users[u.ID] = u
	s.identifiers[ident.ID] = ident
	return u, ident, nil
}

// Get returns the user and logs the read: on this host every Get is a local
// status read by the external engine or an identity projection.
func (r Users) Get(_ context.Context, id string) (user.User, error) {
	r.mu.RLock()
	u, ok := r.users[id]
	r.mu.RUnlock()
	r.log.Info("memstore: local user read", "user_id", id, "found", ok)
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	return u, nil
}

// Update persists DisplayName and UpdatedAt only.
func (r Users) Update(_ context.Context, id string, u user.User) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.users[id]
	if !ok {
		return user.User{}, sdk.ErrNotFound
	}
	existing.DisplayName = u.DisplayName
	existing.UpdatedAt = u.UpdatedAt
	r.users[id] = existing
	return existing, nil
}

// Get returns the identifier with id, or sdk.ErrNotFound.
func (r Identifiers) Get(_ context.Context, id string) (identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ident, ok := r.identifiers[id]
	if !ok {
		return identifier.Identifier{}, sdk.ErrNotFound
	}
	return ident, nil
}

// GetLogin returns the active login claim on (kind, value).
func (r Identifiers) GetLogin(_ context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ident, ok := r.activeClaimLocked(identifier.Kind(kind), normalizedValue, func(i identifier.Identifier) bool { return i.LoginEnabled }); ok {
		return ident, nil
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

// GetRecovery returns the active recovery claim on (kind, value).
func (r Identifiers) GetRecovery(_ context.Context, kind, normalizedValue string) (identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if ident, ok := r.activeClaimLocked(identifier.Kind(kind), normalizedValue, func(i identifier.Identifier) bool { return i.RecoveryEnabled }); ok {
		return ident, nil
	}
	return identifier.Identifier{}, sdk.ErrNotFound
}

// ListByUser returns the user's active identifiers, oldest first.
func (r Identifiers) ListByUser(_ context.Context, userID string) ([]identifier.Identifier, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []identifier.Identifier{}
	for _, ident := range r.identifiers {
		if ident.UserID == userID && ident.ReplacedAt.IsZero() {
			out = append(out, ident)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// ApplyVerifiedChange is unsupported: see ErrContactChangeUnsupported.
func (r Identifiers) ApplyVerifiedChange(context.Context, identifier.ApplyVerifiedChangeInput, int64, time.Time) (identifier.Identifier, error) {
	return identifier.Identifier{}, ErrContactChangeUnsupported
}

func (s *Store) activeClaimLocked(kind identifier.Kind, value string, use func(identifier.Identifier) bool) (identifier.Identifier, bool) {
	for _, ident := range s.identifiers {
		if ident.Kind == kind && ident.NormalizedValue == value && ident.ReplacedAt.IsZero() && use(ident) {
			return ident, true
		}
	}
	return identifier.Identifier{}, false
}

func claimsAuthentication(ident identifier.Identifier) bool {
	return ident.ReplacedAt.IsZero() && (ident.LoginEnabled || ident.RecoveryEnabled)
}

// Get returns the mirror row for (provider, subject), or sdk.ErrNotFound.
func (r ExternalSubjects) Get(_ context.Context, provider, subject string) (externalsubject.ExternalSubject, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.subjects[subjectKey{provider, subject}]
	if !ok {
		return externalsubject.ExternalSubject{}, sdk.ErrNotFound
	}
	return s, nil
}

// Link maps a subject to an existing active user.
func (r ExternalSubjects) Link(_ context.Context, s externalsubject.ExternalSubject) (externalsubject.ExternalSubject, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[s.UserID]
	if !ok {
		return externalsubject.ExternalSubject{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return externalsubject.ExternalSubject{}, session.ErrUserNotActive
	}
	key := subjectKey{s.Provider, s.Subject}
	if _, taken := r.subjects[key]; taken {
		return externalsubject.ExternalSubject{}, sdk.ErrAlreadyExists
	}
	s.FirstSeenAt = s.FirstSeenAt.UTC()
	r.subjects[key] = s
	return s, nil
}

// ListByUser returns the user's mirror rows, oldest first.
func (r ExternalSubjects) ListByUser(_ context.Context, userID string) ([]externalsubject.ExternalSubject, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []externalsubject.ExternalSubject{}
	for _, s := range r.subjects {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].FirstSeenAt.Equal(out[j].FirstSeenAt) {
			return out[i].FirstSeenAt.Before(out[j].FirstSeenAt)
		}
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Subject < out[j].Subject
	})
	return out, nil
}

// Unlink removes a mirror row, or returns sdk.ErrNotFound.
func (r ExternalSubjects) Unlink(_ context.Context, provider, subject string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := subjectKey{provider, subject}
	if _, ok := r.subjects[key]; !ok {
		return sdk.ErrNotFound
	}
	delete(r.subjects, key)
	return nil
}

// Create appends an audit row and logs its type and status (never details).
func (r SecurityEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	if evt.ID == "" {
		evt.ID = r.ids.MustGenerate()
	}
	details := make(map[string]any, len(evt.Details))
	for k, v := range evt.Details {
		details[k] = v
	}
	evt.Details = details
	r.mu.Lock()
	r.events = append(r.events, evt)
	r.mu.Unlock()
	r.log.Info("memstore: security event", "type", evt.EventType, "status", evt.EventStatus, "user_id", evt.UserID)
	return evt, nil
}

// List pages matching events newest first (created_at DESC, id DESC). Only the
// cursor strategy is supported.
func (r SecurityEvents) List(_ context.Context, filter securityevent.ListFilter, req list.Request) (list.Page[securityevent.SecurityEvent], error) {
	if err := req.Validate(); err != nil {
		return list.Page[securityevent.SecurityEvent]{}, err
	}
	if req.ResolvedStrategy() != list.StrategyCursor {
		return list.Page[securityevent.SecurityEvent]{}, fmt.Errorf("memstore: only cursor paging is supported: %w", sdk.ErrInvalidInput)
	}
	cursor, err := list.DecodeCursor(req.Cursor, eventOrderField)
	if err != nil {
		return list.Page[securityevent.SecurityEvent]{}, err
	}
	r.mu.RLock()
	matched := make([]securityevent.SecurityEvent, 0, len(r.events))
	for _, evt := range r.events {
		if filter.Match(evt) {
			matched = append(matched, evt)
		}
	}
	r.mu.RUnlock()
	sort.Slice(matched, func(i, j int) bool {
		return newer(matched[i].CreatedAt, matched[i].ID, matched[j].CreatedAt, matched[j].ID)
	})
	if cursor != nil {
		at, _ := cursor.OrderValue.(time.Time)
		start := sort.Search(len(matched), func(i int) bool { return newer(at, cursor.PK, matched[i].CreatedAt, matched[i].ID) })
		matched = matched[start:]
	}
	limit := req.NormalizedLimit(list.Limits{})
	if len(matched) > limit+1 {
		matched = matched[:limit+1]
	}
	return list.TrimPage(matched, limit, func(evt securityevent.SecurityEvent) (string, error) {
		return list.EncodeCursor(eventOrderField, evt.CreatedAt, evt.ID)
	})
}

// newer reports whether (at, id) sorts before (bt, bid) under created_at DESC, id DESC.
func newer(at time.Time, id string, bt time.Time, bid string) bool {
	if !at.Equal(bt) {
		return at.After(bt)
	}
	return id > bid
}
