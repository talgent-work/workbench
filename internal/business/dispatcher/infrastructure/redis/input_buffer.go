package redis

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

const (
	// InputBufferCapacity bounds a Work's unsettled entries.
	InputBufferCapacity = 20
	// InputAppendedChannel names a Work whose buffer changed, so the replica
	// reading its stream writes the new entries.
	InputAppendedChannel = keyPrefix + "input-appended"
	// settledRetention is how long a settled identity is remembered, so a
	// late client retry is recognised instead of buffered again.
	settledRetention = 24 * time.Hour
)

// A Work's buffer is an entry hash, an admission order and the set of entries
// written to the current process. Its keys have no TTL: Redis is reliable
// storage here, and Closing drops them.
func inputEntriesKey(w string) string { return keyPrefix + "input:" + w }
func inputOrderKey(w string) string   { return keyPrefix + "input-order:" + w }
func inputWrittenKey(w string) string { return keyPrefix + "input-written:" + w }
func inputSeqKey(w string) string     { return keyPrefix + "input-seq:" + w }
func inputSettledKey(w, entry string) string {
	return keyPrefix + "input-settled:" + w + ":" + entry
}

// entryKey separates input identities from interaction identities.
func entryKey(input command.BufferedInput) string {
	if input.Reply != nil {
		return "reply:" + input.ID
	}
	return "input:" + input.ID
}

// InputEntry is a buffered input with its delivery position.
type InputEntry struct {
	command.BufferedInput
	// Written means the entry was written to the Work's current process.
	Written bool
}

// Key identifies the entry within its Work's buffer.
func (e InputEntry) Key() string { return entryKey(e.BufferedInput) }

type storedInput struct {
	Text             string `json:"text,omitempty"`
	Allow            bool   `json:"allow,omitempty"`
	AlwaysAllow      bool   `json:"always_allow,omitempty"`
	Reason           string `json:"reason,omitempty"`
	UpdatedInputJSON string `json:"updated_input_json,omitempty"`
}

func encodeInput(input command.BufferedInput) (string, error) {
	stored := storedInput{Text: input.Text}
	if reply := input.Reply; reply != nil {
		stored = storedInput{Allow: reply.Allow, AlwaysAllow: reply.AlwaysAllow, Reason: reply.Reason, UpdatedInputJSON: reply.UpdatedInputJSON}
	}
	raw, err := json.Marshal(stored)
	return string(raw), oops.Wrap(err)
}

func decodeInput(key, raw string) (command.BufferedInput, error) {
	var stored storedInput
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return command.BufferedInput{}, oops.Wrap(err)
	}
	kind, id, _ := strings.Cut(key, ":")
	if kind == "reply" {
		return command.BufferedInput{ID: id, Reply: &command.BufferedReply{Allow: stored.Allow, AlwaysAllow: stored.AlwaysAllow, Reason: stored.Reason, UpdatedInputJSON: stored.UpdatedInputJSON}}, nil
	}
	return command.BufferedInput{ID: id, Text: stored.Text}, nil
}

// InputBuffer keeps each Work's input buffer in Redis.
type InputBuffer struct {
	rdb *goredis.Client
}

var _ command.InputBuffer = (*InputBuffer)(nil)

func NewInputBuffer(rdb *goredis.Client) *InputBuffer {
	return &InputBuffer{rdb: rdb}
}

// appendScript deduplicates by identity, remembers settled identities, bounds
// the buffer and notifies the Work's reader in one step.
const appendScript = `
if redis.call("EXISTS", KEYS[5]) == 1 then return "settled" end
local existing = redis.call("HGET", KEYS[1], ARGV[1])
if existing then
  if existing == ARGV[2] then return "repeated" end
  return "conflict"
end
if redis.call("HLEN", KEYS[1]) >= tonumber(ARGV[3]) then return "full" end
local seq = redis.call("INCR", KEYS[3])
redis.call("HSET", KEYS[1], ARGV[1], ARGV[2])
redis.call("ZADD", KEYS[2], seq, ARGV[1])
redis.call("PUBLISH", ARGV[4], ARGV[5])
return "buffered"
`

func (b *InputBuffer) Append(ctx context.Context, workID string, input command.BufferedInput) (command.InputHandOff, error) {
	content, err := encodeInput(input)
	if err != nil {
		return "", err
	}
	key := entryKey(input)
	result, err := b.rdb.Eval(ctx, appendScript, []string{inputEntriesKey(workID), inputOrderKey(workID), inputSeqKey(workID), inputWrittenKey(workID), inputSettledKey(workID, key)},
		key, content, InputBufferCapacity, InputAppendedChannel, workID).Text()
	if err != nil {
		return "", oops.Wrap(err)
	}
	switch result {
	case "buffered":
		return command.InputBuffered, nil
	case "repeated":
		return command.InputRepeated, nil
	case "settled":
		return command.InputSettled, nil
	case "conflict":
		return "", command.ErrInputConflict
	case "full":
		return "", command.ErrInputBufferFull
	}
	return "", oops.With("agent_work_id", workID).Errorf("unknown input buffer answer %q", result)
}

// Entries reads a Work's unsettled entries in admission order.
func (b *InputBuffer) Entries(ctx context.Context, workID string) ([]InputEntry, error) {
	pipe := b.rdb.TxPipeline()
	order := pipe.ZRange(ctx, inputOrderKey(workID), 0, -1)
	contents := pipe.HGetAll(ctx, inputEntriesKey(workID))
	written := pipe.SMembers(ctx, inputWrittenKey(workID))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, goredis.Nil) {
		return nil, oops.Wrap(err)
	}
	marked := make(map[string]bool, len(written.Val()))
	for _, key := range written.Val() {
		marked[key] = true
	}
	entries := make([]InputEntry, 0, len(order.Val()))
	for _, key := range order.Val() {
		raw, ok := contents.Val()[key]
		if !ok {
			continue
		}
		input, err := decodeInput(key, raw)
		if err != nil {
			return nil, oops.With("agent_work_id", workID).Wrap(err)
		}
		entries = append(entries, InputEntry{BufferedInput: input, Written: marked[key]})
	}
	return entries, nil
}

func (b *InputBuffer) List(ctx context.Context, workID string) ([]command.BufferedInput, error) {
	entries, err := b.Entries(ctx, workID)
	if err != nil {
		return nil, err
	}
	inputs := make([]command.BufferedInput, len(entries))
	for i, entry := range entries {
		inputs[i] = entry.BufferedInput
	}
	return inputs, nil
}

const markWrittenScript = `
for _, key in ipairs(ARGV) do
  if redis.call("HEXISTS", KEYS[1], key) == 1 then redis.call("SADD", KEYS[2], key) end
end
return 1
`

// MarkWritten records that entries were written to the Work's process.
func (b *InputBuffer) MarkWritten(ctx context.Context, workID string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	args := make([]any, len(keys))
	for i, key := range keys {
		args[i] = key
	}
	return oops.Wrap(b.rdb.Eval(ctx, markWrittenScript, []string{inputEntriesKey(workID), inputWrittenKey(workID)}, args...).Err())
}

const settleScript = `
local removed = redis.call("HDEL", KEYS[1], ARGV[1])
redis.call("ZREM", KEYS[2], ARGV[1])
redis.call("SREM", KEYS[3], ARGV[1])
if removed == 1 then redis.call("SET", KEYS[4], "1", "PX", ARGV[2]) end
return removed
`

// Settle removes an entry, leaving its identity remembered as settled for a
// while. It reports whether this call removed it, so a repeated settlement
// is reported once.
func (b *InputBuffer) Settle(ctx context.Context, workID, key string) (bool, error) {
	removed, err := b.rdb.Eval(ctx, settleScript, []string{inputEntriesKey(workID), inputOrderKey(workID), inputWrittenKey(workID), inputSettledKey(workID, key)},
		key, strconv.FormatInt(settledRetention.Milliseconds(), 10)).Int()
	return removed == 1, oops.Wrap(err)
}

const dropScript = `
redis.call("DEL", KEYS[1], KEYS[2], KEYS[3], KEYS[4])
redis.call("PUBLISH", ARGV[1], ARGV[2])
return 1
`

// Drop empties a Work's buffer when the Work enters Closing.
func (b *InputBuffer) Drop(ctx context.Context, workID string) error {
	return oops.Wrap(b.rdb.Eval(ctx, dropScript, []string{inputEntriesKey(workID), inputOrderKey(workID), inputWrittenKey(workID), inputSeqKey(workID)}, InputAppendedChannel, workID).Err())
}

// Appended subscribes to buffer changes; each message names a Work. The
// caller closes the subscription.
func (b *InputBuffer) Appended(ctx context.Context) *goredis.PubSub {
	return b.rdb.Subscribe(ctx, InputAppendedChannel)
}
