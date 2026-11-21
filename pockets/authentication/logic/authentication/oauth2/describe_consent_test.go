package oauth2

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

// TestDescribeConsentIsBoundAndReadOnly pins #380's consent read: it returns
// the prepared consent (client, the requested redirect, resource, the same
// token) for the approving user's own web session, writes no code, and refuses
// another user, another web session, an expired token and a tampered token.
func TestDescribeConsentIsBoundAndReadOnly(t *testing.T) {
	h := newAssuranceHarness(session.AuthenticationMetadata{})
	other := session.Session{ID: "web-2", UserID: "u-2", Profile: session.ProfileFirstParty, ExpiresAt: time.Now().Add(time.Hour)}
	sibling := session.Session{ID: "web-3", UserID: h.web.UserID, Profile: session.ProfileFirstParty, ExpiresAt: time.Now().Add(time.Hour)}
	h.st.sessions[other.ID], h.st.sessions[sibling.ID] = other, sibling
	now := time.Now()
	resolver := clientResolverFunc(func(_ context.Context, id string) (Client, error) {
		return Client{ID: id, Name: "Native client", RedirectURIs: []string{"http://localhost/callback"}}, nil
	})
	svc, err := New(Repositories{OAuth2: plainCodes{st: h.st}, Users: assuranceUsers{}, Sessions: assuranceSessions{h.st}},
		h.signer, resolver, assuranceVerifier{h.signer}, environment.ModeProduction, protocolConfig(), WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := loopbackRequest(svc, "http://localhost:54321/callback")
	req.State = "client-state"
	prepared, err := svc.PrepareAuthorization(t.Context(), req, h.web.UserID, h.web.ID)
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.DescribeConsent(t.Context(), prepared.ConsentToken, h.web.UserID, h.web.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Client.Name != "Native client" || got.Client.ID != denialClientID || got.RedirectURI != req.RedirectURI || got.Resource != svc.config.MCPResource || got.ConsentToken != prepared.ConsentToken || got.UserID != h.web.UserID {
		t.Fatalf("describe = %+v", got)
	}
	if len(h.st.codes) != 0 {
		t.Fatal("describing consent wrote a code")
	}

	for name, tc := range map[string]struct {
		token, user, web string
		want             error
	}{
		"another user":        {prepared.ConsentToken, other.UserID, other.ID, ErrAccessDenied},
		"another web session": {prepared.ConsentToken, sibling.UserID, sibling.ID, ErrAccessDenied},
		"tampered":            {prepared.ConsentToken + "x", h.web.UserID, h.web.ID, ErrInvalidRequest},
		"empty":               {"", h.web.UserID, h.web.ID, ErrInvalidRequest},
	} {
		if _, err := svc.DescribeConsent(t.Context(), tc.token, tc.user, tc.web); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	now = now.Add(svc.config.ConsentTTL)
	if _, err := svc.DescribeConsent(t.Context(), prepared.ConsentToken, h.web.UserID, h.web.ID); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("expired: err = %v, want ErrAccessDenied", err)
	}
}
