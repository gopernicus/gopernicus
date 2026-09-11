package gcs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/auth"
	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

func TestResumableUsesConfiguredAuthenticatedClientAndOrigin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const session = "https://upload.invalid/session?upload_id=secret-session"
	calls := 0
	st := testStore(t, Config{Prefix: "tenant", Endpoint: "https://storage.invalid/storage/v1/"}, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Host != "storage.invalid" || r.URL.Path != "/upload/storage/v1/b/test-bucket/o" {
			t.Errorf("target=%s %s", r.Method, r.URL)
		}
		if r.URL.Query().Get("uploadType") != "resumable" || r.URL.Query().Get("name") != "tenant/文件/space name" {
			t.Errorf("query=%v", r.URL.Query())
		}
		if r.Header.Get("Origin") != "https://app.example.invalid" || r.Header.Get("X-Upload-Content-Type") != "video/mp4" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("headers=%v", r.Header)
		}
		deadline, _ := r.Context().Deadline()
		want, _ := ctx.Deadline()
		if deadline != want {
			t.Error("deadline changed")
		}
		var metadata struct {
			Name        string `json:"name"`
			ContentType string `json:"contentType"`
		}
		if err := json.NewDecoder(r.Body).Decode(&metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.Name != "tenant/文件/space name" || metadata.ContentType != "video/mp4" {
			t.Errorf("metadata=%+v", metadata)
		}
		res := response(r, 200, "")
		res.Header.Set("Location", session)
		return res, nil
	})
	// No signing identity/private key is configured: initiation uses the retained
	// HTTP client directly, whose host-provided transport owns authentication.
	got, err := st.InitiateResumableUpload(ctx, "文件/space name", filestorage.ResumableUploadOptions{ContentType: "video/mp4", Origin: "https://app.example.invalid"})
	if err != nil || got != session || calls != 1 {
		t.Fatalf("session=%q calls=%d err=%v", got, calls, err)
	}
}

func TestResumableCredentialRefreshUsesCallerContext(t *testing.T) {
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	called := false
	creds := auth.NewCredentials(&auth.CredentialsOptions{TokenProvider: tokenProviderFunc(func(tokenCtx context.Context) (*auth.Token, error) {
		called = true
		want, _ := ctx.Deadline()
		got, _ := tokenCtx.Deadline()
		if got != want {
			t.Error("credential refresh lost upload deadline")
		}
		cancel()
		return nil, tokenCtx.Err()
	})})
	st, err := Open(context.Background(), Config{Bucket: "bucket"}, WithClientOption(option.WithAuthCredentials(creds)))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	session, err := st.InitiateResumableUpload(ctx, "key", filestorage.ResumableUploadOptions{})
	if !called || session != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("called=%v session=%q err=%v", called, session, err)
	}
}

func TestResumableErrorsDoNotPrintSessionMaterial(t *testing.T) {
	const secret = "secret-session"
	transportErr := errors.New("connection failed")
	for _, tc := range []struct {
		name     string
		status   int
		location string
		failure  error
	}{
		{name: "provider", status: 403},
		{name: "missing location", status: 200},
		{name: "bad location", status: 200, location: "https://%" + secret},
		{name: "relative location", status: 200, location: "/session?token=" + secret},
		{name: "transport", failure: &url.Error{Op: "POST", URL: "https://upload.invalid/?token=" + secret, Err: transportErr}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := testStore(t, Config{}, func(r *http.Request) (*http.Response, error) {
				if tc.failure != nil {
					return nil, tc.failure
				}
				res := response(r, tc.status, secret)
				res.Header.Set("Location", tc.location)
				return res, nil
			})
			session, err := st.InitiateResumableUpload(context.Background(), "key", filestorage.ResumableUploadOptions{})
			if err == nil || session != "" {
				t.Fatalf("session=%q err=%v", session, err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error printed session material: %v", err)
			}
			if tc.status == http.StatusForbidden {
				var provider *googleapi.Error
				if !errors.As(err, &provider) || provider.Code != tc.status {
					t.Errorf("provider status lost: %v", err)
				}
			}
			if tc.failure != nil && !errors.Is(err, transportErr) {
				t.Errorf("transport cause lost: %v", err)
			}
		})
	}
}

func TestResumableRejectsHeadersAndCanceledSuccess(t *testing.T) {
	st := &Store{}
	for _, options := range []filestorage.ResumableUploadOptions{{ContentType: "text/plain\r\nother: bad"}, {Origin: "https://app.invalid\n"}} {
		if _, err := st.InitiateResumableUpload(context.Background(), "key", options); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("headers: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st = testStore(t, Config{}, func(r *http.Request) (*http.Response, error) {
		cancel()
		res := response(r, 200, "")
		res.Header.Set("Location", "https://upload.invalid/session")
		return res, nil
	})
	if session, err := st.InitiateResumableUpload(ctx, "key", filestorage.ResumableUploadOptions{}); session != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("session=%q err=%v", session, err)
	}
}
