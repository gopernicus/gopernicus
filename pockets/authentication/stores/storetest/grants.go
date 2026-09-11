package storetest

import (
	"context"
	"errors"
	"time"

	auth "github.com/gopernicus/gopernicus/pockets/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/authgrant"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/identifier"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/user"
	"github.com/gopernicus/gopernicus/sdk"
)

// grantFixture supplies the live owner/session required by the grant contract to
// the original metadata, expiry and one-use conformance cases.
type grantFixture struct{ auth.Repositories }

func (f grantFixture) Create(ctx context.Context, g authgrant.Grant) (authgrant.Grant, error) {
	now := time.Now().UTC()
	u, err := f.Users.Get(ctx, g.UserID)
	if errors.Is(err, sdk.ErrNotFound) {
		u, _, err = f.Users.CreateWithPrimaryIdentifier(ctx, user.User{ID: g.UserID, Status: user.StatusActive, CreatedAt: now, UpdatedAt: now}, identifier.Identifier{ID: "grant-identifier-" + g.UserID, Kind: identifier.KindEmail, NormalizedValue: g.UserID + "@grant.example", LoginEnabled: true, CreatedAt: now, UpdatedAt: now})
	}
	if err != nil {
		return authgrant.Grant{}, err
	}
	if _, err := f.Sessions.Get(ctx, g.SessionID); errors.Is(err, sdk.ErrNotFound) {
		_, err = f.Sessions.Create(ctx, session.Session{ID: g.SessionID, UserID: g.UserID, RefreshTokenHash: "grant-test-" + g.SessionID, ExpiresAt: now.Add(time.Hour), CreatedAt: now})
		if err != nil {
			return authgrant.Grant{}, err
		}
	} else if err != nil {
		return authgrant.Grant{}, err
	}
	return f.AuthenticationGrants.Create(ctx, g, u.AuthRevision, now)
}

func (f grantFixture) Consume(ctx context.Context, sessionID, purpose, digest string, now time.Time) (authgrant.Grant, error) {
	sess, err := f.Sessions.Get(ctx, sessionID)
	if err != nil {
		return authgrant.Grant{}, err
	}
	return f.AuthenticationGrants.Consume(ctx, authgrant.Requirement{
		SessionID: sessionID, UserID: sess.UserID, Purpose: purpose, ContextDigest: digest,
		AuthenticatedAfter: now.Add(-5 * time.Minute), MinAssurance: session.AssuranceAAL1,
	}, now)
}

func (f grantFixture) DeleteBySession(ctx context.Context, sessionID string) error {
	return f.AuthenticationGrants.DeleteBySession(ctx, sessionID)
}
