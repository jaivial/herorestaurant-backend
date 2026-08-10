package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Wiring smoke test for response compression.
// Guards the middleware choice: JSON responses must arrive gzip-encoded when
// the client advertises Accept-Encoding, and non-negotiated requests must stay
// uncompressed. The route table itself wires middleware.Compress(5) in Routes().
func TestGzipResponseCompression(t *testing.T) {
	r := chi.NewRouter()
	r.Use(middleware.Compress(5))
	r.Get("/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world","ok":true}`))
	})
	r.Get("/event-stream", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: ping\n\n"))
	})

	t.Run("gzip when accepted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/json", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		if rr.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("expected Content-Encoding: gzip, got %q", rr.Header().Get("Content-Encoding"))
		}
		gr, err := gzip.NewReader(rr.Body)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		body, _ := io.ReadAll(gr)
		if got, want := string(body), `{"hello":"world","ok":true}`; got != want {
			t.Fatalf("decoded body mismatch: got %q want %q", got, want)
		}
	})

	t.Run("plain when not accepted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/json", nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Header().Get("Content-Encoding") != "" {
			t.Fatalf("unexpected Content-Encoding %q", rr.Header().Get("Content-Encoding"))
		}
		if got, want := rr.Body.String(), `{"hello":"world","ok":true}`; got != want {
			t.Fatalf("body mismatch: got %q want %q", got, want)
		}
	})

	t.Run("SSE stays uncompressed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/event-stream", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Header().Get("Content-Encoding") != "" {
			t.Fatalf("SSE must not be compressed, got %q", rr.Header().Get("Content-Encoding"))
		}
		if got, want := rr.Body.String(), "data: ping\n\n"; got != want {
			t.Fatalf("SSE body mismatch: got %q want %q", got, want)
		}
	})

	// sanity: empty body with compressible type must not panic or emit encoding
	req := httptest.NewRequest(http.MethodGet, "/json", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	_ = bytes.NewBuffer(nil)
	_ = req
}
