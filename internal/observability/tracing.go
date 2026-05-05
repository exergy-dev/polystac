package observability

import (
	"context"
	"errors"
)

// Tracer is a minimal tracing facade. PolyStac will wire OpenTelemetry
// against this interface in a follow-up — keeping the seam thin avoids
// pulling otel-sdk into the core dependency surface before there's a
// concrete configured exporter to validate against.
type Tracer interface {
	Start(ctx context.Context, name string) (context.Context, EndFunc)
}

// EndFunc closes a span. Callers must call End even on error paths.
type EndFunc func(err error)

// NoopTracer satisfies Tracer with zero overhead. Suitable when no OTLP
// endpoint is configured.
type NoopTracer struct{}

// Start is a no-op.
func (NoopTracer) Start(ctx context.Context, _ string) (context.Context, EndFunc) {
	return ctx, func(error) {}
}

// ErrTracerNotConfigured is the placeholder error returned when callers
// try to attach a real tracer before the OTLP wiring lands.
var ErrTracerNotConfigured = errors.New("observability: tracer not configured")
