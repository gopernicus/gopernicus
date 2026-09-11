package golangjwt_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	golangjwt "github.com/gopernicus/gopernicus/integrations/cryptids/golang-jwt"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestMethodOptionSnapshotsBeforeConstruction(t *testing.T) {
	method := *jwt.SigningMethodHS512
	opt := golangjwt.WithMethod(&method)
	method = *jwt.SigningMethodHS256
	for range 2 {
		if signer, err := golangjwt.New(strings.Repeat("s", 32), opt, golangjwt.WithMethod(nil)); signer != nil || !errors.Is(err, golangjwt.ErrSecretTooShort) {
			t.Fatalf("snapshotted HS512 accepted short key: %v", err)
		}
		signer, err := golangjwt.New(testSecret, opt)
		if err != nil {
			t.Fatal(err)
		}
		token, err := signer.Sign(nil, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := jwt.Parse(token, func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != "HS512" {
				t.Errorf("alg = %q", token.Method.Alg())
			}
			return []byte(testSecret), nil
		}, jwt.WithValidMethods([]string{"HS512"}))
		if err != nil || !parsed.Valid {
			t.Fatalf("verify snapshot token: %v", err)
		}
	}
	if _, err := golangjwt.New(strings.Repeat("s", 32), opt, golangjwt.WithMethod(jwt.SigningMethodHS256)); err != nil {
		t.Fatalf("last method should determine key requirement: %v", err)
	}
}

func TestNewRejectsNilOption(t *testing.T) {
	signer, err := golangjwt.New(testSecret, nil)
	if signer != nil || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("New(nil) = %v, %v", signer, err)
	}
}
