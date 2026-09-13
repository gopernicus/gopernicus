package authentication

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestIdentifierRemoveRejectsInvalidReplacementBeforeConsumingProof(t *testing.T) {
	for _, scenario := range []string{"foreign", "missing", "retired", "self", "wrong_kind", "secondary"} {
		t.Run(scenario, func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			uid, email, sid := h.mustVerifiedLogin(t, "owner@example.com", "password123456789")
			other, _, _ := h.mustVerifiedLogin(t, "other@example.com", "password123456789")
			target, err := h.idents.GetLogin(ctx, "email", email)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			h.idents.insert(identifier.Identifier{ID: "recovery", UserID: uid, Kind: identifier.KindEmail, NormalizedValue: "recovery@example.com", LoginEnabled: true, RecoveryEnabled: true, VerifiedAt: now, CreatedAt: now, UpdatedAt: now})
			replacement := identifier.Identifier{ID: "replacement", UserID: uid, Kind: identifier.KindEmail, NormalizedValue: "replacement@example.com", NotificationEnabled: true, CreatedAt: now, UpdatedAt: now}
			switch scenario {
			case "foreign":
				replacement.UserID = other
			case "retired":
				replacement.Retire(now)
			case "wrong_kind":
				replacement.Kind = identifier.KindPhone
				replacement.NormalizedValue = "+12025550101"
			case "secondary":
				target = h.idents.insert(identifier.Identifier{ID: "secondary", UserID: uid, Kind: identifier.KindEmail, NormalizedValue: "secondary@example.com", CreatedAt: now, UpdatedAt: now})
			}
			replacement = h.idents.insert(replacement)
			replacementID := replacement.ID
			if scenario == "missing" {
				replacementID = "missing"
			}
			if scenario == "self" {
				replacementID = target.ID
			}
			h.backdateLogin(sid, time.Hour)
			owner, err := h.users.Get(ctx, uid)
			if err != nil {
				t.Fatal(err)
			}
			_, err = h.grants.Create(ctx, authgrant.Grant{UserID: uid, SessionID: sid, Purpose: authgrant.PurposeRemoveIdentifier, ContextDigest: grantContextDigest(target.ID), AuthenticatedAt: now, Assurance: session.AssuranceAAL1, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}, owner.AuthRevision, now)
			if err != nil {
				t.Fatal(err)
			}
			usersBefore := maps.Clone(h.users.byID)
			identsBefore := maps.Clone(h.idents.byID)
			sessionsBefore := maps.Clone(h.sess.m)
			grantsBefore := maps.Clone(h.grants.m)
			eventsBefore := h.events.recorded()
			err = h.svc.RemoveIdentifier(ctx, IdentifierRemoveInput{UserID: uid, SessionID: sid, IdentifierID: target.ID, ReplacementID: replacementID})
			if !errors.Is(err, sdk.ErrInvalidInput) && !errors.Is(err, sdk.ErrNotFound) {
				t.Errorf("invalid replacement: %v", err)
			}
			if !reflect.DeepEqual(h.users.byID, usersBefore) || !reflect.DeepEqual(h.idents.byID, identsBefore) || !reflect.DeepEqual(h.sess.m, sessionsBefore) || !reflect.DeepEqual(h.grants.m, grantsBefore) {
				t.Fatal("invalid replacement changed identifiers, users, sessions or consumed the proof")
			}
			if !reflect.DeepEqual(h.events.recorded(), eventsBefore) {
				t.Fatal("invalid replacement recorded success")
			}
		})
	}
}

func TestIdentifierRemoveExplicitContactOnlyReplacement(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	uid, _, sid := h.mustVerifiedLogin(t, "owner@example.com", "password123456789")
	now := time.Now().UTC()
	primary := h.idents.insert(identifier.Identifier{ID: "primary-phone", UserID: uid, Kind: identifier.KindPhone, NormalizedValue: "+12025550101", NotificationEnabled: true, IsPrimary: true, CreatedAt: now, UpdatedAt: now})
	replacement := h.idents.insert(identifier.Identifier{ID: "secondary-phone", UserID: uid, Kind: identifier.KindPhone, NormalizedValue: "+12025550102", NotificationEnabled: true, CreatedAt: now, UpdatedAt: now})
	if err := h.svc.RemoveIdentifier(ctx, IdentifierRemoveInput{UserID: uid, SessionID: sid, IdentifierID: primary.ID, ReplacementID: replacement.ID}); err != nil {
		t.Fatal(err)
	}
	retired, _ := h.idents.Get(ctx, primary.ID)
	promoted, _ := h.idents.Get(ctx, replacement.ID)
	if retired.Active() || !promoted.Active() || !promoted.IsPrimary || promoted.Verified() || promoted.LoginEnabled || promoted.RecoveryEnabled {
		t.Fatalf("invalid replacement state: retired=%+v promoted=%+v", retired, promoted)
	}
}
