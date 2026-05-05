package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPMiddlewareAndExposition(t *testing.T) {
	m := NewMetrics()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	wrapped := m.HTTPMiddleware("inmem")(mux)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest("GET", "/a", nil))
	if rec.Code != 204 {
		t.Fatalf("status %d", rec.Code)
	}

	out := httptest.NewRecorder()
	m.Handler().ServeHTTP(out, httptest.NewRequest("GET", "/metrics", nil))
	if out.Code != 200 {
		t.Fatalf("metrics status %d", out.Code)
	}
	body := out.Body.String()
	if !strings.Contains(body, "polystac_request_duration_seconds") {
		t.Errorf("missing histogram: %s", body[:min(400, len(body))])
	}
	if !strings.Contains(body, `backend="inmem"`) {
		t.Errorf("missing backend label: %s", body[:min(400, len(body))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
