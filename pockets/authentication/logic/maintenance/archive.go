package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/gopernicus/gopernicus/pockets/authentication/logic/authentication/securityevent"
)

// archiveFormatVersion is the "v" every archive line carries. A change to the
// line's keys or their meaning is a new version.
const archiveFormatVersion = 1

// archiveTimeLayout renders every archive timestamp: UTC, microseconds, no
// zone offset other than Z.
const archiveTimeLayout = "2006-01-02T15:04:05.000000Z"

// archiveKeyTimeLayout renders the cutoff segment of an object key. It carries
// no colon: several object stores and filesystems refuse one in a key.
const archiveKeyTimeLayout = "20060102T150405.000000Z"

// ErrArchiveFailed wraps every failure between exporting a chunk and recording
// its archive evidence: the object key, the upload, the read-back and the
// security_events_archived row. A chunk whose archive failed is never purged.
var ErrArchiveFailed = errors.New("maintenance: archive failed")

// archiveRecord is version 1 of one NDJSON archive line. The keys are the rail's
// column names, defined here rather than by Go field names, so the archive
// schema does not move when a domain type does.
type archiveRecord struct {
	V           int            `json:"v"`
	ID          string         `json:"id"`
	Seq         *int64         `json:"seq"`
	RecordedAt  *string        `json:"recorded_at"`
	UserID      string         `json:"user_id"`
	ActorType   string         `json:"actor_type"`
	ActorID     string         `json:"actor_id"`
	EventType   string         `json:"event_type"`
	EventStatus string         `json:"event_status"`
	Details     map[string]any `json:"details"`
	IPAddress   string         `json:"ip_address"`
	UserAgent   string         `json:"user_agent"`
	RequestID   string         `json:"request_id"`
	TraceID     string         `json:"trace_id"`
	CreatedAt   string         `json:"created_at"`
}

// encodedArchive is one object's bytes and the facts the read-back must match.
type encodedArchive struct {
	body   []byte
	digest string
	lines  int
}

// encodeArchive renders rows as NDJSON, one archiveRecord per line.
func encodeArchive(rows []ArchivedEvent) (encodedArchive, error) {
	var buf bytes.Buffer
	for _, row := range rows {
		rec := archiveRecord{
			V:           archiveFormatVersion,
			ID:          row.ID,
			Seq:         row.Seq,
			UserID:      row.UserID,
			ActorType:   row.Actor.Type,
			ActorID:     row.Actor.ID,
			EventType:   row.EventType,
			EventStatus: row.EventStatus,
			Details:     row.Details,
			IPAddress:   row.IPAddress,
			UserAgent:   row.UserAgent,
			RequestID:   row.RequestID,
			TraceID:     row.TraceID,
			CreatedAt:   formatArchiveTime(row.CreatedAt),
		}
		if rec.Details == nil {
			rec.Details = map[string]any{}
		}
		if row.RecordedAt != nil {
			recorded := formatArchiveTime(*row.RecordedAt)
			rec.RecordedAt = &recorded
		}
		line, err := json.Marshal(rec)
		if err != nil {
			return encodedArchive{}, fmt.Errorf("encode archive line %q: %w", row.ID, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	sum := sha256.Sum256(buf.Bytes())
	return encodedArchive{body: buf.Bytes(), digest: hex.EncodeToString(sum[:]), lines: len(rows)}, nil
}

// verifyArchive reads key back and checks that its size, line count and SHA-256
// equal what was written. The sink's success proves nothing on its own — a disk
// sink does not fsync and a remote one may report an error after committing —
// so this read-back is the service's durability check.
func verifyArchive(ctx context.Context, sink ArchiveSink, key string, want encodedArchive) error {
	size, err := sink.GetObjectSize(ctx, key)
	if err != nil {
		return fmt.Errorf("read back size of %s: %w", key, err)
	}
	if size != int64(len(want.body)) {
		return fmt.Errorf("read back %s: size %d, wrote %d", key, size, len(want.body))
	}
	rc, err := sink.Download(ctx, key)
	if err != nil {
		return fmt.Errorf("read back %s: %w", key, err)
	}
	defer rc.Close()
	h := sha256.New()
	counter := &lineCounter{}
	n, err := io.Copy(io.MultiWriter(h, counter), rc)
	if err != nil {
		return fmt.Errorf("read back %s: %w", key, err)
	}
	if n != int64(len(want.body)) || counter.lines != want.lines {
		return fmt.Errorf("read back %s: %d bytes in %d lines, wrote %d bytes in %d lines", key, n, counter.lines, len(want.body), want.lines)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want.digest {
		return fmt.Errorf("read back %s: sha256 %s, wrote %s", key, got, want.digest)
	}
	return nil
}

// lineCounter counts newline bytes written through it.
type lineCounter struct{ lines int }

func (c *lineCounter) Write(p []byte) (int, error) {
	c.lines += bytes.Count(p, []byte{'\n'})
	return len(p), nil
}

func formatArchiveTime(t time.Time) string {
	return t.UTC().Format(archiveTimeLayout)
}

// archivedDetails is the Details of the security_events_archived row for one
// verified object.
func archivedDetails(key, location string, enc encodedArchive, rows []ArchivedEvent) map[string]any {
	first, last := rows[0].SecurityEvent, rows[len(rows)-1].SecurityEvent
	return map[string]any{
		"object":           key,
		"sha256":           enc.digest,
		"rows":             enc.lines,
		"first_created_at": formatArchiveTime(first.CreatedAt),
		"first_id":         first.ID,
		"last_created_at":  formatArchiveTime(last.CreatedAt),
		"last_id":          last.ID,
		"archive_location": location,
	}
}

// archivedEvent builds the security_events_archived row.
func archivedEvent(id string, now time.Time, details map[string]any) securityevent.SecurityEvent {
	evt := securityevent.SecurityEvent{
		ID:          id,
		Actor:       systemActor,
		EventType:   securityevent.TypeSecurityEventsArchived,
		EventStatus: securityevent.StatusSuccess,
		Details:     details,
		CreatedAt:   now.UTC(),
	}
	return evt
}
