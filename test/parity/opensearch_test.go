//go:build integration && opensearch

package parity_test

import (
	"context"
	"testing"

	"github.com/example/polystac/internal/backends"
	_ "github.com/example/polystac/internal/backends/opensearch"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/test/parity"
)

// TestOpenSearchParity runs the parity corpus against OpenSearch. By
// default it spins up a fresh OpenSearch container via testcontainers
// (security plugin disabled); set POLYSTAC_TEST_ES_HOSTS to point at an
// already-running instance and skip the spin-up.
func TestOpenSearchParity(t *testing.T) {
	hosts, user, pass := openSearchHosts(t)
	parity.Suite{
		Name: "opensearch",
		Open: func(t *testing.T) repository.Repository {
			cfg := config.Defaults()
			cfg.Backend = "opensearch"
			cfg.BackendConfig = map[string]string{
				"hosts":             hosts,
				"username":          user,
				"password":          pass,
				"verify_certs":      "false",
				"index_prefix":      "test_items_",
				"collections_index": "test_collections",
			}
			repo, err := backends.Open(context.Background(), "opensearch", cfg)
			if err != nil {
				t.Fatalf("open opensearch: %v", err)
			}
			return repo
		},
	}.Run(t)
}
