package repository

import (
	"context"
	"iter"

	"github.com/example/polystac/pkg/stac"
)

// Repository is the storage-agnostic interface every backend implements.
//
// Concurrency: implementations MUST be safe for concurrent use across
// goroutines.
//
// Cancellation: implementations MUST honor ctx and abort in-flight work
// when ctx is canceled.
//
// Mutation: implementations MUST NOT retain or mutate input pointers
// after the call returns.
//
// Errors: implementations MUST return the sentinel errors from this
// package (ErrNotFound, ErrConflict, ...) — wrapped with %w if a more
// specific cause is available — so the service layer can map them to
// HTTP status codes without backend-specific knowledge.
type Repository interface {
	// ---- Read path -------------------------------------------------------

	GetCollection(ctx context.Context, id string) (*stac.Collection, error)
	ListCollections(ctx context.Context, opts ListCollectionsOptions) (*Page[*stac.Collection], error)

	GetItem(ctx context.Context, collectionID, itemID string) (*stac.Item, error)
	Search(ctx context.Context, req SearchRequest) (*Page[*stac.Item], error)

	// ---- Write path (Transaction extension) ------------------------------

	UpsertCollection(ctx context.Context, c *stac.Collection) error
	DeleteCollection(ctx context.Context, id string) error

	UpsertItem(ctx context.Context, item *stac.Item) error
	DeleteItem(ctx context.Context, collectionID, itemID string) error

	// BulkUpsertItems streams items from the provided range-over-func
	// iterator and reports an aggregate result. The iterator is consumed
	// once; implementations MUST NOT call it after returning.
	BulkUpsertItems(ctx context.Context, items iter.Seq2[*stac.Item, error]) (*BulkResult, error)

	// ---- Capability discovery --------------------------------------------

	Capabilities() Capabilities

	// Health performs a cheap connectivity check and is called by /_health
	// and /_ready handlers. It MUST be cheaper than a full Search round-trip.
	Health(ctx context.Context) error

	// Close releases any held resources (connection pools, HTTP transports).
	Close() error
}

// Aggregator is the optional sub-interface implemented by backends that
// support the Aggregation extension. The service layer probes for it via
// type assertion at startup; if absent, /aggregate routes are not wired.
type Aggregator interface {
	Aggregate(ctx context.Context, req AggregationRequest) (*AggregationResponse, error)
}

// Queryables is the optional sub-interface implemented by backends that
// can enumerate filterable fields per collection (Filter extension).
type Queryables interface {
	Queryables(ctx context.Context, collectionID string) (*QueryablesDocument, error)
}
