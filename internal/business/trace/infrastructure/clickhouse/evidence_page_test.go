package clickhouse

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

// record is a read record named by its identity; its cursor is its position
// in the stored order, which a read resumes strictly after.
type record struct{ id, at string }

func (r record) identity() string { return r.id }
func (r record) cursor() string   { return r.at }

// stored reads records in sort key order: up to limit+1 of them strictly
// after a cursor, and counts the reads.
type stored struct {
	records []record
	reads   int
}

func (s *stored) read(limit int) func(context.Context, string) ([]record, error) {
	return func(_ context.Context, after string) ([]record, error) {
		s.reads++
		var out []record
		for _, r := range s.records {
			if r.at > after && len(out) < limit+1 {
				out = append(out, r)
			}
		}
		return out, nil
	}
}

func ids(records []record) []string {
	var out []string
	for _, r := range records {
		out = append(out, r.id)
	}
	return out
}

// Copies of a record lie next to each other and share its cursor position.
func copies(id, at string, n int) []record {
	return slices.Repeat([]record{{id, at}}, n)
}

func TestReadEvidencePageKeepsOneOfEachRunOfCopies(t *testing.T) {
	s := &stored{records: slices.Concat(copies("a", "1", 2), copies("b", "2", 1), copies("c", "3", 3), copies("d", "4", 1))}
	page, next, err := readEvidencePage(context.Background(), "", 3, s.read(3))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(page), []string{"a", "b", "c"}) || next != "3" {
		t.Fatalf("page = %v next = %q, want a b c then a cursor after c", ids(page), next)
	}
	rest, next, err := readEvidencePage(context.Background(), next, 3, s.read(3))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(rest), []string{"d"}) || next != "" {
		t.Fatalf("second page = %v next = %q, want d and exhausted", ids(rest), next)
	}
}

// A page whose read copies filled is completed by reading on, and an exhausted
// Work Caller ends without a cursor rather than with an empty page after it.
func TestReadEvidencePageReadsOnPastCopiesAndEndsWithoutCursor(t *testing.T) {
	s := &stored{records: slices.Concat(copies("a", "1", 4), copies("b", "2", 1))}
	page, next, err := readEvidencePage(context.Background(), "", 2, s.read(2))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(page), []string{"a", "b"}) || next != "" || s.reads != 2 {
		t.Fatalf("page = %v next = %q reads = %d, want a b, exhausted, after two reads", ids(page), next, s.reads)
	}
}

// Reads per page are bounded: a page still short after the last read ends
// with a cursor past what it read, and the next page continues from there.
func TestReadEvidencePageBoundsItsReads(t *testing.T) {
	var records []record
	for i, id := range []string{"a", "b", "c", "d", "e", "f"} {
		records = append(records, copies(id, string(rune('1'+i)), 6)...)
	}
	s := &stored{records: records}
	page, next, err := readEvidencePage(context.Background(), "", 5, s.read(5))
	if err != nil {
		t.Fatal(err)
	}
	if s.reads != evidenceReads || !slices.Equal(ids(page), []string{"a", "b", "c", "d"}) || next != "4" {
		t.Fatalf("page = %v next = %q reads = %d", ids(page), next, s.reads)
	}
	rest, next, err := readEvidencePage(context.Background(), next, 5, s.read(5))
	if err != nil || !slices.Equal(ids(rest), []string{"e", "f"}) || next != "" {
		t.Fatalf("next page = %v next = %q, %v, want e f and exhausted", ids(rest), next, err)
	}
}

func TestReadEvidencePageReturnsReadFailure(t *testing.T) {
	failed := errors.New("read failed")
	_, _, err := readEvidencePage(context.Background(), "", 3, func(context.Context, string) ([]record, error) { return nil, failed })
	if !errors.Is(err, failed) {
		t.Fatalf("err = %v, want the read failure", err)
	}
}

// A cursor resumes strictly after its record in the read's direction, with a
// time bound the primary key can use.
func TestAfterCursorFilterResumesAfterTheRecord(t *testing.T) {
	at := time.Date(2026, 5, 1, 10, 0, 0, 123456789, time.UTC)
	cursor, err := encodeEvidenceCursor(at, "session-1", "42")
	if err != nil {
		t.Fatal(err)
	}
	params := clickhousego.Parameters{}
	oldest, err := afterCursorFilter(cursor, false, "SessionId", "EventSequence", params)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(oldest, []string{
		"Timestamp >= {after_time:DateTime64(9, 'UTC')}",
		"(Timestamp, SessionId, EventSequence) > ({after_time:DateTime64(9, 'UTC')}, {after_first:String}, {after_second:String})",
	}) || params["after_time"] != "2026-05-01 10:00:00.123456789" || params["after_first"] != "session-1" || params["after_second"] != "42" {
		t.Fatalf("filters = %v params = %v", oldest, params)
	}
	newest, err := afterCursorFilter(cursor, true, "SessionId", "EventSequence", clickhousego.Parameters{})
	if err != nil || !strings.HasPrefix(newest[0], "Timestamp <= ") || !strings.Contains(newest[1], ") < (") {
		t.Fatalf("newest-first filters = %v, %v", newest, err)
	}
}

func TestAfterCursorFilterRejectsACursorNamingNoRecord(t *testing.T) {
	noKey, err := encodeEvidenceCursor(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"not-base64!", noKey} {
		if _, err := afterCursorFilter(cursor, false, "TraceId", "SpanId", clickhousego.Parameters{}); !errors.Is(err, query.ErrInvalidSpanCursor) {
			t.Fatalf("cursor %q: err = %v, want ErrInvalidSpanCursor", cursor, err)
		}
	}
}
