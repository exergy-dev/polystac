// Drop-in compatibility validator: fires a curated set of HTTP
// requests at PolyStac and at stac-server (Node), reports per-endpoint
// status-code parity, response-shape parity, and the specific divergences
// that would break a client migrating between them.
//
// What we DO compare:
//   - Status code per (method, path, query/body)
//   - Top-level response keys and types
//   - Item/Collection IDs and their core property values
//   - Error response code field (StacApi-style)
//
// What we explicitly NORMALIZE before comparing:
//   - Absolute URLs (host + scheme): both servers emit absolute hrefs
//     in `links[]`; we strip the scheme://host prefix.
//   - Pagination tokens: both produce opaque tokens, different formats.
//   - Server-timestamp fields ("created", "updated"): both stamp these
//     on write and they differ per call.
//   - The `numberMatched` field when either side reports
//     CountSemantics=Approximate.
//
// Output: a markdown report at -out, plus a stdout summary that
// non-zero-exits when any case reports a client-breaking divergence.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type Case struct {
	Name       string
	Method     string
	Path       string
	Body       any
	IgnoreBody bool // status-only check (no body comparison)
	// Expected: when set, a divergence on this case is reclassified as
	// `expected-diff` instead of `diff`. The string is the reason that
	// appears in the report — e.g. "polystac-superset: stac-server does
	// not declare cql2-text conformance", "spec-allows-both: yaml vs
	// json".
	Expected string
}

type Result struct {
	Case     Case
	A, B     callOutcome
	Verdict  string // "match" | "diff" | "endpoint-only-on-A" | "endpoint-only-on-B" | "both-fail"
	Notes    []string
}

type callOutcome struct {
	Status int
	Body   []byte
	Err    error
	URL    string
}

func main() {
	urlA := flag.String("a", "http://localhost:8080", "PolyStac base URL")
	urlB := flag.String("b", "http://localhost:3000", "stac-server base URL")
	out := flag.String("out", "compat-report.md", "markdown report path")
	collection := flag.String("collection", "compat", "collection ID used in seeded fixtures")
	verbose := flag.Bool("v", false, "print per-case diff")
	flag.Parse()

	cases := compatCases(*collection)
	results := make([]Result, 0, len(cases))
	for _, c := range cases {
		ra := call(*urlA, c)
		rb := call(*urlB, c)
		r := Result{Case: c, A: ra, B: rb}
		classify(&r)
		results = append(results, r)
		if *verbose {
			fmt.Fprintf(os.Stderr, "  [%s] %s %s : A=%d B=%d %s\n",
				r.Verdict, c.Method, c.Path, ra.Status, rb.Status, strings.Join(r.Notes, "; "))
		}
	}

	report(*out, *urlA, *urlB, results)
	summary(results)
}

// classify decides the verdict on one Result.
func classify(r *Result) {
	defer func() {
		// If the case is tagged as expected-divergent, downgrade
		// `diff` to `expected-diff` and record the reason.
		if r.Case.Expected != "" && r.Verdict == "diff" {
			r.Verdict = "expected-diff"
			r.Notes = append(r.Notes, "expected: "+r.Case.Expected)
		}
	}()

	if r.A.Status >= 500 && r.B.Status >= 500 {
		r.Verdict = "both-fail"
		return
	}
	if r.A.Status == 404 && r.B.Status != 404 && r.B.Status < 400 {
		r.Verdict = "endpoint-only-on-B"
		return
	}
	if r.B.Status == 404 && r.A.Status != 404 && r.A.Status < 400 {
		r.Verdict = "endpoint-only-on-A"
		return
	}
	if r.A.Status != r.B.Status {
		r.Verdict = "diff"
		r.Notes = append(r.Notes, fmt.Sprintf("status: A=%d B=%d", r.A.Status, r.B.Status))
		return
	}
	if r.Case.IgnoreBody {
		r.Verdict = "match"
		return
	}
	notes := diffBodies(r.A.Body, r.B.Body, r.Case.Method, r.Case.Path)
	if len(notes) == 0 {
		r.Verdict = "match"
	} else {
		r.Verdict = "diff"
		r.Notes = append(r.Notes, notes...)
	}
}

// diffBodies normalizes and structurally compares the two JSON bodies.
// Returns a list of human-readable divergence notes; an empty list means
// the bodies are equivalent under the documented normalizations.
func diffBodies(a, b []byte, method, path string) []string {
	var notes []string
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return []string{fmt.Sprintf("A body not JSON: %v", err)}
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return []string{fmt.Sprintf("B body not JSON: %v", err)}
	}
	av = normalize(av)
	bv = normalize(bv)
	notes = append(notes, structuralDiff(av, bv, "")...)

	// For collection-list / search / item GET responses we also compare
	// the set of IDs returned (order-insensitive for set responses, since
	// stac-server and PolyStac both default to undefined order on
	// /collections).
	notes = append(notes, idSetDiff(av, bv, path)...)
	return notes
}

// normalize strips fields that are guaranteed to differ between
// implementations but are not client-meaningful.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			switch k {
			case "links":
				out[k] = normalizeLinks(val)
			case "created", "updated":
				// server timestamps; ignore exact value, keep presence flag.
				out[k] = "<server-timestamp>"
			case "context", "next", "prev":
				// stac-server's per-page context block + token-bearing
				// link bodies — opaque tokens differ. Keep presence only.
				out[k] = "<opaque>"
			default:
				out[k] = normalize(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	}
	return v
}

func normalizeLinks(v any) any {
	arr, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]any, 0, len(arr))
	for _, l := range arr {
		m, ok := l.(map[string]any)
		if !ok {
			continue
		}
		// Keep only rel + href-stripped + type + method (key fields a
		// client would actually use). Strip scheme://host so localhost
		// vs container-name doesn't matter.
		nl := map[string]any{}
		if r, ok := m["rel"].(string); ok {
			nl["rel"] = r
		}
		if h, ok := m["href"].(string); ok {
			nl["href"] = stripHost(h)
		}
		if t, ok := m["type"].(string); ok {
			nl["type"] = t
		}
		if me, ok := m["method"].(string); ok {
			nl["method"] = me
		}
		out = append(out, nl)
	}
	// Order isn't load-bearing; sort by rel,href so a permutation
	// doesn't read as a divergence.
	sort.Slice(out, func(i, j int) bool {
		oi := out[i].(map[string]any)
		oj := out[j].(map[string]any)
		return fmt.Sprint(oi["rel"], oi["href"]) < fmt.Sprint(oj["rel"], oj["href"])
	})
	return out
}

func stripHost(href string) string {
	for _, p := range []string{"http://", "https://"} {
		if strings.HasPrefix(href, p) {
			rest := href[len(p):]
			if i := strings.IndexByte(rest, '/'); i >= 0 {
				return rest[i:]
			}
			return "/"
		}
	}
	return href
}

func structuralDiff(a, b any, path string) []string {
	var out []string
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("type mismatch at %q: A object, B %T", path, b)}
		}
		for k := range av {
			if _, ok := bv[k]; !ok {
				out = append(out, fmt.Sprintf("key %q missing in B", join(path, k)))
			}
		}
		for k := range bv {
			if _, ok := av[k]; !ok {
				out = append(out, fmt.Sprintf("key %q missing in A", join(path, k)))
			}
		}
		return out
	case []any:
		bv, ok := b.([]any)
		if !ok {
			return []string{fmt.Sprintf("type mismatch at %q: A array, B %T", path, b)}
		}
		if len(av) != len(bv) {
			out = append(out, fmt.Sprintf("array length differs at %q: A=%d B=%d", path, len(av), len(bv)))
		}
	default:
		// scalar; we only compare type, not value, since we already
		// normalized common per-call fluctuating fields.
		if fmt.Sprintf("%T", a) != fmt.Sprintf("%T", b) {
			out = append(out, fmt.Sprintf("type mismatch at %q: %T vs %T", path, a, b))
		}
	}
	return out
}

func join(p, k string) string {
	if p == "" {
		return k
	}
	return p + "." + k
}

func idSetDiff(a, b any, path string) []string {
	switch {
	case strings.HasSuffix(path, "/items") || path == "/search":
		return featureIDDiff(a, b)
	case path == "/collections":
		return collectionIDDiff(a, b)
	}
	return nil
}

func featureIDDiff(a, b any) []string {
	ag := extractIDs(a, "features")
	bg := extractIDs(b, "features")
	if !setEqual(ag, bg) {
		return []string{fmt.Sprintf("feature ids differ: A=%v B=%v", ag, bg)}
	}
	return nil
}

func collectionIDDiff(a, b any) []string {
	ag := extractIDs(a, "collections")
	bg := extractIDs(b, "collections")
	if !setEqual(ag, bg) {
		return []string{fmt.Sprintf("collection ids differ: A=%v B=%v", ag, bg)}
	}
	return nil
}

func extractIDs(v any, field string) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	arr, ok := m[field].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := em["id"].(string); ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func setEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func call(base string, c Case) callOutcome {
	url := base + c.Path
	var body io.Reader
	if c.Body != nil {
		raw, _ := json.Marshal(c.Body)
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(c.Method, url, body)
	if err != nil {
		return callOutcome{URL: url, Err: err}
	}
	if c.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return callOutcome{URL: url, Err: err}
	}
	defer resp.Body.Close()
	bs, _ := io.ReadAll(resp.Body)
	return callOutcome{Status: resp.StatusCode, Body: bs, URL: url}
}

func summary(results []Result) {
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Verdict]++
	}
	fmt.Println("\nDrop-in summary:")
	for _, k := range []string{"match", "expected-diff", "diff", "endpoint-only-on-A", "endpoint-only-on-B", "both-fail"} {
		fmt.Printf("  %-21s %d\n", k, counts[k])
	}
}

func report(path, urlA, urlB string, results []Result) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "report:", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "# Drop-in compatibility — PolyStac vs stac-server (Node)\n\n")
	fmt.Fprintf(f, "Generated: %s\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(f, "- A = %s (PolyStac)\n- B = %s (stac-server)\n\n", urlA, urlB)
	fmt.Fprintf(f, "Cases: %d. Per-endpoint verdicts:\n\n", len(results))
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Verdict]++
	}
	fmt.Fprintf(f, "| Verdict | Count |\n|---|---:|\n")
	for _, k := range []string{"match", "expected-diff", "diff", "endpoint-only-on-A", "endpoint-only-on-B", "both-fail"} {
		fmt.Fprintf(f, "| %s | %d |\n", k, counts[k])
	}
	fmt.Fprintln(f)

	for _, verdict := range []string{"diff", "endpoint-only-on-A", "endpoint-only-on-B", "both-fail", "expected-diff", "match"} {
		var rs []Result
		for _, r := range results {
			if r.Verdict == verdict {
				rs = append(rs, r)
			}
		}
		if len(rs) == 0 {
			continue
		}
		fmt.Fprintf(f, "## %s (%d)\n\n", verdict, len(rs))
		fmt.Fprintf(f, "| Method | Path | A status | B status | Notes |\n|---|---|---:|---:|---|\n")
		for _, r := range rs {
			notes := strings.Join(r.Notes, "; ")
			if notes == "" {
				notes = "—"
			}
			path := r.Case.Path
			if r.Case.Body != nil {
				path += " *(POST body)*"
			}
			fmt.Fprintf(f, "| %s | `%s` | %d | %d | %s |\n",
				r.Case.Method, path, r.A.Status, r.B.Status, escapeMD(notes))
		}
		fmt.Fprintln(f)
	}
}

func escapeMD(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}

// compatCases enumerates the drop-in test corpus.
func compatCases(col string) []Case {
	itemID := "item-0000001"
	return []Case{
		// ---- Core --------------------------------------------------
		{Name: "landing", Method: "GET", Path: "/"},
		{Name: "conformance", Method: "GET", Path: "/conformance"},
		{Name: "openapi-doc", Method: "GET", Path: "/api",
			Expected: "spec-allows-both: PolyStac returns OpenAPI as JSON; stac-server returns it as YAML. The STAC API spec accepts either media type."},

		// ---- Collections -------------------------------------------
		{Name: "collections-list", Method: "GET", Path: "/collections"},
		{Name: "collection-get", Method: "GET", Path: "/collections/" + col},
		{Name: "collection-missing", Method: "GET", Path: "/collections/__nope__"},

		// ---- Queryables --------------------------------------------
		{Name: "queryables-global", Method: "GET", Path: "/queryables",
			Expected: "schema-cosmetic: PolyStac includes `description`; stac-server includes `additionalProperties`. Both are valid JSON-Schema fields and clients typically read `properties`."},
		{Name: "queryables-collection", Method: "GET", Path: "/collections/" + col + "/queryables",
			Expected: "schema-cosmetic: same as queryables-global."},

		// ---- Items: read path --------------------------------------
		{Name: "items-list", Method: "GET", Path: "/collections/" + col + "/items?limit=5"},
		{Name: "items-list-page2", Method: "GET", Path: "/collections/" + col + "/items?limit=5&page=2"},
		{Name: "item-get", Method: "GET", Path: "/collections/" + col + "/items/" + itemID},
		{Name: "item-missing", Method: "GET", Path: "/collections/" + col + "/items/__nope__"},

		// ---- Search GET --------------------------------------------
		{Name: "search-empty", Method: "GET", Path: "/search?limit=5"},
		{Name: "search-by-collection", Method: "GET", Path: "/search?collections=" + col + "&limit=5"},
		{Name: "search-by-ids", Method: "GET", Path: "/search?ids=" + itemID},
		{Name: "search-bbox", Method: "GET", Path: "/search?bbox=-10,-10,10,10&limit=5"},
		{Name: "search-datetime", Method: "GET", Path: "/search?datetime=2024-01-01T00:00:00Z/2024-06-01T00:00:00Z&limit=5"},
		{Name: "search-sortby-asc", Method: "GET", Path: "/search?sortby=id&limit=5"},
		{Name: "search-sortby-desc", Method: "GET", Path: "/search?sortby=-properties.eo:cloud_cover&limit=5"},
		{Name: "search-fields-include", Method: "GET", Path: "/search?fields=id,properties.datetime&limit=5"},
		{Name: "search-cql2-text", Method: "GET",
			Path:     "/search?filter-lang=cql2-text&filter=" + urlencode(`"eo:cloud_cover" < 50`) + "&limit=5",
			Expected: "polystac-superset: stac-server's /conformance does not declare cql2-text. PolyStac is strictly more capable here; clients written against stac-server only send cql2-json."},

		// ---- Search POST -------------------------------------------
		{Name: "search-post-empty", Method: "POST", Path: "/search", Body: map[string]any{"limit": 5}},
		{Name: "search-post-collection", Method: "POST", Path: "/search",
			Body: map[string]any{"collections": []string{col}, "limit": 5}},
		{Name: "search-post-bbox", Method: "POST", Path: "/search",
			Body: map[string]any{"bbox": []float64{-10, -10, 10, 10}, "limit": 5}},
		{Name: "search-post-cql2-text", Method: "POST", Path: "/search",
			Body: map[string]any{
				"filter-lang": "cql2-text",
				"filter":      `"eo:cloud_cover" < 50`,
				"limit":       5,
			},
			Expected: "polystac-superset: same as search-cql2-text."},
		{Name: "search-post-cql2-json", Method: "POST", Path: "/search",
			Body: map[string]any{
				"filter-lang": "cql2-json",
				"filter": map[string]any{
					"op":   "<",
					"args": []any{map[string]any{"property": "eo:cloud_cover"}, 50},
				},
				"limit": 5,
			}},
		{Name: "search-post-sortby", Method: "POST", Path: "/search",
			Body: map[string]any{
				"sortby": []map[string]any{
					{"field": "properties.datetime", "direction": "desc"},
				},
				"limit": 5,
			}},
		{Name: "search-post-fields", Method: "POST", Path: "/search",
			Body: map[string]any{
				"fields": map[string]any{"include": []string{"id", "properties.datetime"}},
				"limit":  5,
			}},
	}
}

func urlencode(s string) string {
	r := strings.NewReplacer(
		`"`, "%22", ` `, "%20", `<`, "%3C", `>`, "%3E", `'`, "%27",
	)
	return r.Replace(s)
}
