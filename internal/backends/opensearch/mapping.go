package opensearch

import "encoding/json"

// itemTemplateName is the composable index template name PolyStac
// installs at startup. The wildcard pattern matches every per-collection
// items index (`<prefix><collection>`).
const itemTemplateName = "polystac-items"

// itemIndexTemplate returns the JSON body for the composable index
// template that backs every items_<collection> index. STAC's hot fields
// are mapped explicitly; user properties fall through to a
// dynamic_templates rule that turns strings into keyword fields (so they
// are filterable and sortable without operator intervention).
func itemIndexTemplate(prefix string) []byte {
	tpl := map[string]any{
		"index_patterns": []string{prefix + "*"},
		"priority":       100,
		"template": map[string]any{
			"settings": map[string]any{
				"index.mapping.total_fields.limit": 4000,
				"number_of_shards":                 1,
				"number_of_replicas":               1,
			},
			"mappings": map[string]any{
				"dynamic_templates": []map[string]any{
					{
						"strings_as_keyword": map[string]any{
							"match_mapping_type": "string",
							"mapping": map[string]any{
								"type":         "keyword",
								"ignore_above": 1024,
							},
						},
					},
				},
				"properties": map[string]any{
					"id":             map[string]any{"type": "keyword"},
					"collection":     map[string]any{"type": "keyword"},
					"stac_version":   map[string]any{"type": "keyword"},
					"type":           map[string]any{"type": "keyword"},
					"bbox":           map[string]any{"type": "double"},
					"geometry":       map[string]any{"type": "geo_shape"},
					"properties": map[string]any{
						"properties": map[string]any{
							"datetime":       map[string]any{"type": "date"},
							"start_datetime": map[string]any{"type": "date"},
							"end_datetime":   map[string]any{"type": "date"},
							"created":        map[string]any{"type": "date"},
							"updated":        map[string]any{"type": "date"},
						},
					},
					"assets": map[string]any{"type": "object", "enabled": true},
					"links":  map[string]any{"type": "object", "enabled": false},
				},
			},
		},
	}
	b, _ := json.Marshal(tpl)
	return b
}

// collectionsIndexBody returns the body to create the collections index
// (a single shared index, since collection counts are small).
func collectionsIndexBody() []byte {
	body := map[string]any{
		"settings": map[string]any{
			"number_of_shards":   1,
			"number_of_replicas": 1,
		},
		"mappings": map[string]any{
			"properties": map[string]any{
				"id":          map[string]any{"type": "keyword"},
				"title":       map[string]any{"type": "text", "fields": map[string]any{"keyword": map[string]any{"type": "keyword", "ignore_above": 256}}},
				"description": map[string]any{"type": "text"},
				"license":     map[string]any{"type": "keyword"},
				"keywords":    map[string]any{"type": "keyword"},
			},
		},
	}
	b, _ := json.Marshal(body)
	return b
}
