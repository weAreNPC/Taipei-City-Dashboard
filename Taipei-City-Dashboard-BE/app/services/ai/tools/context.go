package tools

import "context"

type accountIDCtxKey struct{}
type uiContextPayloadCtxKey struct{}

// WithAccountID 將目前請求的使用者帳號 id 放入 context，供需依權限查資料的工具使用（guest 為 0）。
func WithAccountID(ctx context.Context, accountID int) context.Context {
	return context.WithValue(ctx, accountIDCtxKey{}, accountID)
}

// AccountIDFromContext 讀取 WithAccountID 設定的帳號 id；未設定時為 0。
func AccountIDFromContext(ctx context.Context) int {
	v, _ := ctx.Value(accountIDCtxKey{}).(int)
	if v < 0 {
		return 0
	}
	return v
}

// WithUIContextPayload 附帶前端本次 HTTP 請求提供的 UI 快照 JSON 字串，供 get_current_ui_context 使用。
func WithUIContextPayload(ctx context.Context, payload string) context.Context {
	return context.WithValue(ctx, uiContextPayloadCtxKey{}, payload)
}

// UIContextPayloadFromContext 讀取 WithUIContextPayload 設定的 JSON 字串；未設定時為空。
func UIContextPayloadFromContext(ctx context.Context) string {
	v, _ := ctx.Value(uiContextPayloadCtxKey{}).(string)
	return v
}
