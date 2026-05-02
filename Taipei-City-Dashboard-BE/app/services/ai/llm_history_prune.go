package ai

import (
	"strings"
	"unicode/utf8"

	"github.com/tmc/langchaingo/llms"
)

// 單次請求內允許保留「完整內容」的 tool 訊息則數上限；更早的輪次改為占位字串以符合 16k 等模型上限。
const maxFullToolResponsesInHistory = 12

const omittedToolPlaceholder = `{"_llm_omitted":true,"hint_zh":"此輪工具輸出已省略以節省上下文；請依較新的工具結果與使用者問題作答。"}`

// 模型上下文較小（如 16k）時，system 內的全站索引過長為主要瓶頸。
const maxLeadingSystemRunes = 7200

// pruneOldestToolOutputsForBudget 將最舊的若干則 tool 訊息內容替換為短占位，保留 role／tool_call_id／name 供 API 結構合法。
func (s *aiSession) pruneOldestToolOutputsForBudget() {
	var toolIdx []int
	for i := range s.currentMessages {
		if s.currentMessages[i].Role == llms.ChatMessageTypeTool {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) <= maxFullToolResponsesInHistory {
		return
	}
	n := len(toolIdx) - maxFullToolResponsesInHistory
	for j := 0; j < n; j++ {
		i := toolIdx[j]
		m := s.currentMessages[i]
		if len(m.Parts) != 1 {
			continue
		}
		tr, ok := m.Parts[0].(llms.ToolCallResponse)
		if !ok {
			continue
		}
		tr.Content = omittedToolPlaceholder
		s.currentMessages[i].Parts = []llms.ContentPart{tr}
	}
}

// capLeadingSystemMessage 將第一則 system 合併為單一文字並截斷。
// 前端常把【全站組件索引】放在前半、【核心】規則放在後半，故採「頭＋尾」保留，刪中間大塊目錄。
func (s *aiSession) capLeadingSystemMessage() {
	if len(s.currentMessages) == 0 {
		return
	}
	m := &s.currentMessages[0]
	if m.Role != llms.ChatMessageTypeSystem {
		return
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if t, ok := p.(llms.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	combined := b.String()
	if utf8.RuneCountInString(combined) <= maxLeadingSystemRunes {
		return
	}
	m.Parts = []llms.ContentPart{
		llms.TextContent{Text: truncateSystemPreservingTail(combined, maxLeadingSystemRunes)},
	}
}

func truncateSystemPreservingTail(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	const mid = "\n... [_bulk_catalog_or_rules_middle_truncated] ...\n"
	runes := []rune(s)
	n := len(runes)
	midR := utf8.RuneCountInString(mid)
	tailWant := maxRunes * 62 / 100
	headWant := maxRunes - tailWant - midR
	if headWant < 500 {
		headWant = 500
	}
	if tailWant < 1500 {
		tailWant = 1500
	}
	for headWant+midR+tailWant > maxRunes && headWant > 400 {
		headWant--
	}
	tailStart := n - tailWant
	if tailStart <= headWant {
		return string(runes[:maxRunes])
	}
	return string(runes[:headWant]) + mid + string(runes[tailStart:])
}

func truncateAssistantPrefaceForTools(text string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 1200
	}
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	const tail = "\n...[_assistant_preface_truncated]"
	budget := maxRunes - utf8.RuneCountInString(tail)
	if budget < 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:budget]) + tail
}
