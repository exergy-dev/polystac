package server

import (
	"net/url"
	"path"
	"strings"

	"github.com/example/polystac/pkg/stac"
)

// LinkBuilder builds canonical link sets for landing, collection,
// item, and search responses. It encapsulates the (rootPath, baseURL)
// computation so handlers don't repeat it.
type LinkBuilder struct {
	BaseURL  string // e.g., "https://stac.example.com" or "" (relative)
	RootPath string // e.g., "/stac" or ""
}

func (lb LinkBuilder) abs(p string) string {
	full := path.Join("/", lb.RootPath, p)
	if !strings.HasPrefix(full, "/") {
		full = "/" + full
	}
	if lb.BaseURL == "" {
		return full
	}
	return strings.TrimRight(lb.BaseURL, "/") + full
}

// Landing builds the link set for the landing page.
func (lb LinkBuilder) Landing() []stac.Link {
	root := lb.abs("/")
	return []stac.Link{
		{Rel: "self", Href: root, Type: stac.MediaTypeJSON},
		{Rel: "root", Href: root, Type: stac.MediaTypeJSON},
		{Rel: "data", Href: lb.abs("/collections"), Type: stac.MediaTypeJSON},
		{Rel: "conformance", Href: lb.abs("/conformance"), Type: stac.MediaTypeJSON},
		{Rel: "search", Href: lb.abs("/search"), Type: stac.MediaTypeGeoJSON, Method: "GET"},
		{Rel: "search", Href: lb.abs("/search"), Type: stac.MediaTypeGeoJSON, Method: "POST"},
		{Rel: "service-desc", Href: lb.abs("/api"), Type: stac.MediaTypeOpenAPI3},
	}
}

// Collection builds the link set for a single collection.
func (lb LinkBuilder) Collection(id string) []stac.Link {
	return []stac.Link{
		{Rel: "self", Href: lb.abs("/collections/" + id), Type: stac.MediaTypeJSON},
		{Rel: "root", Href: lb.abs("/"), Type: stac.MediaTypeJSON},
		{Rel: "parent", Href: lb.abs("/"), Type: stac.MediaTypeJSON},
		{Rel: "items", Href: lb.abs("/collections/" + id + "/items"), Type: stac.MediaTypeGeoJSON},
	}
}

// Collections builds the wrapper links for the /collections list.
func (lb LinkBuilder) Collections() []stac.Link {
	return []stac.Link{
		{Rel: "self", Href: lb.abs("/collections"), Type: stac.MediaTypeJSON},
		{Rel: "root", Href: lb.abs("/"), Type: stac.MediaTypeJSON},
	}
}

// Item builds the link set for a single item.
func (lb LinkBuilder) Item(collectionID, itemID string) []stac.Link {
	return []stac.Link{
		{Rel: "self", Href: lb.abs("/collections/" + collectionID + "/items/" + itemID), Type: stac.MediaTypeGeoJSON},
		{Rel: "root", Href: lb.abs("/"), Type: stac.MediaTypeJSON},
		{Rel: "parent", Href: lb.abs("/collections/" + collectionID), Type: stac.MediaTypeJSON},
		{Rel: "collection", Href: lb.abs("/collections/" + collectionID), Type: stac.MediaTypeJSON},
	}
}

// Pagination links for an ItemCollection given the request URL and tokens.
//
// The reqURL parameter is the original request URL (so query params other
// than `token` are preserved across pages). nextTok / prevTok come from
// the Repository's Page envelope; empty means "no link".
func (lb LinkBuilder) Pagination(reqURL *url.URL, nextTok, prevTok string) []stac.Link {
	links := []stac.Link{
		{Rel: "self", Href: lb.abs(reqURL.RequestURI()), Type: stac.MediaTypeGeoJSON},
		{Rel: "root", Href: lb.abs("/"), Type: stac.MediaTypeJSON},
	}
	if nextTok != "" {
		links = append(links, stac.Link{Rel: "next", Href: withToken(lb.abs(reqURL.Path), reqURL.Query(), nextTok), Type: stac.MediaTypeGeoJSON})
	}
	if prevTok != "" {
		links = append(links, stac.Link{Rel: "prev", Href: withToken(lb.abs(reqURL.Path), reqURL.Query(), prevTok), Type: stac.MediaTypeGeoJSON})
	}
	return links
}

func withToken(base string, q url.Values, token string) string {
	cp := make(url.Values, len(q))
	for k, v := range q {
		cp[k] = append([]string(nil), v...)
	}
	cp.Set("token", token)
	return base + "?" + cp.Encode()
}
