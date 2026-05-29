package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

type contextKey int

const loggerKey contextKey = 0

// loggerFromCtx returns the request-scoped logger stored by RequestLogger middleware.
// Falls back to slog.Default() for non-request contexts.
func loggerFromCtx(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// statusWriter wraps http.ResponseWriter to capture the response status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) written() int {
	if sw.status == 0 {
		return http.StatusOK
	}
	return sw.status
}

// RequestLogger returns middleware that logs each request with method, path,
// status, latency, and a unique request_id. It stores a request-scoped logger
// in the context so handler errors can be correlated to their request.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			var b [8]byte
			_, _ = rand.Read(b[:])
			reqID := hex.EncodeToString(b[:])
			log := logger.With("request_id", reqID)

			sw := &statusWriter{ResponseWriter: w}
			ctx := context.WithValue(r.Context(), loggerKey, log)

			next.ServeHTTP(sw, r.WithContext(ctx))

			log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.written(),
				"latency_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
