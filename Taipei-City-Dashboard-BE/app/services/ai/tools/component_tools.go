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
	DashboardIndex string `json:"dashboard_index"`
	City           string `json:"city"`
	MaxComponents  int    `json:"max_components"`
}

type nearbyComponentArgs struct {
	ComponentIndex string  `json:"component_index"`
	Provider       string  `json:"provider"`
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	RadiusMeters   int     `json:"radius_meters"`
	TopN           int     `json:"top_n"`
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
	return buildNearbySummary(params, points, map[string]interface{}{
		"provider":        params.Provider,
		"component_index": params.ComponentIndex,
	}, func(items []map[string]interface{}) map[string]interface{} {
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

	payload := map[string]interface{}{
		"query_location": map[string]float64{
			"latitude":  params.Latitude,
			"longitude": params.Longitude,
		},
		"radius_meters":        params.RadiusMeters,
		"nearby_station_count": len(nearby),
		"nearby_stations":      nearbyItems,
		"nearest_stations":     formatItems(nearest),
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
