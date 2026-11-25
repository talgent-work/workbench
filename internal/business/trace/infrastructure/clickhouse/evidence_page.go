package clickhouse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

// A native evidence table orders a Work Caller's records by time and then by
// their source identity, and every page reads in that order. Delivery is at
// least once, and a record's copies share the whole sort key, so until
// ClickHouse merges them they come back next to each other. A read never adds
// FINAL: it would merge the Work Caller's whole retention before the limit
// applies, which is the cost this layout avoids.

// evidenceReads bounds the reads one page takes. A read that copies cut
// short is followed by one more from its last record, so a page holds its
// limit of distinct records, or all that remain, and has a cursor only when
// more follow. After the last read a page still short ends with a cursor: the
// next page continues past the copies.
const evidenceReads = 4

// listedRecord is a record a page read returns: its source identity and the
// cursor that resumes strictly after it.
type listedRecord interface {
	identity() string
	cursor() string
}

// readEvidencePage reads records in sort key order through read, which
// returns up to limit+1 records after a cursor ("" for the start), and keeps
// one of each run of copies. A record's copies lie next to each other, and a
// read after a record's cursor starts past them.
func readEvidencePage[T listedRecord](ctx context.Context, after string, limit int, read func(context.Context, string) ([]T, error)) ([]T, string, error) {
	var kept []T
	last := ""
	for reads := 1; ; reads++ {
		batch, err := read(ctx, after)
		if err != nil {
			return nil, "", err
		}
		for _, record := range batch {
			if id := record.identity(); len(kept) == 0 || id != last {
				last = id
				kept = append(kept, record)
			}
		}
		switch {
		case len(kept) > limit:
			return kept[:limit], kept[limit-1].cursor(), nil
		case len(batch) <= limit:
			return kept, "", nil
		case reads == evidenceReads:
			return kept, kept[len(kept)-1].cursor(), nil
		}
		after = batch[len(batch)-1].cursor()
	}
}

// evidenceCursor names the last record of a page by its sort key after the
// Work Caller: its time and its source identity.
type evidenceCursor struct {
	StartTime string   `json:"start_time"`
	Key       []string `json:"key"`
}

func encodeEvidenceCursor(startTime time.Time, key ...string) (string, error) {
	payload, err := json.Marshal(evidenceCursor{StartTime: startTime.UTC().Format(time.RFC3339Nano), Key: key})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeEvidenceCursor(cursor string, parts int) (time.Time, []string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("%w: %v", query.ErrInvalidSpanCursor, err)
	}
	var payload evidenceCursor
	if err := json.Unmarshal(raw, &payload); err != nil {
		return time.Time{}, nil, fmt.Errorf("%w: %v", query.ErrInvalidSpanCursor, err)
	}
	start, err := time.Parse(time.RFC3339Nano, payload.StartTime)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("%w: %v", query.ErrInvalidSpanCursor, err)
	}
	if len(payload.Key) != parts {
		return time.Time{}, nil, fmt.Errorf("%w: the cursor names no record", query.ErrInvalidSpanCursor)
	}
	return start, payload.Key, nil
}

// afterCursorFilter narrows a read to the records strictly after the cursor
// in the read's direction. The bound on time alone lets the primary key skip
// to the cursor; the tuple orders the records sharing its time.
func afterCursorFilter(cursor string, newestFirst bool, first, second string, params clickhousego.Parameters) ([]string, error) {
	at, key, err := decodeEvidenceCursor(cursor, 2)
	if err != nil {
		return nil, err
	}
	params["after_time"] = at.UTC().Format("2006-01-02 15:04:05.999999999")
	params["after_first"], params["after_second"] = key[0], key[1]
	bound, beyond := ">=", ">"
	if newestFirst {
		bound, beyond = "<=", "<"
	}
	return []string{
		fmt.Sprintf("Timestamp %s {after_time:DateTime64(9, 'UTC')}", bound),
		fmt.Sprintf("(Timestamp, %s, %s) %s ({after_time:DateTime64(9, 'UTC')}, {after_first:String}, {after_second:String})", first, second, beyond),
	}, nil
}
