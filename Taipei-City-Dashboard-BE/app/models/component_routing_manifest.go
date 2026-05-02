package models

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/lib/pq"
)

// ComponentPlacementInfo 組件出現在側欄哪一個儀表板（同一組件可能因 city 不同有多筆）。
type ComponentPlacementInfo struct {
	DashboardIndex string `json:"dashboard_index"`
	DashboardName  string `json:"dashboard_name"`
	SidebarSource  string `json:"sidebar_source"` // taipei | metrotaipei | public | personal
}

// ComponentRoutingLinks 前端相對路徑（不含 origin）；Agent 可直接組完整 URL。
type ComponentRoutingLinks struct {
	ComponentPage string `json:"component_page"`
	Dashboard     string `json:"dashboard"`
	Mapview       string `json:"mapview"`
}

// ComponentRoutingManifestEntry 單一 components.id × query_charts.city 的導覽資訊。
type ComponentRoutingManifestEntry struct {
	ID             int64                     `json:"id"`
	ComponentIndex string                    `json:"component_index"`
	Name           string                    `json:"name"`
	City           string                    `json:"city"`
	HasMapLayer    bool                      `json:"has_map_layer"`
	// GeoNearbySupported 由後端推導：index=youbike_availability，或 map_config 所綁 component_maps 含 source=geojson。
	GeoNearbySupported bool                  `json:"geo_nearby_supported"`
	GeoNearbyProvider  string                `json:"geo_nearby_provider,omitempty"` // 推導值 ubike | map_geojson，僅供模型參考
	Links          ComponentRoutingLinks     `json:"links"`
	Placements     []ComponentPlacementInfo  `json:"placements"`
}

// GetComponentRoutingManifest 依使用者可見的儀表板，彙整所有組件的可分享連結與所屬儀表板（供 AI「全知」導覽）。
func GetComponentRoutingManifest(accountID int) ([]ComponentRoutingManifestEntry, error) {
	data, err := GetAllDashboards(accountID)
	if err != nil {
		return nil, err
	}

	placementsByKey := make(map[string][]ComponentPlacementInfo)
	idSet := make(map[int64]struct{})

	addPlacements := func(d Dashboard, sidebarSource string, cities []string) {
		for _, cid := range d.Components {
			idSet[cid] = struct{}{}
			for _, city := range cities {
				key := fmt.Sprintf("%d:%s", cid, city)
				placementsByKey[key] = append(placementsByKey[key], ComponentPlacementInfo{
					DashboardIndex: d.Index,
					DashboardName:  d.Name,
					SidebarSource:  sidebarSource,
				})
			}
		}
	}

	for _, d := range data.Taipei {
		addPlacements(d, "taipei", []string{"taipei"})
	}
	for _, d := range data.MetroTaipei {
		addPlacements(d, "metrotaipei", []string{"metrotaipei"})
	}
	for _, d := range data.Public {
		addPlacements(d, "public", []string{"taipei", "metrotaipei"})
	}
	for _, d := range data.Personal {
		addPlacements(d, "personal", []string{"taipei", "metrotaipei"})
	}

	if len(idSet) == 0 {
		return []ComponentRoutingManifestEntry{}, nil
	}

	ids := make([]int64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	type qcJoinRow struct {
		ID                 int64         `gorm:"column:id"`
		Index              string        `gorm:"column:index"`
		Name               string        `gorm:"column:name"`
		City               string        `gorm:"column:city"`
		MapConfigIDs       pq.Int64Array `gorm:"column:map_config_ids;type:integer[]"`
		GeoNearbySupported bool          `gorm:"column:geo_nearby_supported"`
		GeoNearbyProvider  string        `gorm:"column:geo_nearby_provider"`
	}
	var qcRows []qcJoinRow
	err = DBManager.Table("components c").
		Select(`c.id, c.index, c.name, qc.city, qc.map_config_ids,
			(c.index = 'youbike_availability' OR (qc.map_config_ids IS NOT NULL AND EXISTS (
				SELECT 1 FROM component_maps cm
				WHERE cm.id = ANY(qc.map_config_ids) AND LOWER(TRIM(cm.source)) = 'geojson'
			))) AS geo_nearby_supported,
			CASE
				WHEN c.index = 'youbike_availability' THEN 'ubike'
				WHEN qc.map_config_ids IS NOT NULL AND EXISTS (
					SELECT 1 FROM component_maps cm
					WHERE cm.id = ANY(qc.map_config_ids) AND LOWER(TRIM(cm.source)) = 'geojson'
				) THEN 'map_geojson'
				ELSE ''
			END AS geo_nearby_provider`).
		Joins("JOIN query_charts qc ON qc.index = c.index").
		Where("c.id IN ?", ids).
		Order("c.id, qc.city").
		Scan(&qcRows).Error
	if err != nil {
		return nil, err
	}

	out := make([]ComponentRoutingManifestEntry, 0, len(qcRows))
	for _, r := range qcRows {
		key := fmt.Sprintf("%d:%s", r.ID, r.City)
		pl := placementsByKey[key]
		if len(pl) == 0 {
			continue
		}
		primary := pl[0]
		idxPath := url.PathEscape(r.Index)
		dashQ := url.QueryEscape(primary.DashboardIndex)
		cityQ := url.QueryEscape(r.City)
		links := ComponentRoutingLinks{
			ComponentPage: fmt.Sprintf("/component/%s?city=%s", idxPath, cityQ),
			Dashboard:     fmt.Sprintf("/dashboard?index=%s&city=%s", dashQ, cityQ),
			Mapview:       fmt.Sprintf("/mapview?index=%s&city=%s", dashQ, cityQ),
		}
		out = append(out, ComponentRoutingManifestEntry{
			ID:             r.ID,
			ComponentIndex: r.Index,
			Name:           r.Name,
			City:           r.City,
			HasMapLayer:    len(r.MapConfigIDs) > 0,
			GeoNearbySupported: r.GeoNearbySupported,
			GeoNearbyProvider:  strings.TrimSpace(r.GeoNearbyProvider),
			Links:          links,
			Placements:     pl,
		})
	}
	return out, nil
}
