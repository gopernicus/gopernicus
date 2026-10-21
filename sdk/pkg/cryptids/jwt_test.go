package cryptids

import (
	"context"
	"reflect"
	"testing"
)

type verifierFunc func(ctx context.Context, token string) (map[string]any, error)

func (f verifierFunc) Verify(ctx context.Context, token string) (map[string]any, error) {
	return f(ctx, token)
}

var _ JWTVerifier = verifierFunc(nil)

func TestJWTVerifierIsDistinctFromSigner(t *testing.T) {
	// A first-party signer must never be wired where an external verifier is
	// expected: its Verify takes no context and pins no issuer.
	if reflect.TypeFor[JWTSigner]().Implements(reflect.TypeFor[JWTVerifier]()) {
		t.Fatal("JWTSigner satisfies JWTVerifier; the two ports must stay distinct")
	}
	if reflect.TypeFor[JWTVerifier]().Implements(reflect.TypeFor[JWTSigner]()) {
		t.Fatal("JWTVerifier satisfies JWTSigner; the two ports must stay distinct")
	}
}
