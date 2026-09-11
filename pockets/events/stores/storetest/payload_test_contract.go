package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gopernicus/gopernicus/pockets/events/logic/outbox"
	"github.com/gopernicus/gopernicus/sdk"
)

func testOpaquePayloads(t *testing.T, repo outbox.EntryRepository) {
	payloads := [][]byte{nil, {}, {0, 255, 128, 'x'}, []byte("not JSON"), []byte(" { \"n\": 1 }\n")}
	for i, payload := range payloads {
		record := rec(fmt.Sprintf("bytes-%d", i))
		record.Payload = payload
		mustAppend(t, repo, record)
	}
	got := entriesByID(t, repo)
	for i, payload := range payloads {
		entry, ok := got[fmt.Sprintf("bytes-%d", i)]
		if !ok || !bytes.Equal(entry.Payload, payload) {
			t.Errorf("payload %d = %x, want %x", i, entry.Payload, payload)
		}
	}
}

func testInvalidBatchIsAtomic(t *testing.T, repo outbox.EntryRepository) {
	for _, invalid := range []struct{ id, kind string }{{"", "test.invalid"}, {"bad-id", ""}, {"bad-type", "*"}} {
		record := rec(invalid.id)
		record.Type = invalid.kind
		if err := repo.Append(context.Background(), rec("valid-first"), record); !errors.Is(err, sdk.ErrInvalidInput) {
			t.Fatalf("Append invalid batch = %v", err)
		}
		if got := mustList(t, repo, 10); len(got) != 0 {
			t.Fatalf("rejected batch persisted %v", entryIDs(got))
		}
	}
}

func testRecordOwnership(t *testing.T, repo outbox.EntryRepository) {
	record := rec("snapshot")
	tenant := "tenant"
	record.TenantID = &tenant
	original := bytes.Clone(record.Payload)
	mustAppend(t, repo, record)
	record.Payload[0] = '!'
	tenant = "changed"
	got := mustList(t, repo, 1)
	if len(got) != 1 || !bytes.Equal(got[0].Payload, original) || got[0].TenantID == nil || *got[0].TenantID != "tenant" {
		t.Fatalf("input mutation changed record: %+v", got)
	}
	got[0].Payload[0] = '?'
	*got[0].TenantID = "changed again"
	got = mustList(t, repo, 1)
	if len(got) != 1 || !bytes.Equal(got[0].Payload, original) || got[0].TenantID == nil || *got[0].TenantID != "tenant" {
		t.Fatalf("output mutation changed record: %+v", got)
	}
}
