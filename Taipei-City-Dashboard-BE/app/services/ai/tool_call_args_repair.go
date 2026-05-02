package ai

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

// repairToolCallArguments 在模型吐出空字串、截斷或非預期型別時，盡量正規化 JSON，
// 降低 invalid arguments 與後續對話狀態異常導致上游錯誤的機率。
// 第二個回傳值表示是否有套用任何修復（供日誌用）。
func repairToolCallArguments(toolName string, args string, req AIChatRequest) (string, bool) {
	orig := args
	args = strings.TrimSpace(args)
	if args == "" {
		out := repairEmptyToolArgs(toolName, req)
		if out != "" {
			return out, true
		}
		return orig, false
	}
	if !json.Valid([]byte(args)) {
		out := repairEmptyToolArgs(toolName, req)
		if out != "" {
			return out, true
		}
		return orig, false
	}
	out := args
	changed := false
	out2 := coerceToolJSONNumericFields(toolName, out)
	if out2 != out {
		out = out2
		changed = true
	}
	if toolName == "resolve_navigation_target" {
		out3 := fillResolveNavigationQueryFromUser(out, req)
		if out3 != out {
			out = out3
			changed = true
		}
	}
	if changed {
		return out, true
	}
	return orig, false
}

func lastUserTextFromMessages(messages []llms.MessageContent) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llms.ChatMessageTypeHuman {
			continue
		}
		t := extractText(messages[i])
		if strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// userMessageWarrantsGetCurrentUIContext 判斷「最後一則使用者內容」是否明確與目前介面／定位相關。
// 一般統計、政策、延續上一則主題的追問（未寫畫面／頁面）應回 false，避免模型濫用 get_current_ui_context。
func userMessageWarrantsGetCurrentUIContext(userText string) bool {
	t := strings.TrimSpace(strings.ToLower(userText))
	if t == "" {
		return false
	}
	phrases := []string{
		"畫面", "螢幕", "這頁", "這一頁", "目前頁", "現在這個頁", "我在看",
		"左側", "側欄", "側邊欄", "navbar",
		"開著的", "已開啟", "已開的", "顯示在螢幕",
		"儀表板上", "這個儀表板", "這個板", "目前的板",
		"地圖視窗", "地圖頁", "圖資頁", "目前地圖",
		"我在哪", "這在哪", "目前位置", "我的位置", "定位",
		"gps", "經緯度",
		"介面", "ui ",
		"這張圖", "這個圖表", "上面那個圖",
		"visible layer", "layer open",
		"this page", "current page", "what am i looking",
		"where am i",
	}
	for _, p := range phrases {
		if strings.Contains(t, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func repairEmptyToolArgs(toolName string, req AIChatRequest) string {
	switch toolName {
	case "resolve_navigation_target":
		q := lastUserTextFromMessages(req.Messages)
		if q == "" {
			return ""
		}
		m := map[string]interface{}{
			"query": q,
			"kind":  "component",
		}
		b, err := json.Marshal(m)
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return ""
	}
}

func fillResolveNavigationQueryFromUser(args string, req AIChatRequest) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return args
	}
	if q, ok := m["query"].(string); ok && strings.TrimSpace(q) != "" {
		return args
	}
	u := lastUserTextFromMessages(req.Messages)
	if strings.TrimSpace(u) == "" {
		return args
	}
	m["query"] = strings.TrimSpace(u)
	if _, ok := m["kind"]; !ok {
		m["kind"] = "component"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return string(b)
}

func coerceToolJSONNumericFields(toolName string, raw string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	changed := false
	switch toolName {
	case "get_dashboard_component_summary":
		if v, ok := m["max_components"]; ok {
			if n, ok2 := coerceToInt(v); ok2 {
				m["max_components"] = n
				changed = true
			}
		}
	case "get_geo_nearby_for_component":
		for _, k := range []string{"radius_meters", "top_n", "component_id"} {
			if v, ok := m[k]; ok {
				if n, ok2 := coerceToInt(v); ok2 {
					m[k] = n
					changed = true
				}
			}
		}
	case "get_component_facts":
		if v, ok := m["component_id"]; ok {
			if n, ok2 := coerceToInt(v); ok2 {
				m["component_id"] = n
				changed = true
			}
		}
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

func coerceToInt(v interface{}) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case float32:
		return int(x), true
	case json.Number:
		i, err := x.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}
