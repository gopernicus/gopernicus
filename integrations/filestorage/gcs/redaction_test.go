package gcs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/api/option"

	"github.com/gopernicus/gopernicus/integrations/filestorage/gcs"
	"github.com/gopernicus/gopernicus/sdk"
)

// TestStoreAndConfigNeverPrintTheKey proves the service-account key stays out
// of every fmt verb and both slog handlers: Store holds the private key in an
// unexported field, which fmt reads without calling any method, so Store
// formats and logs itself.
func TestStoreAndConfigNeverPrintTheKey(t *testing.T) {
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	saJSON := generateServiceAccountJSON(t)
	var sa struct {
		PrivateKey string `json:"private_key"`
	}
	if err := json.Unmarshal([]byte(saJSON), &sa); err != nil {
		t.Fatal(err)
	}
	body := strings.Split(sa.PrivateKey, "\n")[1]
	noNetwork := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected network")
	})}
	cfg := gcs.Config{Bucket: "test-bucket", Prefix: "dir/", CredentialsJSON: sdk.Secret(saJSON)}
	store, err := gcs.Open(context.Background(), cfg,
		gcs.WithClientOption(option.WithoutAuthentication(), option.WithHTTPClient(noNetwork)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var out bytes.Buffer
	for _, v := range []any{store, cfg} {
		fmt.Fprintf(&out, "%v %+v %#v %s\n", v, v, v, v)
	}
	slog.New(slog.NewTextHandler(&out, nil)).Info("text", "store", store, "config", cfg)
	slog.New(slog.NewJSONHandler(&out, nil)).Info("json", "store", store, "config", cfg)

	for _, leak := range []string{body, "PRIVATE KEY", "private_key"} {
		if strings.Contains(out.String(), leak) {
			t.Fatalf("output carries %q:\n%s", leak, out.String())
		}
	}
	for _, want := range []string{sdk.Redacted, "test-bucket", `"store":{"bucket":"test-bucket"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
