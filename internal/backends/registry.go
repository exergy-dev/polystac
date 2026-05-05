// Package backends holds the runtime registry of Repository
// implementations. Each backend package (pgstac, opensearch, inmem, ...)
// registers a constructor at init time; the service layer then looks up
// the configured backend by name (POLYSTAC_BACKEND) and constructs it
// with the parsed config.
//
// All backends are compiled into the binary regardless of which one is
// selected at runtime. Unused ones are dead-code-eliminated by the linker
// (SDD §6).
package backends

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/example/polystac/pkg/repository"
)

// Constructor is the signature every backend exposes to the registry.
//
// The cfg parameter is the raw, parsed configuration tree (typically a
// *config.Config); each backend reads only the keys it cares about.
// We deliberately type cfg as `any` here to avoid an import cycle on
// internal/config — the only typed contract the registry enforces is
// the result type.
type Constructor func(ctx context.Context, cfg any) (repository.Repository, error)

var (
	regMu sync.RWMutex
	reg   = map[string]Constructor{}
)

// Register adds a backend constructor to the registry. Intended to be
// called from a backend package's init() function. Panics on a duplicate
// name — at init time, that's a programming error worth a hard fail.
func Register(name string, c Constructor) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, exists := reg[name]; exists {
		panic(fmt.Sprintf("backends: duplicate registration for %q", name))
	}
	if c == nil {
		panic(fmt.Sprintf("backends: nil constructor for %q", name))
	}
	reg[name] = c
}

// Open looks up a backend constructor by name and invokes it.
func Open(ctx context.Context, name string, cfg any) (repository.Repository, error) {
	regMu.RLock()
	c, ok := reg[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("backends: unknown backend %q (registered: %v)", name, Names())
	}
	return c(ctx, cfg)
}

// Names returns the registered backend names in sorted order.
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(reg))
	for n := range reg {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
