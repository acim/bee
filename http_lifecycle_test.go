package bee

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPDrainExpiryWaitsForHandlerFinalization(t *testing.T) {
	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	app.timeout = 30 * time.Millisecond
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	finish := make(chan struct{})
	closed := make(chan struct{})
	defer close(finish)
	server := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-finish:
			return
		}
		<-finish
	})}
	app.Root("serve", func(ctx *Ctx[appTestConfig]) error {
		ctx.Register("dependency", func(context.Context) error { close(closed); return nil })
		ctx.HTTPServer("http", server)
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- app.RunE() }()
	clientDone := make(chan error, 1)
	go func() { clientDone <- getUntilStatus("http://"+addr, http.StatusOK) }()
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	app.cancel()
	select {
	case <-cancelled:
	case err := <-done:
		t.Fatalf("runner returned before cancelling handler: %v", err)
	case <-time.After(time.Second):
		t.Fatal("drain expiry did not cancel handler")
	}
	select {
	case <-closed:
		t.Fatal("dependency closed during finalization")
	case <-time.After(30 * time.Millisecond):
	}
	// Release finalization without closing finish twice in the deferred cleanup.
	finish <- struct{}{}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("RunE error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not finish")
	}
	select {
	case <-closed:
	default:
		t.Fatal("dependency was not closed")
	}
	select {
	case err := <-clientDone:
		if err == nil {
			t.Fatal("forced shutdown did not close the client connection")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not finish after forced shutdown")
	}
}

func TestHTTPDrainTimeoutIsIndependentOfCloserTimeout(t *testing.T) {
	app := newTestApp(t, appTestConfig{}, &bytes.Buffer{})
	app.timeout = 30 * time.Millisecond
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finish := make(chan struct{})
	defer close(finish)
	server := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-finish:
		case <-r.Context().Done():
			t.Error("request cancelled before HTTP drain budget elapsed")
		}
	})}
	app.Root("serve", func(ctx *Ctx[appTestConfig]) error {
		ctx.Register("dependency", func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 30*time.Millisecond {
				t.Error("closer inherited HTTP drain timeout")
			}
			return nil
		})
		ctx.HTTPServer("http", server, WithHTTPDrainTimeout(time.Second))
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- app.RunE() }()
	clientDone := make(chan error, 1)
	go func() { clientDone <- getUntilStatus("http://"+addr, http.StatusOK) }()
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	app.cancel()
	select {
	case err := <-done:
		t.Fatalf("runner returned during drain: %v", err)
	case <-time.After(70 * time.Millisecond):
	}
	finish <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not finish")
	}
	select {
	case err := <-clientDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not finish")
	}
}

func TestHTTPDrainRejectsLateHandlers(t *testing.T) {
	called := false
	server := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })}
	drain := newHTTPDrain(server)
	drain.finish()
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if called || response.Code != http.StatusServiceUnavailable {
		t.Fatalf("late request: called=%v status=%d", called, response.Code)
	}
}

func TestHTTPDrainPreservesRequestContextAndDefaultMux(t *testing.T) {
	server := &http.Server{}
	drain := newHTTPDrain(server)
	defer drain.finish()
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/bee-unregistered-lifecycle-test", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("default mux status = %d", response.Code)
	}
	type key struct{}
	original, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "connection value"))
	defer cancel()
	server = &http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Context().Value(key{}) != "connection value" {
			t.Error("request context value lost")
		}
		cancel()
		if !errors.Is(r.Context().Err(), context.Canceled) {
			t.Error("parent cancellation lost")
		}
	})}
	drain = newHTTPDrain(server)
	defer drain.finish()
	server.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(original))
}
