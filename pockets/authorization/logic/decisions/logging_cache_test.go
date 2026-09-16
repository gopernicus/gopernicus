package decisions_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
	authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuplecache"
	"github.com/gopernicus/gopernicus/pockets/authorization/logic/tuples"
	"github.com/gopernicus/gopernicus/pockets/authorization/stores/memory"
)

func TestDecisionLoggingCacheFallbackRecordsOnlyFinalOutcome(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "revoked", true: "durable completion failed"}[failure], func(t *testing.T) {
			store := memory.NewTuples()
			grant := tuples.Tuple{Scope: tuples.Global(), Relation: "admin", Subject: tuples.SubjectRef{Type: "user", ID: "alice"}}
			if err := store.ApplyTuples(t.Context(), tuples.Changes{Add: []tuples.Tuple{grant}}); err != nil {
				t.Fatal(err)
			}
			source := &testTupleSource{binding: "store", facts: []tuples.Tuple{grant}, durable: store.ReadTupleSnapshot}
			backend := &filterValidationBackend{Backend: memory.NewTupleCache()}
			var logs bytes.Buffer
			s, err := decisions.NewService(&boundTuples{Tuples: store, binding: "store"}, decisions.WithLogger(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))), decisions.WithTupleCache(backend, source, tuplecache.Policy{MaxStaleness: time.Minute}))
			if err != nil {
				t.Fatal(err)
			}
			cache := s.TupleCache()
			t.Cleanup(func() { _ = cache.Close() })
			if err := cache.Poll(t.Context()); err != nil {
				t.Fatal(err)
			}
			principal := authmodel.PrincipalRef{Type: "user", ID: "alice"}
			warm, err := s.Evaluate(t.Context(), principal, decisions.Role("admin"))
			if err != nil || !warm.Allowed || source.reads != 0 {
				t.Fatalf("warm=%+v/%v reads=%d", warm, err, source.reads)
			}
			logs.Reset()
			backend.fail = true
			if failure {
				source.durableErr = errors.New("private database failed")
			} else if err := store.ApplyTuples(t.Context(), tuples.Changes{Remove: []tuples.Tuple{grant}}); err != nil {
				t.Fatal(err)
			}
			result, err := s.Evaluate(t.Context(), principal, decisions.Role("admin"))
			if result.Allowed || (err != nil) != failure || source.reads != 1 {
				t.Fatalf("fallback=%+v/%v reads=%d", result, err, source.reads)
			}
			lines := bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n"))
			if len(lines) != 1 {
				t.Fatalf("fallback logs=%s", logs.String())
			}
			var entry map[string]any
			if err := json.Unmarshal(lines[0], &entry); err != nil {
				t.Fatal(err)
			}
			want := "denied"
			if failure {
				want = "error"
			}
			if entry["operation"] != "Evaluate" || entry["outcome"] != want || entry["bound"] != false {
				t.Fatalf("final log=%+v", entry)
			}
			if bytes.Contains(logs.Bytes(), []byte("private database")) {
				t.Fatal("raw infrastructure error logged")
			}
		})
	}
}
