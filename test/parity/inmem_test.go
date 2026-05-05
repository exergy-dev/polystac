package parity_test

import (
	"testing"

	"github.com/example/polystac/internal/backends/inmem"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/test/parity"
)

// TestInmemParity runs the corpus against the in-memory backend.
// pgstac and opensearch backends run the same suite under their own
// integration-tagged tests (see test/parity/pgstac_test.go and
// opensearch_test.go).
func TestInmemParity(t *testing.T) {
	parity.Suite{
		Name: "inmem",
		Open: func(t *testing.T) repository.Repository { return inmem.New() },
	}.Run(t)
}
