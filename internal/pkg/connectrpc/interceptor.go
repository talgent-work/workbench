package connectrpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"
	"github.com/go-jimu/components/sloghelper"
)

// executionInterceptor is the execution boundary of every RPC except health
// checks, unary and streaming alike: it carries the process logger in ctx,
// recovers a handler panic as internal, and writes one "request complete"
// entry.
func executionInterceptor(logger *slog.Logger) connect.ServerInterceptor {
	execution := execution{logger: logger}
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) (err error) {
			if strings.HasPrefix(spec.Procedure, healthProcedures) {
				return next(ctx, spec, stream)
			}
			start := time.Now()
			defer func() {
				if recovered := recover(); recovered != nil {
					// net/http aborts a response with this panic on purpose.
					if recovered == http.ErrAbortHandler { //nolint:errorlint // net/http compares with ==
						panic(recovered)
					}
					err = panicError(recovered)
				}
				execution.log(ctx, spec, start, err)
			}()
			return next(sloghelper.NewContext(ctx, logger), spec, stream)
		}
	}
}

// healthProcedures prefixes the health checks, which bypass the execution log.
const healthProcedures = "/" + grpchealth.HealthV1ServiceName + "/"

type execution struct {
	logger *slog.Logger
}

// panicError answers internal without the panic's value, which stays in the
// execution log as the cause.
func panicError(recovered any) error {
	return connect.NewError(connect.CodeInternal, "handler panicked").WithCause(fmt.Errorf("handler panicked: %v", recovered))
}

func (i execution) log(ctx context.Context, spec connect.Spec, start time.Time, err error) {
	code := codeOf(err)
	// A call whose caller went away failed for the caller's reason, whatever
	// code the handler answered the cancellation with (a lookup or query it
	// cut short may surface as internal or unavailable): a browser closing
	// its poll is no server fault.
	if err != nil && errors.Is(ctx.Err(), context.Canceled) {
		code = connect.CodeCanceled
	}
	attrs := []slog.Attr{
		slog.String("operation", spec.Procedure),
		slog.Int64("elapsed_ms", time.Since(start).Milliseconds()),
		slog.String("outcome", "success"),
		slog.String("connect_code", "ok"),
	}
	if err == nil {
		i.logger.LogAttrs(ctx, slog.LevelInfo, "request complete", attrs...)
		return
	}
	attrs[2] = slog.String("outcome", "failure")
	attrs[3] = slog.String("connect_code", code.String())
	i.logger.LogAttrs(ctx, levelOf(code), "request complete", append(attrs, sloghelper.Error(causeOf(err)))...)
}

// codeOf classifies an error as Connect will answer it: Connect codes a bare
// context error only after interceptors return.
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

// causeOf is what went wrong behind the message sent to the client.
func causeOf(err error) error {
	if connectErr := new(connect.Error); errors.As(err, &connectErr) && connectErr.Unwrap() != nil {
		return connectErr.Unwrap()
	}
	return err
}

// levelOf follows what the code means for the service: an answer about the
// request at Info, a transient service or dependency failure at Warn, and a
// server fault at Error.
func levelOf(code connect.Code) slog.Level {
	switch code {
	case connect.CodeCanceled, connect.CodeInvalidArgument, connect.CodeNotFound, connect.CodeAlreadyExists,
		connect.CodePermissionDenied, connect.CodeResourceExhausted, connect.CodeFailedPrecondition,
		connect.CodeOutOfRange, connect.CodeUnauthenticated:
		return slog.LevelInfo
	case connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeAborted:
		return slog.LevelWarn
	}
	return slog.LevelError
}
