package stac

import "encoding/json"

// Spec versions and well-known constants.
const (
	StacVersion       = "1.0.0"
	ItemTypeFeature   = "Feature"
	CollectionType    = "Collection"
	CatalogType       = "Catalog"
	MediaTypeGeoJSON  = "application/geo+json"
	MediaTypeJSON     = "application/json"
	MediaTypeOpenAPI3 = "application/vnd.oai.openapi+json;version=3.0"
)

// Link is a STAC link object.
type Link struct {
	Href     string `json:"href"`
	Rel      string `json:"rel"`
	Type     string `json:"type,omitempty"`
	Title    string `json:"title,omitempty"`
	Method   string `json:"method,omitempty"`
	Body     any    `json:"body,omitempty"`
	Merge    bool   `json:"merge,omitempty"`
	Hreflang string `json:"hreflang,omitempty"`
}

// Provider describes a STAC provider entry.
type Provider struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	URL         string   `json:"url,omitempty"`
}

// Asset is a STAC asset attached to an Item or Collection.
type Asset struct {
	Href        string         `json:"href"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Type        string         `json:"type,omitempty"`
	Roles       []string       `json:"roles,omitempty"`
	Extra       map[string]any `json:"-"`
}

// MarshalJSON emits canonical asset key order with Extra merged in.
func (a Asset) MarshalJSON() ([]byte, error) {
	base := map[string]any{}
	if a.Href != "" {
		base["href"] = a.Href
	}
	if a.Title != "" {
		base["title"] = a.Title
	}
	if a.Description != "" {
		base["description"] = a.Description
	}
	if a.Type != "" {
		base["type"] = a.Type
	}
	if a.Roles != nil {
		base["roles"] = a.Roles
	}
	for k, v := range a.Extra {
		if _, taken := base[k]; !taken {
			base[k] = v
		}
	}
	canonical := []string{"href", "title", "description", "type", "roles"}
	return marshalOrdered(base, canonical), nil
}

// UnmarshalJSON splits known fields from the unstructured remainder.
func (a *Asset) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	pop := func(k string, dst any) error {
		v, ok := raw[k]
		if !ok {
			return nil
		}
		delete(raw, k)
		return json.Unmarshal(v, dst)
	}
	if err := pop("href", &a.Href); err != nil {
		return err
	}
	if err := pop("title", &a.Title); err != nil {
		return err
	}
	if err := pop("description", &a.Description); err != nil {
		return err
	}
	if err := pop("type", &a.Type); err != nil {
		return err
	}
	if err := pop("roles", &a.Roles); err != nil {
		return err
	}
	if len(raw) == 0 {
		a.Extra = nil
		return nil
	}
	a.Extra = make(map[string]any, len(raw))
	for k, v := range raw {
		var val any
		if err := json.Unmarshal(v, &val); err != nil {
			return err
		}
		a.Extra[k] = val
	}
	return nil
}

// ItemProperties holds the STAC item properties block. The "datetime" key
// (which may be null per spec when start/end_datetime are present) is the
// only required field; the rest are extension or user properties.
type ItemProperties map[string]any

// Item is a STAC Item (a GeoJSON Feature with STAC fields).
type Item struct {
	Type           string           `json:"type"`
	StacVersion    string           `json:"stac_version"`
	StacExtensions []string         `json:"stac_extensions,omitempty"`
	ID             string           `json:"id"`
	Geometry       *Geometry        `json:"geometry"`
	BBox           []float64        `json:"bbox,omitempty"`
	Properties     ItemProperties   `json:"properties"`
	Links          []Link           `json:"links"`
	Assets         map[string]Asset `json:"assets"`
	Collection     string           `json:"collection,omitempty"`
}

// MarshalJSON emits canonical item key order. Property keys are emitted
// with datetime-family fields first (datetime, start_datetime,
// end_datetime, created, updated), then the rest alphabetically. This
// mirrors stac-pydantic's typical output.
func (i Item) MarshalJSON() ([]byte, error) {
	if i.Type == "" {
		i.Type = ItemTypeFeature
	}
	if i.StacVersion == "" {
		i.StacVersion = StacVersion
	}
	if i.Properties == nil {
		i.Properties = ItemProperties{}
	}
	if i.Links == nil {
		i.Links = []Link{}
	}
	if i.Assets == nil {
		i.Assets = map[string]Asset{}
	}
	out := map[string]any{
		"type":         i.Type,
		"stac_version": i.StacVersion,
		"id":           i.ID,
		"geometry":     i.Geometry, // null when nil — see Item geometry rules
		"properties":   propertiesValue(i.Properties),
		"links":        i.Links,
		"assets":       i.Assets,
	}
	if len(i.StacExtensions) > 0 {
		out["stac_extensions"] = i.StacExtensions
	}
	if len(i.BBox) > 0 {
		out["bbox"] = i.BBox
	}
	if i.Collection != "" {
		out["collection"] = i.Collection
	}
	canonical := []string{
		"type", "stac_version", "stac_extensions", "id",
		"geometry", "bbox", "properties", "links", "assets", "collection",
	}
	return marshalOrdered(out, canonical), nil
}

// propertiesValue is a thin wrapper that marshals ItemProperties with
// datetime-family keys first, then alphabetical.
type propertiesValue ItemProperties

func (p propertiesValue) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("{}"), nil
	}
	front := []string{"datetime", "start_datetime", "end_datetime", "created", "updated"}
	flat := map[string]any(p)
	return marshalOrdered(flat, front), nil
}

// SpatialExtent describes a Collection's spatial extent.
type SpatialExtent struct {
	BBox [][]float64 `json:"bbox"`
}

// TemporalExtent describes a Collection's temporal extent.
//
// Each interval is a [start, end] pair where each endpoint is either an
// RFC 3339 timestamp or nil (open).
type TemporalExtent struct {
	Interval [][]*string `json:"interval"`
}

// Extent is the combined spatial+temporal extent on a Collection.
type Extent struct {
	Spatial  SpatialExtent  `json:"spatial"`
	Temporal TemporalExtent `json:"temporal"`
}

// Collection is a STAC Collection.
type Collection struct {
	Type           string                 `json:"type"`
	StacVersion    string                 `json:"stac_version"`
	StacExtensions []string               `json:"stac_extensions,omitempty"`
	ID             string                 `json:"id"`
	Title          string                 `json:"title,omitempty"`
	Description    string                 `json:"description"`
	Keywords       []string               `json:"keywords,omitempty"`
	License        string                 `json:"license"`
	Providers      []Provider             `json:"providers,omitempty"`
	Extent         Extent                 `json:"extent"`
	Summaries      map[string]any         `json:"summaries,omitempty"`
	Links          []Link                 `json:"links"`
	Assets         map[string]Asset       `json:"assets,omitempty"`
	ItemAssets     map[string]Asset       `json:"item_assets,omitempty"`
	Extra          map[string]any         `json:"-"`
}

// MarshalJSON emits canonical collection key order with Extra merged in.
func (c Collection) MarshalJSON() ([]byte, error) {
	if c.Type == "" {
		c.Type = CollectionType
	}
	if c.StacVersion == "" {
		c.StacVersion = StacVersion
	}
	if c.Links == nil {
		c.Links = []Link{}
	}
	out := map[string]any{
		"type":         c.Type,
		"stac_version": c.StacVersion,
		"id":           c.ID,
		"description":  c.Description,
		"license":      c.License,
		"extent":       c.Extent,
		"links":        c.Links,
	}
	if len(c.StacExtensions) > 0 {
		out["stac_extensions"] = c.StacExtensions
	}
	if c.Title != "" {
		out["title"] = c.Title
	}
	if len(c.Keywords) > 0 {
		out["keywords"] = c.Keywords
	}
	if len(c.Providers) > 0 {
		out["providers"] = c.Providers
	}
	if len(c.Summaries) > 0 {
		out["summaries"] = c.Summaries
	}
	if len(c.Assets) > 0 {
		out["assets"] = c.Assets
	}
	if len(c.ItemAssets) > 0 {
		out["item_assets"] = c.ItemAssets
	}
	for k, v := range c.Extra {
		if _, taken := out[k]; !taken {
			out[k] = v
		}
	}
	canonical := []string{
		"type", "stac_version", "stac_extensions", "id",
		"title", "description", "keywords", "license",
		"providers", "extent", "summaries",
		"links", "assets", "item_assets",
	}
	return marshalOrdered(out, canonical), nil
}

// Clone returns a deep copy of the item suitable for handing to a
// caller that may mutate slices/maps without disturbing the source.
func (i *Item) Clone() *Item {
	if i == nil {
		return nil
	}
	cp := *i
	if i.Links != nil {
		cp.Links = append([]Link(nil), i.Links...)
	}
	if i.Properties != nil {
		cp.Properties = make(ItemProperties, len(i.Properties))
		for k, v := range i.Properties {
			cp.Properties[k] = v
		}
	}
	if i.Assets != nil {
		cp.Assets = make(map[string]Asset, len(i.Assets))
		for k, v := range i.Assets {
			cp.Assets[k] = v
		}
	}
	if i.Geometry != nil {
		g := *i.Geometry
		cp.Geometry = &g
	}
	if i.BBox != nil {
		cp.BBox = append([]float64(nil), i.BBox...)
	}
	if i.StacExtensions != nil {
		cp.StacExtensions = append([]string(nil), i.StacExtensions...)
	}
	return &cp
}

// Clone returns a deep copy of the collection.
func (c *Collection) Clone() *Collection {
	if c == nil {
		return nil
	}
	cp := *c
	if c.Links != nil {
		cp.Links = append([]Link(nil), c.Links...)
	}
	if c.Keywords != nil {
		cp.Keywords = append([]string(nil), c.Keywords...)
	}
	if c.StacExtensions != nil {
		cp.StacExtensions = append([]string(nil), c.StacExtensions...)
	}
	return &cp
}

// Catalog is a STAC Catalog (a lightweight container for collections).
type Catalog struct {
	Type           string   `json:"type"`
	StacVersion    string   `json:"stac_version"`
	StacExtensions []string `json:"stac_extensions,omitempty"`
	ID             string   `json:"id"`
	Title          string   `json:"title,omitempty"`
	Description    string   `json:"description"`
	Links          []Link   `json:"links"`
	ConformsTo     []string `json:"conformsTo,omitempty"`
}

// MarshalJSON enforces canonical catalog key order.
func (c Catalog) MarshalJSON() ([]byte, error) {
	if c.Type == "" {
		c.Type = CatalogType
	}
	if c.StacVersion == "" {
		c.StacVersion = StacVersion
	}
	if c.Links == nil {
		c.Links = []Link{}
	}
	out := map[string]any{
		"type":         c.Type,
		"stac_version": c.StacVersion,
		"id":           c.ID,
		"description":  c.Description,
		"links":        c.Links,
	}
	if len(c.StacExtensions) > 0 {
		out["stac_extensions"] = c.StacExtensions
	}
	if c.Title != "" {
		out["title"] = c.Title
	}
	if len(c.ConformsTo) > 0 {
		out["conformsTo"] = c.ConformsTo
	}
	canonical := []string{
		"type", "stac_version", "stac_extensions", "id",
		"title", "description", "conformsTo", "links",
	}
	return marshalOrdered(out, canonical), nil
}

// ItemCollection is the GeoJSON FeatureCollection wrapper STAC search uses.
type ItemCollection struct {
	Type           string   `json:"type"`
	Features       []Item   `json:"features"`
	Links          []Link   `json:"links,omitempty"`
	NumberMatched  *int64   `json:"numberMatched,omitempty"`
	NumberReturned int      `json:"numberReturned"`
	Context        any      `json:"context,omitempty"`
	StacVersion    string   `json:"stac_version,omitempty"`
	StacExtensions []string `json:"stac_extensions,omitempty"`
}

// MarshalJSON enforces canonical FeatureCollection key order.
func (ic ItemCollection) MarshalJSON() ([]byte, error) {
	if ic.Type == "" {
		ic.Type = "FeatureCollection"
	}
	if ic.Features == nil {
		ic.Features = []Item{}
	}
	out := map[string]any{
		"type":           ic.Type,
		"features":       ic.Features,
		"numberReturned": ic.NumberReturned,
	}
	if ic.NumberMatched != nil {
		out["numberMatched"] = *ic.NumberMatched
	}
	if len(ic.Links) > 0 {
		out["links"] = ic.Links
	}
	if ic.Context != nil {
		out["context"] = ic.Context
	}
	if ic.StacVersion != "" {
		out["stac_version"] = ic.StacVersion
	}
	if len(ic.StacExtensions) > 0 {
		out["stac_extensions"] = ic.StacExtensions
	}
	canonical := []string{
		"type", "stac_version", "stac_extensions",
		"numberMatched", "numberReturned", "features", "links", "context",
	}
	return marshalOrdered(out, canonical), nil
}
