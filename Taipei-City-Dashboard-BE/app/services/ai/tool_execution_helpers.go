package ai

import (
	"encoding/json"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// prioritizeToolCallsForContextUbike 將 get_current_ui_context 置於最前、get_nearby_ubike_summary 置於最後，
// 其餘工具維持原有相對順序；避免同一輪並行時 ubike 先於快照執行而無法覆寫經緯度。
func prioritizeToolCallsForContextUbike(calls []llms.ToolCall) []llms.ToolCall {
	if len(calls) < 2 {
		return calls
	}
	var ctxCalls, ubikeCalls, rest []llms.ToolCall
	for _, tc := range calls {
		switch tc.FunctionCall.Name {
		case "get_current_ui_context":
			ctxCalls = append(ctxCalls, tc)
		case "get_nearby_ubike_summary":
			ubikeCalls = append(ubikeCalls, tc)
		default:
			rest = append(rest, tc)
		}
	}
	out := make([]llms.ToolCall, 0, len(calls))
	out = append(out, ctxCalls...)
	out = append(out, rest...)
	out = append(out, ubikeCalls...)
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

// patchNearbyUbikeArgsFromUISnapshot 以前端快照（工具回傳或請求 body 之 ui_context）內 map_context.user_location
// 覆寫 get_nearby_ubike_summary 的經緯度，避免模型在同一輪並行呼叫時填入錯誤座標（如熱門景點預設點）。
func patchNearbyUbikeArgsFromUISnapshot(contextToolResult, requestUIPayload, args string) string {
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
