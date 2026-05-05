// Command polystac is the PolyStac STAC API server.
//
// Subcommands:
//
//	polystac serve     — run the HTTP server (the default)
//	polystac version   — print version and exit
//
// `migrate` and `admin` arrive in Gate 2.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/polystac/internal/app"
	_ "github.com/example/polystac/internal/backends/inmem"      // register backends so migrate sees them
	_ "github.com/example/polystac/internal/backends/opensearch" // register backends so migrate sees them
	_ "github.com/example/polystac/internal/backends/pgstac"     // register backends so migrate sees them
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/internal/migrate"
)

const version = "0.0.1-contract"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "polystac:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return runServe(nil)
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "migrate":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return migrate.RunCLI(ctx, args[1:])
	case "version", "-version", "--version":
		fmt.Println("polystac", version)
		return nil
	case "help", "-h", "--help":
		printHelp()
		return nil
	default:
		// Treat bare flags as `serve` flags so `polystac -listen :9000`
		// works the same as `polystac serve -listen :9000`.
		return runServe(args)
	}
}

func runServe(args []string) error {
	cfg, err := config.Load(args, config.EnvMap())
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv, _, cleanup, err := app.Build(ctx, cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	httpSrv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintln(os.Stderr, "polystac listening on", cfg.Server.Listen, "backend="+cfg.Backend)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	}
}

func printHelp() {
	fmt.Println(`polystac — STAC API server

Usage:
  polystac [serve] [flags]
  polystac version
  polystac help

Flags (serve):
  -backend       backend to use (inmem|pgstac|opensearch)  [POLYSTAC_BACKEND]
  -listen        address to listen on                       [POLYSTAC_LISTEN]
  -root-path     URL prefix when behind a proxy             [POLYSTAC_ROOT_PATH]
  -log-level     debug|info|warn|error                      [POLYSTAC_LOG_LEVEL]
  -log-format    json|text                                  [POLYSTAC_LOG_FORMAT]
  -default-limit page size default                          [POLYSTAC_DEFAULT_LIMIT]
  -max-limit     page size max                              [POLYSTAC_MAX_LIMIT]

See ARCHITECTURE.md and the SDD for the full configuration surface.`)
}
