//go:build integration

// Testcontainers helpers used by the integration-tagged parity tests.
//
// Each helper returns a connection target plus a t.Cleanup-registered
// teardown so the container is removed at the end of the test. The
// helpers honor a fast-path env override (POLYSTAC_TEST_PG_DSN,
// POLYSTAC_TEST_ES_HOSTS) so a developer with an already-running
// service can skip the container spin-up.
//
// Default images:
//
//	pgstac        ghcr.io/stac-utils/pgstac:v0.8.5
//	opensearch    opensearchproject/opensearch:2.13.0
//	elasticsearch docker.elastic.co/elasticsearch/elasticsearch:8.11.0
//
// Override with POLYSTAC_TEST_{PGSTAC,OPENSEARCH,ELASTICSEARCH}_IMAGE.

package parity_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver for wait.ForSQL
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/elasticsearch"
	"github.com/testcontainers/testcontainers-go/modules/opensearch"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	defaultPgstacImage        = "ghcr.io/stac-utils/pgstac:v0.8.5"
	defaultOpenSearchImage    = "opensearchproject/opensearch:2.13.0"
	defaultElasticsearchImage = "docker.elastic.co/elasticsearch/elasticsearch:8.11.0"

	containerStartupTimeout = 3 * time.Minute
)

// pgstacDSN returns a DSN to a running pgstac. Honors
// POLYSTAC_TEST_PG_DSN; otherwise spins up a pgstac container and
// registers cleanup with t.
func pgstacDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("POLYSTAC_TEST_PG_DSN"); dsn != "" {
		return dsn
	}
	if testing.Short() {
		t.Skip("short mode: skipping container-backed pgstac test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), containerStartupTimeout)
	defer cancel()

	image := envOr("POLYSTAC_TEST_PGSTAC_IMAGE", defaultPgstacImage)
	// pgstac's official image runs pypgstac migrate during init; the
	// database restarts once. The "ready to accept connections" log
	// appears twice — once before the restart and once after — so we
	// wait for the post-restart occurrence AND additionally for a
	// successful SQL probe so the test never connects mid-restart.
	ctr, err := postgres.Run(ctx, image,
		postgres.WithDatabase("postgis"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(containerStartupTimeout),
				wait.ForSQL("5432/tcp", "pgx", func(host string, port string) string {
					// port arrives as "5432/tcp"; strip the proto suffix.
					if i := strings.IndexByte(port, '/'); i > 0 {
						port = port[:i]
					}
					return "postgres://postgres:postgres@" + host + ":" + port + "/postgis?sslmode=disable"
				}).WithStartupTimeout(containerStartupTimeout),
			).WithStartupTimeout(containerStartupTimeout),
		),
	)
	if err != nil {
		t.Fatalf("pgstac container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("pgstac terminate: %v", err)
		}
	})
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("pgstac dsn: %v", err)
	}
	return dsn
}

// openSearchHosts returns (hosts, username, password) for a running OS.
// Honors POLYSTAC_TEST_ES_HOSTS; otherwise spins up an OpenSearch
// container with security disabled (single-node, no TLS) and registers
// cleanup with t.
func openSearchHosts(t *testing.T) (hosts, user, pass string) {
	t.Helper()
	if hosts := os.Getenv("POLYSTAC_TEST_ES_HOSTS"); hosts != "" {
		return hosts, os.Getenv("POLYSTAC_TEST_ES_USERNAME"), os.Getenv("POLYSTAC_TEST_ES_PASSWORD")
	}
	if testing.Short() {
		t.Skip("short mode: skipping container-backed opensearch test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), containerStartupTimeout)
	defer cancel()

	image := envOr("POLYSTAC_TEST_OPENSEARCH_IMAGE", defaultOpenSearchImage)
	// The OpenSearch testcontainers module already configures
	// single-node mode and disables the security plugin; we only
	// override the wait strategy for the slower-than-default startup.
	ctr, err := opensearch.Run(ctx, image,
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/").
				WithPort("9200/tcp").
				WithStartupTimeout(containerStartupTimeout),
		),
	)
	if err != nil {
		t.Fatalf("opensearch container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("opensearch terminate: %v", err)
		}
	})
	addr, err := ctr.Address(ctx)
	if err != nil {
		t.Fatalf("opensearch address: %v", err)
	}
	return addr, "", "" // security disabled → no creds
}

// elasticsearchHosts returns connection details for a running ES.
// Honors POLYSTAC_TEST_ES_HOSTS as a shared override (since the
// PolyStac backend is the same code path for ES and OS); otherwise
// spins up an Elasticsearch container with TLS disabled.
func elasticsearchHosts(t *testing.T) (hosts, user, pass string) {
	t.Helper()
	if hosts := os.Getenv("POLYSTAC_TEST_ES_HOSTS"); hosts != "" {
		return hosts, os.Getenv("POLYSTAC_TEST_ES_USERNAME"), os.Getenv("POLYSTAC_TEST_ES_PASSWORD")
	}
	if testing.Short() {
		t.Skip("short mode: skipping container-backed elasticsearch test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), containerStartupTimeout)
	defer cancel()

	image := envOr("POLYSTAC_TEST_ELASTICSEARCH_IMAGE", defaultElasticsearchImage)
	ctr, err := elasticsearch.Run(ctx, image,
		// Disable security for the single-node test cluster; CI doesn't
		// provision a CA. Production deployments use TLS.
		testcontainers.WithEnv(map[string]string{
			"discovery.type":         "single-node",
			"xpack.security.enabled": "false",
		}),
	)
	if err != nil {
		t.Fatalf("elasticsearch container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("elasticsearch terminate: %v", err)
		}
	})
	// Security disabled → username/password ignored, plain HTTP.
	return ctr.Settings.Address, "", ""
}

// httpClientInsecure returns an http.Client that skips TLS verification.
// Used by the OS/ES helpers when self-signed certs are in play.
func httpClientInsecure() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
		Timeout: 30 * time.Second,
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
