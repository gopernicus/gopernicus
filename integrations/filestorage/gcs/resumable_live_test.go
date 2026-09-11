package gcs_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/integrations/filestorage/gcs"
	"github.com/gopernicus/gopernicus/sdk/capabilities/filestorage"
)

func TestResumableSession_GCS(t *testing.T) {
	bucket, endpoint := os.Getenv("GCS_TEST_BUCKET"), os.Getenv("GCS_TEST_ENDPOINT")
	if bucket == "" || endpoint == "" {
		t.Skip("GCS_TEST_BUCKET and GCS_TEST_ENDPOINT not set: emulator resumable session not verified")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ensureEmulatorBucket(t, ctx, bucket, endpoint)
	st, err := gcs.Open(ctx, gcs.Config{Bucket: bucket, Endpoint: endpoint, Prefix: "resumable/" + rand.Text()})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const key = "folder/upload.txt"
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = st.Delete(cleanup, key)
	}()
	session, err := st.InitiateResumableUpload(ctx, key, filestorage.ResumableUploadOptions{ContentType: "text/plain", Origin: "https://browser.example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("client PUT through the returned session")
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, session, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Origin", "https://browser.example.invalid")
	client := &http.Client{Timeout: 10 * time.Second}
	result, err := client.Do(request)
	if err != nil {
		t.Fatalf("client PUT failed (%T); session URI withheld", err)
	}
	_, copyErr := io.Copy(io.Discard, result.Body)
	closeErr := result.Body.Close()
	if copyErr != nil || closeErr != nil || result.StatusCode < 200 || result.StatusCode >= 300 {
		t.Fatalf("PUT status=%d read=%v close=%v", result.StatusCode, copyErr, closeErr)
	}
	reader, err := st.Download(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("download=%q err=%v", got, err)
	}
	size, err := st.GetObjectSize(ctx, key)
	if err != nil || size != int64(len(payload)) {
		t.Fatalf("size=%d err=%v", size, err)
	}
}
