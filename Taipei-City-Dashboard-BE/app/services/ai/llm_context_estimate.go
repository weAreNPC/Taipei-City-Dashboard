package ai

import (
	"encoding/json"
	"unicode/utf8"

	"TaipeiCityDashboardBE/global"
	"github.com/tmc/langchaingo/llms"
)

// 以字元量粗估「訊息＋工具定義」所占 token（中英混排與 JSON 的保守近似；與實際 API 計費可能略有差）。
// 採 1 token ≈ 1.6 rune（多為中文與字串欄位時略保守）。
const (
	runesPerTokenNumerator   = 10
	runesPerTokenDenominator = 16
	perMessageOverheadRunes    = 8
	defaultCompressThresholdTokens = 10000
)

func contextCompressThresholdTokens() int {
	th := global.TWCC.ContextCompressAtEstTokens
	if th <= 0 {
		return defaultCompressThresholdTokens
	}
	return th
}

// estimatedContextTokens 粗估本輪送進 LLM 的內容量（currentMessages ＋ 本輪註冊的 tools 定義）。
func (s *aiSession) estimatedContextTokens() int {
	r := estimateMessageContentsRunes(s.currentMessages)
	msgTok := (r * runesPerTokenNumerator) / runesPerTokenDenominator
	msgTok += estimateToolDefsTokens(s.callOpts)
	return msgTok
}

func estimateMessageContentsRunes(msgs []llms.MessageContent) int {
	var runes int
	for _, m := range msgs {
		runes += perMessageOverheadRunes
		for _, p := range m.Parts {
			switch t := p.(type) {
			case llms.TextContent:
				runes += utf8.RuneCountInString(t.Text)
			case llms.ToolCallResponse:
				runes += utf8.RuneCountInString(t.ToolCallID)
				runes += utf8.RuneCountInString(t.Name)
				runes += utf8.RuneCountInString(t.Content)
			case llms.ToolCall:
				runes += utf8.RuneCountInString(t.ID)
				if t.FunctionCall != nil {
					runes += utf8.RuneCountInString(t.FunctionCall.Name)
					runes += utf8.RuneCountInString(t.FunctionCall.Arguments)
				}
			default:
				if b, err := json.Marshal(p); err == nil {
					runes += utf8.RuneCountInString(string(b))
				}
			}
		}
	}
	return runes
}

func estimateToolDefsTokens(opts llms.CallOptions) int {
	if len(opts.Tools) == 0 {
		return 0
	}
	var runes int
	for _, t := range opts.Tools {
		if t.Function == nil {
			continue
		}
		runes += utf8.RuneCountInString(t.Function.Name)
		runes += utf8.RuneCountInString(t.Function.Description)
		if t.Function.Parameters != nil {
			if b, err := json.Marshal(t.Function.Parameters); err == nil {
				runes += utf8.RuneCountInString(string(b))
			}
		}
	}
	return (runes * runesPerTokenNumerator) / runesPerTokenDenominator
}

// compressContextIfEstimatedTokensReachBudget 當粗估 token ≥ 閾值時才做 system 頭尾截斷與舊 tool 占位。
func (s *aiSession) compressContextIfEstimatedTokensReachBudget() {
	if s.estimatedContextTokens() < contextCompressThresholdTokens() {
		return
	}
	s.capLeadingSystemMessage()
	s.pruneOldestToolOutputsForBudget()
}
