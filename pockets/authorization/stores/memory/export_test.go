package memory

import "time"

// SetOperationCommittedAt rewrites the commit time of a recorded operation so
// the shared expiry cases can place a record exactly at a boundary.
func SetOperationCommittedAt(m *Mutations, operationID string, committedAt time.Time) bool {
	m.st.mu.Lock()
	defer m.st.mu.Unlock()
	record, ok := m.st.operations[operationID]
	if !ok {
		return false
	}
	record.committedAt = committedAt
	m.st.operations[operationID] = record
	return true
}
