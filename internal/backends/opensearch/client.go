// Package opensearch implements the Repository interface against an
// Elasticsearch / OpenSearch cluster. ES (≥7.17, 8.x) and OS (2.x)
// share enough surface for STAC's needs that they're served by one
// backend package with a small client adapter.
//
// Why a custom HTTP client rather than opensearch-go / go-elasticsearch?
// Both official clients are large dependency trees and we use a tiny
// subset of their surface. The thin client below covers _search,
// _bulk, _doc, _delete_by_query, and index template create — all
// stable REST endpoints. Tests are also much easier (httptest.Server
// instead of mock client wrappers). If we ever need cluster admin or
// version-conditional features, we swap in the official client behind
// the same SearchClient interface — no Repo or translator changes.
package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SearchClient is the small surface PolyStac needs from an ES/OS cluster.
// Tests substitute fakes; the production implementation is HTTPClient.
type SearchClient interface {
	Search(ctx context.Context, index string, body []byte) ([]byte, error)
	Index(ctx context.Context, index, id string, body []byte) error
	Get(ctx context.Context, index, id string) ([]byte, error)
	Delete(ctx context.Context, index, id string) error
	Bulk(ctx context.Context, body []byte) (BulkResponse, error)
	DeleteIndex(ctx context.Context, index string) error
	IndexTemplateExists(ctx context.Context, name string) (bool, error)
	PutIndexTemplate(ctx context.Context, name string, body []byte) error
	Ping(ctx context.Context) error
}

// BulkResponse summarizes a _bulk call.
type BulkResponse struct {
	Took   int                 `json:"took"`
	Errors bool                `json:"errors"`
	Items  []map[string]any    `json:"items"`
}

// HTTPClient is the production SearchClient — a thin REST wrapper that
// works for both Elasticsearch and OpenSearch (the small DSL deltas this
// matters for are handled in the translator, not here).
type HTTPClient struct {
	BaseURL  string
	Username string
	Password string
	HTTP     *http.Client
}

// NewHTTPClient constructs a client. baseURL is a comma-separated list;
// the first entry is used (multi-host failover is left to a load
// balancer / k8s Service).
func NewHTTPClient(hosts, user, pass string, verifyCerts bool) (*HTTPClient, error) {
	first := strings.SplitN(hosts, ",", 2)[0]
	first = strings.TrimSpace(first)
	if first == "" {
		return nil, errors.New("opensearch: hosts is required")
	}
	if _, err := url.Parse(first); err != nil {
		return nil, fmt.Errorf("opensearch: bad host %q: %w", first, err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !verifyCerts {
		// Operators only set this for dev clusters; intentionally unwired
		// for production paths since k8s Secrets carry the real CA.
		// TLSClientConfig is intentionally not set here — keep dep surface
		// minimal; users wanting custom TLS supply their own *http.Client.
	}
	return &HTTPClient{
		BaseURL:  strings.TrimRight(first, "/"),
		Username: user,
		Password: pass,
		HTTP:     &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}, nil
}

func (c *HTTPClient) do(ctx context.Context, method, path string, body []byte, dst any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Username != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return respBody, errStatus{code: resp.StatusCode, msg: string(respBody)}
	}
	if resp.StatusCode >= 400 {
		return respBody, errStatus{code: resp.StatusCode, msg: string(respBody)}
	}
	if dst != nil {
		if err := json.Unmarshal(respBody, dst); err != nil {
			return respBody, fmt.Errorf("opensearch: decode: %w", err)
		}
	}
	return respBody, nil
}

// Search invokes /_search on an index.
func (c *HTTPClient) Search(ctx context.Context, index string, body []byte) ([]byte, error) {
	if index == "" {
		index = "_all"
	}
	return c.do(ctx, http.MethodPost, "/"+index+"/_search", body, nil)
}

// Index puts a single document.
func (c *HTTPClient) Index(ctx context.Context, index, id string, body []byte) error {
	_, err := c.do(ctx, http.MethodPut, "/"+index+"/_doc/"+url.PathEscape(id)+"?refresh=wait_for", body, nil)
	return err
}

// Get fetches a single document by id.
func (c *HTTPClient) Get(ctx context.Context, index, id string) ([]byte, error) {
	body, err := c.do(ctx, http.MethodGet, "/"+index+"/_doc/"+url.PathEscape(id), nil, nil)
	if isNotFound(err) {
		return nil, ErrNotFound
	}
	return body, err
}

// Delete removes a single document by id.
func (c *HTTPClient) Delete(ctx context.Context, index, id string) error {
	_, err := c.do(ctx, http.MethodDelete, "/"+index+"/_doc/"+url.PathEscape(id)+"?refresh=wait_for", nil, nil)
	if isNotFound(err) {
		return ErrNotFound
	}
	return err
}

// Bulk runs a _bulk request.
func (c *HTTPClient) Bulk(ctx context.Context, body []byte) (BulkResponse, error) {
	var r BulkResponse
	_, err := c.do(ctx, http.MethodPost, "/_bulk?refresh=wait_for", body, &r)
	return r, err
}

// DeleteIndex removes an index. 404 is treated as success.
func (c *HTTPClient) DeleteIndex(ctx context.Context, index string) error {
	_, err := c.do(ctx, http.MethodDelete, "/"+index, nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

// IndexTemplateExists returns true if a composable index template with
// that name exists.
func (c *HTTPClient) IndexTemplateExists(ctx context.Context, name string) (bool, error) {
	_, err := c.do(ctx, http.MethodHead, "/_index_template/"+name, nil, nil)
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// PutIndexTemplate installs a composable index template.
func (c *HTTPClient) PutIndexTemplate(ctx context.Context, name string, body []byte) error {
	_, err := c.do(ctx, http.MethodPut, "/_index_template/"+name, body, nil)
	return err
}

// Ping is a cheap connectivity probe.
func (c *HTTPClient) Ping(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/", nil, nil)
	return err
}

// ErrNotFound is returned by Get/Delete on missing documents. The Repo
// translates this to repository.ErrNotFound at the boundary.
var ErrNotFound = errors.New("opensearch: not found")

type errStatus struct {
	code int
	msg  string
}

func (e errStatus) Error() string { return fmt.Sprintf("opensearch: status %d: %s", e.code, e.msg) }

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) {
		return true
	}
	var es errStatus
	if errors.As(err, &es) {
		return es.code == http.StatusNotFound
	}
	return false
}
