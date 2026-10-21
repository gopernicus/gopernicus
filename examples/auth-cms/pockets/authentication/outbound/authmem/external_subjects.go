package authmem

import (
	"context"
	"sort"

	externalsubject "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/externalsubject"
	session "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

var _ externalsubject.Repository = externalSubjectRepo{}

// externalKey is the mirror's (provider, subject) primary key.
type externalKey struct{ provider, subject string }

// --- externalsubject.Repository ---

// externalSubjectRepo hand-enforces the (provider, subject) primary key and the
// active-user precondition of Link under the shared mutex; Provision writes the
// first row in the same critical section as its user.
type externalSubjectRepo struct{ *data }

func (r externalSubjectRepo) Get(_ context.Context, provider, subject string) (externalsubject.ExternalSubject, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.externalSubjects[externalKey{provider, subject}]
	if !ok {
		return externalsubject.ExternalSubject{}, sdk.ErrNotFound
	}
	return s, nil
}

func (r externalSubjectRepo) Link(_ context.Context, s externalsubject.ExternalSubject) (externalsubject.ExternalSubject, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[s.UserID]
	if !ok {
		return externalsubject.ExternalSubject{}, sdk.ErrNotFound
	}
	if !u.Active() {
		return externalsubject.ExternalSubject{}, session.ErrUserNotActive
	}
	key := externalKey{s.Provider, s.Subject}
	if _, taken := r.externalSubjects[key]; taken {
		return externalsubject.ExternalSubject{}, sdk.ErrAlreadyExists
	}
	s.FirstSeenAt = s.FirstSeenAt.UTC()
	r.externalSubjects[key] = s
	return s, nil
}

func (r externalSubjectRepo) ListByUser(_ context.Context, userID string) ([]externalsubject.ExternalSubject, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []externalsubject.ExternalSubject{}
	for _, s := range r.externalSubjects {
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

func (r externalSubjectRepo) Unlink(_ context.Context, provider, subject string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := externalKey{provider, subject}
	if _, ok := r.externalSubjects[key]; !ok {
		return sdk.ErrNotFound
	}
	delete(r.externalSubjects, key)
	return nil
}
