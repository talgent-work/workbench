package connectlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Intent (real): unary RPC boundaries must emit one success completion log with
// a stable operation name and duration so application calls are observable.
func TestInterceptorLogsUnarySuccessCompletion(t *testing.T) {
	logger, logs := testLogger()
	handler := connect.NewUnaryHandler(
		"/test.Service/Success",
		func(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
			return connect.NewResponse(&emptypb.Empty{}), nil
		},
		connect.WithInterceptors(NewInterceptor(logger)),
	)
	server := httptest.NewServer(handler)
	defer server.Close()

	client := connect.NewClient[emptypb.Empty, emptypb.Empty](server.Client(), server.URL+"/test.Service/Success")
	if _, err := client.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{})); err != nil {
		t.Fatalf("CallUnary: %v", err)
	}

	entry := lastLogEntry(t, logs)
	if entry["operation"] != "/test.Service/Success" {
		t.Fatalf("operation = %v, want /test.Service/Success", entry["operation"])
	}
	if entry["outcome"] != "success" {
		t.Fatalf("outcome = %v, want success", entry["outcome"])
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Fatalf("duration_ms missing from log entry: %#v", entry)
	}
}

// Intent (real): a failed RPC is logged once with its code; caller outcomes
// such as NotFound are WARN so they neither mask nor page as server faults,
// while server faults stay ERROR. An error without its own stack trace is
// logged without one: the log's source already names the call site.
func TestInterceptorLogsUnaryFailureLevelByCode(t *testing.T) {
	for _, tc := range []struct {
		code  connect.Code
		err   error
		level string
	}{
		{code: connect.CodeCanceled, err: fmt.Errorf("refused: %w", context.Canceled), level: "WARN"},
		{code: connect.CodeDeadlineExceeded, err: fmt.Errorf("refused: %w", context.DeadlineExceeded), level: "ERROR"},
		{code: connect.CodeUnknown, err: errors.New("refused"), level: "ERROR"},
		{code: connect.CodeNotFound, level: "WARN"},
		{code: connect.CodeInvalidArgument, level: "WARN"},
		{code: connect.CodePermissionDenied, level: "WARN"},
		{code: connect.CodeFailedPrecondition, level: "WARN"},
		{code: connect.CodeUnauthenticated, level: "WARN"},
		{code: connect.CodeCanceled, level: "WARN"},
		{code: connect.CodeInternal, level: "ERROR"},
		{code: connect.CodeUnavailable, level: "ERROR"},
		{code: connect.CodeUnknown, level: "ERROR"},
		{code: connect.CodeDeadlineExceeded, level: "ERROR"},
	} {
		err := tc.err
		if err == nil {
			err = connect.NewError(tc.code, errors.New("refused"))
		}
		t.Run(tc.code.String()+"/"+fmt.Sprintf("%T", err), func(t *testing.T) {
			logger, logs := testLogger()
			handler := connect.NewUnaryHandler(
				"/test.Service/Fail",
				func(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
					return nil, err
				},
				connect.WithInterceptors(NewInterceptor(logger)),
			)
			server := httptest.NewServer(handler)
			defer server.Close()

			client := connect.NewClient[emptypb.Empty, emptypb.Empty](server.Client(), server.URL+"/test.Service/Fail")
			if _, err := client.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{})); err == nil {
				t.Fatal("CallUnary succeeded, want error")
			}

			entry := lastLogEntry(t, logs)
			if entry["operation"] != "/test.Service/Fail" || entry["outcome"] != "failed" {
				t.Fatalf("entry = %#v, want failed /test.Service/Fail", entry)
			}
			if entry["level"] != tc.level || entry["code"] != tc.code.String() {
				t.Fatalf("level = %v code = %v, want %s %s", entry["level"], entry["code"], tc.level, tc.code)
			}
			errorAttrs, ok := entry["error"].(map[string]any)
			if !ok || !strings.Contains(fmt.Sprint(errorAttrs["msg"]), "refused") {
				t.Fatalf("error = %#v, want object with message", entry["error"])
			}
			if _, hasTrace := errorAttrs["trace"]; hasTrace {
				t.Fatalf("trace present for an error without a stack: %#v", entry["error"])
			}
		})
	}
}

func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func lastLogEntry(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) == 0 || len(lines[0]) == 0 {
		t.Fatal("no log entries captured")
	}
	var entry map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &entry); err != nil {
		t.Fatalf("unmarshal log entry: %v; line=%s", err, lines[len(lines)-1])
	}
	return entry
}
