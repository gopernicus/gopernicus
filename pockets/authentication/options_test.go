package authentication

import (
	"context"
	"errors"
	"testing"

	authlogic "github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication"
	"github.com/gopernicus/gopernicus/pockets/authentication/logic/delivery"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/pkg/environment"
)

func TestPasswordOptionReplacesTheWholePolicy(t *testing.T) {
	repos := testRepositories(Repositories{})
	disabled := WithPassword(PasswordConfig{PasswordFlowsDisabled: true})
	parts, err := New(repos, stubSigner{}, environment.ModeDevelopment, delivery.ModeOff, disabled)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parts.Authentication.Login(context.Background(), "user@example.com", "password"); !errors.Is(err, authlogic.ErrPasswordFlowsDisabled) {
		t.Fatalf("disabled login: %v", err)
	}
	if _, err := New(repos, stubSigner{}, environment.ModeDevelopment, delivery.ModeOff, disabled, WithPassword(PasswordConfig{})); !errors.Is(err, ErrHasherRequired) {
		t.Fatalf("zero replacement retained the disabled posture: %v", err)
	}
	if _, err := New(repos, stubSigner{}, environment.ModeDevelopment, delivery.ModeOff, nil); !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("nil option: %v", err)
	}
}
