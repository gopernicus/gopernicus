package gcs

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gopernicus/gopernicus/sdk"
	"google.golang.org/api/option"
)

func TestClientOptionsSnapshotAndAppend(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("construction performed unexpected network request")
		return nil, nil
	})}
	vendorOpts := []option.ClientOption{option.WithEndpoint("https://snapshot.invalid/"), option.WithoutAuthentication(), option.WithHTTPClient(client)}
	opt := WithClientOption(vendorOpts...)
	vendorOpts[0] = option.WithEndpoint("https://changed.invalid/")
	for range 2 {
		store, err := Open(context.Background(), Config{Bucket: "bucket", Endpoint: "https://config.invalid/"}, WithClientOption(option.WithEndpoint("https://earlier.invalid/")), opt)
		if err != nil {
			t.Fatal(err)
		}
		if store.endpoint != "https://snapshot.invalid/" || store.httpClient != client {
			t.Fatalf("resolved endpoint = %q, client = %p", store.endpoint, store.httpClient)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsNilOptionBeforeCredentials(t *testing.T) {
	store, err := Open(context.Background(), Config{Bucket: "bucket", CredentialsJSON: "deliberately invalid"}, nil)
	if store != nil || !errors.Is(err, sdk.ErrInvalidInput) {
		t.Fatalf("Open(nil) = %v, %v", store, err)
	}
}
