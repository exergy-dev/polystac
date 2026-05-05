// Command polystac-lambda is the AWS Lambda variant of the PolyStac
// server. It shares the same internal/app pipeline as the main binary
// — only the I/O wrapping differs.
//
// Configuration is read from environment variables exactly as in the
// main binary (POLYSTAC_*, STAC_FASTAPI_* aliases). The function URL
// or API Gateway integration is up to the operator (SAM/Terraform
// modules in deploy/ wire one of each).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/example/polystac/internal/app"
	"github.com/example/polystac/internal/config"
)

func main() {
	cfg, err := config.Load(nil, config.EnvMap())
	if err != nil {
		fmt.Fprintln(os.Stderr, "polystac-lambda: config:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	srv, _, _, err := app.Build(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "polystac-lambda: build:", err)
		os.Exit(1)
	}

	lambda.Start(httpadapter.New(srv.Handler()).ProxyWithContext)
}
