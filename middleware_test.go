package bee

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewaresWrap(t *testing.T) {
	t.Parallel()

	var calls []string

	first := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, "first before")
			next.ServeHTTP(w, r)
			calls = append(calls, "first after")
		})
	}

	second := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, "second before")
			next.ServeHTTP(w, r)
			calls = append(calls, "second after")
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {
		calls = append(calls, "handler")
	})

	var middlewares Middlewares
	middlewares.Add(first)
	middlewares.Add(second)

	middlewares.Wrap(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"first before", "second before", "handler", "second after", "first after"}
	if len(calls) != len(want) {
		t.Fatalf("want calls %v, got %v", want, calls)
	}

	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("want calls %v, got %v", want, calls)
		}
	}
}

func TestMiddlewaresWrapEmpty(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()

	var middlewares Middlewares
	if got := middlewares.Wrap(mux); got != mux {
		t.Fatalf("want original mux, got %T", got)
	}
}

func TestSlogLogger(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))

	handler := SlogLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/things?id=42", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want status %d, got %d", http.StatusCreated, rec.Code)
	}

	if got, want := rec.Body.String(), "created"; got != want {
		t.Fatalf("want body %q, got %q", want, got)
	}

	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}

	assertLogValue(t, entry, "msg", "request completed")
	assertLogValue(t, entry, "method", http.MethodPost)
	assertLogValue(t, entry, "uri", "/things?id=42")
	assertLogValue(t, entry, "status", float64(http.StatusCreated))
	assertLogValue(t, entry, "bytes", float64(len("created")))

	if _, ok := entry["duration"]; !ok {
		t.Fatal("want duration in log entry")
	}
}

func TestSlogLoggerSkipPaths(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		path    string
		options []SlogLoggerOption
		status  int
		wantLog bool
	}{
		{name: "default logs probes", path: "/health", status: http.StatusOK, wantLog: true},
		{name: "nil paths", path: "/health", options: []SlogLoggerOption{WithSkipPaths(nil)}, status: http.StatusOK, wantLog: true},
		{name: "empty paths", path: "/health", options: []SlogLoggerOption{WithSkipPaths([]string{})}, status: http.StatusOK, wantLog: true},
		{name: "health", path: "/health", options: []SlogLoggerOption{WithSkipPaths([]string{"/health", "/ready"})}, status: http.StatusOK},
		{name: "ready", path: "/ready", options: []SlogLoggerOption{WithSkipPaths([]string{"/health", "/ready"})}, status: http.StatusOK},
		{name: "query string", path: "/health?full=true", options: []SlogLoggerOption{WithSkipPaths([]string{"/health"})}, status: http.StatusOK},
		{name: "failed probe", path: "/ready", options: []SlogLoggerOption{WithSkipPaths([]string{"/ready"})}, status: http.StatusServiceUnavailable},
		{name: "ordinary request", path: "/things", options: []SlogLoggerOption{WithSkipPaths([]string{"/health"})}, status: http.StatusCreated, wantLog: true},
		{name: "path prefix", path: "/health/details", options: []SlogLoggerOption{WithSkipPaths([]string{"/health"})}, status: http.StatusOK, wantLog: true},
		{name: "trailing slash", path: "/health/", options: []SlogLoggerOption{WithSkipPaths([]string{"/health"})}, status: http.StatusOK, wantLog: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&logs, nil))
			calls := 0
			handler := SlogLogger(log, tt.options...)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("X-Probe", "checked")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("response"))
			}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if calls != 1 || rec.Code != tt.status || rec.Body.String() != "response" || rec.Header().Get("X-Probe") != "checked" {
				t.Fatalf("handler response changed: calls=%d status=%d body=%q headers=%v", calls, rec.Code, rec.Body.String(), rec.Header())
			}

			if !tt.wantLog {
				if logs.Len() != 0 {
					t.Fatalf("want no log, got %s", logs.String())
				}
				return
			}

			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("decode log entry: %v", err)
			}
			assertLogValue(t, entry, "uri", tt.path)
			assertLogValue(t, entry, "status", float64(tt.status))
		})
	}
}

func assertLogValue(t *testing.T, entry map[string]any, key string, want any) {
	t.Helper()

	if got := entry[key]; got != want {
		t.Fatalf("want log %s=%v, got %v", key, want, got)
	}
}
