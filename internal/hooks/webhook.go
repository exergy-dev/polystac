// Package hooks provides the out-of-process hook delivery mechanisms
// (HTTP webhook, AWS Lambda) that operators wire via configuration. The
// in-process Go API lives in pkg/polystac/hook; both flavors collapse
// to the Hook middleware shape consumed by server.Options.Middleware.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/example/polystac/pkg/polystac/hook"
)

// HTTPWebhook returns a pre-hook that POSTs the inbound request to the
// given URL and decides what to do with the result.
//
// Wire format: a JSON body with `method`, `path`, `query`, `headers`,
// `body` (base64-omitted-when-empty). The webhook responds with one of:
//
//   - 200 + a JSON body to short-circuit ({status:int, headers:map,
//     body:string}). PolyStac copies the response to the client and the
//     real handler is not invoked.
//   - 200 with no body — proceed to the real handler unmodified.
//   - 204 — proceed.
//   - any other status — short-circuit with that status and the body
//     verbatim. Useful for 401/403 responses.
//
// Latency: each request adds one extra HTTP round-trip. The SDD
// recommends in-process Go hooks for self-hosted deployments and
// reserves this mode for migration from stac-server's Lambda hooks.
func HTTPWebhook(url string, opts ...WebhookOption) hook.Hook {
	cfg := webhookConfig{
		client: &http.Client{Timeout: 5 * time.Second},
	}
	for _, o := range opts {
		o(&cfg)
	}

	return hook.PreFunc(func(w http.ResponseWriter, r *http.Request) *http.Request {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		_ = r.Body.Close()
		// Restore body so the downstream handler can read it.
		r.Body = io.NopCloser(bytes.NewReader(body))

		envelope := webhookRequest{
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.Query(),
			Headers: r.Header,
			Body:    string(body),
		}
		payload, _ := json.Marshal(envelope)

		ctx, cancel := context.WithTimeout(r.Context(), cfg.client.Timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
		if err != nil {
			return shortCircuit(w, http.StatusInternalServerError, []byte(err.Error()))
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := cfg.client.Do(req)
		if err != nil {
			return shortCircuit(w, http.StatusBadGateway, []byte(fmt.Sprintf("hook %s: %v", url, err)))
		}
		defer resp.Body.Close()

		// 204: pass through unchanged.
		if resp.StatusCode == http.StatusNoContent {
			return r
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

		// Non-200: short-circuit with that status.
		if resp.StatusCode != http.StatusOK {
			return shortCircuit(w, resp.StatusCode, respBody)
		}

		// 200 with empty body: proceed.
		if len(bytes.TrimSpace(respBody)) == 0 {
			return r
		}

		var rewrite webhookResponse
		if err := json.Unmarshal(respBody, &rewrite); err != nil {
			// 200 + non-JSON body: short-circuit with that body.
			return shortCircuit(w, http.StatusOK, respBody)
		}
		for k, vs := range rewrite.Headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		status := rewrite.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(rewrite.Body))
		return nil
	})
}

func shortCircuit(w http.ResponseWriter, status int, body []byte) *http.Request {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	return nil
}

// WebhookOption tunes a webhook hook.
type WebhookOption func(*webhookConfig)

// WithClient overrides the default *http.Client.
func WithClient(c *http.Client) WebhookOption {
	return func(cfg *webhookConfig) {
		if c != nil {
			cfg.client = c
		}
	}
}

type webhookConfig struct {
	client *http.Client
}

type webhookRequest struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string][]string `json:"query"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body,omitempty"`
}

type webhookResponse struct {
	Status  int                 `json:"status,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
}

// ErrNotImplemented is returned by stub backends (e.g. Lambda before its
// adapter ships) so operators get a clear startup error.
var ErrNotImplemented = errors.New("hooks: delivery mechanism not yet implemented")
