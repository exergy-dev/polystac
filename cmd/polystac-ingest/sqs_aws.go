//go:build aws

// SQS receiver. Builds only with -tags aws so the default binary stays
// free of aws-sdk-go-v2 dependency weight (NF-7 / SDD §7.1).
//
// To compile:
//   go build -tags aws ./cmd/polystac-ingest
// You will need:
//   go get github.com/aws/aws-sdk-go-v2 github.com/aws/aws-sdk-go-v2/config github.com/aws/aws-sdk-go-v2/service/sqs

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqsTypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/example/polystac/internal/ingest"
	"github.com/example/polystac/pkg/stac"
)

func sqsReceiver(queueURL string) (ingest.Receiver, error) {
	if queueURL == "" {
		return nil, fmt.Errorf("sqs: queue URL required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("sqs: aws config: %w", err)
	}
	return &sqsRecv{client: sqs.NewFromConfig(cfg), queueURL: queueURL}, nil
}

type sqsRecv struct {
	client   *sqs.Client
	queueURL string
}

func (r *sqsRecv) Name() string { return "sqs:" + r.queueURL }

func (r *sqsRecv) Receive(ctx context.Context) (iter.Seq2[*stac.Item, error], error) {
	return func(yield func(*stac.Item, error) bool) {
		for {
			if ctx.Err() != nil {
				return
			}
			out, err := r.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
				QueueUrl:            &r.queueURL,
				MaxNumberOfMessages: 10,
				WaitTimeSeconds:     20,
			})
			if err != nil {
				if !yield(nil, err) {
					return
				}
				continue
			}
			if len(out.Messages) == 0 {
				return
			}
			for _, m := range out.Messages {
				body := []byte(*m.Body)
				var it stac.Item
				if err := json.Unmarshal(body, &it); err != nil {
					if !yield(nil, fmt.Errorf("sqs message %s: %w", *m.MessageId, err)) {
						return
					}
					continue
				}
				if !yield(&it, nil) {
					return
				}
				_, _ = r.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
					QueueUrl:      &r.queueURL,
					ReceiptHandle: m.ReceiptHandle,
				})
			}
		}
	}, nil
}

// keep imports used even when no real types referenced
var _ sqsTypes.Message
