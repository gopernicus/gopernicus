package decisions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

func TestCacheCanonicalKeysPreserveEveryArgument(t *testing.T) {
	base := cacheQuery{family: familyBatch, model: "model", resourceType: "doc", resourceID: "one", resourceIDs: []string{"a", "b", "a"}, relation: "viewer", subjectType: "user", subjectID: "u1", limit: 12}
	seen := map[string]bool{base.digest(): true}
	for _, change := range []func(*cacheQuery){
		func(q *cacheQuery) { q.family = familyDirect }, func(q *cacheQuery) { q.model = "different" }, func(q *cacheQuery) { q.resourceType = "folder" }, func(q *cacheQuery) { q.resourceID = "two" }, func(q *cacheQuery) { q.resourceIDs = []string{"a", "a", "b"} }, func(q *cacheQuery) { q.resourceIDs = []string{"a", "b"} }, func(q *cacheQuery) { q.relation = "owner" }, func(q *cacheQuery) { q.subjectType = "group" }, func(q *cacheQuery) { q.subjectID = "u2" }, func(q *cacheQuery) { q.limit = 13 },
	} {
		q := base
		change(&q)
		digest := q.digest()
		if seen[digest] {
			t.Fatalf("query identity collided: %+v", q)
		}
		seen[digest] = true
	}
	a := cacheQuery{family: familyRole, subjectType: "a:b", subjectID: "c"}
	b := cacheQuery{family: familyRole, subjectType: "a", subjectID: "b:c"}
	if a.digest() == b.digest() {
		t.Fatal("delimited coordinates collided")
	}
	version := CacheVersion{Epoch: "0123456789abcdef0123456789abcdef"}
	first := base.key(version, base.digest())
	version.Generation++
	if first == base.key(version, base.digest()) {
		t.Fatal("generation omitted")
	}
}
func TestCacheTypedEnvelopeRejectsCorruption(t *testing.T) {
	r, source, _ := newTestCacheRuntime(t)
	q := cacheQuery{family: familyRole, resourceType: "doc", resourceID: "d1", relation: "viewer", subjectType: "user", subjectID: "u1"}
	digest := q.digest()
	key := q.key(source.version, digest)
	correct := cacheEnvelope{Format: cacheFormat, Epoch: source.version.Epoch, Generation: 0, Family: familyRole, Digest: digest, Value: json.RawMessage("false")}
	for _, change := range []func(*cacheEnvelope){func(e *cacheEnvelope) { e.Format = "v0" }, func(e *cacheEnvelope) { e.Epoch = "other" }, func(e *cacheEnvelope) { e.Generation++ }, func(e *cacheEnvelope) { e.Family = familyDirect }, func(e *cacheEnvelope) { e.Digest = "other" }, func(e *cacheEnvelope) { e.Value = json.RawMessage("null") }, func(e *cacheEnvelope) { e.Value = json.RawMessage("[]") }} {
		e := correct
		change(&e)
		encoded, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.cache.Set(t.Context(), key, encoded, r.policy.EntryTTL); err != nil {
			t.Fatal(err)
		}
		if _, err := (&cacheReads{runtime: r, version: source.version}).HasExactRole(t.Context(), "user", "u1", "viewer", "doc", "d1"); !errors.Is(err, errCacheMiss) {
			t.Fatalf("accepted %+v: %v", e, err)
		}
	}
	encoded, _ := json.Marshal(correct)
	if err := r.cache.Set(t.Context(), key, encoded, r.policy.EntryTTL); err != nil {
		t.Fatal(err)
	}
	if held, err := (&cacheReads{runtime: r, version: source.version}).HasExactRole(t.Context(), "user", "u1", "viewer", "doc", "d1"); err != nil || held {
		t.Fatalf("false is not hit: %v/%v", held, err)
	}
}
func TestCacheStagingBoundsAndOwnedValues(t *testing.T) {
	r, source, _ := newTestCacheRuntime(t)
	r.policy.MaxFillEntries = 1
	staged := &cacheReads{runtime: r, version: source.version, durable: source}
	model := relationships.NewReadModel([]relationships.SubjectRule{{ResourceType: "doc", Relation: "viewer", SubjectType: "user"}})
	q := cacheQuery{family: familyTargets, model: modelDigest(model.JSON()), resourceType: "doc", resourceID: "d1", relation: "viewer"}
	original := []relationships.RelationTarget{{Type: "user", ID: "u1"}}
	staged.stage(q, original, 100)
	original[0].ID = "mutated"
	staged.stage(cacheQuery{family: familyRole, relation: "second"}, true, 5)
	if len(staged.staged) != 1 || !staged.full {
		t.Fatalf("staging count=%d full=%t", len(staged.staged), staged.full)
	}
	staged.publish(t.Context())
	reader := (&cacheReads{runtime: r, version: source.version}).ForChecks(model)
	got, err := reader.GetRelationTargets(t.Context(), "doc", "d1", "viewer")
	if err != nil || got[0].ID != "u1" {
		t.Fatalf("staging retained input: %v/%v", got, err)
	}
	got[0].ID = "caller mutation"
	again, err := reader.GetRelationTargets(t.Context(), "doc", "d1", "viewer")
	if err != nil || again[0].ID != "u1" {
		t.Fatalf("cache shared decoded slice: %v/%v", again, err)
	}
	oversize := &cacheReads{runtime: r, version: source.version, durable: source}
	oversize.stage(q, strings.Repeat("x", r.policy.MaxEntryBytes), r.policy.MaxEntryBytes)
	if len(oversize.staged) != 0 {
		t.Fatal("oversized staging accepted")
	}
}
func TestCacheBatchRejectsIncompleteAndNullValues(t *testing.T) {
	r, source, _ := newTestCacheRuntime(t)
	model := relationships.ReadModel{}
	q := cacheQuery{family: familyBatch, model: modelDigest(model.JSON()), resourceType: "doc", resourceIDs: []string{"d1", "d2", "d1"}, relation: "viewer", subjectType: "user", subjectID: "u1", limit: 100}
	digest := q.digest()
	key := q.key(source.version, digest)
	for _, raw := range []string{`{"d1":true}`, `{"d1":true,"d2":null}`, `{"d1":true,"d2":false,"extra":true}`} {
		encoded, _ := json.Marshal(cacheEnvelope{Format: cacheFormat, Epoch: source.version.Epoch, Family: familyBatch, Digest: digest, Value: json.RawMessage(raw)})
		if err := r.cache.Set(t.Context(), key, encoded, r.policy.EntryTTL); err != nil {
			t.Fatal(err)
		}
		reader := (&cacheReads{runtime: r, version: source.version}).ForChecks(model)
		if _, err := reader.CheckBatchDirect(context.Background(), "doc", q.resourceIDs, "viewer", "user", "u1", 100); !errors.Is(err, errCacheMiss) {
			t.Fatalf("accepted %s: %v", raw, err)
		}
	}
	staged := &cacheReads{runtime: r, version: source.version, durable: source}
	want := map[string]bool{"d1": true, "d2": false}
	staged.stage(q, want, 60)
	staged.publish(t.Context())
	got, err := (&cacheReads{runtime: r, version: source.version}).ForChecks(model).CheckBatchDirect(t.Context(), "doc", q.resourceIDs, "viewer", "user", "u1", 100)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("complete batch=%v/%v", got, err)
	}
}
