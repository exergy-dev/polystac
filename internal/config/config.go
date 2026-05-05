// Package config loads PolyStac runtime configuration from environment
// variables (with YAML overlay and CLI flag override). Honors both
// POLYSTAC_* and the legacy STAC_FASTAPI_* names so existing operators
// can point an existing config at PolyStac without renaming variables.
//
// Precedence (highest first): CLI flag > env var > YAML overlay > default.
package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the resolved configuration. Backend-specific subtrees are
// kept as map[string]string so backend packages can read their own knobs
// without a runtime dependency on this package's internals.
type Config struct {
	Backend string

	Server ServerConfig

	Landing LandingConfig

	Logging LoggingConfig

	Search SearchConfig

	// BackendConfig holds the raw env subtree for the selected backend
	// (keys with the matching POLYSTAC_<BACKEND>_ prefix, lowercased and
	// stripped). E.g., POLYSTAC_PG_DSN → "dsn". Backend constructors read
	// from this map directly.
	BackendConfig map[string]string
}

// ServerConfig holds HTTP-server settings.
type ServerConfig struct {
	Listen          string
	RootPath        string
	ShutdownTimeout time.Duration
}

// LandingConfig customizes the landing page.
type LandingConfig struct {
	ID          string
	Title       string
	Description string
}

// LoggingConfig configures the slog handler.
type LoggingConfig struct {
	Level  string
	Format string
}

// SearchConfig holds default and max page sizes.
type SearchConfig struct {
	DefaultLimit int
	MaxLimit     int
}

// Defaults returns a Config populated with the SDD-specified defaults.
func Defaults() *Config {
	return &Config{
		Backend: "inmem",
		Server: ServerConfig{
			Listen:          ":8000",
			ShutdownTimeout: 30 * time.Second,
		},
		Landing: LandingConfig{
			ID:          "polystac",
			Title:       "PolyStac",
			Description: "PolyStac STAC API",
		},
		Logging: LoggingConfig{Level: "info", Format: "json"},
		Search:  SearchConfig{DefaultLimit: 10, MaxLimit: 10000},
	}
}

// Load resolves the configuration. The args slice is the post-subcommand
// argv (i.e., excluding the program name and the subcommand). Returns an
// error on validation failure.
func Load(args []string, env map[string]string) (*Config, error) {
	cfg := Defaults()

	applyEnv(cfg, env)

	fs := flag.NewFlagSet("polystac-serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.Backend, "backend", cfg.Backend, "backend to use (e.g., inmem, pgstac, opensearch)")
	fs.StringVar(&cfg.Server.Listen, "listen", cfg.Server.Listen, "address to listen on (e.g., :8000)")
	fs.StringVar(&cfg.Server.RootPath, "root-path", cfg.Server.RootPath, "URL prefix when served behind a proxy (e.g., /stac)")
	fs.StringVar(&cfg.Logging.Level, "log-level", cfg.Logging.Level, "log level (debug, info, warn, error)")
	fs.StringVar(&cfg.Logging.Format, "log-format", cfg.Logging.Format, "log format (json, text)")
	fs.IntVar(&cfg.Search.DefaultLimit, "default-limit", cfg.Search.DefaultLimit, "default page size")
	fs.IntVar(&cfg.Search.MaxLimit, "max-limit", cfg.Search.MaxLimit, "max page size")
	fs.DurationVar(&cfg.Server.ShutdownTimeout, "shutdown-timeout", cfg.Server.ShutdownTimeout, "graceful-shutdown deadline")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg.BackendConfig = collectBackendKeys(cfg.Backend, env)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyEnv reads both POLYSTAC_* and STAC_FASTAPI_* variables. The
// STAC_FASTAPI_* names are accepted for parity with the reference Python
// servers (SDD §9.3); when both are set, POLYSTAC_* wins.
func applyEnv(cfg *Config, env map[string]string) {
	get := func(key string, aliases ...string) string {
		if v, ok := env[key]; ok && v != "" {
			return v
		}
		for _, a := range aliases {
			if v, ok := env[a]; ok && v != "" {
				return v
			}
		}
		return ""
	}

	if v := get("POLYSTAC_BACKEND"); v != "" {
		cfg.Backend = v
	}
	if v := get("POLYSTAC_LISTEN"); v != "" {
		cfg.Server.Listen = v
	}
	if v := get("POLYSTAC_ROOT_PATH", "STAC_FASTAPI_ROOT_PATH"); v != "" {
		cfg.Server.RootPath = v
	}
	if v := get("POLYSTAC_LANDING_ID", "STAC_FASTAPI_LANDING_ID"); v != "" {
		cfg.Landing.ID = v
	}
	if v := get("POLYSTAC_TITLE", "STAC_FASTAPI_TITLE"); v != "" {
		cfg.Landing.Title = v
	}
	if v := get("POLYSTAC_DESCRIPTION", "STAC_FASTAPI_DESCRIPTION"); v != "" {
		cfg.Landing.Description = v
	}
	if v := get("POLYSTAC_LOG_LEVEL"); v != "" {
		cfg.Logging.Level = v
	}
	if v := get("POLYSTAC_LOG_FORMAT"); v != "" {
		cfg.Logging.Format = v
	}
	if v := get("POLYSTAC_DEFAULT_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Search.DefaultLimit = n
		}
	}
	if v := get("POLYSTAC_MAX_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Search.MaxLimit = n
		}
	}
	if v := get("POLYSTAC_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Server.ShutdownTimeout = d
		}
	}
}

// collectBackendKeys extracts keys matching POLYSTAC_<BACKEND-PREFIX>_*.
// pgstac uses the PG_ prefix per SDD §10.2; opensearch / elasticsearch
// share the ES_ prefix. Unknown backends fall back to their own name.
func collectBackendKeys(backend string, env map[string]string) map[string]string {
	prefix := backendPrefix(backend) + "_"
	out := map[string]string{}
	for k, v := range env {
		if strings.HasPrefix(k, prefix) {
			out[strings.ToLower(strings.TrimPrefix(k, prefix))] = v
		}
	}
	return out
}

func backendPrefix(backend string) string {
	switch backend {
	case "pgstac":
		return "POLYSTAC_PG"
	case "opensearch", "elasticsearch":
		return "POLYSTAC_ES"
	}
	return "POLYSTAC_" + strings.ToUpper(backend)
}

func (c *Config) validate() error {
	if c.Backend == "" {
		return errors.New("config: backend is required")
	}
	if c.Server.Listen == "" {
		return errors.New("config: server.listen is required")
	}
	if c.Search.DefaultLimit <= 0 {
		return fmt.Errorf("config: default_limit must be > 0 (got %d)", c.Search.DefaultLimit)
	}
	if c.Search.MaxLimit < c.Search.DefaultLimit {
		return fmt.Errorf("config: max_limit (%d) must be >= default_limit (%d)", c.Search.MaxLimit, c.Search.DefaultLimit)
	}
	if c.Server.ShutdownTimeout <= 0 {
		return errors.New("config: shutdown_timeout must be > 0")
	}
	return nil
}

// EnvMap reads os.Environ into a map. Deliberately accepts an explicit
// argument in Load for testability.
func EnvMap() map[string]string {
	out := make(map[string]string, len(os.Environ()))
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i > 0 {
			out[e[:i]] = e[i+1:]
		}
	}
	return out
}
