package pgxdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OutboxEvent is the data written to the thin event_outbox table.
// Use InsertOutboxEvent inside a transaction to guarantee atomicity
// with a business write. If ID is empty, one is generated automatically.
type OutboxEvent struct {
	ID      string
	Type    string
	Payload json.RawMessage
}

// InsertOutboxEvent writes an event to the event_outbox table using the
// provided transaction. Call this inside a store's transaction (or writable
// CTE) to guarantee the outbox row is committed atomically with the
// business write. If event.ID is empty, a random ID is generated.
func InsertOutboxEvent(ctx context.Context, tx pgx.Tx, event OutboxEvent) error {
	if event.ID == "" {
		id, err := generateOutboxID()
		if err != nil {
			return fmt.Errorf("generate outbox id: %w", err)
		}
		event.ID = id
	}

	_, err := tx.Exec(ctx,
		`INSERT INTO event_outbox (event_id, event_type, payload) VALUES ($1, $2, $3)`,
		event.ID, event.Type, event.Payload,
	)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

func generateOutboxID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
