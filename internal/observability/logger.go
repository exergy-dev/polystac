package observability

import (
	"log/slog"
	"os"
	"strings"
)

// LoggerOptions configures the slog handler.
type LoggerOptions struct {
	Level  string
	Format string // "json" (default) or "text"
}

// NewLogger constructs a *slog.Logger wired to stderr with the requested
// level and format. Any unrecognized value falls through to the defaults
// (info, json) so misconfiguration cannot silence logs.
func NewLogger(o LoggerOptions) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(o.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(o.Format, "text") {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(h)
}
