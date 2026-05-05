// Command bench-seed populates a pgstac or OpenSearch instance with
// N synthetic STAC items so the benchmark harness can run identical
// queries against PolyStac and the reference implementations on the
// same data.
//
// Usage:
//
//	bench-seed -backend pgstac     -n 10000 -dsn   postgresql://...
//	bench-seed -backend opensearch -n 10000 -hosts http://localhost:9200
package main

import (
	"context"
	"flag"
	"fmt"
	"iter"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/example/polystac/internal/backends"
	_ "github.com/example/polystac/internal/backends/opensearch"
	_ "github.com/example/polystac/internal/backends/pgstac"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/pkg/stac"
)

func main() {
	var (
		backend = flag.String("backend", "pgstac", "pgstac|opensearch|elasticsearch")
		n       = flag.Int("n", 10000, "number of items to seed")
		dsn     = flag.String("dsn", "", "POLYSTAC_PG_DSN (pgstac only)")
		hosts   = flag.String("hosts", "", "POLYSTAC_ES_HOSTS (opensearch / elasticsearch only)")
		colID   = flag.String("collection", "bench", "collection ID")
	)
	flag.Parse()

	cfg := config.Defaults()
	cfg.Backend = *backend
	cfg.BackendConfig = map[string]string{}
	switch *backend {
	case "pgstac":
		if *dsn == "" {
			fail("--dsn required for pgstac")
		}
		cfg.BackendConfig["dsn"] = *dsn
	case "opensearch", "elasticsearch":
		if *hosts == "" {
			fail("--hosts required for OpenSearch / Elasticsearch")
		}
		cfg.BackendConfig["hosts"] = *hosts
		cfg.BackendConfig["verify_certs"] = "false"
		cfg.BackendConfig["index_prefix"] = "items_"
		cfg.BackendConfig["collections_index"] = "collections"
	default:
		fail("unknown backend %q", *backend)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	repo, err := backends.Open(ctx, *backend, cfg)
	if err != nil {
		fail("open: %v", err)
	}
	defer repo.Close()

	col := &stac.Collection{
		ID:          *colID,
		Description: "benchmark synthetic collection",
		License:     "proprietary",
		Extent: stac.Extent{
			Spatial:  stac.SpatialExtent{BBox: [][]float64{{-180, -90, 180, 90}}},
			Temporal: stac.TemporalExtent{Interval: [][]*string{{nil, nil}}},
		},
	}
	if err := repo.UpsertCollection(ctx, col); err != nil {
		fail("upsert collection: %v", err)
	}

	fmt.Fprintf(os.Stderr, "seeding %d items into %s...\n", *n, *backend)
	start := time.Now()

	// Deterministic PRNG so the same seed reproduces the same fixture
	// across runs and across backends.
	rng := rand.New(rand.NewSource(1))
	platforms := []string{"S2A", "S2B", "L8", "L9", "MODIS"}
	stream := iter.Seq2[*stac.Item, error](func(yield func(*stac.Item, error) bool) {
		for i := 0; i < *n; i++ {
			lon := -180 + rng.Float64()*360
			lat := -90 + rng.Float64()*180
			t := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rng.Intn(365*86400)) * time.Second)
			it := &stac.Item{
				ID:         fmt.Sprintf("item-%07d", i),
				Collection: *colID,
				Geometry:   &stac.Geometry{Type: stac.GeometryPoint, Coordinates: []float64{lon, lat}},
				BBox:       []float64{lon, lat, lon, lat},
				Properties: stac.ItemProperties{
					"datetime":       t.Format(time.RFC3339),
					"eo:cloud_cover": rng.Float64() * 100,
					"platform":       platforms[rng.Intn(len(platforms))],
				},
			}
			if !yield(it, nil) {
				return
			}
		}
	})
	res, err := repo.BulkUpsertItems(ctx, stream)
	if err != nil {
		fail("bulk upsert: %v", err)
	}
	fmt.Fprintf(os.Stderr, "succeeded=%d failed=%d in %s\n", res.Succeeded, res.Failed, time.Since(start).Round(time.Millisecond))
	if res.Failed > 0 {
		for id, e := range res.Errors {
			if strings.Count(id, "") > 32 {
				continue
			}
			fmt.Fprintf(os.Stderr, "  %s: %v\n", id, e)
		}
		os.Exit(1)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bench-seed: "+format+"\n", args...)
	os.Exit(1)
}
