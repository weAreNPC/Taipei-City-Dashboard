package tools

import (
	"context"
	"encoding/json"
	"strings"
)

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
	return payload, nil
}
