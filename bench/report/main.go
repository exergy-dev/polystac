// Aggregates the per-impl k6 summary JSON files and the raw.csv into a
// single markdown report.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// k6 v0.51 summary-export: metric values live directly on the metric
// object. Most fields are numeric, but http_req_failed adds a nested
// `thresholds` object — `any` accepts both, asFloat coerces back when
// we need a number.
type k6Summary struct {
	Metrics map[string]map[string]any `json:"metrics"`
}

func asFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

type implRow struct {
	Label, Image                          string
	ColdMs                                int
	IdleRSS, PeakRSS                      string
	ImageSizeMiB                          float64
	HTTPReqs                              float64
	HTTPRate                              float64
	Failed                                float64
	PerScenarioP95, PerScenarioMed        map[string]float64
	PerScenarioReqs                       map[string]float64
}

var scenarios = []string{"landing", "collections", "search_all", "search_bbox", "search_dt", "search_cql2"}

func main() {
	dir := flag.String("dir", "", "results directory")
	items := flag.Int("items", 0, "items seeded")
	duration := flag.String("duration", "", "k6 duration")
	vus := flag.Int("vus", 0, "k6 VUs")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "report: -dir is required")
		os.Exit(2)
	}

	rows, err := loadRows(*dir)
	if err != nil {
		fail(err)
	}
	for i := range rows {
		if err := loadK6(*dir, &rows[i]); err != nil {
			fmt.Fprintf(os.Stderr, "warn: %s: %v\n", rows[i].Label, err)
		}
	}
	out, err := os.Create(filepath.Join(*dir, "report.md"))
	if err != nil {
		fail(err)
	}
	defer out.Close()
	writeReport(out, rows, *items, *duration, *vus)
}

func loadRows(dir string) ([]implRow, error) {
	f, err := os.Open(filepath.Join(dir, "raw.csv"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rdr := csv.NewReader(f)
	records, err := rdr.ReadAll()
	if err != nil {
		return nil, err
	}
	out := make([]implRow, 0, len(records))
	for i, rec := range records {
		if i == 0 {
			continue // header
		}
		size, _ := strconv.ParseInt(rec[5], 10, 64)
		cold, _ := strconv.Atoi(rec[2])
		out = append(out, implRow{
			Label:        rec[0],
			Image:        rec[1],
			ColdMs:       cold,
			IdleRSS:      rec[3],
			PeakRSS:      rec[4],
			ImageSizeMiB: float64(size) / (1024 * 1024),
		})
	}
	return out, nil
}

func loadK6(dir string, r *implRow) error {
	path := filepath.Join(dir, r.Label, "k6-summary.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var s k6Summary
	if err := json.Unmarshal(body, &s); err != nil {
		return err
	}

	r.PerScenarioP95 = map[string]float64{}
	r.PerScenarioMed = map[string]float64{}
	r.PerScenarioReqs = map[string]float64{}
	for _, scn := range scenarios {
		if m, ok := s.Metrics["latency_"+scn+"_ms"]; ok {
			r.PerScenarioP95[scn] = asFloat(m["p(95)"])
			r.PerScenarioMed[scn] = asFloat(m["med"])
		}
		if m, ok := s.Metrics["requests_"+scn]; ok {
			r.PerScenarioReqs[scn] = asFloat(m["count"])
		}
	}
	if m, ok := s.Metrics["http_reqs"]; ok {
		r.HTTPReqs = asFloat(m["count"])
		r.HTTPRate = asFloat(m["rate"])
	}
	if m, ok := s.Metrics["http_req_failed"]; ok {
		// k6 reports rate metrics with `value` (a 0..1 ratio) when no
		// failures occur and `passes`/`fails` separately when there are.
		// Use `value` as the canonical fail-rate.
		r.Failed = asFloat(m["value"])
	}
	return nil
}

func writeReport(w *os.File, rows []implRow, items int, duration string, vus int) {
	fmt.Fprintf(w, "# PolyStac vs reference implementations — performance diff\n\n")
	fmt.Fprintf(w, "Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "**Configuration**\n\n")
	fmt.Fprintf(w, "- Items seeded per backend: **%d**\n", items)
	fmt.Fprintf(w, "- k6 duration: **%s**\n", duration)
	fmt.Fprintf(w, "- k6 VUs: **%d**\n", vus)
	fmt.Fprintf(w, "- Mix: 6 endpoints uniformly sampled — landing, /collections, search (all/bbox/datetime/cql2-text).\n")
	fmt.Fprintf(w, "- pgstac (v0.8.5) is shared across both pgstac impls (both call the same `pgstac.search`/`create_items` SQL — apples-to-apples).\n")
	fmt.Fprintf(w, "- OpenSearch (2.13.0) gets a fresh container per impl (impls use incompatible index layouts) and each impl HTTP-seeds its own data via its native write path.\n\n")

	// Group by backend so the diff is clear.
	groups := map[string][]implRow{}
	order := []string{"pgstac", "opensearch"}
	for _, r := range rows {
		switch {
		case strings.Contains(r.Label, "pgstac"):
			groups["pgstac"] = append(groups["pgstac"], r)
		case strings.Contains(r.Label, "os"):
			groups["opensearch"] = append(groups["opensearch"], r)
		}
	}

	for _, g := range order {
		gr := groups[g]
		if len(gr) == 0 {
			continue
		}
		// Polystac first in each group.
		sort.SliceStable(gr, func(i, j int) bool {
			return strings.HasPrefix(gr[i].Label, "polystac") && !strings.HasPrefix(gr[j].Label, "polystac")
		})
		fmt.Fprintf(w, "## %s backend\n\n", g)

		// Static-cost table.
		fmt.Fprintf(w, "### Static cost\n\n")
		fmt.Fprintf(w, "| Impl | Image size | Cold start | Idle RSS | Peak RSS (under load) |\n")
		fmt.Fprintf(w, "|---|---:|---:|---:|---:|\n")
		for _, r := range gr {
			cold := "—"
			if r.ColdMs > 0 {
				cold = fmt.Sprintf("%d ms", r.ColdMs)
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n",
				r.Label, fmtMiB(r.ImageSizeMiB), cold, r.IdleRSS, r.PeakRSS)
		}
		fmt.Fprintln(w)

		// Throughput row.
		fmt.Fprintf(w, "### Throughput & error rate\n\n")
		fmt.Fprintf(w, "| Impl | Total requests | Req/sec | Error rate |\n")
		fmt.Fprintf(w, "|---|---:|---:|---:|\n")
		for _, r := range gr {
			fmt.Fprintf(w, "| %s | %.0f | %.1f | %.2f%% |\n",
				r.Label, r.HTTPReqs, r.HTTPRate, r.Failed*100)
		}
		fmt.Fprintln(w)

		// Per-scenario p95.
		fmt.Fprintf(w, "### Per-scenario p95 latency (ms, lower is better)\n\n")
		fmt.Fprintf(w, "| Scenario |")
		for _, r := range gr {
			fmt.Fprintf(w, " %s |", r.Label)
		}
		fmt.Fprintln(w, " ratio (ref ÷ polystac) |")
		fmt.Fprintf(w, "|---|")
		for range gr {
			fmt.Fprintf(w, "---:|")
		}
		fmt.Fprintln(w, "---:|")
		for _, scn := range scenarios {
			fmt.Fprintf(w, "| %s |", scn)
			var poly, other float64
			for _, r := range gr {
				v := r.PerScenarioP95[scn]
				fmt.Fprintf(w, " %s |", fmtMs(v))
				if strings.HasPrefix(r.Label, "polystac") {
					poly = v
				} else {
					other = v
				}
			}
			ratio := "—"
			if poly > 0 && other > 0 {
				ratio = fmt.Sprintf("%.2f×", other/poly)
			}
			fmt.Fprintf(w, " %s |\n", ratio)
		}
		fmt.Fprintln(w)

		// Per-scenario median.
		fmt.Fprintf(w, "### Per-scenario median latency (ms)\n\n")
		fmt.Fprintf(w, "| Scenario |")
		for _, r := range gr {
			fmt.Fprintf(w, " %s |", r.Label)
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "|---|")
		for range gr {
			fmt.Fprintf(w, "---:|")
		}
		fmt.Fprintln(w)
		for _, scn := range scenarios {
			fmt.Fprintf(w, "| %s |", scn)
			for _, r := range gr {
				fmt.Fprintf(w, " %s |", fmtMs(r.PerScenarioMed[scn]))
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "## Methodology\n\n")
	fmt.Fprintf(w, "- pgstac side: the same Postgres+pgstac container feeds both PolyStac and stac-fastapi-pgstac; data is bulk-seeded once via `pgstac.create_items` and read by both impls. The only thing that changes between rows is the API server.\n")
	fmt.Fprintf(w, "- OpenSearch side: each impl gets its own fresh OpenSearch and seeds itself by ingesting the same N items through its own POST /collections/{id}/items endpoint. Data is logically identical but stored in each impl's native index layout.\n")
	fmt.Fprintf(w, "- Cold start: wall-clock from `docker run` to the first 200 on `/`.\n")
	fmt.Fprintf(w, "- Idle RSS: `docker stats` snapshot 5 s after the impl reports ready, no traffic.\n")
	fmt.Fprintf(w, "- Peak RSS: max `docker stats` sample taken once per second during the k6 run.\n")
	fmt.Fprintf(w, "- Latency includes one localhost network hop, JSON marshal, and (for /search) one round-trip to the backend service.\n")
	fmt.Fprintf(w, "- All requests in the mix have `limit=10` so payload size is comparable.\n\n")
	fmt.Fprintf(w, "Run with `bench/run.sh [items] [duration] [vus]` (defaults: 1000 / 30s / 20).\n")
}

func fmtMiB(v float64) string {
	if v < 1 {
		return "—"
	}
	if v < 1024 {
		return fmt.Sprintf("%.0f MiB", v)
	}
	return fmt.Sprintf("%.2f GiB", v/1024)
}

func fmtMs(v float64) string {
	if v == 0 || math.IsNaN(v) {
		return "—"
	}
	if v < 10 {
		return fmt.Sprintf("%.2f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "report:", err)
	os.Exit(1)
}
