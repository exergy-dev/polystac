package migrate

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/example/polystac/internal/backends"
	"github.com/example/polystac/internal/config"
)

// RunCLI is the entry point for `polystac migrate`. It is invoked from
// cmd/polystac when args[0] == "migrate"; the remaining args are the
// migrate-subcommand flags.
func RunCLI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("polystac-migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		fromBackend   string
		toBackend     string
		fromEnv       envFlag
		toEnv         envFlag
		colsCSV       string
		batch, work   int
		resume        string
		sample        int
	)
	fs.StringVar(&fromBackend, "from", "", "source backend (inmem|pgstac|opensearch|elasticsearch)")
	fs.StringVar(&toBackend, "to", "", "destination backend")
	fs.Var(&fromEnv, "from-env", "src env override KEY=VALUE (repeat); applied as POLYSTAC_<BACKEND>_<KEY> would be")
	fs.Var(&toEnv, "to-env", "dest env override KEY=VALUE (repeat)")
	fs.StringVar(&colsCSV, "collections", "", "comma-separated collection IDs to migrate (default: all)")
	fs.IntVar(&batch, "batch-size", 500, "items per BulkUpsertItems call")
	fs.IntVar(&work, "workers", 4, "concurrent batches per collection")
	fs.StringVar(&resume, "resume", "", "JSON file path to persist per-collection cursors between batches")
	fs.IntVar(&sample, "sample-verify", 0, "after migration, re-fetch this many items per collection and diff source vs destination")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fromBackend == "" || toBackend == "" {
		fs.PrintDefaults()
		return fmt.Errorf("migrate: --from and --to are required")
	}

	srcCfg := buildSubConfig(fromBackend, fromEnv)
	dstCfg := buildSubConfig(toBackend, toEnv)

	src, err := backends.Open(ctx, fromBackend, srcCfg)
	if err != nil {
		return fmt.Errorf("migrate: open source: %w", err)
	}
	defer src.Close()
	dst, err := backends.Open(ctx, toBackend, dstCfg)
	if err != nil {
		return fmt.Errorf("migrate: open destination: %w", err)
	}
	defer dst.Close()

	res, err := Run(ctx, Options{
		Source:       src,
		Destination:  dst,
		Collections:  splitCSV(colsCSV),
		BatchSize:    batch,
		Workers:      work,
		ResumePath:   resume,
		SampleVerify: sample,
		Logf:         func(f string, a ...any) { fmt.Fprintf(os.Stderr, "migrate: "+f+"\n", a...) },
	})
	if err != nil {
		return err
	}
	for col, cr := range res.Collections {
		fmt.Fprintf(os.Stderr, "  %s: read=%d written=%d failed=%d in %s\n", col, cr.Read, cr.Written, cr.Failed, cr.Duration)
	}
	if len(res.VerifyMismatches) > 0 {
		for _, m := range res.VerifyMismatches {
			fmt.Fprintf(os.Stderr, "  VERIFY MISMATCH %s/%s: %s\n", m.Collection, m.ItemID, m.Reason)
		}
		return fmt.Errorf("migrate: %d sample-verify mismatches", len(res.VerifyMismatches))
	}
	return nil
}

func buildSubConfig(backend string, override envFlag) *config.Config {
	cfg := config.Defaults()
	cfg.Backend = backend
	cfg.BackendConfig = map[string]string{}
	for k, v := range override {
		cfg.BackendConfig[strings.ToLower(k)] = v
	}
	// Also accept POLYSTAC_<BACKEND>_* from the ambient env so
	// operators can keep their existing configuration.
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key, val := kv[:eq], kv[eq+1:]
		prefix := backendEnvPrefix(backend) + "_"
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		short := strings.ToLower(strings.TrimPrefix(key, prefix))
		if _, taken := cfg.BackendConfig[short]; taken {
			continue
		}
		cfg.BackendConfig[short] = val
	}
	return cfg
}

func backendEnvPrefix(backend string) string {
	switch backend {
	case "pgstac":
		return "POLYSTAC_PG"
	case "opensearch", "elasticsearch":
		return "POLYSTAC_ES"
	}
	return "POLYSTAC_" + strings.ToUpper(backend)
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// envFlag implements flag.Value to collect repeated KEY=VALUE pairs.
type envFlag map[string]string

func (e *envFlag) String() string {
	if e == nil {
		return ""
	}
	pairs := make([]string, 0, len(*e))
	for k, v := range *e {
		pairs = append(pairs, k+"="+v)
	}
	return strings.Join(pairs, ",")
}

func (e *envFlag) Set(s string) error {
	if *e == nil {
		*e = envFlag{}
	}
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return fmt.Errorf("expected KEY=VALUE, got %q", s)
	}
	(*e)[s[:eq]] = s[eq+1:]
	return nil
}
