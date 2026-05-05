package ingest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/polystac/internal/backends/inmem"
	"github.com/example/polystac/internal/ingest"
	"github.com/example/polystac/pkg/stac"
)

func TestStdinNDJSONIntoInmem(t *testing.T) {
	ctx := context.Background()
	dst := inmem.New()
	if err := dst.UpsertCollection(ctx, &stac.Collection{ID: "c1", Description: "x", License: "y"}); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"type":"Feature","stac_version":"1.0.0","id":"a","collection":"c1","geometry":null,"properties":{"datetime":"2024-01-01T00:00:00Z"}}`,
		`{"type":"Feature","stac_version":"1.0.0","id":"b","collection":"c1","geometry":null,"properties":{"datetime":"2024-02-01T00:00:00Z"}}`,
	}, "\n")
	res, err := ingest.Run(ctx, ingest.StdinReceiver{R: strings.NewReader(body)}, dst)
	if err != nil {
		t.Fatal(err)
	}
	if res.Succeeded != 2 || res.Failed != 0 {
		t.Errorf("ingest result: %+v", res)
	}
}

func TestDirReceiverIntoInmem(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"a", "b"} {
		body := `{"type":"Feature","stac_version":"1.0.0","id":"` + id + `","collection":"c1","geometry":null,"properties":{"datetime":"2024-01-01T00:00:00Z"}}`
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	dst := inmem.New()
	if err := dst.UpsertCollection(ctx, &stac.Collection{ID: "c1", Description: "x", License: "y"}); err != nil {
		t.Fatal(err)
	}
	res, err := ingest.Run(ctx, ingest.DirReceiver{Path: dir}, dst)
	if err != nil {
		t.Fatal(err)
	}
	if res.Succeeded != 2 || res.Failed != 0 {
		t.Errorf("ingest result: %+v", res)
	}
}
