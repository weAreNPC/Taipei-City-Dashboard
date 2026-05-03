package models

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// InferPreferredAgentCityFromUIPayload 由前端 body 之 ui_context JSON 推斷使用者偏好之資料縣市（taipei／metrotaipei）。
// 優先順序：map_context.reverse_geocode（GPS 反向地理）→ current_dashboard.city。
func InferPreferredAgentCityFromUIPayload(uiJSON string) string {
	s := strings.TrimSpace(uiJSON)
	if s == "" {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil || m == nil {
		return ""
	}
	if mc, ok := m["map_context"].(map[string]interface{}); ok && mc != nil {
		if rg, ok := mc["reverse_geocode"].(map[string]interface{}); ok && rg != nil {
			if c := inferTaipeiMetroFromCountyCityZh(fmt.Sprint(rg["county_city"])); c != "" {
				return c
			}
		}
	}
	if cd, ok := m["current_dashboard"].(map[string]interface{}); ok && cd != nil {
		raw := strings.TrimSpace(fmt.Sprint(cd["city"]))
		if c := NormalizeAgentCity(raw); c == "taipei" || c == "metrotaipei" {
			return c
		}
	}
	return ""
}

func inferTaipeiMetroFromCountyCityZh(countyCity string) string {
	s := strings.TrimSpace(countyCity)
	if s == "" {
		return ""
	}
	// 村里界回傳之縣市名；新北與台北分開判斷
	if strings.Contains(s, "新北") {
		return "metrotaipei"
	}
	if strings.Contains(s, "臺北") || strings.Contains(s, "台北") {
		return "taipei"
	}
	return ""
}

// InferPreferredAgentCityFromUIPayloadWithContext 在 InferPreferredAgentCityFromUIPayload 無結果時，
// 以 map_context.user_location 經村里界查詢補齊縣市（與 get_current_ui_context 反向地理一致）。
func InferPreferredAgentCityFromUIPayloadWithContext(ctx context.Context, uiJSON string) string {
	if s := InferPreferredAgentCityFromUIPayload(uiJSON); s != "" {
		return s
	}
	s := strings.TrimSpace(uiJSON)
	if s == "" {
		return ""
	}
	var m map[string]interface{}
	if json.Unmarshal([]byte(s), &m) != nil || m == nil {
		return ""
	}
	mc, ok := m["map_context"].(map[string]interface{})
	if !ok || mc == nil {
		return ""
	}
	ul, ok := mc["user_location"].(map[string]interface{})
	if !ok || ul == nil {
		return ""
	}
	lat, latOk := jsonNumberToFloat64Location(ul["latitude"])
	lng, lngOk := jsonNumberToFloat64Location(ul["longitude"])
	if !latOk || !lngOk || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	place, err := GetLocationDataContext(ctx, lat, lng)
	if err != nil {
		return ""
	}
	return inferTaipeiMetroFromCountyCityZh(strings.TrimSpace(place.CtyName))
}

func jsonNumberToFloat64Location(v interface{}) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// PreferredCityAndReverseGeocodeFromLatLng 單次村里界查詢，供前端決定性導覽推斷縣市並填入 map_context.reverse_geocode。
func PreferredCityAndReverseGeocodeFromLatLng(ctx context.Context, lat, lng float64) (preferredCity string, reverseGeocode map[string]interface{}, err error) {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return "", nil, fmt.Errorf("invalid coordinates")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
	}
	place, err := GetLocationDataContext(ctx, lat, lng)
	if err != nil {
		return "", map[string]interface{}{
			"source": "nlsc_town_village",
			"error":  "lookup_failed",
			"detail": err.Error(),
		}, err
	}
	cty := strings.TrimSpace(place.CtyName)
	town := strings.TrimSpace(place.TownName)
	sect := strings.TrimSpace(place.SectName)
	vil := strings.TrimSpace(place.VillageName)
	line := strings.TrimSpace(cty + town + sect + vil)
	preferredCity = inferTaipeiMetroFromCountyCityZh(cty)
	if line == "" {
		return preferredCity, map[string]interface{}{
			"source": "nlsc_town_village",
			"error":  "empty_result",
		}, nil
	}
	return preferredCity, map[string]interface{}{
		"source":        "nlsc_town_village",
		"county_city":   cty,
		"town_district": town,
		"sect":          sect,
		"village":       vil,
		"admin_line":    line,
	}, nil
}
