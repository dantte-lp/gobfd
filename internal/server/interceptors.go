package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"time"

	"connectrpc.com/connect/v2"
)

// ErrPanicRecovered indicates an RPC handler panicked and was recovered.
var ErrPanicRecovered = errors.New("panic recovered in rpc handler")

// LoggingInterceptor returns a ConnectRPC server interceptor that logs every
// RPC call with the procedure name, duration, and error (if any). In connect
// v2 server interceptors wrap unary and streaming calls alike; a streaming
// call is logged when the stream ends.
//
// Log level is Info for successful calls and for calls the client canceled
// (a closed watch stream is routine), and Warn for other errors.
func LoggingInterceptor(logger *slog.Logger) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			start := time.Now()
			err := next(ctx, spec, stream)

			attrs := []slog.Attr{
				slog.String("procedure", spec.Procedure),
				slog.Duration("duration", time.Since(start)),
			}
			switch {
			case errors.Is(err, context.Canceled) || connect.CodeOf(err) == connect.CodeCanceled:
				attrs = append(attrs, slog.String("error", err.Error()))
				logger.LogAttrs(ctx, slog.LevelInfo, "rpc canceled", attrs...)
			case err != nil:
				attrs = append(attrs, slog.String("error", err.Error()))
				logger.LogAttrs(ctx, slog.LevelWarn, "rpc completed with error", attrs...)
			default:
				logger.LogAttrs(ctx, slog.LevelInfo, "rpc completed", attrs...)
			}
			return err
		}
	}
}

// RecoveryInterceptor returns a ConnectRPC server interceptor that recovers
// from panics in RPC handlers. On panic, it logs the panic value and stack
// trace at Error level and returns a CodeInternal error to the client.
func RecoveryInterceptor(logger *slog.Logger) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) (retErr error) {
			defer func() {
				if r := recover(); r != nil {
					// Capture a stack trace for debugging.
					buf := make([]byte, 4096)
					n := runtime.Stack(buf, false)

					logger.ErrorContext(ctx, "panic recovered in rpc handler",
						slog.String("procedure", spec.Procedure),
						slog.Any("panic", r),
						slog.String("stack", string(buf[:n])),
					)

					retErr = connect.NewError(connect.CodeInternal,
						fmt.Errorf("%s: %w", spec.Procedure, ErrPanicRecovered).Error())
				}
			}()

			return next(ctx, spec, stream)
		}
	}
}
