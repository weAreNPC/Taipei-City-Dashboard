package tools

import (
	"TaipeiCityDashboardBE/app/models"
	"context"
	"encoding/json"
	"fmt"
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
			"id":              component.ID,
			"index":           component.Index,
			"name":            component.Name,
			"city":            component.City,
			"query_type":      component.QueryType,
			"source":          component.Source,
			"short_desc":      component.ShortDesc,
			"use_case":        component.UseCase,
			"update_freq":     component.UpdateFreq,
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
