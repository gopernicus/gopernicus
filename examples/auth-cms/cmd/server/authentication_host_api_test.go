package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	authenticationlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	session "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/session"
)

// A host transport can establish caller identity and drive step-up without
// depending on the pocket's internal service or HTTP request structures.
func TestHostCanUsePublicSensitiveAuthentication(t *testing.T) {
	host := newLinkHost(t)
	host.signUp("public-api@example.com")
	pair, _, err := host.svc.Authentication.Login(context.Background(), "public-api@example.com", linkPassword)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := host.svc.HTTP.RequireAccessTokenLive()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		uid, ok := host.svc.Authentication.CurrentUser(r.Context())
		if !ok {
			t.Fatal("no authenticated user")
		}
		sid, ok := host.svc.Authentication.CurrentSessionID(r.Context())
		if !ok {
			t.Fatal("no live session")
		}
		methods, err := host.svc.Authentication.Methods(r.Context(), uid)
		if err != nil || !methods.HasPassword {
			t.Fatalf("public inventory: %+v %v", methods, err)
		}
		_, err = host.svc.Authentication.RequireRecentAuthentication(r.Context(), sid, uid, "host_operation", "resource", authenticationlogic.RecentAuthPolicy{MinAssurance: session.AssuranceAAL2})
		if !errors.Is(err, authenticationlogic.ErrStepUpRequired) {
			t.Fatalf("stronger host policy accepted weaker login: %v", err)
		}
		proof, err := host.svc.Authentication.CompleteStepUpWithPassword(r.Context(), authenticationlogic.StepUpCompletion{UserID: uid, SessionID: sid, Purpose: "host_operation", Context: "resource"}, linkPassword)
		if err != nil || proof.SessionID != sid || proof.Assurance != session.AssuranceAAL1 {
			t.Fatalf("public password proof: %+v %v", proof, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodPost, "/host-operation", nil)
	r.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if !called || w.Code != http.StatusNoContent {
		t.Fatalf("host live gate: %d %s", w.Code, w.Body)
	}
}
