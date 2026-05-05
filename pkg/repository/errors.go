// Package repository defines PolyStac's storage-agnostic interface for
// STAC objects. Backends (pgstac, opensearch, inmem, ...) implement
// Repository; the service layer talks to nothing else for storage.
//
// All methods MUST be safe for concurrent use, MUST honor context
// cancellation, and MUST NOT mutate inputs.
package repository

import "errors"

// Sentinel errors returned by Repository implementations. Callers compare
// with errors.Is. Implementations MUST wrap these (with %w) when returning
// failures from the underlying datastore so the cause chain is preserved.
var (
	// ErrNotFound is returned when the requested resource does not exist.
	ErrNotFound = errors.New("repository: not found")

	// ErrConflict is returned when a write would collide with an existing
	// resource (duplicate ID, version conflict, etc.).
	ErrConflict = errors.New("repository: conflict")

	// ErrNotImplemented is returned when a backend does not implement an
	// optional feature that was nevertheless invoked. Callers should
	// generally avoid this path by probing capabilities and optional
	// sub-interfaces at startup; ErrNotImplemented is the runtime fallback.
	ErrNotImplemented = errors.New("repository: not implemented")

	// ErrInvalidInput indicates the request was syntactically valid but
	// semantically wrong (e.g., bbox outside [-180,180,-90,90], malformed
	// pagination token, sort field not allowed by the backend).
	ErrInvalidInput = errors.New("repository: invalid input")

	// ErrBackendUnavailable indicates a transient failure communicating
	// with the underlying datastore.
	ErrBackendUnavailable = errors.New("repository: backend unavailable")
)
