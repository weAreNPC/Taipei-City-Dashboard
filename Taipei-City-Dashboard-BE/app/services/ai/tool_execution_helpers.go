package ai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// prioritizeToolCallsForContextGeoNearby 將 get_current_ui_context、resolve_coordinates_zh 置於前，
// get_geo_nearby_for_component 置於最後；避免同一輪並行時地理鄰近早於快照／地名轉座標。
func prioritizeToolCallsForContextGeoNearby(calls []llms.ToolCall) []llms.ToolCall {
	if len(calls) < 2 {
		return calls
	}
	var ctxCalls, geocodeCalls, geoCalls, rest []llms.ToolCall
	for _, tc := range calls {
		switch tc.FunctionCall.Name {
		case "get_current_ui_context":
			ctxCalls = append(ctxCalls, tc)
		case "resolve_coordinates_zh":
			geocodeCalls = append(geocodeCalls, tc)
		case "get_geo_nearby_for_component":
			geoCalls = append(geoCalls, tc)
		default:
			rest = append(rest, tc)
		}
	}
	out := make([]llms.ToolCall, 0, len(calls))
	out = append(out, ctxCalls...)
	out = append(out, geocodeCalls...)
	out = append(out, rest...)
	out = append(out, geoCalls...)
	return out
}

func jsonValueToFloat64(v interface{}) (float64, bool) {
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

func userLocationFromUISnapshotJSON(raw string) (lat, lng float64, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "{\"error\"") {
		return 0, 0, false
	}
	var snap map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return 0, 0, false
	}
	mc, ok := snap["map_context"].(map[string]interface{})
	if !ok {
		return 0, 0, false
	}
	ul, ok := mc["user_location"].(map[string]interface{})
	if !ok || ul == nil {
		return 0, 0, false
	}
	lat, latOk := jsonValueToFloat64(ul["latitude"])
	lng, lngOk := jsonValueToFloat64(ul["longitude"])
	if !latOk || !lngOk {
		return 0, 0, false
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return 0, 0, false
	}
	return lat, lng, true
}

func userLocationFromAnyUISnapshot(contextToolResult, requestUIPayload string) (lat, lng float64, ok bool) {
	if lat, lng, ok = userLocationFromUISnapshotJSON(contextToolResult); ok {
		return lat, lng, true
	}
	return userLocationFromUISnapshotJSON(requestUIPayload)
}

// resolveGeoNearbyArgsOrNoGPS：location_anchor=explicit 時使用參數內經緯度（指定地點）；否則使用裝置 GPS 覆寫。
// resolveCoordinatesToolResult 為同輪已執行之 resolve_coordinates_zh 回傳 JSON，可在 explicit 且座標為 0 時自動注入。
func resolveGeoNearbyArgsOrNoGPS(contextToolResult, requestUIPayload, modelArgs string, resolveCoordinatesToolResult string) (execArgs string, blockedJSON string) {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(modelArgs), &m); err != nil {
		return modelArgs, ""
	}
	anchor := strings.ToLower(strings.TrimSpace(anyStringForGeo(m["location_anchor"])))
	if anchor == "" {
		anchor = "user_device"
	}
	if anchor == "explicit" {
		lat, latOk := jsonValueToFloat64(m["latitude"])
		lng, lngOk := jsonValueToFloat64(m["longitude"])
		if (!latOk || !lngOk || (lat == 0 && lng == 0)) && strings.TrimSpace(resolveCoordinatesToolResult) != "" {
			if rlat, rlng, ok := coordsFromResolveCoordinatesZhJSON(resolveCoordinatesToolResult); ok {
				m["latitude"] = rlat
				m["longitude"] = rlng
				lat, lng, latOk, lngOk = rlat, rlng, true, true
			}
		}
		if !latOk || !lngOk {
			blocked, _ := json.Marshal(map[string]interface{}{
				"error":   "explicit_coordinates_invalid",
				"message": "location_anchor 為 explicit 時須提供有效 latitude／longitude（可先呼叫 resolve_coordinates_zh）。",
			})
			return "", string(blocked)
		}
		if lat == 0 && lng == 0 {
			blocked, _ := json.Marshal(map[string]interface{}{
				"error":   "explicit_coordinates_invalid",
				"message": "explicit 模式不可使用 0,0；請先 resolve_coordinates_zh 或輸入正確經緯度。",
			})
			return "", string(blocked)
		}
		if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
			blocked, _ := json.Marshal(map[string]interface{}{
				"error":   "explicit_coordinates_invalid",
				"message": "經緯度超出有效範圍。",
			})
			return "", string(blocked)
		}
		b, err := json.Marshal(m)
		if err != nil {
			return modelArgs, ""
		}
		return string(b), ""
	}

	if _, _, ok := userLocationFromAnyUISnapshot(contextToolResult, requestUIPayload); !ok {
		blocked, err := json.Marshal(map[string]interface{}{
			"error": "no_gps",
			"message": "尚未取得 GPS 定位（location_anchor 預設為 user_device）。請允許瀏覽器定位，或改用使用者指定的地點：先 resolve_coordinates_zh，再 get_geo_nearby_for_component 並設 location_anchor=explicit 與對應經緯度。",
		})
		if err != nil {
			return "", `{"error":"no_gps","message":"尚未取得 GPS 定位；可改用 explicit 模式並先 resolve_coordinates_zh。"}`
		}
		return "", string(blocked)
	}
	return patchGeoNearbyArgsFromUISnapshot(contextToolResult, requestUIPayload, modelArgs), ""
}

func anyStringForGeo(v interface{}) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func coordsFromResolveCoordinatesZhJSON(raw string) (lat, lng float64, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "{\"error\"") {
		return 0, 0, false
	}
	var o map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		return 0, 0, false
	}
	la, laOk := jsonValueToFloat64(o["latitude"])
	ln, lnOk := jsonValueToFloat64(o["longitude"])
	if !laOk || !lnOk {
		return 0, 0, false
	}
	if la == 0 && ln == 0 {
		return 0, 0, false
	}
	return la, ln, true
}

// patchGeoNearbyArgsFromUISnapshot 以前端快照（工具回傳或請求 body 之 ui_context）內 map_context.user_location
// 覆寫 get_geo_nearby_for_component 的經緯度，避免模型在同一輪並行呼叫時填入錯誤座標（如熱門景點預設點）。
func patchGeoNearbyArgsFromUISnapshot(contextToolResult, requestUIPayload, args string) string {
	lat, lng, ok := userLocationFromUISnapshotJSON(contextToolResult)
	if !ok {
		lat, lng, ok = userLocationFromUISnapshotJSON(requestUIPayload)
	}
	if !ok {
		return args
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return args
	}
	m["latitude"] = lat
	m["longitude"] = lng
	out, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return string(out)
}
