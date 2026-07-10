package turso

import (
	"context"
	"testing"
)

// StatusCheck must run a real statement: the remote libSQL driver's Ping is
// lazy (nil without a round-trip), so Ping alone can never report a down DB.
func TestStatusCheck(t *testing.T) {
	db := newMemDB(t)
	if err := StatusCheck(context.Background(), db); err != nil {
		t.Fatalf("StatusCheck on a live DB: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := StatusCheck(context.Background(), db); err == nil {
		t.Fatal("StatusCheck on a closed DB returned nil; want error (the SELECT 1 round-trip must surface it)")
	}
}
