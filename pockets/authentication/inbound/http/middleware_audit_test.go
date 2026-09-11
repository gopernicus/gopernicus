package authenticationhttp

import (
	"context"
	"sync"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// spySecurityEvents is the shared in-package audit spy the WI6 tests assert
// against. It records every Create; a non-nil createErr drives the never-fail
// and WARN-capture paths.
type spySecurityEvents struct {
	mu        sync.Mutex
	events    []securityevent.SecurityEvent
	createErr error
}

func newSpySecurityEvents() *spySecurityEvents { return &spySecurityEvents{} }

func (s *spySecurityEvents) Create(_ context.Context, evt securityevent.SecurityEvent) (securityevent.SecurityEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return securityevent.SecurityEvent{}, s.createErr
	}
	s.events = append(s.events, evt)
	return evt, nil
}

func (s *spySecurityEvents) List(_ context.Context, filter securityevent.ListFilter, _ list.Request) (list.Page[securityevent.SecurityEvent], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []securityevent.SecurityEvent{}
	for _, e := range s.events {
		if filter.Match(e) {
			items = append(items, e)
		}
	}
	return list.Page[securityevent.SecurityEvent]{Items: items}, nil
}

func (s *spySecurityEvents) recorded() []securityevent.SecurityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]securityevent.SecurityEvent(nil), s.events...)
}

func (s *spySecurityEvents) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// requireEvent asserts exactly one recorded event of eventType exists (later
// A-then-B ops record distinct types) with the expected status, returning it.
func requireEvent(t *testing.T, spy *spySecurityEvents, eventType, status string) securityevent.SecurityEvent {
	t.Helper()
	var found *securityevent.SecurityEvent
	var types []string
	for _, e := range spy.recorded() {
		types = append(types, e.EventType+"/"+e.EventStatus)
		if e.EventType == eventType {
			e := e
			found = &e
		}
	}
	if found == nil {
		t.Fatalf("no %s security event recorded; got %v", eventType, types)
	}
	if found.EventStatus != status {
		t.Fatalf("%s event status = %q, want %q", eventType, found.EventStatus, status)
	}
	return *found
}
