package bee

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// httpDrain tracks handlers rather than connections: closing a connection does
// not mean the handler has finished using application dependencies.
type httpDrain struct {
	server   *http.Server
	cancel   context.CancelFunc
	mu       sync.Mutex
	stopping bool
	handlers sync.WaitGroup
}

func newHTTPDrain(server *http.Server) *httpDrain {
	force, cancel := context.WithCancel(context.Background())
	drain := &httpDrain{server: server, cancel: cancel}
	handler := server.Handler
	if handler == nil {
		handler = http.DefaultServeMux
	}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		drain.mu.Lock()
		if drain.stopping {
			drain.mu.Unlock()
			http.Error(w, "server shutting down", http.StatusServiceUnavailable)
			return
		}
		drain.handlers.Add(1)
		drain.mu.Unlock()
		defer drain.handlers.Done()
		ctx, cancelRequest := context.WithCancel(r.Context())
		stop := context.AfterFunc(force, cancelRequest)
		defer func() { stop(); cancelRequest() }()
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
	return drain
}

func (d *httpDrain) finish() {
	// Seal admission under the same lock used by Add before waiting. A connection
	// may have parsed a request without entering its handler when Shutdown expires.
	d.mu.Lock()
	d.stopping = true
	d.mu.Unlock()
	d.cancel()
	// Close interrupts transport I/O; the handler wait below covers finalization.
	_ = d.server.Close()
	d.handlers.Wait()
}

// HTTPOption configures an HTTP server's lifecycle.
type HTTPOption func(*httpOptions)

type httpOptions struct{ drainTimeout time.Duration }

// WithHTTPDrainTimeout overrides the graceful HTTP drain timeout without
// changing registered closer budgets. If omitted, WithShutdownTimeout applies.
// A nonpositive duration starts cancellation immediately on shutdown.
func WithHTTPDrainTimeout(timeout time.Duration) HTTPOption {
	return func(options *httpOptions) { options.drainTimeout = timeout }
}
