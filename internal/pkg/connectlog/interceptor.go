package connectlog

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-jimu/components/sloghelper"
)

// NewInterceptor logs ConnectRPC execution-boundary completion once per call.
func NewInterceptor(logger *slog.Logger) connect.ServerInterceptor {
	if logger == nil {
		logger = slog.Default()
	}
	i := &interceptor{logger: logger}
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			start := time.Now()
			err := next(ctx, spec, stream)
			i.log(ctx, spec, start, err)
			return err
		}
	}
}

type interceptor struct {
	logger *slog.Logger
}

func (i *interceptor) log(ctx context.Context, spec connect.Spec, start time.Time, err error) {
	attrs := []slog.Attr{
		slog.String("operation", spec.Procedure),
		slog.String("outcome", "success"),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	}
	if err != nil {
		code := codeOf(err)
		attrs[1] = slog.String("outcome", "failed")
		attrs = append(attrs, slog.String("code", code.String()))
		if callerOutcome(code) {
			// An answer about the request, not a server fault: no stack, and
			// below ERROR so expected outcomes cannot mask or page on failures.
			// The error group keeps sloghelper.Error's shape, minus the trace.
			attrs = append(attrs, slog.Group("error", slog.String("msg", err.Error())))
			i.logger.LogAttrs(ctx, slog.LevelWarn, "rpc completed", attrs...)
			return
		}
		attrs = append(attrs, sloghelper.Error(err))
		i.logger.LogAttrs(ctx, slog.LevelError, "rpc completed", attrs...)
		return
	}
	i.logger.LogAttrs(ctx, slog.LevelInfo, "rpc completed", attrs...)
}

// codeOf classifies a handler's error as Connect will answer it: Connect
// codes a bare context error only after interceptors return.
func codeOf(err error) connect.Code {
	if connectErr := new(connect.Error); errors.As(err, &connectErr) {
		return connectErr.Code()
	}
	switch {
	case errors.Is(err, context.Canceled):
		return connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return connect.CodeDeadlineExceeded
	}
	return connect.CodeUnknown
}

// callerOutcome reports codes that describe the request or the caller rather
// than a server failure.
func callerOutcome(code connect.Code) bool {
	switch code {
	case connect.CodeCanceled, connect.CodeInvalidArgument, connect.CodeNotFound, connect.CodeAlreadyExists,
		connect.CodePermissionDenied, connect.CodeResourceExhausted, connect.CodeFailedPrecondition,
		connect.CodeAborted, connect.CodeOutOfRange, connect.CodeUnauthenticated:
		return true
	}
	return false
}
