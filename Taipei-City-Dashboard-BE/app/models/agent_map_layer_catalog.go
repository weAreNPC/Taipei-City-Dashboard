package models

import (
	"sort"
	"strings"
)

// MapviewOpenableLayer 在某一 mapview 儀表板路由下，可嘗試 open_map_layer 的組件（HasMapLayer）。
type MapviewOpenableLayer struct {
	ComponentIndex string `json:"component_index"`
	Name           string `json:"name"`
	ComponentCity  string `json:"component_city"` // 呼叫 open_map_layer／API 時建議帶的 city
}

// AgentMapviewCatalogEntry 對應 /mapview?index=&city= 的一組 index×city，列出該板含地圖圖層之組件（精簡給 chatbot）。
type AgentMapviewCatalogEntry struct {
	DashboardIndex string                 `json:"dashboard_index"`
	MapviewCity    string                 `json:"mapview_city"`
	OpenableLayers []MapviewOpenableLayer `json:"openable_layers"`
}

const (
	agentMapCatalogMaxEntries     = 100
	agentMapCatalogMaxLayersPerDC = 48
)

// BuildAgentMapLayerCatalog 由 GetComponentRoutingManifest 結果推導「儀表板×city → 可開圖層」小表；與實際側欄一致、非手抄靜態檔。
func BuildAgentMapLayerCatalog(manifest []ComponentRoutingManifestEntry) (out []AgentMapviewCatalogEntry, truncated bool) {
	type groupKey string
	groups := make(map[groupKey]*AgentMapviewCatalogEntry)
	seenLayer := make(map[groupKey]map[string]struct{})

	for _, e := range manifest {
		if !e.HasMapLayer {
			continue
		}
		city := strings.TrimSpace(e.City)
		ci := strings.TrimSpace(e.ComponentIndex)
		if ci == "" {
			continue
		}
		name := strings.TrimSpace(e.Name)
		for _, pl := range e.Placements {
			d := strings.TrimSpace(pl.DashboardIndex)
			if d == "" {
				continue
			}
			gk := groupKey(d + "\x00" + city)
			if groups[gk] == nil {
				groups[gk] = &AgentMapviewCatalogEntry{
					DashboardIndex: d,
					MapviewCity:    city,
					OpenableLayers: nil,
				}
				seenLayer[gk] = make(map[string]struct{})
			}
			if _, ok := seenLayer[gk][ci]; ok {
				continue
			}
			if len(groups[gk].OpenableLayers) >= agentMapCatalogMaxLayersPerDC {
				truncated = true
				continue
			}
			seenLayer[gk][ci] = struct{}{}
			groups[gk].OpenableLayers = append(groups[gk].OpenableLayers, MapviewOpenableLayer{
				ComponentIndex: ci,
				Name:           name,
				ComponentCity:  city,
			})
		}
	}

	keys := make([]string, 0, len(groups))
	for gk := range groups {
		keys = append(keys, string(gk))
	}
	sort.Strings(keys)
	if len(keys) > agentMapCatalogMaxEntries {
		truncated = true
		keys = keys[:agentMapCatalogMaxEntries]
	}
	out = make([]AgentMapviewCatalogEntry, 0, len(keys))
	for _, k := range keys {
		ent := groups[groupKey(k)]
		sort.Slice(ent.OpenableLayers, func(i, j int) bool {
			return ent.OpenableLayers[i].ComponentIndex < ent.OpenableLayers[j].ComponentIndex
		})
		out = append(out, *ent)
	}
	return out, truncated
}
