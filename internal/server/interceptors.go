package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"time"

	"connectrpc.com/connect"
)

// ErrPanicRecovered indicates an RPC handler panicked and was recovered.
var ErrPanicRecovered = errors.New("panic recovered in rpc handler")

// LoggingInterceptor returns a ConnectRPC unary interceptor that logs every
// RPC call with the procedure name, duration, and error (if any).
//
// Log level is Info for successful calls and Warn for calls that return errors.
func LoggingInterceptor(logger *slog.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			duration := time.Since(start)

			attrs := []slog.Attr{
				slog.String("procedure", req.Spec().Procedure),
				slog.Duration("duration", duration),
			}

			if err != nil {
				attrs = append(attrs, slog.String("error", err.Error()))
				logger.LogAttrs(ctx, slog.LevelWarn, "rpc completed with error", attrs...)
			} else {
				logger.LogAttrs(ctx, slog.LevelInfo, "rpc completed", attrs...)
			}

			return resp, err
		}
	}
}

// RecoveryInterceptor returns a ConnectRPC unary interceptor that recovers from
// panics in RPC handlers. On panic, it logs the panic value and stack trace at
// Error level and returns a CodeInternal error to the client.
func RecoveryInterceptor(logger *slog.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (resp connect.AnyResponse, retErr error) {
			defer func() {
				if r := recover(); r != nil {
					// Capture a stack trace for debugging.
					buf := make([]byte, 4096)
					n := runtime.Stack(buf, false)

					logger.ErrorContext(ctx, "panic recovered in rpc handler",
						slog.String("procedure", req.Spec().Procedure),
						slog.Any("panic", r),
						slog.String("stack", string(buf[:n])),
					)

					retErr = connect.NewError(connect.CodeInternal,
						fmt.Errorf("%s: %w", req.Spec().Procedure, ErrPanicRecovered))
				}
			}()

			return next(ctx, req)
		}
	}
}
