package documents

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"

	domain "github.com/gopernicus/gopernicus/examples/auth-cms/internal/logic/domains/documents"
	decisions "github.com/gopernicus/gopernicus/pockets/authorization/logic/decisions"
)

type Memory struct {
	mu   sync.RWMutex
	rows map[string]row
}

func NewMemory(documents []domain.Document) (*Memory, error) {
	m := &Memory{rows: make(map[string]row, len(documents))}
	for _, doc := range documents {
		if err := m.Put(doc); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Memory) Put(doc domain.Document) error {
	if err := doc.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[doc.ID] = row{ID: doc.ID, TenantID: doc.TenantID, Name: doc.Name, NameKey: strings.ToLower(doc.Name)}
	return nil
}

func (m *Memory) read(ctx context.Context, query domain.Query, after position, limit int, filter decisions.ResourceSet) ([]row, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	allowed := make(map[string]bool, len(filter.IDs))
	for _, id := range filter.IDs {
		allowed[id] = true
	}
	m.mu.RLock()
	rows := make([]row, 0)
	for _, r := range m.rows {
		if r.TenantID != query.TenantID || !strings.Contains(r.NameKey, query.Search) || (!filter.Unrestricted && !allowed[r.ID]) {
			continue
		}
		if after.ID != "" {
			order := comparePosition(rowPosition(r), after)
			if (!query.Desc && order <= 0) || (query.Desc && order >= 0) {
				continue
			}
		}
		rows = append(rows, r)
	}
	m.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	slices.SortFunc(rows, func(a, b row) int {
		order := comparePosition(rowPosition(a), rowPosition(b))
		if query.Desc {
			return -order
		}
		return order
	})
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return rows, more, nil
}

func comparePosition(a, b position) int {
	if order := cmp.Compare(a.NameKey, b.NameKey); order != 0 {
		return order
	}
	return cmp.Compare(a.ID, b.ID)
}
