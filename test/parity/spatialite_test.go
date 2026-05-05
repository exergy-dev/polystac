//go:build cgo && spatialite

package parity_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/polystac/internal/backends"
	_ "github.com/example/polystac/internal/backends/spatialite"
	"github.com/example/polystac/internal/config"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/test/parity"
)

// TestSpatialiteParity runs the parity corpus against a fresh
// SpatiaLite-backed repository in a temp directory.
//
// Build constraints: requires `-tags 'cgo spatialite'` and
// mod_spatialite installed at runtime. Skips with a clear message when
// the extension cannot be loaded so CI on a host without the package
// reports SKIP rather than FAIL.
func TestSpatialiteParity(t *testing.T) {
	dir := t.TempDir()
	parity.Suite{
		Name: "spatialite",
		Open: func(t *testing.T) repository.Repository {
			cfg := config.Defaults()
			cfg.Backend = "spatialite"
			cfg.BackendConfig = map[string]string{
				"database": filepath.Join(dir, "stac.db"),
			}
			repo, err := backends.Open(context.Background(), "spatialite", cfg)
			if err != nil {
				if strings.Contains(err.Error(), "mod_spatialite") {
					t.Skip("mod_spatialite not available: " + err.Error())
				}
				t.Fatalf("open spatialite: %v", err)
			}
			return repo
		},
	}.Run(t)
}
