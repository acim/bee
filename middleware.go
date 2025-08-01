package bee

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// Middlewares provides chaining of middlewares.
type Middlewares []func(http.Handler) http.Handler

// Add add middleware to a chain.
func (ms *Middlewares) Add(h func(http.Handler) http.Handler) {
	*ms = append(*ms, h)
}

// Wrap wraps multiplexes in a chain of middlewares.
func (ms *Middlewares) Wrap(mux *http.ServeMux) http.Handler {
	if len(*ms) == 0 {
		return mux
	}

	var wrapped http.Handler

	wrapped = mux

	// loop in reverse to preserve middleware order
	for i := len(*ms) - 1; i >= 0; i-- {
		wrapped = (*ms)[i](wrapped)
	}

	return wrapped
}

// SlogLogger is a middleware for slog logging.
func SlogLogger(log *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
			writer := middleware.NewWrapResponseWriter(res, req.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(writer, req)

			log.Info(
				"request completed",
				slog.Time("time", start),
				slog.String("method", req.Method),
				slog.String("uri", req.RequestURI),
				slog.Int("status", writer.Status()),
				slog.Int("bytes", writer.BytesWritten()),
				slog.Duration("duration", time.Since(start)))
		})
	}
}
