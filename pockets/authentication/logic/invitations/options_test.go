package invitations

import (
	"context"
	"errors"
	"strings"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestConstructorRejectsNilOption(t *testing.T) {
	if _, err := New(nil, nil, nil); !errors.Is(err, sdk.ErrInvalidInput) || !strings.Contains(err.Error(), "nil option") {
		t.Fatalf("nil option: %v", err)
	}
}

// uncomparableEvents is a repository value type that == cannot compare.
type uncomparableEvents struct {
	*fakeSecurityEvents
	tags []string
}

func TestSecurityEventRecorderPrecedence(t *testing.T) {
	shared := &fakeSecurityEvents{}
	rec := securityevent.NewRecorder(shared, sdk.IDGenerator{}, nil, nil)

	svc, err := New(newFakeInvRepo(), &fakeGranter{}, WithSecurityEventRecorder(rec))
	if err != nil {
		t.Fatal(err)
	}
	if svc.securityEvents != rec {
		t.Fatal("WithSecurityEventRecorder did not become the service's recorder")
	}

	svc, err = New(newFakeInvRepo(), &fakeGranter{}, WithSecurityEvents(shared), WithSecurityEventRecorder(rec))
	if err != nil {
		t.Fatalf("both options over the same repository: %v", err)
	}
	if svc.securityEvents != rec {
		t.Fatal("the recorder must win over WithSecurityEvents")
	}

	other := &fakeSecurityEvents{}
	for _, opts := range [][]Option{
		{WithSecurityEvents(other), WithSecurityEventRecorder(rec)},
		{WithSecurityEventRecorder(rec), WithSecurityEvents(other)},
		{WithSecurityEvents(uncomparableEvents{fakeSecurityEvents: shared}), WithSecurityEventRecorder(securityevent.NewRecorder(uncomparableEvents{fakeSecurityEvents: shared}, sdk.IDGenerator{}, nil, nil))},
		{WithSecurityEvents(other), WithSecurityEventRecorder(securityevent.NewRecorder(nil, sdk.IDGenerator{}, nil, nil))},
	} {
		if _, err := New(newFakeInvRepo(), &fakeGranter{}, opts...); !errors.Is(err, ErrSecurityEventsConflict) || !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("different repositories: %v, want ErrSecurityEventsConflict", err)
		}
	}

	svc, err = New(newFakeInvRepo(), &fakeGranter{}, WithSecurityEvents(other))
	if err != nil {
		t.Fatal(err)
	}
	if svc.securityEvents == nil || svc.securityEvents == rec || svc.securityEvents.Repository() != other {
		t.Fatal("WithSecurityEvents alone must build the service's own recorder over that repository")
	}
	svc.recordAs(authlogic.WithClientInfo(context.Background(), "203.0.113.9", "agent/2"), securityevent.Principal{}, "u-1", securityevent.TypeInvitationDeclined, securityevent.StatusSuccess, nil)
	if len(other.created) != 1 || other.created[0].IPAddress != "203.0.113.9" || other.created[0].UserAgent != "agent/2" {
		t.Fatalf("own recorder row = %+v, want client attribution from the carrier", other.created)
	}
}
