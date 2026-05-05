//go:build cgo && spatialite

// Build-tag-gated blank import of the spatialite backend. The default
// polystac binary is pure-Go (NF-7); the spatialite backend depends on
// CGO (mattn/go-sqlite3) and the mod_spatialite shared library, so it
// only links into a build that explicitly opts in with `-tags 'cgo
// spatialite'`. The companion `polystac-spatialite` artifact is built
// with these tags.
package app

import _ "github.com/example/polystac/internal/backends/spatialite"
