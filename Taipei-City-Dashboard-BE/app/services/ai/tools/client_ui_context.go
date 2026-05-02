package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"TaipeiCityDashboardBE/app/models"
)

const (
	maxUIContextDigestItems   = 100
	maxUIContextMapviewItems  = 32
)

// compactUIContextJSON 壓縮重複送入模型之 ui_context，避免觸發 context 長度上限（不刪除 map_context 等關鍵欄位）。
func compactUIContextJSON(raw string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	changed := false
	if d, ok := m["component_routing_digest"].([]interface{}); ok && len(d) > maxUIContextDigestItems {
		m["component_routing_digest"] = d[:maxUIContextDigestItems]
		m["component_routing_digest_truncated"] = true
		changed = true
	}
	if c, ok := m["mapview_layer_catalog"].([]interface{}); ok && len(c) > maxUIContextMapviewItems {
		m["mapview_layer_catalog"] = c[:maxUIContextMapviewItems]
		m["mapview_layer_catalog_truncated"] = true
		changed = true
	}
	if !changed {
		return raw
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}

func jsonNumberToFloat64(v interface{}) (float64, bool) {
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
	default:
		return 0, false
	}
}

// enrichUIContextWithReverseGeocode 於 map_context 附加 NLSC 村里界反向查詢（供「我在哪」等題直接照抄，勿與 current_dashboard.city 混淆）。
func enrichUIContextWithReverseGeocode(ctx context.Context, raw string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	mc, ok := m["map_context"].(map[string]interface{})
	if !ok || mc == nil {
		return raw
	}
	ul, ok := mc["user_location"].(map[string]interface{})
	if !ok || ul == nil {
		return raw
	}
	lat, latOk := jsonNumberToFloat64(ul["latitude"])
	lng, lngOk := jsonNumberToFloat64(ul["longitude"])
	if !latOk || !lngOk {
		return raw
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return raw
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
	}
	place, err := models.GetLocationDataContext(ctx, lat, lng)
	if err != nil {
		mc["reverse_geocode"] = map[string]interface{}{
			"source": "nlsc_town_village",
			"error":  "lookup_failed",
			"detail": err.Error(),
		}
	} else {
		cty := strings.TrimSpace(place.CtyName)
		town := strings.TrimSpace(place.TownName)
		sect := strings.TrimSpace(place.SectName)
		vil := strings.TrimSpace(place.VillageName)
		line := strings.TrimSpace(cty + town + sect + vil)
		if line == "" {
			mc["reverse_geocode"] = map[string]interface{}{
				"source": "nlsc_town_village",
				"error":  "empty_result",
			}
		} else {
			mc["reverse_geocode"] = map[string]interface{}{
				"source":        "nlsc_town_village",
				"county_city":   cty,
				"town_district": town,
				"sect":          sect,
				"village":       vil,
				"admin_line":    line,
			}
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}

// GetCurrentUIContext 將前端於同一請求 body 附帶之 ui_context JSON 回給模型；用於按需載入介面快照以節省 system token。
func GetCurrentUIContext(ctx context.Context, _ string) (string, error) {
	payload := strings.TrimSpace(UIContextPayloadFromContext(ctx))
	if payload == "" {
		out, _ := json.Marshal(map[string]interface{}{
			"error":   "ui_context_unavailable",
			"message": "此請求未附帶 ui_context。一般路由請用 resolve_navigation_target；若需要「目前頁面／地圖」資訊，請確認前端有傳 ui_context 後再呼叫本工具。",
		})
		return string(out), nil
	}
	payload = enrichUIContextWithReverseGeocode(ctx, payload)
	return compactUIContextJSON(payload), nil
}
