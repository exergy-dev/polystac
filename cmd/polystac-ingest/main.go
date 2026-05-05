// Command polystac-ingest streams STAC items into the configured
// backend. Source is selected by --source: stdin, dir:<path>, or sqs:
// (the last only with the `aws` build tag).
//
// All backend configuration (POLYSTAC_BACKEND, POLYSTAC_PG_DSN, …) is
// read from the environment exactly as in the main `polystac` binary,
// so operators reuse one config schema across both processes.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/example/polystac/internal/backends"
	_ "github.com/example/polystac/internal/backends/inmem"
	_ "github.com/example/polystac/internal/backends/opensearch"
	_ "github.com/example/polystac/internal/backends/pgstac"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/internal/ingest"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "polystac-ingest:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("polystac-ingest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	source := fs.String("source", "stdin", `source: "stdin", "dir:<path>", or "sqs:<queue-url>" (sqs requires the "aws" build tag)`)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(nil, config.EnvMap())
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	dst, err := backends.Open(ctx, cfg.Backend, cfg)
	if err != nil {
		return fmt.Errorf("open backend %q: %w", cfg.Backend, err)
	}
	defer dst.Close()

	recv, err := receiverFor(*source)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "polystac-ingest: %s → backend=%s\n", recv.Name(), cfg.Backend)
	res, err := ingest.Run(ctx, recv, dst)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "polystac-ingest: succeeded=%d failed=%d\n", res.Succeeded, res.Failed)
	if res.Failed > 0 {
		for id, e := range res.Errors {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", id, e)
		}
		return fmt.Errorf("ingest: %d failures", res.Failed)
	}
	return nil
}

func receiverFor(source string) (ingest.Receiver, error) {
	switch {
	case source == "stdin":
		return ingest.StdinReceiver{}, nil
	case strings.HasPrefix(source, "dir:"):
		return ingest.DirReceiver{Path: strings.TrimPrefix(source, "dir:")}, nil
	case strings.HasPrefix(source, "sqs:"):
		return sqsReceiver(strings.TrimPrefix(source, "sqs:"))
	}
	return nil, fmt.Errorf("ingest: unknown source %q (want stdin|dir:<path>|sqs:<queue-url>)", source)
}
