package authorization

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopernicus/gopernicus/pockets"
	authorizationhttp "github.com/gopernicus/gopernicus/pockets/authorization/inbound/http"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
	"github.com/gopernicus/gopernicus/sdk"
)

func TestRootDecisionLoggerUsedByRealGuardRequest(t *testing.T) {
	for _, useDefault := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured", true: "captured default"}[useDefault], func(t *testing.T) {
			var configured, mounted bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&configured, &slog.HandlerOptions{Level: slog.LevelDebug})).With("host", "configured")
			option := WithLogger(logger)
			if useDefault {
				prior := slog.Default()
				slog.SetDefault(logger)
				t.Cleanup(func() { slog.SetDefault(prior) })
				option = WithLogger(nil)
			}
			store := memory.New()
			if err := store.Tuples().ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}}}); err != nil {
				t.Fatal(err)
			}
			c, err := New(Repositories{Tuples: store.Tuples()}, option)
			if err != nil {
				t.Fatal(err)
			}
			mountLogger := slog.New(slog.NewJSONHandler(&mounted, &slog.HandlerOptions{Level: slog.LevelDebug}))
			if useDefault {
				slog.SetDefault(mountLogger)
			}
			if err := c.Register(pockets.Mount{Logger: mountLogger}); err != nil {
				t.Fatal(err)
			}
			configured.Reset()
			gate := c.HTTP.Require(authorizationhttp.HasRole("admin", authorizationhttp.Global()))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r = r.WithContext(sdk.WithPrincipal(r.Context(), sdk.Principal{Type: "user", ID: "alice"}))
				gate.ServeHTTP(w, r)
			}))
			t.Cleanup(server.Close)
			response, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			server.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("HTTP status=%d", response.StatusCode)
			}
			lines := bytes.Split(bytes.TrimSpace(configured.Bytes()), []byte("\n"))
			if len(lines) != 1 {
				t.Fatalf("decision logs=%s", configured.String())
			}
			var entry map[string]any
			if err := json.Unmarshal(lines[0], &entry); err != nil {
				t.Fatal(err)
			}
			if entry["operation"] != "EvaluateResolved" || entry["outcome"] != "allowed" || entry["host"] != "configured" || entry["level"] != "DEBUG" {
				t.Fatalf("configured logger lost=%+v", entry)
			}
			if mounted.Len() != 0 {
				t.Fatalf("mount/default replaced captured logger: %s", mounted.String())
			}
		})
	}
}
