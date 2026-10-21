package firebase

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	cloudauth "cloud.google.com/go/auth"
	"firebase.google.com/go/v4/auth"
	"firebase.google.com/go/v4/errorutils"
	"golang.org/x/oauth2"

	"github.com/gopernicus/gopernicus/sdk"
	sdkauth "github.com/gopernicus/gopernicus/sdk/capabilities/authentication"
)

// classify keeps an outage from ever reading as a bad credential, and names a
// rejected service credential (a rotated or revoked key, a missing permission)
// apart from a Google outage. Anything else is fallback.
func classify(ctx context.Context, err error, fallback error) error {
	if err == nil {
		return nil
	}
	if kind, ok := unavailableKind(ctx, err); ok {
		return &sdkauth.UnavailableError{Kind: kind, Err: err}
	}
	return fmt.Errorf("%w: %v", fallback, err)
}

func unavailableKind(ctx context.Context, err error) (sdkauth.UnavailableKind, bool) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errorutils.IsDeadlineExceeded(err) || ctx.Err() != nil {
		return sdkauth.UnavailableDeadline, true
	}

	// Token-endpoint failures for this process's own credential: the legacy
	// oauth2 stack (ADC) and the new auth library (service-account JSON).
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) {
		return credentialStatusKind(retrieve.Response), true
	}
	var cloud *cloudauth.Error
	if errors.As(err, &cloud) {
		return credentialStatusKind(cloud.Response), true
	}

	if errorutils.IsUnauthenticated(err) || errorutils.IsPermissionDenied(err) {
		return sdkauth.UnavailableRejectedCredential, true
	}
	if errorutils.IsUnavailable(err) || errorutils.IsInternal(err) {
		return sdkauth.UnavailableUpstream, true
	}
	if resp := errorutils.HTTPResponse(err); resp != nil && resp.StatusCode >= http.StatusInternalServerError {
		return sdkauth.UnavailableUpstream, true
	}
	if errors.Is(err, sdk.ErrUnavailable) {
		return sdkauth.UnavailableNetwork, true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return sdkauth.UnavailableNetwork, true
	}
	if auth.IsCertificateFetchFailed(err) {
		return sdkauth.UnavailableNetwork, true
	}
	return "", false
}

func credentialStatusKind(resp *http.Response) sdkauth.UnavailableKind {
	if resp != nil && resp.StatusCode >= http.StatusInternalServerError {
		return sdkauth.UnavailableUpstream
	}
	return sdkauth.UnavailableRejectedCredential
}
