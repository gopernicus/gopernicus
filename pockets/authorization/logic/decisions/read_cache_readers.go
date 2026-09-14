package decisions

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/gopernicus/gopernicus/pockets/authorization/logic/relationships"
)

type stagedEntry struct {
	key   string
	value []byte
}
type cacheReads struct {
	runtime     *CacheRuntime
	version     CacheVersion
	durable     CheckReads
	staged      []stagedEntry
	stagedBytes int
	stagedKeys  map[string]bool
	full        bool
}
type cacheCheckReader struct {
	reads   *cacheReads
	model   relationships.ReadModel
	digest  string
	durable relationships.CheckReader
}

func (s *cacheReads) ForChecks(model relationships.ReadModel) relationships.CheckReader {
	reader := &cacheCheckReader{reads: s, model: model, digest: modelDigest(model.JSON())}
	if s.durable != nil {
		reader.durable = s.durable.ForChecks(model)
	}
	return reader
}
func (s *cacheReads) HasExactRole(ctx context.Context, st, sid, role, rt, rid string) (bool, error) {
	q := cacheQuery{family: familyRole, resourceType: rt, resourceID: rid, relation: role, subjectType: st, subjectID: sid}
	if s.durable == nil {
		return readCached[bool](ctx, s, q, validBool)
	}
	value, err := s.durable.HasExactRole(ctx, st, sid, role, rt, rid)
	if err == nil && ctx.Err() == nil {
		s.stage(q, value, 5)
	}
	return value, err
}
func (s *cacheCheckReader) CheckRelationWithGroupExpansion(ctx context.Context, rt, rid, rel, st, sid string, limit int) (bool, error) {
	q := cacheQuery{family: familyDirect, model: s.digest, resourceType: rt, resourceID: rid, relation: rel, subjectType: st, subjectID: sid, limit: limit}
	if s.reads.durable == nil {
		return readCached[bool](ctx, s.reads, q, validBool)
	}
	if s.durable == nil || typedNil(s.durable) {
		return false, ErrCacheVersion
	}
	value, err := s.durable.CheckRelationWithGroupExpansion(ctx, rt, rid, rel, st, sid, limit)
	if err == nil && ctx.Err() == nil {
		s.reads.stage(q, value, 5)
	}
	return value, err
}
func (s *cacheCheckReader) GetRelationTargets(ctx context.Context, rt, rid, rel string) ([]relationships.RelationTarget, error) {
	q := cacheQuery{family: familyTargets, model: s.digest, resourceType: rt, resourceID: rid, relation: rel}
	validate := func(_ []byte, values []relationships.RelationTarget) bool {
		for _, value := range values {
			if value.Validate() != nil || !s.model.Allows(rt, rel, value.Type, value.Relation) {
				return false
			}
		}
		return true
	}
	if s.reads.durable == nil {
		return readCached[[]relationships.RelationTarget](ctx, s.reads, q, validate)
	}
	if s.durable == nil || typedNil(s.durable) {
		return nil, ErrCacheVersion
	}
	values, err := s.durable.GetRelationTargets(ctx, rt, rid, rel)
	if err == nil && ctx.Err() == nil && validate(nil, values) {
		size := 2
		for _, value := range values {
			if !addEstimate(&size, 64, s.reads.runtime.policy.MaxEntryBytes) || !addStringEstimate(&size, value.Type, s.reads.runtime.policy.MaxEntryBytes) || !addStringEstimate(&size, value.ID, s.reads.runtime.policy.MaxEntryBytes) || !addStringEstimate(&size, value.Relation, s.reads.runtime.policy.MaxEntryBytes) {
				size = s.reads.runtime.policy.MaxEntryBytes
				break
			}
		}
		s.reads.stage(q, values, size)
	}
	return values, err
}
func (s *cacheCheckReader) CheckBatchDirect(ctx context.Context, rt string, ids []string, rel, st, sid string, limit int) (map[string]bool, error) {
	q := cacheQuery{family: familyBatch, model: s.digest, resourceType: rt, resourceIDs: ids, relation: rel, subjectType: st, subjectID: sid, limit: limit}
	validate := func(raw []byte, values map[string]bool) bool {
		if raw != nil {
			var entries map[string]json.RawMessage
			if json.Unmarshal(raw, &entries) != nil {
				return false
			}
			for _, value := range entries {
				if !validBool(value, false) {
					return false
				}
			}
		}
		wanted := make(map[string]bool, len(ids))
		for _, id := range ids {
			wanted[id] = true
		}
		if len(values) != len(wanted) {
			return false
		}
		for id := range wanted {
			if _, ok := values[id]; !ok {
				return false
			}
		}
		return true
	}
	if s.reads.durable == nil {
		return readCached[map[string]bool](ctx, s.reads, q, validate)
	}
	if s.durable == nil || typedNil(s.durable) {
		return nil, ErrCacheVersion
	}
	values, err := s.durable.CheckBatchDirect(ctx, rt, ids, rel, st, sid, limit)
	if err == nil && ctx.Err() == nil && validate(nil, values) {
		size := 2
		for id := range values {
			if !addEstimate(&size, 10, s.reads.runtime.policy.MaxEntryBytes) || !addStringEstimate(&size, id, s.reads.runtime.policy.MaxEntryBytes) {
				size = s.reads.runtime.policy.MaxEntryBytes
				break
			}
		}
		s.reads.stage(q, values, size)
	}
	return values, err
}
func validBool(raw []byte, _ bool) bool {
	return bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false"))
}
func readCached[T any](ctx context.Context, s *cacheReads, q cacheQuery, validate func([]byte, T) bool) (T, error) {
	var zero T
	digest := q.digest()
	key := q.key(s.version, digest)
	raw, found, err := s.runtime.cache.Get(ctx, key)
	if err != nil {
		s.runtime.count(func(stats *CacheStats) { stats.CacheErrors++ })
		return zero, errCacheMiss
	}
	if !found {
		return zero, errCacheMiss
	}
	invalid := func() (T, error) {
		s.runtime.count(func(stats *CacheStats) { stats.CacheErrors++ })
		return zero, errCacheMiss
	}
	if len(raw) > s.runtime.policy.MaxEntryBytes {
		return invalid()
	}
	var envelope cacheEnvelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.Format != cacheFormat || envelope.Epoch != s.version.Epoch || envelope.Generation != s.version.Generation || envelope.Family != q.family || envelope.Digest != digest || len(envelope.Value) == 0 {
		return invalid()
	}
	var value T
	if json.Unmarshal(envelope.Value, &value) != nil || !validate(envelope.Value, value) {
		return invalid()
	}
	if err := ctx.Err(); err != nil {
		return zero, errCacheMiss
	}
	return value, nil
}
func addEstimate(size *int, n, limit int) bool {
	if n < 0 || *size > limit || n > limit-*size {
		return false
	}
	*size += n
	return true
}
func addStringEstimate(size *int, value string, limit int) bool {
	if len(value) > limit/6 {
		return false
	}
	return addEstimate(size, 6*len(value), limit)
}
func (s *cacheReads) stage(q cacheQuery, value any, estimate int) {
	if s.full {
		return
	}
	policy := s.runtime.policy
	skip := func() { s.runtime.count(func(stats *CacheStats) { stats.SkippedFills++ }) }
	if len(s.staged) >= policy.MaxFillEntries {
		s.full = true
		skip()
		return
	}
	// Worst-case string escaping is bounded before JSON allocation. Envelopes
	// are staged as owned bytes; publication still waits for successful closure.
	if !addEstimate(&estimate, 300, policy.MaxEntryBytes) {
		skip()
		return
	}
	digest := q.digest()
	key := q.key(s.version, digest)
	if s.stagedKeys[key] {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		skip()
		return
	}
	envelope, err := json.Marshal(cacheEnvelope{Format: cacheFormat, Epoch: s.version.Epoch, Generation: s.version.Generation, Family: q.family, Digest: digest, Value: encoded})
	if err != nil || len(envelope) > policy.MaxEntryBytes {
		skip()
		return
	}
	size := len(envelope) + len(key) + namespaceBytes(policy.Namespace)
	if size > policy.MaxFillBytes-s.stagedBytes {
		s.full = true
		skip()
		return
	}
	if s.stagedKeys == nil {
		s.stagedKeys = make(map[string]bool)
	}
	s.stagedKeys[key] = true
	s.stagedBytes += size
	s.staged = append(s.staged, stagedEntry{key: key, value: envelope})
}
func (s *cacheReads) publish(ctx context.Context) {
	if len(s.staged) == 0 {
		return
	}
	fillCtx, cancel := context.WithTimeout(ctx, s.runtime.policy.CacheTimeout)
	defer cancel()
	for _, entry := range s.staged {
		select {
		case <-s.runtime.done:
			return
		default:
		}
		if err := fillCtx.Err(); err != nil {
			return
		}
		if err := s.runtime.cache.Set(fillCtx, entry.key, entry.value, s.runtime.policy.EntryTTL); err != nil {
			s.runtime.count(func(stats *CacheStats) { stats.CacheErrors++ })
			return
		}
	}
}
