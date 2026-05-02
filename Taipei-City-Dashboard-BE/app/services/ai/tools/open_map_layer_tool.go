package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// OpenMapLayer 為「僅前端執行」之 UI 動作；模型常誤當成可呼叫的 function tool。
// 此處回傳結構化 JSON 供後續 finalize／rewriteNavigate 讀取，並引導模型在 ui_actions 輸出標準格式。
func OpenMapLayer(ctx context.Context, args string) (string, error) {
	var p struct {
		ComponentIndex string `json:"component_index"`
		City           string `json:"city"`
		Index          string `json:"index"`
		Component      string `json:"component"`
		Layer          string `json:"layer"`
	}
	if err := parseArgs(args, &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	ci := strings.TrimSpace(p.ComponentIndex)
	if ci == "" {
		ci = strings.TrimSpace(p.Index)
	}
	if ci == "" {
		ci = strings.TrimSpace(p.Component)
	}
	if ci == "" && strings.TrimSpace(p.Layer) != "" {
		switch strings.ToLower(strings.TrimSpace(p.Layer)) {
		case "ubike", "youbike":
			ci = "youbike_availability"
		}
	}
	if ci == "" {
		return `{"ok":false,"error":"missing component_index"}`, nil
	}
	cy := normalizeCity(p.City)
	out := map[string]interface{}{
		"ok":               true,
		"component_index":  ci,
		"city":             cy,
		"ui_actions_hint_zh": "請在回覆 JSON 中輸出 ui_actions（勿再呼叫工具）：" +
			fmt.Sprintf(`[{"type":"open_map_layer","params":{"component_index":%q,"city":%q}}]`, ci, cy),
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
