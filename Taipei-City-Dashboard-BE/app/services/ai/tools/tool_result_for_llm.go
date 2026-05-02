package tools

import (
	"encoding/json"
	"unicode/utf8"
)

// LLM 對話歷程中每則 tool 回傳的上限（約字符；中日文以 rune 計）。
// 目的：多輪 tool loop 時不把完整 JSON 重複堆進 context。
const (
	maxLLMToolResultRunes         = 4000
	maxGeoStationItems            = 6
	maxUIContextSidebarItems      = 6
	maxUIContextDashComponents    = 16
	maxUIContextDigestItemsLLM    = 24
	maxUIContextMapviewItemsLLM   = 14
	maxJSONArrayElemsGeneric      = 24
)

// CompactToolResultForLLM 將寫入 LLM 訊息之 tool 內容縮減；不影響工具實際回傳值（呼叫端可另存完整字串）。
func CompactToolResultForLLM(toolName, raw string) string {
	if raw == "" {
		return raw
	}
	var out string
	switch toolName {
	case "get_current_ui_context":
		out = trimUIContextForLLMChat(compactUIContextJSON(raw))
	case "get_geo_nearby_for_component":
		out = compactGeoNearbyForLLM(raw)
	case "get_component_facts", "get_dashboard_component_summary":
		out = compactFactsLikeJSONForLLM(raw)
	default:
		out = raw
	}
	return truncateRunesWithNotice(out, maxLLMToolResultRunes)
}

func trimUIContextForLLMChat(raw string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	if d, ok := m["component_routing_digest"].([]interface{}); ok && len(d) > maxUIContextDigestItemsLLM {
		m["component_routing_digest"] = d[:maxUIContextDigestItemsLLM]
		m["digest_truncated_for_llm"] = true
	}
	if c, ok := m["mapview_layer_catalog"].([]interface{}); ok && len(c) > maxUIContextMapviewItemsLLM {
		m["mapview_layer_catalog"] = c[:maxUIContextMapviewItemsLLM]
		m["mapview_truncated_for_llm"] = true
	}
	if sb, ok := m["sidebar_by_city"].([]interface{}); ok && len(sb) > maxUIContextSidebarItems {
		m["sidebar_by_city"] = sb[:maxUIContextSidebarItems]
		m["sidebar_by_city_truncated_for_llm"] = true
	}
	if cd, ok := m["current_dashboard_components"].([]interface{}); ok && len(cd) > maxUIContextDashComponents {
		m["current_dashboard_components"] = cd[:maxUIContextDashComponents]
		m["current_dashboard_components_truncated_for_llm"] = true
	}
	if th, ok := m["thematic_map_component_indexes_loaded"].([]interface{}); ok && len(th) > 12 {
		m["thematic_map_component_indexes_loaded"] = th[:12]
		m["thematic_map_indexes_truncated_for_llm"] = true
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}

func compactGeoNearbyForLLM(raw string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	for _, key := range []string{"nearby_stations", "nearest_stations"} {
		if arr, ok := m[key].([]interface{}); ok && len(arr) > maxGeoStationItems {
			m[key] = arr[:maxGeoStationItems]
			m[key+"_truncated_for_llm"] = true
		}
	}
	stripVerboseGeoHintsForLLM(m)
	m = deepTruncateJSONArrayFields(m, maxGeoStationItems)
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}

func deepTruncateJSONArrayFields(m map[string]interface{}, maxEach int) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = truncateArraysInValue(v, maxEach, 0)
	}
	return out
}

func truncateArraysInValue(v interface{}, maxEach int, depth int) interface{} {
	if depth > 14 {
		return v
	}
	switch x := v.(type) {
	case []interface{}:
		if len(x) > maxEach {
			tail := len(x) - maxEach
			trunc := make([]interface{}, maxEach+1)
			for i := 0; i < maxEach; i++ {
				trunc[i] = truncateArraysInValue(x[i], maxEach, depth+1)
			}
			trunc[maxEach] = map[string]interface{}{
				"_llm_truncated_tail": tail,
			}
			return trunc
		}
		out := make([]interface{}, len(x))
		for i := range x {
			out[i] = truncateArraysInValue(x[i], maxEach, depth+1)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, val := range x {
			out[k] = truncateArraysInValue(val, maxEach, depth+1)
		}
		return out
	default:
		return v
	}
}

func stripVerboseGeoHintsForLLM(m map[string]interface{}) {
	if s, ok := m["interpretation_hint_zh"].(string); ok {
		m["interpretation_hint_zh"] = shortenRunes(s, 140)
	}
	if rs, ok := m["radius_semantics"].(map[string]interface{}); ok {
		if ins, ok := rs["instruction_zh"].(string); ok {
			rs["instruction_zh"] = shortenRunes(ins, 200)
		}
	}
}

func shortenRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

func compactFactsLikeJSONForLLM(raw string) string {
	var top map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		return raw
	}
	trimChartPreviewInMap(top)
	if comps, ok := top["components"].([]interface{}); ok {
		for _, c := range comps {
			if cm, ok := c.(map[string]interface{}); ok {
				trimChartPreviewInMap(cm)
			}
		}
	}
	b, err := json.Marshal(top)
	if err != nil {
		return raw
	}
	s := string(b)
	if utf8.RuneCountInString(s) <= maxLLMToolResultRunes {
		return s
	}
	// 仍過長：拆掉 chart 細節只留提示
	if _, ok := top["chart_preview"]; ok {
		top["chart_preview"] = map[string]interface{}{
			"_omitted_for_llm": true,
			"hint_zh":          "chart_preview 過大已省略；請用 component 欄位與 short_desc 回答，必要時請使用者縮小範圍。",
		}
	}
	if comps, ok := top["components"].([]interface{}); ok {
		for _, c := range comps {
			if cm, ok := c.(map[string]interface{}); ok {
				if _, ok := cm["chart_preview"]; ok {
					cm["chart_preview"] = map[string]interface{}{
						"_omitted_for_llm": true,
					}
				}
			}
		}
	}
	b2, err := json.Marshal(top)
	if err != nil {
		return truncateRunesWithNotice(s, maxLLMToolResultRunes)
	}
	return truncateRunesWithNotice(string(b2), maxLLMToolResultRunes)
}

func trimChartPreviewInMap(m map[string]interface{}) {
	v, ok := m["chart_preview"]
	if !ok || v == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) <= 1800 {
		return
	}
	preview := deepTruncateJSONArrayFields(toMapOrWrap(v), maxJSONArrayElemsGeneric)
	m["chart_preview"] = preview
	if pb, err := json.Marshal(preview); err == nil && len(pb) > 4500 {
		m["chart_preview"] = map[string]interface{}{
			"_compressed_for_llm": true,
			"approx_bytes":        len(b),
			"hint_zh":             "圖表摘要仍過長，已改為占位；請依 component 與其他欄位回答。",
		}
	}
}

func toMapOrWrap(v interface{}) map[string]interface{} {
	switch x := v.(type) {
	case map[string]interface{}:
		return x
	default:
		return map[string]interface{}{"value": x}
	}
}

func truncateRunesWithNotice(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	const tailMsg = "\n... [_llm_truncated: output shortened for context budget]"
	if maxRunes <= utf8.RuneCountInString(tailMsg)+20 {
		runes := []rune(s)
		if len(runes) > maxRunes {
			return string(runes[:maxRunes])
		}
		return s
	}
	budget := maxRunes - utf8.RuneCountInString(tailMsg)
	runes := []rune(s)
	if len(runes) <= budget {
		return s
	}
	return string(runes[:budget]) + tailMsg
}
