package connectlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Intent (real): unary RPC boundaries must emit one success completion log with
// a stable operation name and duration so application calls are observable.
func TestInterceptorLogsUnarySuccessCompletion(t *testing.T) {
	logger, logs := testLogger()
	if err := callUnary(t, logger, "/test.Service/Success", nil); err != nil {
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
			err = connect.NewError(tc.code, "refused")
		}
		t.Run(tc.code.String()+"/"+fmt.Sprintf("%T", err), func(t *testing.T) {
			logger, logs := testLogger()
			if err := callUnary(t, logger, "/test.Service/Fail", err); err == nil {
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

// callUnary calls procedure on a server logging through the interceptor; its
// handler answers fail when it is set.
func callUnary(t *testing.T, logger *slog.Logger, procedure string, fail error) error {
	t.Helper()
	spec := connect.Spec{StreamType: connect.StreamTypeUnary, Procedure: procedure}
	server := connect.NewServer(NewInterceptor(logger))
	server.Register(connect.Method{Spec: spec, Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
		if err := stream.Receive(&emptypb.Empty{}); err != nil {
			return err
		}
		if fail != nil {
			return fail
		}
		return stream.Send(&emptypb.Empty{})
	}})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	client := connect.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL))
	return client.CallUnary(context.Background(), spec, &emptypb.Empty{}, &emptypb.Empty{})
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
