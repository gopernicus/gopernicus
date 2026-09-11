package outbox

import (
	"context"
	"errors"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	sdkevents "github.com/gopernicus/gopernicus/sdk/capabilities/events"
)

func newTestPoller(t *testing.T, repo EntryRepository, deliver DeliverFunc, opts ...PollerOption) *Poller {
	t.Helper()
	poller, err := NewPoller(repo, deliver, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return poller
}

func TestNewPollerRejectsInvalidWiringWithoutDelivery(t *testing.T) {
	deliver := func(context.Context, sdkevents.Event) error {
		t.Fatal("constructor called delivery")
		return nil
	}
	var typedNil *fakeRepo
	for _, tc := range []struct {
		name    string
		repo    EntryRepository
		deliver DeliverFunc
		opts    []PollerOption
	}{
		{"nil repository", nil, deliver, nil},
		{"typed nil repository", typedNil, deliver, nil},
		{"nil delivery", newFakeRepo(), nil, nil},
		{"nil option", newFakeRepo(), deliver, []PollerOption{nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			poller, err := NewPoller(tc.repo, tc.deliver, tc.opts...)
			if poller != nil || !errors.Is(err, sdk.ErrInvalidInput) {
				t.Fatalf("poller=%v error=%v", poller, err)
			}
		})
	}
}
