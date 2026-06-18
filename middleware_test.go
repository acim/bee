package bee

import (
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
