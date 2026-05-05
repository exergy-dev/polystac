// Command bench-seed-http populates a STAC API server (any
// implementation that follows the standard wire spec) with N synthetic
// items. Used by the benchmark harness to seed OpenSearch-backed
// servers via their own HTTP endpoints, so each impl manages its own
// indices in its own format — apples-to-apples.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"time"
)

func main() {
	url := flag.String("url", "http://localhost:8080", "STAC API base URL")
	n := flag.Int("n", 1000, "number of items")
	colID := flag.String("collection", "bench", "collection ID")
	flag.Parse()

	if err := waitReady(*url, 90*time.Second); err != nil {
		fail("server not ready: %v", err)
	}

	col := map[string]any{
		"type":         "Collection",
		"stac_version": "1.0.0",
		"id":           *colID,
		"description":  "benchmark synthetic collection",
		"license":      "proprietary",
		"extent": map[string]any{
			"spatial":  map[string]any{"bbox": [][]float64{{-180, -90, 180, 90}}},
			"temporal": map[string]any{"interval": [][]any{{nil, nil}}},
		},
		"links": []any{},
	}
	if status, body := postJSON(*url+"/collections", col); status != 200 && status != 201 && status != 409 {
		fail("create collection: status %d: %s", status, body)
	}

	// Some impls (notably stac-server) return 201 on POST /collections
	// before the collection doc has refreshed enough to be visible to
	// subsequent reads. The next POST /items handler internally checks
	// for the collection and 404s if it can't see it. Poll the GET
	// endpoint until it succeeds, capped at 30 s.
	{
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := http.Get(*url + "/collections/" + *colID)
			if err == nil && resp.StatusCode == 200 {
				resp.Body.Close()
				break
			}
			if resp != nil {
				resp.Body.Close()
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	rng := rand.New(rand.NewSource(1))
	platforms := []string{"S2A", "S2B", "L8", "L9", "MODIS"}

	fmt.Fprintf(os.Stderr, "seeding %d items into %s ...\n", *n, *url)
	start := time.Now()
	itemsPath := *url + "/collections/" + *colID + "/items"

	for i := 0; i < *n; i++ {
		lon := -180 + rng.Float64()*360
		lat := -90 + rng.Float64()*180
		t := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rng.Intn(365*86400)) * time.Second)
		it := map[string]any{
			"type":         "Feature",
			"stac_version": "1.0.0",
			"id":           fmt.Sprintf("item-%07d", i),
			"collection":   *colID,
			"geometry":     map[string]any{"type": "Point", "coordinates": []float64{lon, lat}},
			"bbox":         []float64{lon, lat, lon, lat},
			"properties": map[string]any{
				"datetime":       t.Format(time.RFC3339),
				"eo:cloud_cover": rng.Float64() * 100,
				"platform":       platforms[rng.Intn(len(platforms))],
			},
			"links":  []any{},
			"assets": map[string]any{},
		}
		if status, body := postJSON(itemsPath, it); status != 200 && status != 201 {
			fail("upsert item-%07d: status %d: %s", i, status, body)
		}
		if (i+1)%500 == 0 {
			fmt.Fprintf(os.Stderr, "  %d/%d (%.0f items/s)\n", i+1, *n, float64(i+1)/time.Since(start).Seconds())
		}
	}
	fmt.Fprintf(os.Stderr, "done in %s\n", time.Since(start).Round(time.Millisecond))
}

func waitReady(base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", timeout)
}

func postJSON(url string, body any) (int, string) {
	raw, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bench-seed-http: "+format+"\n", args...)
	os.Exit(1)
}
