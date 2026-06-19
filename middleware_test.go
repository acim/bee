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

func assertLogValue(t *testing.T, entry map[string]any, key string, want any) {
	t.Helper()

	if got := entry[key]; got != want {
		t.Fatalf("want log %s=%v, got %v", key, want, got)
	}
}
