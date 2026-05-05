package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

// parseSearchGET turns a GET /search query string into a SearchRequest.
func parseSearchGET(r *http.Request, defaultLimit int) (repository.SearchRequest, error) {
	q := r.URL.Query()
	req := repository.SearchRequest{Limit: defaultLimit, Token: q.Get("token")}
	if v := q.Get("collections"); v != "" {
		req.Collections = splitCSV(v)
	}
	if v := q.Get("ids"); v != "" {
		req.IDs = splitCSV(v)
	}
	if v := q.Get("bbox"); v != "" {
		bb, err := parseBBox(v)
		if err != nil {
			return req, err
		}
		req.BBox = bb
	}
	if v := q.Get("datetime"); v != "" {
		ti, err := parseDatetime(v)
		if err != nil {
			return req, err
		}
		req.Datetime = ti
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return req, fmt.Errorf("limit %q: %w", v, repository.ErrInvalidInput)
		}
		req.Limit = n
	}
	if v := q.Get("filter"); v != "" {
		expr, err := cql2.Parse([]byte(v))
		if err != nil {
			return req, err
		}
		req.Filter = expr
		req.FilterLang = repository.FilterLangText
		if fl := q.Get("filter-lang"); fl == "cql2-json" {
			req.FilterLang = repository.FilterLangJSON
		}
	}
	if v := q.Get("sortby"); v != "" {
		req.SortBy = parseSortBy(v)
	}
	if inc := q.Get("fields"); inc != "" {
		req.Fields = parseFieldsParam(inc)
	}
	return req, nil
}

// parseSearchPOST parses the JSON body of POST /search.
func parseSearchPOST(r *http.Request, defaultLimit int) (repository.SearchRequest, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return repository.SearchRequest{}, err
	}
	var raw struct {
		Collections []string         `json:"collections"`
		IDs         []string         `json:"ids"`
		BBox        []float64        `json:"bbox"`
		Intersects  *stac.Geometry   `json:"intersects"`
		Datetime    string           `json:"datetime"`
		Limit       int              `json:"limit"`
		Token       string           `json:"token"`
		Filter      json.RawMessage  `json:"filter"`
		FilterLang  string           `json:"filter-lang"`
		SortBy      []sortClauseJSON `json:"sortby"`
		Fields      *fieldsJSON      `json:"fields"`
		Query       json.RawMessage  `json:"query"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &raw); err != nil {
			return repository.SearchRequest{}, fmt.Errorf("body: %w", repository.ErrInvalidInput)
		}
	}
	req := repository.SearchRequest{
		Collections: raw.Collections,
		IDs:         raw.IDs,
		BBox:        raw.BBox,
		Intersects:  raw.Intersects,
		Limit:       raw.Limit,
		Token:       raw.Token,
	}
	if req.Limit == 0 {
		req.Limit = defaultLimit
	}
	if raw.Datetime != "" {
		ti, err := parseDatetime(raw.Datetime)
		if err != nil {
			return req, err
		}
		req.Datetime = ti
	}
	if len(raw.Filter) > 0 && string(raw.Filter) != "null" {
		// Per the STAC API search spec a CQL2-text filter arrives as a
		// JSON string literal (e.g. `"filter": "eo:cloud_cover < 30"`)
		// while CQL2-JSON is a structured object. Unwrap the string
		// before handing the bytes to the parser; otherwise it sees a
		// quoted blob and rejects it as syntactically invalid.
		filterBytes := []byte(raw.Filter)
		if len(filterBytes) > 0 && filterBytes[0] == '"' {
			var s string
			if err := json.Unmarshal(filterBytes, &s); err != nil {
				return req, fmt.Errorf("filter string: %w", repository.ErrInvalidInput)
			}
			filterBytes = []byte(s)
		}
		expr, err := cql2.Parse(filterBytes)
		if err != nil {
			return req, err
		}
		req.Filter = expr
		req.FilterLang = repository.FilterLangJSON
		if raw.FilterLang == "cql2-text" {
			req.FilterLang = repository.FilterLangText
		}
	}
	for _, s := range raw.SortBy {
		dir := repository.SortAsc
		if strings.ToLower(s.Direction) == "desc" {
			dir = repository.SortDesc
		}
		req.SortBy = append(req.SortBy, repository.SortClause{Field: s.Field, Direction: dir})
	}
	if raw.Fields != nil {
		req.Fields = &repository.FieldsSpec{Include: raw.Fields.Include, Exclude: raw.Fields.Exclude}
	}
	return req, nil
}

type sortClauseJSON struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type fieldsJSON struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseBBox(s string) ([]float64, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 && len(parts) != 6 {
		return nil, fmt.Errorf("bbox needs 4 or 6 elements, got %d: %w", len(parts), repository.ErrInvalidInput)
	}
	out := make([]float64, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, fmt.Errorf("bbox[%d]=%q: %w", i, p, repository.ErrInvalidInput)
		}
		out[i] = f
	}
	return out, nil
}

func parseDatetime(s string) (*repository.TemporalInterval, error) {
	parsePoint := func(raw string) (*time.Time, error) {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == ".." {
			return nil, nil
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("datetime %q: %w", raw, repository.ErrInvalidInput)
		}
		return &t, nil
	}
	if !strings.Contains(s, "/") {
		t, err := parsePoint(s)
		if err != nil {
			return nil, err
		}
		return &repository.TemporalInterval{Start: t, End: t}, nil
	}
	parts := strings.SplitN(s, "/", 2)
	start, err := parsePoint(parts[0])
	if err != nil {
		return nil, err
	}
	end, err := parsePoint(parts[1])
	if err != nil {
		return nil, err
	}
	return &repository.TemporalInterval{Start: start, End: end}, nil
}

func parseSortBy(s string) []repository.SortClause {
	out := []repository.SortClause{}
	for _, p := range splitCSV(s) {
		dir := repository.SortAsc
		field := p
		switch {
		case strings.HasPrefix(p, "-"):
			dir, field = repository.SortDesc, strings.TrimPrefix(p, "-")
		case strings.HasPrefix(p, "+"):
			field = strings.TrimPrefix(p, "+")
		}
		if field == "" {
			continue
		}
		out = append(out, repository.SortClause{Field: field, Direction: dir})
	}
	return out
}

func parseFieldsParam(s string) *repository.FieldsSpec {
	spec := &repository.FieldsSpec{}
	for _, p := range splitCSV(s) {
		switch {
		case strings.HasPrefix(p, "-"):
			spec.Exclude = append(spec.Exclude, strings.TrimPrefix(p, "-"))
		default:
			spec.Include = append(spec.Include, strings.TrimPrefix(p, "+"))
		}
	}
	return spec
}
