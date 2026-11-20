package goredis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus/sdk"
	"github.com/gopernicus/gopernicus/sdk/capabilities/events"
	"github.com/redis/go-redis/v9"
)

const deadLetterInfix = "dlq:"

var (
	// deadLetterScript moves one entry to the dead-letter stream only while
	// this consumer still owns its pending delivery, so competing consumers
	// dead-letter it at most once. XDEL runs only when this is the stream's
	// sole group: XDEL removes the entry for every group.
	// KEYS: stream, dlq. ARGV: group, id, consumer, raw record, error.
	deadLetterScript = redis.NewScript(`
local owned = redis.call('XPENDING', KEYS[1], ARGV[1], ARGV[2], ARGV[2], 1, ARGV[3])
if #owned == 0 then return false end
local deliveries = owned[1][4]
local now = redis.call('TIME')
local failedAt = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local id = redis.call('XADD', KEYS[2], '*', 'record', ARGV[4], 'source_id', ARGV[2],
  'error', ARGV[5], 'deliveries', deliveries, 'consumer', ARGV[3], 'group', ARGV[1],
  'failed_at', failedAt)
redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
if #redis.call('XINFO', 'GROUPS', KEYS[1]) == 1 then
  redis.call('XDEL', KEYS[1], ARGV[2])
end
return {id, deliveries, failedAt}
`)

	// redeliverScript moves a dead letter back onto its stream as a new entry.
	// KEYS: dlq, stream. ARGV: dead-letter id.
	redeliverScript = redis.NewScript(`
local rows = redis.call('XRANGE', KEYS[1], ARGV[1], ARGV[1])
if #rows == 0 then return 0 end
local fields = rows[1][2]
for i = 1, #fields, 2 do
  if fields[i] == 'record' then
    redis.call('XADD', KEYS[2], '*', 'record', fields[i + 1])
    redis.call('XDEL', KEYS[1], ARGV[1])
    return 1
  end
end
return redis.error_reply('dead letter has no record field')
`)

	// backlogScript snapshots one group's backlog. It returns pending, the
	// oldest unacknowledged id ('' if none), Redis time in ms, and the
	// dead-letter count. KEYS: stream, dlq. ARGV: group.
	backlogScript = redis.NewScript(`
local dl = redis.call('XLEN', KEYS[2])
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local groups = redis.pcall('XINFO', 'GROUPS', KEYS[1])
if groups.err then return {0, '', now, dl} end
for _, g in ipairs(groups) do
  local name, pending, last
  for i = 1, #g, 2 do
    if g[i] == 'name' then name = g[i + 1]
    elseif g[i] == 'pending' then pending = g[i + 1]
    elseif g[i] == 'last-delivered-id' then last = g[i + 1] end
  end
  if name == ARGV[1] then
    local oldest = ''
    if pending > 0 then
      oldest = redis.call('XPENDING', KEYS[1], name)[2]
    else
      local rows = redis.call('XRANGE', KEYS[1], '(' .. last, '+', 'COUNT', 1)
      if #rows > 0 then oldest = rows[1][1] end
    end
    return {pending, oldest, now, dl}
  end
end
return {0, '', now, dl}
`)

	// trimScript trims entries older than ARGV[1] ms that every group has
	// acknowledged: the floor is each group's oldest pending id, or the entry
	// after its last-delivered id. With no group, nothing is trimmed.
	// KEYS: stream. ARGV: olderThan ms.
	trimScript = redis.NewScript(`
local groups = redis.pcall('XINFO', 'GROUPS', KEYS[1])
if groups.err or #groups == 0 then return 0 end
local function parse(id)
  local dash = string.find(id, '-', 1, true)
  return tonumber(string.sub(id, 1, dash - 1)), tonumber(string.sub(id, dash + 1))
end
local function less(ams, aseq, bms, bseq)
  return ams < bms or (ams == bms and aseq < bseq)
end
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local fms, fseq = now - tonumber(ARGV[1]) + 1, 0
for _, g in ipairs(groups) do
  local name, pending, last
  for i = 1, #g, 2 do
    if g[i] == 'name' then name = g[i + 1]
    elseif g[i] == 'pending' then pending = g[i + 1]
    elseif g[i] == 'last-delivered-id' then last = g[i + 1] end
  end
  local ms, seq
  if pending > 0 then
    ms, seq = parse(redis.call('XPENDING', KEYS[1], name)[2])
  else
    ms, seq = parse(last)
    seq = seq + 1
  end
  if less(ms, seq, fms, fseq) then fms, fseq = ms, seq end
end
if fms < 0 then return 0 end
return redis.call('XTRIM', KEYS[1], 'MINID', string.format('%.0f-%.0f', fms, fseq))
`)
)

func (b *Bus) deadLetterStream(topic string) string {
	return b.cfg.StreamPrefix + deadLetterInfix + topic
}

// deadLetter moves an exhausted entry under a fresh context: the handler's
// context may already have expired. It does nothing when a peer owns the
// delivery now.
func (b *Bus) deadLetter(stream, messageID, consumer string, values map[string]any, cause error) {
	topic := stream[len(b.cfg.StreamPrefix):]
	raw := rawRecord(values)
	ctx, cancel := context.WithTimeout(b.ctx, opTimeout)
	defer cancel()
	reply, err := deadLetterScript.Run(ctx, b.rdb, []string{stream, b.deadLetterStream(topic)},
		b.cfg.ConsumerGroup, messageID, consumer, raw, cause.Error()).Slice()
	if errors.Is(err, redis.Nil) {
		return // a peer reclaimed the delivery; it decides the entry's fate
	}
	if err != nil {
		b.log.Error("events: dead-lettering work failed; entry remains pending",
			"stream", stream, "message_id", messageID, "error", err)
		return
	}
	dl := events.DeadLetter{ID: fmt.Sprint(reply[0]), Topic: topic, SourceID: messageID,
		Consumer: consumer, Error: cause.Error(), Deliveries: int(toInt64(reply[1])),
		FailedAt: time.UnixMilli(toInt64(reply[2])).UTC(), Raw: []byte(raw)}
	if event, err := decodeRecord(raw); err == nil {
		dl.Record = &event.Record
	}
	b.log.Error("events: task dead-lettered", "stream", stream, "message_id", messageID,
		"dead_letter_id", dl.ID, "deliveries", dl.Deliveries, "error", cause)
	b.notifyDeadLetter(dl)
}

func (b *Bus) notifyDeadLetter(dl events.DeadLetter) {
	hook := b.cfg.policy.OnDeadLetter
	if hook == nil {
		return
	}
	defer func() {
		if value := recover(); value != nil {
			b.log.Error("events: dead-letter hook panicked", "dead_letter_id", dl.ID, "panic", value)
		}
	}()
	hook(context.WithoutCancel(b.ctx), dl)
}

// BacklogStats implements events.TaskAdmin for the bus's consumer group.
func (b *Bus) BacklogStats(ctx context.Context, topic string) (events.BacklogStats, error) {
	if err := b.beginAdmin(ctx, topic); err != nil {
		return events.BacklogStats{}, err
	}
	defer b.wg.Done()
	reply, err := backlogScript.Run(ctx, b.rdb,
		[]string{b.cfg.StreamPrefix + topic, b.deadLetterStream(topic)}, b.cfg.ConsumerGroup).Slice()
	if err != nil {
		return events.BacklogStats{}, b.adminError(ctx, "reading work backlog", err)
	}
	stats := events.BacklogStats{Pending: toInt64(reply[0]), DeadLetters: toInt64(reply[3])}
	if oldest, _ := reply[1].(string); oldest != "" {
		if ms, ok := streamIDMillis(oldest); ok {
			stats.OldestAge = max(time.Duration(toInt64(reply[2])-ms)*time.Millisecond, time.Nanosecond)
		}
	}
	return stats, nil
}

// DeadLetters implements events.TaskAdmin, oldest first.
func (b *Bus) DeadLetters(ctx context.Context, topic string, limit int) ([]events.DeadLetter, error) {
	if err := b.beginAdmin(ctx, topic); err != nil {
		return nil, err
	}
	defer b.wg.Done()
	if limit <= 0 {
		return nil, fmt.Errorf("goredis: dead-letter limit must be positive: %w", sdk.ErrInvalidInput)
	}
	rows, err := b.rdb.XRangeN(ctx, b.deadLetterStream(topic), "-", "+", int64(limit)).Result()
	if err != nil {
		return nil, b.adminError(ctx, "listing dead letters", err)
	}
	out := make([]events.DeadLetter, 0, len(rows))
	for _, row := range rows {
		out = append(out, parseDeadLetter(topic, row))
	}
	return out, nil
}

// Redeliver implements events.TaskAdmin. The new entry gets a fresh delivery
// count; its envelope, and therefore its EventID, is unchanged.
func (b *Bus) Redeliver(ctx context.Context, topic, deadLetterID string) error {
	if err := b.beginAdmin(ctx, topic); err != nil {
		return err
	}
	defer b.wg.Done()
	if !validStreamID(deadLetterID) {
		return events.ErrDeadLetterNotFound
	}
	moved, err := redeliverScript.Run(ctx, b.rdb,
		[]string{b.deadLetterStream(topic), b.cfg.StreamPrefix + topic}, deadLetterID).Int64()
	if err != nil {
		return b.adminError(ctx, "redelivering dead letter", err)
	}
	if moved == 0 {
		return events.ErrDeadLetterNotFound
	}
	return nil
}

// DiscardDeadLetter implements events.TaskAdmin.
func (b *Bus) DiscardDeadLetter(ctx context.Context, topic, deadLetterID string) error {
	if err := b.beginAdmin(ctx, topic); err != nil {
		return err
	}
	defer b.wg.Done()
	if !validStreamID(deadLetterID) {
		return events.ErrDeadLetterNotFound
	}
	removed, err := b.rdb.XDel(ctx, b.deadLetterStream(topic), deadLetterID).Result()
	if err != nil {
		return b.adminError(ctx, "discarding dead letter", err)
	}
	if removed == 0 {
		return events.ErrDeadLetterNotFound
	}
	return nil
}

// Trim implements events.TaskAdmin with XTRIM MINID below every group's
// oldest unacknowledged entry. Dead-letter streams are not trimmed.
func (b *Bus) Trim(ctx context.Context, topic string, olderThan time.Duration) (int64, error) {
	if err := b.beginAdmin(ctx, topic); err != nil {
		return 0, err
	}
	defer b.wg.Done()
	if olderThan < 0 {
		return 0, fmt.Errorf("goredis: trim age must not be negative: %w", sdk.ErrInvalidInput)
	}
	removed, err := trimScript.Run(ctx, b.rdb, []string{b.cfg.StreamPrefix + topic}, olderThan.Milliseconds()).Int64()
	if err != nil {
		return 0, b.adminError(ctx, "trimming work stream", err)
	}
	return removed, nil
}

func (b *Bus) beginAdmin(ctx context.Context, topic string) error {
	if err := b.begin(ctx); err != nil {
		return err
	}
	if err := events.ValidateTaskTopic(topic); err != nil {
		b.wg.Done()
		return err
	}
	return nil
}

func (b *Bus) adminError(ctx context.Context, action string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("goredis: %s: %w", action, err)
}

func parseDeadLetter(topic string, row redis.XMessage) events.DeadLetter {
	str := func(key string) string { s, _ := row.Values[key].(string); return s }
	raw := str("record")
	dl := events.DeadLetter{ID: row.ID, Topic: topic, SourceID: str("source_id"),
		Consumer: str("consumer"), Error: str("error"), Raw: []byte(raw)}
	if n, err := strconv.Atoi(str("deliveries")); err == nil {
		dl.Deliveries = n
	}
	if ms, err := strconv.ParseInt(str("failed_at"), 10, 64); err == nil {
		dl.FailedAt = time.UnixMilli(ms).UTC()
	}
	if event, err := decodeRecord(raw); err == nil {
		dl.Record = &event.Record
	}
	return dl
}

// rawRecord returns the entry's envelope bytes, or, for an entry without a
// string "record" field, its whole field map as JSON.
func rawRecord(values map[string]any) string {
	if raw, ok := values["record"].(string); ok {
		return raw
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return fmt.Sprint(values)
	}
	return string(encoded)
}

func streamIDMillis(id string) (int64, bool) {
	ms, _, ok := strings.Cut(id, "-")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(ms, 10, 64)
	return n, err == nil
}

func validStreamID(id string) bool {
	ms, seq, ok := strings.Cut(id, "-")
	if !ok {
		return false
	}
	_, errMS := strconv.ParseUint(ms, 10, 64)
	_, errSeq := strconv.ParseUint(seq, 10, 64)
	return errMS == nil && errSeq == nil
}

func toInt64(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}
