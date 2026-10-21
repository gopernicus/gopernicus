package memstore

import (
	"context"
	"errors"
	"testing"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
	"github.com/gopernicus/gopernicus/pockets/authentication/stores/storetest"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/list"
)

// The external mirror conformance suite: Provision must persist
// InitialCredentials.External, or the engine fails closed in production.
func TestExternalSubjectsConformance(t *testing.T) {
	storetest.RunExternalSubjects(t, func(*testing.T) auth.Repositories {
		repos := New(nil).Repositories()
		return auth.Repositories{
			Users:            repos.Users,
			Identifiers:      repos.Identifiers,
			ExternalSubjects: repos.ExternalSubjects,
			SecurityEvents:   repos.SecurityEvents,
		}
	})
}

func TestApplyVerifiedChangeIsUnsupported(t *testing.T) {
	_, err := Identifiers{New(nil)}.ApplyVerifiedChange(context.Background(), identifier.ApplyVerifiedChangeInput{}, 0, time.Now())
	if !errors.Is(err, ErrContactChangeUnsupported) || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("ApplyVerifiedChange error = %v", err)
	}
}

func TestSecurityEventsPageNewestFirst(t *testing.T) {
	ctx := context.Background()
	events := SecurityEvents{New(nil)}
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c"} {
		if _, err := events.Create(ctx, securityevent.SecurityEvent{ID: id, EventType: "t", EventStatus: "success", CreatedAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := events.List(ctx, securityevent.ListFilter{}, list.Request{Limit: 2})
	if err != nil || len(first.Items) != 2 || first.Items[0].ID != "c" || first.Items[1].ID != "b" || !first.HasMore {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := events.List(ctx, securityevent.ListFilter{}, list.Request{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "a" || second.HasMore {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	if _, err := events.List(ctx, securityevent.ListFilter{}, list.Request{Strategy: list.StrategyOffset}); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("offset paging error = %v; want ErrInvalidInput", err)
	}
}
