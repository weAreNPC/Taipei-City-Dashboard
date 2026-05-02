package tools

import (
	"TaipeiCityDashboardBE/app/models"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type componentFactsArgs struct {
	ComponentID    *int   `json:"component_id"`
	ComponentIndex string `json:"component_index"`
	City           string `json:"city"`
	IncludeChart   *bool  `json:"include_chart"`
}

type dashboardSummaryArgs struct {
	DashboardIndex   string   `json:"dashboard_index"`
	City             string   `json:"city"`
	MaxComponents    int      `json:"max_components"`
	ComponentIndexes []string `json:"component_indexes"`
}

type nearbyComponentArgs struct {
	ComponentIndex   string  `json:"component_index"`
	Provider         string  `json:"provider"`
	Latitude         float64 `json:"latitude"`
	Longitude        float64 `json:"longitude"`
	RadiusMeters     int     `json:"radius_meters"`
	TopN             int     `json:"top_n"`
	LocationAnchor   string  `json:"location_anchor,omitempty"` // user_device | explicit；影響 query_location_description 語意提示
}

// geoNearbyForComponentArgs 統一地理鄰近查詢（工具名 get_geo_nearby_for_component）。
// location_anchor：user_device（預設）以瀏覽器 GPS 覆寫經緯度；explicit 以參數經緯度為準（指定地名時須先 resolve_coordinates_zh 或已知座標）。
type geoNearbyForComponentArgs struct {
	ComponentIndex string  `json:"component_index"`
	ComponentID    *int    `json:"component_id"`
	City           string  `json:"city"`
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	RadiusMeters   int     `json:"radius_meters"`
	TopN           int     `json:"top_n"`
	LocationAnchor string  `json:"location_anchor,omitempty"`
}

type geoNearbyPoint struct {
	Latitude  float64
	Longitude float64
	Fields    map[string]interface{}
}

type ubikeFeatureCollection struct {
	Features []struct {
		Geometry struct {
			Coordinates []float64 `json:"coordinates"`
		} `json:"geometry"`
		Properties struct {
			Name                 string      `json:"sna"`
			StationID            interface{} `json:"sno"`
			AvailableRentBikes   float64     `json:"available_rent_general_bikes"`
			AvailableReturnBikes float64     `json:"available_return_bikes"`
		} `json:"properties"`
	} `json:"features"`
}

type ubikeStation struct {
	Name                 string
	StationID            string
	Latitude             float64
	Longitude            float64
	AvailableRentBikes   int
	AvailableReturnBikes int
}

var (
	ubikeStationsOnce sync.Once
	ubikeStations     []ubikeStation
	ubikeStationsErr  error
	// mapGeoJSONPointsCache key = 圖層 index（與 /mapData/{index}.geojson 檔名一致）
	mapGeoJSONPointsCache sync.Map
)

func GetComponentFacts(ctx context.Context, args string) (string, error) {
	var params componentFactsArgs
	if err := parseArgs(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}

	if params.ComponentID == nil && params.ComponentIndex == "" {
		return "", fmt.Errorf("component_id or component_index is required")
	}

	city := params.City
	if city == "" {
		city = "taipei"
	}
	city = normalizeCity(city)

	component, err := findComponent(city, params.ComponentID, params.ComponentIndex)
	if err != nil {
		return "", err
	}

	includeChart := true
	if params.IncludeChart != nil {
		includeChart = *params.IncludeChart
	}

	facts := map[string]interface{}{
		"component": map[string]interface{}{
			"id":               component.ID,
			"index":            component.Index,
			"name":             component.Name,
			"city":             component.City,
			"query_type":       component.QueryType,
			"source":           component.Source,
			"short_desc":       component.ShortDesc,
			"use_case":         component.UseCase,
			"update_freq":      component.UpdateFreq,
			"update_freq_unit": component.UpdateFreqUnit,
		},
		// 供模型對齊工具參數 city 與統計口徑；chart_preview 數值須依此解讀，避免 taipei／metrotaipei 混談。
		"chart_data_scope": chartPreviewDataScopeForAgent(city),
	}

	if includeChart {
		preview, previewErr := getChartPreview(int(component.ID), city, component.TimeFrom, component.TimeTo, component.QueryType)
		if previewErr == nil {
			facts["chart_preview"] = preview
		} else {
			facts["chart_preview_error"] = previewErr.Error()
		}
	}

	raw, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func GetDashboardComponentSummary(ctx context.Context, args string) (string, error) {
	var params dashboardSummaryArgs
	if err := parseArgs(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}

	if params.DashboardIndex == "" {
		return "", fmt.Errorf("dashboard_index is required")
	}

	city := params.City
	if city == "" {
		city = "taipei"
	}
	city = normalizeCity(city)
	maxComponents := params.MaxComponents
	if maxComponents <= 0 {
		maxComponents = 5
	}
	if maxComponents > 10 {
		maxComponents = 10
	}

	groups, err := models.GetAllPublicGroupsID()
	if err != nil {
		return "", fmt.Errorf("failed to get public groups: %v", err)
	}

	components, err := models.GetDashboardByIndex(params.DashboardIndex, groups, city)
	if err != nil {
		return "", fmt.Errorf("failed to load dashboard components: %v", err)
	}

	if len(components) == 0 {
		return "", fmt.Errorf("dashboard has no components")
	}

	if len(params.ComponentIndexes) > 0 {
		want := make(map[string]struct{})
		for _, idx := range params.ComponentIndexes {
			s := strings.TrimSpace(idx)
			if s != "" {
				want[s] = struct{}{}
			}
		}
		if len(want) > 0 {
			filtered := make([]models.CityComponent, 0)
			for _, c := range components {
				if _, ok := want[c.Index]; ok {
					filtered = append(filtered, c)
				}
			}
			if len(filtered) > 0 {
				components = filtered
			}
		}
	}

	if len(components) > maxComponents {
		components = components[:maxComponents]
	}

	summary := make([]map[string]interface{}, 0, len(components))
	for _, component := range components {
		item := map[string]interface{}{
			"id":         component.ID,
			"index":      component.Index,
			"name":       component.Name,
			"city":       component.City,
			"query_type": component.QueryType,
			"short_desc": component.ShortDesc,
		}

		preview, previewErr := getChartPreview(int(component.ID), component.City, component.TimeFrom, component.TimeTo, component.QueryType)
		if previewErr == nil {
			item["chart_preview"] = preview
		}

		summary = append(summary, item)
	}

	result := map[string]interface{}{
		"dashboard_index": params.DashboardIndex,
		"city":            city,
		"component_count": len(summary),
		"components":      summary,
	}

	raw, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// deriveGeoNearbyMode 不依賴額外 DB 欄位：YouBike 組件走即時站點；其餘若 map_config 含 geojson 圖層則走 mapData 點位。
func deriveGeoNearbyMode(comp models.CityComponent) string {
	if strings.EqualFold(strings.TrimSpace(comp.Index), "youbike_availability") {
		return "ubike"
	}
	if _, err := firstGeoJSONLayerIndexFromMapConfig(comp.MapConfig); err == nil {
		return "map_geojson"
	}
	return ""
}

// GetGeoNearbyForComponent 依 deriveGeoNearbyMode 路由：ubike | map_geojson。
func GetGeoNearbyForComponent(ctx context.Context, args string) (string, error) {
	var p geoNearbyForComponentArgs
	if err := parseArgs(args, &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	if p.ComponentID == nil && strings.TrimSpace(p.ComponentIndex) == "" {
		return "", fmt.Errorf("component_id or component_index is required")
	}
	city := p.City
	if city == "" {
		city = "taipei"
	}
	city = normalizeCity(city)

	comp, err := findComponent(city, p.ComponentID, strings.TrimSpace(p.ComponentIndex))
	if err != nil {
		return "", err
	}

	switch deriveGeoNearbyMode(comp) {
	case "ubike":
		ub := nearbyComponentArgs{
			ComponentIndex: comp.Index,
			Latitude:       p.Latitude,
			Longitude:      p.Longitude,
			RadiusMeters:   p.RadiusMeters,
			TopN:           p.TopN,
			LocationAnchor: normalizeLocationAnchor(p.LocationAnchor),
		}
		ub = normalizeNearbyComponentArgs(ub, "youbike_availability", "ubike")
		b, err := json.Marshal(ub)
		if err != nil {
			return "", err
		}
		return GetNearbyUbikeSummary(ctx, string(b))
	case "map_geojson":
		points, layerIdx, mergedScope, err := loadMapGeoJSONPointsDualMetro(comp)
		if err != nil {
			return "", err
		}
		near := nearbyComponentArgs{
			ComponentIndex: comp.Index,
			Latitude:       p.Latitude,
			Longitude:      p.Longitude,
			RadiusMeters:   p.RadiusMeters,
			TopN:           p.TopN,
		}
		near = normalizeNearbyComponentArgs(near, comp.Index, "map_geojson")
		itemLabel := "地點"
		if strings.EqualFold(comp.Index, "green_stores") {
			itemLabel = "綠色店家"
		}
		base := map[string]interface{}{
			"provider":          "map_geojson",
			"component_index":   comp.Index,
			"geo_layer_index":   layerIdx,
			"nearby_item_label": itemLabel,
			"interpretation_hint_zh": "本結果僅代表「查詢錨點（query_location）方圓 radius_meters 公尺內」的圖資點位數與距離，不是某行政區、縣市或全臺的註冊／認證總家數。地名經 forward geocoding 後為單一座標，整區級地名可能落在區內任一參考點，邊界外（例如鄰近鄉鎮）的點仍可能出現在 nearest_stations。若使用者要各行政區匯總家數或圖表，應改呼叫 get_component_facts（含 chart，如 green_stores 之 district 彙總）並在 reply 說明兩種口徑差異。",
		}
		for k, v := range mergedScope {
			base[k] = v
		}
		return buildNearbySummary(near, points, base, nil)
	default:
		out, _ := json.Marshal(map[string]interface{}{
			"error":           "geo_nearby_not_supported",
			"component_index": comp.Index,
			"message":         "此組件非 youbike_availability，且 map_config 無 geojson 圖層（或無對應 mapData）；請查 GET /ai/component-routing-manifest 的 geo_nearby_supported。",
		})
		return string(out), nil
	}
}

func GetNearbyUbikeSummary(ctx context.Context, args string) (string, error) {
	var params nearbyComponentArgs
	if err := parseArgs(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	params = normalizeNearbyComponentArgs(params, "youbike_availability", "ubike")

	stations, err := loadUbikeStations()
	if err != nil {
		return "", err
	}

	points := make([]geoNearbyPoint, 0, len(stations))
	for _, station := range stations {
		points = append(points, geoNearbyPoint{
			Latitude:  station.Latitude,
			Longitude: station.Longitude,
			Fields: map[string]interface{}{
				"name":                   station.Name,
				"station_id":             station.StationID,
				"available_rent_bikes":   station.AvailableRentBikes,
				"available_return_bikes": station.AvailableReturnBikes,
			},
		})
	}
	base := map[string]interface{}{
		"provider":        params.Provider,
		"component_index": params.ComponentIndex,
	}
	if ext := queryLocationDescriptionPayload(ctx, params.Latitude, params.Longitude, params.LocationAnchor); len(ext) > 0 {
		for k, v := range ext {
			base[k] = v
		}
	}
	return buildNearbySummary(params, points, base, func(items []map[string]interface{}) map[string]interface{} {
		availableRentTotal := 0
		availableReturnTotal := 0
		for _, item := range items {
			availableRentTotal += getMapInt(item, "available_rent_bikes")
			availableReturnTotal += getMapInt(item, "available_return_bikes")
		}
		return map[string]interface{}{
			"nearby_available_rent_total":   availableRentTotal,
			"nearby_available_return_total": availableReturnTotal,
		}
	})
}

func normalizeNearbyComponentArgs(params nearbyComponentArgs, defaultComponentIndex string, defaultProvider string) nearbyComponentArgs {
	if params.ComponentIndex == "" {
		params.ComponentIndex = defaultComponentIndex
	}
	if params.Provider == "" {
		params.Provider = defaultProvider
	}
	params.LocationAnchor = normalizeLocationAnchor(params.LocationAnchor)
	if params.RadiusMeters <= 0 {
		params.RadiusMeters = 500
	}
	if params.TopN <= 0 {
		params.TopN = 5
	}
	if params.TopN > 20 {
		params.TopN = 20
	}
	return params
}

// normalizeLocationAnchor explicit 表示查詢錨點來自行地名／座標，非瀏覽器 GPS。
func normalizeLocationAnchor(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), "explicit") {
		return "explicit"
	}
	return "user_device"
}

func buildNearbySummary(
	params nearbyComponentArgs,
	points []geoNearbyPoint,
	basePayload map[string]interface{},
	nearbyAggregateFn func(nearbyItems []map[string]interface{}) map[string]interface{},
) (string, error) {
	if params.Latitude < -90 || params.Latitude > 90 || params.Longitude < -180 || params.Longitude > 180 {
		return "", fmt.Errorf("latitude/longitude out of range")
	}

	type pointDistance struct {
		Point          geoNearbyPoint
		DistanceMeters int
	}
	results := make([]pointDistance, 0, len(points))
	for _, point := range points {
		distanceMeters := int(math.Round(haversineMeters(params.Latitude, params.Longitude, point.Latitude, point.Longitude)))
		results = append(results, pointDistance{
			Point:          point,
			DistanceMeters: distanceMeters,
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].DistanceMeters < results[j].DistanceMeters })

	formatItems := func(items []pointDistance) []map[string]interface{} {
		output := make([]map[string]interface{}, 0, len(items))
		for _, item := range items {
			row := map[string]interface{}{
				"distance_meters": item.DistanceMeters,
				"latitude":        item.Point.Latitude,
				"longitude":       item.Point.Longitude,
			}
			for k, v := range item.Point.Fields {
				row[k] = v
			}
			output = append(output, row)
		}
		return output
	}

	nearby := make([]pointDistance, 0)
	for _, item := range results {
		if item.DistanceMeters <= params.RadiusMeters {
			nearby = append(nearby, item)
		}
	}
	nearestLimit := params.TopN
	if len(results) < nearestLimit {
		nearestLimit = len(results)
	}
	nearest := results[:nearestLimit]
	nearbyItems := formatItems(nearby)

	nearestOutside := 0
	for _, item := range nearest {
		if item.DistanceMeters > params.RadiusMeters {
			nearestOutside++
		}
	}
	payload := map[string]interface{}{
		"query_location": map[string]float64{
			"latitude":  params.Latitude,
			"longitude": params.Longitude,
		},
		"radius_meters":        params.RadiusMeters,
		"nearby_station_count": len(nearby),
		"nearby_stations":      nearbyItems,
		"nearest_stations":     formatItems(nearest),
		"radius_semantics": map[string]interface{}{
			"radius_meters":                         params.RadiusMeters,
			"count_within_radius_only":              len(nearby),
			"nearest_list_is_global_distance_rank":  true,
			"nearest_list_max_entries":              len(nearest),
			"nearest_entries_entirely_outside_radius": nearestOutside,
			"instruction_zh": "nearby_station_count／nearby_stations 僅包含距離不超過 radius_meters 的點。nearest_stations 為「全部候選點」依距離排序後取前 top_n 筆，可能全部在半徑外；請逐筆讀取 distance_meters。若 count_within_radius_only 為 0，禁止將 nearest 描述成「附近（半徑內）」，應明講半徑內 0 點，並說明最近點約幾公尺（跨縣市時須說明資料為雙北合併後結果）。",
		},
	}
	for k, v := range basePayload {
		payload[k] = v
	}
	if nearbyAggregateFn != nil {
		for k, v := range nearbyAggregateFn(nearbyItems) {
			payload[k] = v
		}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func getMapInt(row map[string]interface{}, key string) int {
	value, ok := row[key]
	if !ok {
		return 0
	}
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

func findComponent(city string, componentID *int, componentIndex string) (models.CityComponent, error) {
	var component models.CityComponent

	query := models.DBManager.Table("components").
		Select(`components.id, components.index, components.name,
			row_to_json(component_charts.*) AS chart_config,
			query_charts.history_config,
			query_charts.map_config_ids,
			(
				SELECT COALESCE(json_agg(component_maps), '[]'::json)
				FROM component_maps
				WHERE component_maps.id = ANY(query_charts.map_config_ids)
			) AS map_config,
			query_charts.map_filter, query_charts.time_from, query_charts.time_to,
			query_charts.update_freq, query_charts.update_freq_unit, query_charts.source,
			query_charts.short_desc, query_charts.long_desc, query_charts.use_case,
			query_charts.links, query_charts.contributors, query_charts.created_at,
			query_charts.updated_at, query_charts.query_type, query_charts.query_chart,
			query_charts.query_history, query_charts.city`).
		Joins("JOIN component_charts ON components.index = component_charts.index").
		Joins("JOIN query_charts ON components.index = query_charts.index").
		Where("query_charts.city = ?", city)

	if componentID != nil {
		query = query.Where("components.id = ?", *componentID)
	} else {
		query = query.Where("components.index = ?", componentIndex)
	}

	err := query.First(&component).Error
	if err != nil {
		return component, fmt.Errorf("failed to find component: %v", err)
	}
	return component, nil
}

func getChartPreview(componentID int, city string, timeFrom string, timeTo *string, queryType string) (interface{}, error) {
	queryTypeFromDB, queryString, err := models.GetComponentChartDataQuery(componentID, city)
	if err != nil {
		return nil, err
	}
	if queryTypeFromDB == "" || queryString == "" {
		return nil, fmt.Errorf("component has no chart query")
	}

	if queryType == "" {
		queryType = queryTypeFromDB
	}

	timeToValue := time.Now().In(time.FixedZone("UTC+8", 8*60*60)).Format("2006-01-02T15:04:05+08:00")
	if timeTo != nil && *timeTo != "" {
		timeToValue = *timeTo
	}

	switch queryTypeFromDB {
	case "two_d":
		data, err := models.GetTwoDimensionalData(&queryString, timeFrom, timeToValue)
		if err != nil {
			return nil, err
		}
		return data, nil
	case "three_d", "percent":
		data, categories, err := models.GetThreeDimensionalData(&queryString, timeFrom, timeToValue)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"categories": categories,
			"series":     data,
		}, nil
	case "time":
		data, err := models.GetTimeSeriesData(&queryString, timeFrom, timeToValue)
		if err != nil {
			return nil, err
		}
		return data, nil
	case "map_legend":
		data, err := models.GetMapLegendData(&queryString, timeFrom, timeToValue)
		if err != nil {
			return nil, err
		}
		return data, nil
	default:
		return nil, fmt.Errorf("unsupported query_type: %s", queryTypeFromDB)
	}
}

func normalizeCity(city string) string {
	value := strings.ToLower(strings.TrimSpace(city))
	switch value {
	case "taipei", "tpe", "台北", "臺北", "台北市", "臺北市":
		return "taipei"
	case "new_taipei", "newtaipei", "metrotaipei", "ntpc", "新北", "新北市":
		return "metrotaipei"
	default:
		return value
	}
}

// chartPreviewDataScopeForAgent 說明本次 chart_preview 對應的資料範圍（與 query_charts.city 一致）。
func chartPreviewDataScopeForAgent(city string) map[string]string {
	c := normalizeCity(city)
	switch c {
	case "taipei":
		return map[string]string{
			"city_param":    "taipei",
			"label_zh":      "僅臺北市",
			"interpretation": "本次 chart_preview 為臺北市行政區統計。不得與 city=metrotaipei（雙北合併）數字混在同一句而不標示來源；亦不可將兩組數字相加當作同一口徑。",
		}
	case "metrotaipei":
		return map[string]string{
			"city_param":    "metrotaipei",
			"label_zh":      "臺北市＋新北市（雙北合併）",
			"interpretation": "本次 chart_preview 為雙北合併後依行政區呈現（可能含新北區名）。臺北市轄區與僅 taipei 查詢之前列名次／數值可能相同屬正常；reply 須標明「雙北（metrotaipei）」。",
		}
	default:
		return map[string]string{
			"city_param": c,
			"label_zh":   c,
			"interpretation": "請依 component.city 與本次呼叫參數 city 解讀 chart_preview。",
		}
	}
}

func loadUbikeStations() ([]ubikeStation, error) {
	ubikeStationsOnce.Do(func() {
		candidates := make([]string, 0)
		if customDir := strings.TrimSpace(os.Getenv("UBIKE_GEOJSON_DIR")); customDir != "" {
			candidates = append(candidates, filepath.Clean(customDir))
		}
		// Resolve from process working directory (for local dev / service runs).
		candidates = append(candidates,
			filepath.Clean(filepath.Join("Taipei-City-Dashboard-FE", "public", "mapData")),
			filepath.Clean(filepath.Join("..", "Taipei-City-Dashboard-FE", "public", "mapData")),
			filepath.Clean(filepath.Join("..", "..", "Taipei-City-Dashboard-FE", "public", "mapData")),
		)
		// Resolve from current source file location (stable against cwd differences).
		if _, sourceFile, _, ok := runtime.Caller(0); ok {
			toolsDir := filepath.Dir(sourceFile)
			beRoot := filepath.Clean(filepath.Join(toolsDir, "..", "..", "..", ".."))
			repoRoot := filepath.Clean(filepath.Join(beRoot, ".."))
			candidates = append(candidates,
				filepath.Join(repoRoot, "Taipei-City-Dashboard-FE", "public", "mapData"),
				filepath.Join(beRoot, "..", "Taipei-City-Dashboard-FE", "public", "mapData"),
			)
		}
		files := []string{"youbike_realtime.geojson", "youbike_realtime_metrotaipei.geojson"}
		var stations []ubikeStation
		for _, dir := range candidates {
			collected, err := loadUbikeStationsFromDir(dir, files)
			ok := err == nil
			if ok && len(collected) > 0 {
				stations = collected
				break
			}
		}
		if len(stations) == 0 {
			for _, baseURL := range ubikeGeojsonBaseURLs() {
				collected, err := loadUbikeStationsFromURL(baseURL, files)
				ok := err == nil
				if ok && len(collected) > 0 {
					stations = collected
					break
				}
			}
		}
		if len(stations) == 0 {
			ubikeStationsErr = fmt.Errorf("failed to load ubike geojson sources from file paths and URLs")
			return
		}
		ubikeStations = stations
	})
	return ubikeStations, ubikeStationsErr
}

func loadUbikeStationsFromDir(dir string, files []string) ([]ubikeStation, error) {
	collected := make([]ubikeStation, 0)
	for _, name := range files {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		stations, err := parseUbikeGeoJSON(raw)
		if err != nil {
			return nil, err
		}
		collected = append(collected, stations...)
	}
	return collected, nil
}

func ubikeGeojsonBaseURLs() []string {
	baseURLs := make([]string, 0)
	if custom := strings.TrimSpace(os.Getenv("UBIKE_GEOJSON_BASE_URL")); custom != "" {
		baseURLs = append(baseURLs, strings.TrimRight(custom, "/"))
	}
	baseURLs = append(baseURLs,
		"http://dashboard-fe:80/mapData",
		"http://dashboard-fe/mapData",
		"http://nginx/mapData",
	)
	return baseURLs
}

func loadUbikeStationsFromURL(baseURL string, files []string) ([]ubikeStation, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	collected := make([]ubikeStation, 0)
	for _, name := range files {
		url := strings.TrimRight(baseURL, "/") + "/" + name
		resp, err := client.Get(url)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("fetch %s failed with status %d", url, resp.StatusCode)
		}
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		stations, err := parseUbikeGeoJSON(raw)
		if err != nil {
			return nil, err
		}
		collected = append(collected, stations...)
	}
	return collected, nil
}

func parseUbikeGeoJSON(raw []byte) ([]ubikeStation, error) {
	var fc ubikeFeatureCollection
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil, err
	}
	stations := make([]ubikeStation, 0, len(fc.Features))
	for _, feature := range fc.Features {
		if len(feature.Geometry.Coordinates) < 2 {
			continue
		}
		stations = append(stations, ubikeStation{
			Name:                 feature.Properties.Name,
			StationID:            fmt.Sprintf("%v", feature.Properties.StationID),
			Latitude:             feature.Geometry.Coordinates[1],
			Longitude:            feature.Geometry.Coordinates[0],
			AvailableRentBikes:   int(feature.Properties.AvailableRentBikes),
			AvailableReturnBikes: int(feature.Properties.AvailableReturnBikes),
		})
	}
	return stations, nil
}

func firstGeoJSONLayerIndexFromMapConfig(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("map_config 空白")
	}
	var layers []map[string]interface{}
	if err := json.Unmarshal(raw, &layers); err != nil {
		return "", fmt.Errorf("map_config: %w", err)
	}
	for _, L := range layers {
		src, _ := L["source"].(string)
		if !strings.EqualFold(strings.TrimSpace(src), "geojson") {
			continue
		}
		idx, _ := L["index"].(string)
		if s := strings.TrimSpace(idx); s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf("map_config 內沒有 source=geojson 的圖層")
}

func mapGeoJSONSearchDirs() []string {
	candidates := make([]string, 0)
	if d := strings.TrimSpace(os.Getenv("MAP_GEOJSON_DIR")); d != "" {
		candidates = append(candidates, filepath.Clean(d))
	}
	candidates = append(candidates,
		filepath.Clean(filepath.Join("Taipei-City-Dashboard-FE", "public", "mapData")),
		filepath.Clean(filepath.Join("..", "Taipei-City-Dashboard-FE", "public", "mapData")),
		filepath.Clean(filepath.Join("..", "..", "Taipei-City-Dashboard-FE", "public", "mapData")),
	)
	if _, sourceFile, _, ok := runtime.Caller(0); ok {
		toolsDir := filepath.Dir(sourceFile)
		beRoot := filepath.Clean(filepath.Join(toolsDir, "..", "..", "..", ".."))
		repoRoot := filepath.Clean(filepath.Join(beRoot, ".."))
		candidates = append(candidates,
			filepath.Join(repoRoot, "Taipei-City-Dashboard-FE", "public", "mapData"),
			filepath.Join(beRoot, "..", "Taipei-City-Dashboard-FE", "public", "mapData"),
		)
	}
	return candidates
}

func loadGeoJSONBytesForMapLayer(layerIndex string) ([]byte, error) {
	name := strings.TrimSpace(layerIndex) + ".geojson"
	for _, dir := range mapGeoJSONSearchDirs() {
		p := filepath.Join(dir, name)
		raw, err := os.ReadFile(p)
		if err == nil {
			return raw, nil
		}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	for _, baseURL := range ubikeGeojsonBaseURLs() {
		u := strings.TrimRight(baseURL, "/") + "/" + name
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		raw, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil {
			continue
		}
		if resp.StatusCode == http.StatusOK && len(raw) > 0 {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("找不到 %s（請放置於 FE public/mapData 或設定 MAP_GEOJSON_DIR／Dashboard FE mapData URL）", name)
}

func geoJSONFeatureCollectionToPoints(raw []byte) ([]geoNearbyPoint, error) {
	var wrap struct {
		Features []struct {
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
			Properties map[string]interface{} `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	out := make([]geoNearbyPoint, 0, len(wrap.Features))
	for _, f := range wrap.Features {
		if f.Geometry.Type != "Point" {
			continue
		}
		var coords []float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &coords); err != nil || len(coords) < 2 {
			continue
		}
		lon, lat := coords[0], coords[1]
		fields := map[string]interface{}{}
		for k, v := range f.Properties {
			fields[k] = v
		}
		out = append(out, geoNearbyPoint{
			Latitude:  lat,
			Longitude: lon,
			Fields:    fields,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("geojson 中沒有可用的 Point 要素")
	}
	return out, nil
}

func loadPointsFromComponentMapGeoJSON(comp models.CityComponent) ([]geoNearbyPoint, string, error) {
	layerIdx, err := firstGeoJSONLayerIndexFromMapConfig(comp.MapConfig)
	if err != nil {
		return nil, "", err
	}
	if cached, ok := mapGeoJSONPointsCache.Load(layerIdx); ok {
		return cached.([]geoNearbyPoint), layerIdx, nil
	}
	raw, err := loadGeoJSONBytesForMapLayer(layerIdx)
	if err != nil {
		return nil, "", err
	}
	points, err := geoJSONFeatureCollectionToPoints(raw)
	if err != nil {
		return nil, "", err
	}
	mapGeoJSONPointsCache.Store(layerIdx, points)
	return points, layerIdx, nil
}

// loadMapGeoJSONPointsDualMetro 合併 taipei／metrotaipei 兩套 map_config 之 geojson 點位（避免使用者在新北卻只載入臺北市圖資）。
func loadMapGeoJSONPointsDualMetro(primary models.CityComponent) ([]geoNearbyPoint, string, map[string]interface{}, error) {
	points, layerIdx, err := loadPointsFromComponentMapGeoJSON(primary)
	if err != nil {
		return nil, "", nil, err
	}
	meta := map[string]interface{}{
		"geojson_merge_scope": "single_city_only",
	}
	otherCity := "metrotaipei"
	if strings.EqualFold(strings.TrimSpace(primary.City), "metrotaipei") {
		otherCity = "taipei"
	}
	secondary, err := findComponent(otherCity, nil, strings.TrimSpace(primary.Index))
	if err != nil {
		return points, layerIdx, meta, nil
	}
	if deriveGeoNearbyMode(secondary) != "map_geojson" {
		return points, layerIdx, meta, nil
	}
	p2, layer2, err := loadPointsFromComponentMapGeoJSON(secondary)
	if err != nil {
		return points, layerIdx, meta, nil
	}
	merged := mergeGeoNearbyPointsDedupe(points, p2)
	meta["geojson_merge_scope"] = "taipei_metrotaipei_merged"
	meta["merged_geo_layer_indexes"] = layerIdx + "+" + layer2
	meta["geojson_merge_note_zh"] = "已合併臺北市與新北市（雙北）圖資點位後計算距離；與單一 city 參數無關。"
	return merged, layerIdx + "+" + layer2, meta, nil
}

// queryLocationDescriptionPayload 以查詢點經緯度做村里界反向查詢。user_device：描述使用者（GPS）所在；explicit：描述使用者詢問的地點錨點，勿說成「您位於」。
func queryLocationDescriptionPayload(ctx context.Context, lat, lng float64, locationAnchor string) map[string]interface{} {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	subCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	place, err := models.GetLocationDataContext(subCtx, lat, lng)
	if err != nil {
		return nil
	}
	cty := strings.TrimSpace(place.CtyName)
	town := strings.TrimSpace(place.TownName)
	sect := strings.TrimSpace(place.SectName)
	vil := strings.TrimSpace(place.VillageName)
	line := strings.TrimSpace(cty + town + sect + vil)
	if line == "" {
		return nil
	}
	anchor := normalizeLocationAnchor(locationAnchor)
	replyHint := "回覆時請在首段用白話簡述使用者推定所在（可引用 admin_line，或至少縣市＋鄉鎮市區），再說明半徑內站點數、最近站名稱與距離、可借／可還概況。"
	if anchor == "explicit" {
		replyHint = "此 admin_line 描述的是**使用者所問地點**（查詢錨點）所在的行政區劃，**不是**使用者本人 GPS 位置。首段請用「詢問地點位於…／該一带為…／此位置在…」，**禁止**「您位於…」「您人在…」等說法。再列半徑內站點數、最近站名稱與距離、可借／可還概況。"
	}
	return map[string]interface{}{
		"location_anchor": anchor,
		"query_location_description": map[string]interface{}{
			"source":        "nlsc_town_village",
			"county_city":   cty,
			"town_district": town,
			"sect":          sect,
			"village":       vil,
			"admin_line":    line,
			"reply_hint_zh": replyHint,
		},
	}
}

func mergeGeoNearbyPointsDedupe(a, b []geoNearbyPoint) []geoNearbyPoint {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]geoNearbyPoint, 0, len(a)+len(b))
	for _, list := range [][]geoNearbyPoint{a, b} {
		for _, p := range list {
			k := geoPointDedupeKey(p)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

func geoPointDedupeKey(p geoNearbyPoint) string {
	if s := fieldStringFromProps(p.Fields, "商店編號", "station_id", "sno", "id"); s != "" {
		return "id:" + s
	}
	return fmt.Sprintf("%.5f,%.5f", p.Latitude, p.Longitude)
}

func fieldStringFromProps(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		v, ok := m[key]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case string:
			if strings.TrimSpace(x) != "" {
				return strings.TrimSpace(x)
			}
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		case int:
			return strconv.Itoa(x)
		case int64:
			return strconv.FormatInt(x, 10)
		case json.Number:
			return x.String()
		default:
			s := strings.TrimSpace(fmt.Sprint(x))
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371000.0
	toRad := math.Pi / 180.0
	dLat := (lat2 - lat1) * toRad
	dLon := (lon2 - lon1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadius * c
}
