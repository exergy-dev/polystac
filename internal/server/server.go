package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/example/polystac/pkg/repository"
	"github.com/example/polystac/pkg/stac"
)

// MetricsHandler is the optional /metrics endpoint provider. Kept as an
// interface so the server package doesn't import observability (avoids
// pulling Prometheus into the server's dep graph for tests that don't
// need it).
type MetricsHandler interface {
	Handler() http.Handler
}

// Options configures the server. Constructed once at startup; all fields
// are immutable from the handler's POV.
type Options struct {
	Repo         repository.Repository
	Logger       *slog.Logger
	BaseURL      string
	RootPath     string
	LandingID    string
	LandingTitle string
	LandingDesc  string
	DefaultLimit int
	MaxLimit     int

	// Metrics is optional. When non-nil, /metrics is wired to its handler.
	Metrics MetricsHandler

	// Middleware is wrapped around every handler in registration order.
	// Hooks (Front J) and the Prometheus per-request observer plug in here.
	Middleware []func(http.Handler) http.Handler
}

// Server is the HTTP service. Its Handler() returns a stdlib http.Handler
// that callers wrap (with TLS, behind a load balancer, in a Lambda
// adapter, etc.) without any further server-side dependency.
type Server struct {
	opt         Options
	links       LinkBuilder
	backendName string
	conformance []string
}

// New constructs a Server.
func New(opt Options) (*Server, error) {
	if opt.Repo == nil {
		return nil, errors.New("server: Repo is required")
	}
	if opt.Logger == nil {
		opt.Logger = slog.Default()
	}
	if opt.DefaultLimit <= 0 {
		opt.DefaultLimit = 10
	}
	if opt.MaxLimit <= 0 {
		opt.MaxLimit = 10000
	}
	caps := opt.Repo.Capabilities()
	return &Server{
		opt:         opt,
		links:       LinkBuilder{BaseURL: opt.BaseURL, RootPath: opt.RootPath},
		backendName: caps.Backend,
		conformance: Conformance(opt.Repo),
	}, nil
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.landing)
	mux.HandleFunc("/", s.notFound)
	mux.HandleFunc("GET /conformance", s.conformance_)
	mux.HandleFunc("GET /api", s.openapi)

	mux.HandleFunc("GET /collections", s.listCollections)
	mux.HandleFunc("POST /collections", s.upsertCollection)
	mux.HandleFunc("GET /collections/{id}", s.getCollection)
	mux.HandleFunc("PUT /collections/{id}", s.upsertCollectionByID)
	mux.HandleFunc("DELETE /collections/{id}", s.deleteCollection)

	mux.HandleFunc("GET /collections/{id}/items", s.listItems)
	mux.HandleFunc("POST /collections/{id}/items", s.createItem)
	mux.HandleFunc("GET /collections/{id}/items/{itemId}", s.getItem)
	mux.HandleFunc("PUT /collections/{id}/items/{itemId}", s.upsertItem)
	mux.HandleFunc("DELETE /collections/{id}/items/{itemId}", s.deleteItem)

	mux.HandleFunc("GET /search", s.searchGET)
	mux.HandleFunc("POST /search", s.searchPOST)

	mux.HandleFunc("GET /queryables", s.queryables(""))
	mux.HandleFunc("GET /collections/{id}/queryables", func(w http.ResponseWriter, r *http.Request) {
		s.queryables(r.PathValue("id"))(w, r)
	})

	mux.HandleFunc("GET /_health", s.health)
	mux.HandleFunc("GET /_ready", s.ready)

	if s.opt.Metrics != nil {
		mux.Handle("GET /metrics", s.opt.Metrics.Handler())
	}

	var h http.Handler = mux
	for i := len(s.opt.Middleware) - 1; i >= 0; i-- {
		h = s.opt.Middleware[i](h)
	}
	return s.middleware(h)
}

// ---- middleware ---------------------------------------------------------

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: 200}
		defer func() {
			if rec := recover(); rec != nil {
				s.opt.Logger.Error("panic", "err", fmt.Sprint(rec), "path", r.URL.Path)
				writeError(ww, s.opt.Logger, fmt.Errorf("internal error"))
			}
			s.opt.Logger.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.status,
				"latency_ms", time.Since(start).Milliseconds(),
				"backend", s.backendName,
			)
		}()
		next.ServeHTTP(ww, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// ---- landing / conformance / openapi -----------------------------------

func (s *Server) landing(w http.ResponseWriter, _ *http.Request) {
	cat := stac.Catalog{
		ID:          s.opt.LandingID,
		Title:       s.opt.LandingTitle,
		Description: s.opt.LandingDesc,
		ConformsTo:  s.conformance,
		Links:       s.links.Landing(),
	}
	writeJSON(w, http.StatusOK, cat)
}

func (s *Server) conformance_(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"conformsTo": s.conformance})
}

func (s *Server) openapi(w http.ResponseWriter, _ *http.Request) {
	// Minimal OpenAPI doc. A richer spec can be regenerated from the
	// route table by a Front-A follow-up task; this stub satisfies the
	// service-desc link without 404'ing.
	doc := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   s.opt.LandingTitle,
			"version": "1.0.0",
		},
		"paths": map[string]any{},
	}
	writeJSON(w, http.StatusOK, doc)
}

// ---- collections -------------------------------------------------------

func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) {
	page, err := s.opt.Repo.ListCollections(r.Context(), repository.ListCollectionsOptions{
		Limit: s.opt.DefaultLimit,
		Token: r.URL.Query().Get("token"),
	})
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	for _, c := range page.Items {
		c.Links = s.links.Collection(c.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"collections": page.Items,
		"links":       s.links.Collections(),
	})
}

func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.opt.Repo.GetCollection(r.Context(), id)
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	c.Links = s.links.Collection(c.ID)
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) upsertCollection(w http.ResponseWriter, r *http.Request) {
	c, err := decodeCollection(r)
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	if err := s.opt.Repo.UpsertCollection(r.Context(), c); err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	c.Links = s.links.Collection(c.ID)
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) upsertCollectionByID(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := decodeCollection(r)
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	if c.ID == "" {
		c.ID = id
	}
	if c.ID != id {
		writeError(w, s.opt.Logger, fmt.Errorf("path id %q does not match body id %q: %w", id, c.ID, repository.ErrInvalidInput))
		return
	}
	if err := s.opt.Repo.UpsertCollection(r.Context(), c); err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	c.Links = s.links.Collection(c.ID)
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request) {
	if err := s.opt.Repo.DeleteCollection(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- items -------------------------------------------------------------

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	colID := r.PathValue("id")
	req, err := parseSearchGET(r, s.opt.DefaultLimit)
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	req.Collections = []string{colID}
	s.runSearch(w, r, req)
}

func (s *Server) createItem(w http.ResponseWriter, r *http.Request) {
	colID := r.PathValue("id")
	it, err := decodeItem(r)
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	if it.Collection == "" {
		it.Collection = colID
	}
	if it.Collection != colID {
		writeError(w, s.opt.Logger, fmt.Errorf("path collection %q != body %q: %w", colID, it.Collection, repository.ErrInvalidInput))
		return
	}
	if err := s.opt.Repo.UpsertItem(r.Context(), it); err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	it.Links = s.links.Item(it.Collection, it.ID)
	writeJSON(w, http.StatusCreated, it)
}

func (s *Server) getItem(w http.ResponseWriter, r *http.Request) {
	colID := r.PathValue("id")
	itemID := r.PathValue("itemId")
	it, err := s.opt.Repo.GetItem(r.Context(), colID, itemID)
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	it.Links = s.links.Item(colID, itemID)
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) upsertItem(w http.ResponseWriter, r *http.Request) {
	colID := r.PathValue("id")
	itemID := r.PathValue("itemId")
	it, err := decodeItem(r)
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	if it.ID == "" {
		it.ID = itemID
	}
	if it.Collection == "" {
		it.Collection = colID
	}
	if it.ID != itemID || it.Collection != colID {
		writeError(w, s.opt.Logger, fmt.Errorf("path/body mismatch: %w", repository.ErrInvalidInput))
		return
	}
	if err := s.opt.Repo.UpsertItem(r.Context(), it); err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	it.Links = s.links.Item(colID, itemID)
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request) {
	if err := s.opt.Repo.DeleteItem(r.Context(), r.PathValue("id"), r.PathValue("itemId")); err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- search ------------------------------------------------------------

func (s *Server) searchGET(w http.ResponseWriter, r *http.Request) {
	req, err := parseSearchGET(r, s.opt.DefaultLimit)
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	s.runSearch(w, r, req)
}

func (s *Server) searchPOST(w http.ResponseWriter, r *http.Request) {
	req, err := parseSearchPOST(r, s.opt.DefaultLimit)
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	s.runSearch(w, r, req)
}

func (s *Server) runSearch(w http.ResponseWriter, r *http.Request, req repository.SearchRequest) {
	if req.Limit > s.opt.MaxLimit {
		req.Limit = s.opt.MaxLimit
	}
	page, err := s.opt.Repo.Search(r.Context(), req)
	if err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	for _, it := range page.Items {
		it.Links = s.links.Item(it.Collection, it.ID)
	}
	ic := stac.ItemCollection{
		Features:       deref(page.Items),
		NumberMatched:  page.Matched,
		NumberReturned: len(page.Items),
		Links:          s.links.Pagination(r.URL, page.NextToken, page.PrevToken),
	}
	writeJSON(w, http.StatusOK, ic)
}

func deref(items []*stac.Item) []stac.Item {
	out := make([]stac.Item, 0, len(items))
	for _, it := range items {
		out = append(out, *it)
	}
	return out
}

// ---- queryables --------------------------------------------------------

func (s *Server) queryables(collectionID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q, ok := s.opt.Repo.(repository.Queryables)
		if !ok {
			writeError(w, s.opt.Logger, fmt.Errorf("queryables: %w", repository.ErrNotImplemented))
			return
		}
		doc, err := q.Queryables(r.Context(), collectionID)
		if err != nil {
			writeError(w, s.opt.Logger, err)
			return
		}
		writeJSON(w, http.StatusOK, doc.Schema)
	}
}

// ---- health ------------------------------------------------------------

func (s *Server) notFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{
		"code":        "NotFound",
		"description": "route not found",
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.opt.Repo.Health(ctx); err != nil {
		writeError(w, s.opt.Logger, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// ---- helpers -----------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	b, err := json.Marshal(body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"code":"InternalError","description":"marshal"}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func decodeCollection(r *http.Request) (*stac.Collection, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var c stac.Collection
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("body: %w", repository.ErrInvalidInput)
	}
	return &c, nil
}

func decodeItem(r *http.Request) (*stac.Item, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var it stac.Item
	if err := json.Unmarshal(body, &it); err != nil {
		return nil, fmt.Errorf("body: %w", repository.ErrInvalidInput)
	}
	return &it, nil
}
