package decisions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
)

const cacheFormat = "authorization-check-reads/v1"
const (
	familyDirect  = "direct"
	familyTargets = "targets"
	familyBatch   = "batch"
	familyRole    = "role"
)

type cacheQuery struct {
	family, model, resourceType, resourceID, relation, subjectType, subjectID string
	resourceIDs                                                               []string
	limit                                                                     int
}

// Each ordered field is a separately framed JSON value. The explicit array
// length preserves order, duplicates and boundaries without encoding a large
// request slice as one allocation.
func (q cacheQuery) digest() string {
	h := sha256.New()
	writeValue := func(value any) {
		encoded, _ := json.Marshal(value)
		_, _ = h.Write(encoded)
		_, _ = h.Write([]byte{'\n'})
	}
	for _, field := range []string{cacheFormat, q.family, q.model, q.resourceType, q.resourceID, q.relation, q.subjectType, q.subjectID} {
		writeValue(field)
	}
	writeValue(q.limit)
	writeValue(len(q.resourceIDs))
	for _, id := range q.resourceIDs {
		writeValue(id)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (q cacheQuery) key(version CacheVersion, digest string) string {
	return fmt.Sprintf("%s/%s/%d/%s/%s", cacheFormat, version.Epoch, version.Generation, q.family, digest)
}
func modelDigest(encoded string) string {
	sum := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(sum[:])
}

type cacheEnvelope struct {
	Format     string          `json:"format"`
	Epoch      string          `json:"epoch"`
	Generation int64           `json:"generation"`
	Family     string          `json:"family"`
	Digest     string          `json:"digest"`
	Value      json.RawMessage `json:"value"`
}

func namespaceBytes(namespace string) int {
	return len(strconv.Itoa(len(namespace))) + 1 + len(namespace) + 1
}
