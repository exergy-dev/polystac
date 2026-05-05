// Package hook is the public API operators use to attach pre/post
// request hooks to a PolyStac server. It mirrors stac-server's Lambda
// hook concept (SDD §7.5) but is shaped as standard Go middleware so
// existing http.Handler patterns slot in without translation.
//
// Two flavors are supported:
//
//   - In-process Go hooks: write an http.Handler-style middleware and
//     pass it to App.PreHook / App.PostHook. Lowest latency.
//   - Out-of-process hooks: see internal/hooks for the HTTP-webhook and
//     Lambda implementations that this package consumes.
//
// Either flavor reduces to a single `Hook` value at the server boundary,
// which is what server.Options.Middleware accepts.
package hook

import "net/http"

// Hook is a request-scoped middleware. Pre and Post hooks share the same
// shape — the difference is intent, not type. Hooks compose left-to-right;
// the slice order in App is the invocation order.
type Hook func(http.Handler) http.Handler

// Chain composes hooks into a single Hook. Useful when constructing the
// pre-hook and post-hook lists separately and merging them at startup.
func Chain(hooks ...Hook) Hook {
	return func(next http.Handler) http.Handler {
		for i := len(hooks) - 1; i >= 0; i-- {
			next = hooks[i](next)
		}
		return next
	}
}

// PreFunc adapts a request-mutating function into a Hook. The function
// MUST return a non-nil request to allow the chain to continue, or write
// to ResponseWriter and return nil to short-circuit (e.g., to 401/403).
func PreFunc(fn func(http.ResponseWriter, *http.Request) *http.Request) Hook {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = fn(w, r)
			if r == nil {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PostFunc adapts a response-rewriting function into a Hook. The function
// receives the buffered response body (and headers via ResponseWriter)
// and may rewrite it before it is flushed to the client.
//
// Buffering: the entire body is held in memory before the post-hook
// runs. This is appropriate for STAC payloads (small JSON documents)
// but operators streaming very large bulk responses should think twice
// before attaching a PostFunc.
func PostFunc(fn func(http.Header, []byte) []byte) Hook {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buf := &bufferingWriter{header: w.Header().Clone(), status: 200}
			next.ServeHTTP(buf, r)
			out := fn(buf.header, buf.body)
			for k, v := range buf.header {
				w.Header()[k] = v
			}
			w.WriteHeader(buf.status)
			_, _ = w.Write(out)
		})
	}
}

type bufferingWriter struct {
	header http.Header
	status int
	body   []byte
}

func (b *bufferingWriter) Header() http.Header { return b.header }

func (b *bufferingWriter) Write(p []byte) (int, error) {
	b.body = append(b.body, p...)
	return len(p), nil
}

func (b *bufferingWriter) WriteHeader(code int) { b.status = code }
