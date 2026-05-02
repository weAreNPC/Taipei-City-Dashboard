package ai

import (
	"TaipeiCityDashboardBE/app/services/ai/tools"
	"TaipeiCityDashboardBE/global"
	"TaipeiCityDashboardBE/logs"
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tmc/langchaingo/llms"
)

const (
	toolResultSummarizeMaxInputBytes   = 56000
	toolResultSummarizeMaxOutputTokens = 1400
)

func toolResultSummarizeMinBytes() int {
	n := global.TWCC.ToolResultLLMSummarizeMinBytes
	if n <= 0 {
		return 1024
	}
	return n
}

func toolResultLLMSummarizeEnabled() bool {
	return global.TWCC.ToolResultLLMSummarize != 0
}

// compactToolResultForLLMChain 先沿用既有機械式縮減；必要時再呼叫輕量模型抽取關鍵數值給主對話使用。
func (s *aiSession) compactToolResultForLLMChain(ctx context.Context, toolName, raw string) string {
	compact := tools.CompactToolResultForLLM(toolName, raw)
	if !toolResultLLMSummarizeEnabled() {
		return compact
	}
	if len(raw) < toolResultSummarizeMinBytes() {
		return compact
	}
	if strings.HasPrefix(strings.TrimSpace(raw), "Error:") {
		return compact
	}
	summary, ok := s.summarizeToolPayloadWithSideModel(ctx, toolName, raw)
	if !ok || strings.TrimSpace(summary) == "" {
		return compact
	}
	return summary
}

func (s *aiSession) summarizeToolPayloadWithSideModel(ctx context.Context, toolName, raw string) (string, bool) {
	start := time.Now()
	payload := raw
	truncated := false
	if len(payload) > toolResultSummarizeMaxInputBytes {
		payload = payload[:toolResultSummarizeMaxInputBytes]
		truncated = true
	}
	userText := strings.Builder{}
	userText.WriteString("tool_name: ")
	userText.WriteString(toolName)
	userText.WriteString("\n\n")
	if truncated {
		userText.WriteString("[輸入已截斷；請只根據以下片段萃取，勿臆測缺失部分]\n\n")
	}
	userText.WriteString(payload)

	msgs := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: toolResultSummarizerSystemPrompt()},
			},
		},
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: userText.String()},
			},
		},
	}

	opts := []llms.CallOption{
		llms.WithTools([]llms.Tool{}),
		llms.WithMaxTokens(toolResultSummarizeMaxOutputTokens),
		llms.WithTemperature(0),
		llms.WithMetadata(map[string]interface{}{
			"max_new_tokens": toolResultSummarizeMaxOutputTokens,
			"temperature":    0.0,
		}),
	}

	resp, err := twccModel.GenerateContent(ctx, msgs, opts...)
	if err != nil {
		logs.FError("tool result LLM summarize failed: %v", err)
		return "", false
	}
	s.mergeUsageFromResponse(resp)
	if resp == nil || len(resp.Choices) == 0 {
		return "", false
	}
	out := strings.TrimSpace(resp.Choices[0].Content)
	if out == "" {
		return "", false
	}
	if utf8.RuneCountInString(out) > 6000 {
		r := []rune(out)
		out = string(r[:6000]) + "\n...[_summarized_truncated]"
	}
	logs.FInfo("tool result LLM summarize ok tool=%s runes=%d ms=%d", toolName, utf8.RuneCountInString(out), time.Since(start).Milliseconds())
	return out, true
}

func (s *aiSession) mergeUsageFromResponse(resp *llms.ContentResponse) {
	if resp == nil || len(resp.Choices) == 0 {
		return
	}
	if usage, ok := resp.Choices[0].GenerationInfo["usage"].(map[string]interface{}); ok {
		s.totalInput += parseUsageInt(usage["input_tokens"])
		s.totalOutput += parseUsageInt(usage["output_tokens"])
	}
}

func toolResultSummarizerSystemPrompt() string {
	return `你是後端「工具回傳壓縮器」，輸入為某個工具回傳的 JSON 或文字。
任務：抽出後續對話回答統計／事實題時需要的關鍵資訊，輸出給主模型閱讀；務必精簡。

硬性規則：
1. 只能使用輸入裡出現的數字、文字、單位、年份、類別名稱；禁止臆測、推斷、補齊缺漏或改寫數值。
2. 若輸入明確是錯誤（例如 Error: 開頭），簡短保留錯誤要點即可。
3. 若有 chart_preview／series／data／x_axis／y_axis，請依組件（component index／name／city）列出關鍵數列或最新年度重點；長表只保留回答常問範圍（例如最近 3～5 年、或總計列）。
4. 輸出格式：繁體中文為主，可用條列或一個 JSON 物件（擇一），總長度要短；不要前言後語、不要客套。

輸出本身即為給主模型的 tool 內容，不要包在 markdown 程式碼區塊。`
}
