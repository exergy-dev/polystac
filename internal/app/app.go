// Package app is the top-level wiring: it composes config → backend →
// server into something cmd/polystac (and cmd/polystac-lambda) can run.
//
// Keeping wiring out of cmd/polystac means it is also reachable from
// integration tests without forking a process.
package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/example/polystac/internal/backends"
	_ "github.com/example/polystac/internal/backends/inmem"      // register inmem backend
	_ "github.com/example/polystac/internal/backends/opensearch" // register opensearch + elasticsearch backends
	_ "github.com/example/polystac/internal/backends/pgstac"     // register pgstac backend
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/internal/observability"
	"github.com/example/polystac/internal/server"
	"github.com/example/polystac/pkg/repository"
)

// Build constructs an http.Handler and the underlying repository from
// the resolved configuration. Caller is responsible for serving and for
// calling the returned cleanup func at shutdown.
func Build(ctx context.Context, cfg *config.Config) (*server.Server, repository.Repository, func() error, error) {
	logger := observability.NewLogger(observability.LoggerOptions{
		Level:  cfg.Logging.Level,
		Format: cfg.Logging.Format,
	})

	repo, err := backends.Open(ctx, cfg.Backend, cfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open backend %q: %w", cfg.Backend, err)
	}

	metrics := observability.NewMetrics()

	srv, err := server.New(server.Options{
		Repo:         repo,
		Logger:       logger,
		RootPath:     cfg.Server.RootPath,
		LandingID:    cfg.Landing.ID,
		LandingTitle: cfg.Landing.Title,
		LandingDesc:  cfg.Landing.Description,
		DefaultLimit: cfg.Search.DefaultLimit,
		MaxLimit:     cfg.Search.MaxLimit,
		Metrics:      metrics,
		Middleware: []func(http.Handler) http.Handler{
			metrics.HTTPMiddleware(repo.Capabilities().Backend),
		},
	})
	if err != nil {
		_ = repo.Close()
		return nil, nil, nil, err
	}

	cleanup := func() error { return repo.Close() }
	return srv, repo, cleanup, nil
}
