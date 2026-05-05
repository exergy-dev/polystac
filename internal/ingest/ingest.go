// Package ingest implements the polystac-ingest companion binary's
// core: a streaming pipeline from a Receiver (source of STAC items) to
// a backend Repository's BulkUpsertItems.
//
// Receiver implementations pluggable behind build tags:
//
//   - default — stdin (one JSON Item per line) and a directory-of-files
//     receiver. Always available.
//   - SQS — behind the `aws` build tag; uses aws-sdk-go-v2/service/sqs.
//     Mirrors the stac-server SNS/SQS deployment shape (SDD §9.4) so
//     operators can swap the consumer without changing topics/queues.
//
// The Receiver abstraction means the same ingest binary serves
// development (stdin), batch jobs (directory), and production (SQS).
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"

	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

// Receiver is the source of STAC items for the ingest pipeline.
//
// Receive returns an iterator over (item, error) pairs. The error in
// the pair lets the source signal a per-item parse failure without
// aborting the stream; a hard receiver-level error returns from the
// outer call.
type Receiver interface {
	// Name is a short human label for the source (used in logs).
	Name() string
	// Receive blocks until the source is exhausted (e.g., stdin EOF) or
	// ctx is canceled. The returned iterator MUST be consumed to
	// completion.
	Receive(ctx context.Context) (iter.Seq2[*stac.Item, error], error)
}

// Run pipes items from the receiver into the destination Repository's
// BulkUpsertItems and returns the aggregate result.
func Run(ctx context.Context, recv Receiver, dst repository.Repository) (*repository.BulkResult, error) {
	if recv == nil {
		return nil, errors.New("ingest: receiver required")
	}
	seq, err := recv.Receive(ctx)
	if err != nil {
		return nil, fmt.Errorf("ingest: %s: %w", recv.Name(), err)
	}
	return dst.BulkUpsertItems(ctx, seq)
}

// ---- stdin receiver -----------------------------------------------------

// StdinReceiver reads NDJSON (one Item per line) from io.Reader.
type StdinReceiver struct {
	R     io.Reader
	Label string
}

// Name implements Receiver.
func (s StdinReceiver) Name() string {
	if s.Label != "" {
		return s.Label
	}
	return "stdin"
}

// Receive parses one Item per line until EOF.
func (s StdinReceiver) Receive(ctx context.Context) (iter.Seq2[*stac.Item, error], error) {
	r := s.R
	if r == nil {
		r = os.Stdin
	}
	dec := json.NewDecoder(r)
	return func(yield func(*stac.Item, error) bool) {
		for {
			if ctx.Err() != nil {
				return
			}
			var it stac.Item
			err := dec.Decode(&it)
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				if !yield(nil, err) {
					return
				}
				continue
			}
			cp := it
			if !yield(&cp, nil) {
				return
			}
		}
	}, nil
}

// ---- directory receiver -------------------------------------------------

// DirReceiver walks a directory tree and emits each .json file as an Item.
// Errors reading or parsing a file surface in the per-item error slot
// rather than aborting the walk.
type DirReceiver struct {
	Path string
}

// Name implements Receiver.
func (d DirReceiver) Name() string { return "dir:" + d.Path }

// Receive walks d.Path.
func (d DirReceiver) Receive(ctx context.Context) (iter.Seq2[*stac.Item, error], error) {
	if d.Path == "" {
		return nil, errors.New("ingest: dir receiver requires a path")
	}
	info, err := os.Stat(d.Path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("ingest: %s is not a directory", d.Path)
	}
	return func(yield func(*stac.Item, error) bool) {
		_ = filepath.WalkDir(d.Path, func(path string, ent os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if !yield(nil, walkErr) {
					return io.EOF
				}
				return nil
			}
			if ent.IsDir() || filepath.Ext(path) != ".json" {
				return nil
			}
			if ctx.Err() != nil {
				return io.EOF
			}
			body, err := os.ReadFile(path)
			if err != nil {
				if !yield(nil, fmt.Errorf("read %s: %w", path, err)) {
					return io.EOF
				}
				return nil
			}
			var it stac.Item
			if err := json.Unmarshal(body, &it); err != nil {
				if !yield(nil, fmt.Errorf("parse %s: %w", path, err)) {
					return io.EOF
				}
				return nil
			}
			if !yield(&it, nil) {
				return io.EOF
			}
			return nil
		})
	}, nil
}
