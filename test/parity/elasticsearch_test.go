//go:build integration && elasticsearch

package parity_test

import (
	"context"
	"testing"

	"github.com/example/polystac/internal/backends"
	_ "github.com/example/polystac/internal/backends/opensearch" // ES + OS share the backend package
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/test/parity"
)

// TestElasticsearchParity runs the parity corpus against an
// Elasticsearch 8.x cluster. By default it spins up a fresh ES
// container via testcontainers (xpack security disabled); set
// POLYSTAC_TEST_ES_HOSTS to point at an already-running instance.
//
// The same backend package serves OS and ES; this test only differs
// from the OS variant in the chosen image and the wait strategy.
func TestElasticsearchParity(t *testing.T) {
	hosts, user, pass := elasticsearchHosts(t)
	parity.Suite{
		Name: "elasticsearch",
		Open: func(t *testing.T) repository.Repository {
			cfg := config.Defaults()
			cfg.Backend = "elasticsearch"
			cfg.BackendConfig = map[string]string{
				"hosts":             hosts,
				"username":          user,
				"password":          pass,
				"verify_certs":      "false",
				"index_prefix":      "test_items_",
				"collections_index": "test_collections",
			}
			repo, err := backends.Open(context.Background(), "elasticsearch", cfg)
			if err != nil {
				t.Fatalf("open elasticsearch: %v", err)
			}
			return repo
		},
	}.Run(t)
}
