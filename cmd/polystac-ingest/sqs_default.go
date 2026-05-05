//go:build !aws

package main

import (
	"errors"

	"github.com/example/polystac/internal/ingest"
)

// sqsReceiver is the default (non-aws build) stub. Build with
// `-tags aws` to compile in the real SQS adapter.
func sqsReceiver(_ string) (ingest.Receiver, error) {
	return nil, errors.New("sqs source requires the 'aws' build tag (rebuild with: go build -tags aws ./cmd/polystac-ingest)")
}
