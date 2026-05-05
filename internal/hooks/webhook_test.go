package hooks_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/polystac/internal/hooks"
	"github.com/example/polystac/pkg/polystac/hook"
)

func TestWebhookPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	wrapped := hooks.HTTPWebhook(upstream.URL)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok-from-handler"))
	}))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ok-from-handler") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWebhookShortCircuitOn401(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"AuthError","description":"nope"}`))
	}))
	defer upstream.Close()

	called := false
	wrapped := hooks.HTTPWebhook(upstream.URL)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Errorf("status: %d", rec.Code)
	}
	if called {
		t.Error("downstream handler should not have been called")
	}
}

func TestPostHookRewritesBody(t *testing.T) {
	h := hook.PostFunc(func(_ http.Header, body []byte) []byte {
		return []byte(strings.ReplaceAll(string(body), "secret", "REDACTED"))
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"k":"secret","v":"secret-too"}`))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	body, _ := io.ReadAll(rec.Body)
	if strings.Contains(string(body), "secret") {
		t.Errorf("post hook did not redact: %s", body)
	}
}
