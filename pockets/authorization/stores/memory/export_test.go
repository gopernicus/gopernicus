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

// SetAuditOccurredAt rewrites the occurrence time of a retained audit record so
// the shared audit expiry cases can place a record at a boundary.
func SetAuditOccurredAt(a *Audit, id string, occurredAt time.Time) bool {
	a.st.mu.Lock()
	defer a.st.mu.Unlock()
	for i, r := range a.st.auditRecords {
		if r.ID == id {
			r.OccurredAt = occurredAt
			a.st.auditRecords[i] = r
			return true
		}
	}
	return false
}
