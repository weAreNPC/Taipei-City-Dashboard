package ai

import (
	"TaipeiCityDashboardBE/app/models"
	"TaipeiCityDashboardBE/app/services/ai/providers/twcc"
	"TaipeiCityDashboardBE/app/services/ai/tools"
	"TaipeiCityDashboardBE/global"
	"TaipeiCityDashboardBE/logs"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms"
	"golang.org/x/sync/semaphore"
)

var (
	// aiSemaphore limits the number of concurrent AI requests
	aiSemaphore *semaphore.Weighted
	twccModel   llms.Model
)

func init() {
	aiSemaphore = semaphore.NewWeighted(int64(global.TWCC.MaxConcurrent))
	twccModel = twcc.New(
		global.TWCC.ApiKey,
		global.TWCC.ApiUrl,
		global.TWCC.Model,
		global.TWCC.Timeout,
	)
}

type AIChatRequest struct {
	SessionID string                 `json:"session"`
	UserID    string                 `json:"user_id"`
	IPAddress string                 `json:"ip_address"`
	Messages  []llms.MessageContent  `json:"messages"`
	Params    map[string]interface{} `json:"params"`
	// UIContextPayload 為前端本次請求附帶的 JSON 字串（與 messages 分離），僅供工具 get_current_ui_context 回傳，避免塞入 system 訊息佔用 token。
	UIContextPayload string `json:"-"`
}

// ChatWithTWCC handles the AI conversation logic including retries, tool calling loop, and logging.
func ChatWithTWCC(ctx context.Context, req AIChatRequest, options ...llms.CallOption) (*models.AIChatLog, error) {
	if err := aiSemaphore.Acquire(ctx, 1); err != nil {
		return nil, fmt.Errorf("server too busy: %v", err)
	}
	defer aiSemaphore.Release(1)

	session := newSession(req, options...)
	return session.run(ctx)
}

func newSession(req AIChatRequest, options ...llms.CallOption) *aiSession {
	s := &aiSession{
		req:             req,
		options:         options,
		currentMessages: make([]llms.MessageContent, 0),
		lastToolResults: make(map[string]string),
		startTime:       time.Now(),
	}
	for _, opt := range options {
		opt(&s.callOpts)
	}
	s.injectInstructions()
	return s
}

type aiSession struct {
	req             AIChatRequest
	options         []llms.CallOption
	callOpts        llms.CallOptions
	currentMessages []llms.MessageContent
	totalInput      int
	totalOutput     int
	toolUsed        bool
	executedTools   []string
	lastToolResults map[string]string
	lastResp        *llms.ContentResponse
	lastErr         error
	startTime       time.Time
}

func (s *aiSession) run(ctx context.Context) (*models.AIChatLog, error) {
	maxLoops := 8
	s.executedTools = make([]string, 0)
	for i := 0; i < maxLoops; i++ {
		s.sendHeartbeat(ctx)

		if err := s.generate(ctx); err != nil {
			break
		}

		toolCalls := s.extractToolCalls()
		if len(toolCalls) == 0 {
			break
		}

		s.toolUsed = true
		logs.FInfo("Loop %d: Processing %d tool calls", i, len(toolCalls))
		if err := s.executeTools(ctx, toolCalls); err != nil {
			break
		}
	}
	if s.lastErr == nil {
		toolCalls := s.extractToolCalls()
		if len(toolCalls) > 0 {
			s.forceFinalJSONResponse(ctx)
		}
	}
	return s.finalize(ctx)
}

func (s *aiSession) sendHeartbeat(ctx context.Context) {
	if s.callOpts.StreamingFunc != nil {
		s.callOpts.StreamingFunc(ctx, []byte(": heartbeat\n\n"))
	}
}

func (s *aiSession) generate(ctx context.Context) error {
	maxRetry := global.TWCC.MaxRetry
	if s.callOpts.StreamingFunc != nil {
		maxRetry = 0
	}

	for i := 0; i <= maxRetry; i++ {
		s.compressContextIfEstimatedTokensReachBudget()
		s.lastResp, s.lastErr = twccModel.GenerateContent(ctx, s.currentMessages, s.options...)
		if s.lastErr == nil {
			s.updateTokens()
			return nil
		}
		logs.FError("Attempt %d failed: %v", i+1, s.lastErr)
		if i < maxRetry {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return s.lastErr
}

func (s *aiSession) extractToolCalls() []llms.ToolCall {
	if s.lastResp == nil || len(s.lastResp.Choices) == 0 {
		return nil
	}
	tc, _ := s.lastResp.Choices[0].GenerationInfo["tool_calls"].([]llms.ToolCall)
	return tc
}

func (s *aiSession) updateTokens() {
	if s.lastResp == nil || len(s.lastResp.Choices) == 0 {
		return
	}
	if usage, ok := s.lastResp.Choices[0].GenerationInfo["usage"].(map[string]interface{}); ok {
		s.totalInput += parseUsageInt(usage["input_tokens"])
		s.totalOutput += parseUsageInt(usage["output_tokens"])
	}
}

func parseAIAccountID(userID string) int {
	id, err := strconv.Atoi(strings.TrimSpace(userID))
	if err != nil || id < 0 {
		return 0
	}
	return id
}

func (s *aiSession) executeTools(ctx context.Context, toolCalls []llms.ToolCall) error {
	choice := s.lastResp.Choices[0]

	assistText := choice.Content
	if s.estimatedContextTokens() >= contextCompressThresholdTokens() {
		assistText = truncateAssistantPrefaceForTools(choice.Content, 1200)
	}
	s.currentMessages = append(s.currentMessages, llms.MessageContent{
		Role:  llms.ChatMessageTypeAI,
		Parts: append([]llms.ContentPart{llms.TextContent{Text: assistText}}, toolsToParts(toolCalls)...),
	})

	toolCtx := tools.WithAccountID(ctx, parseAIAccountID(s.req.UserID))
	toolCtx = tools.WithUIContextPayload(toolCtx, s.req.UIContextPayload)
	orderedCalls := prioritizeToolCallsForContextGeoNearby(toolCalls)
	for _, tc := range orderedCalls {
		s.executedTools = append(s.executedTools, tc.FunctionCall.Name)
		result := ""
		args, argsRepaired := repairToolCallArguments(tc.FunctionCall.Name, tc.FunctionCall.Arguments, s.req)
		if argsRepaired {
			logs.FInfo("tool args repair applied: %s", tc.FunctionCall.Name)
		}
		skipExecute := false
		if tc.FunctionCall.Name == "get_current_ui_context" {
			u := lastUserTextFromMessages(s.req.Messages)
			if !userMessageWarrantsGetCurrentUIContext(u) {
				result = `{"error":"ui_context_blocked","message_zh":"使用者未詢問目前畫面、頁面、地圖視窗、已開圖層或「我在哪」等介面／定位情境；已阻擋 get_current_ui_context。請改用 resolve_navigation_target、get_component_facts、get_dashboard_component_summary 取得資料；延續上一則主題時請依對話與工具結果作答，勿改讀目前 UI。"}`
				skipExecute = true
			}
		}
		if tc.FunctionCall.Name == "get_geo_nearby_for_component" {
			var blocked string
			args, blocked = resolveGeoNearbyArgsOrNoGPS(
				s.lastToolResults["get_current_ui_context"],
				s.req.UIContextPayload,
				args,
				s.lastToolResults["resolve_coordinates_zh"],
			)
			if blocked != "" {
				result = blocked
				skipExecute = true
			}
		}
		if !skipExecute {
			if !tools.Exists(tc.FunctionCall.Name) {
				result = fmt.Sprintf("Error: tool %s is not a backend tool. Do not call it as a tool. If it is an UI action (navigate_dashboard, open_component_info, switch_city, open_map_layer), place it in final JSON field ui_actions instead.", tc.FunctionCall.Name)
				logs.FError("Tool Error: tool %s not found", tc.FunctionCall.Name)
			} else {
				var err error
				result, err = tools.Execute(toolCtx, tc.FunctionCall.Name, args)
				if err != nil {
					result = fmt.Sprintf("Error: %v. Please verify arguments.", err)
					logs.FError("Tool Error: %v", err)
				}
			}
		}
		s.lastToolResults[tc.FunctionCall.Name] = result

		llmToolContent := s.compactToolResultForLLMChain(ctx, tc.FunctionCall.Name, result)
		s.currentMessages = append(s.currentMessages, llms.MessageContent{
			Role: llms.ChatMessageTypeTool,
			Parts: []llms.ContentPart{llms.ToolCallResponse{
				ToolCallID: tc.ID, Name: tc.FunctionCall.Name, Content: llmToolContent,
			}},
		})
	}
	s.compressContextIfEstimatedTokensReachBudget()
	return nil
}

func (s *aiSession) forceFinalJSONResponse(ctx context.Context) {
	s.compressContextIfEstimatedTokensReachBudget()
	s.currentMessages = append(s.currentMessages, llms.MessageContent{
		Role: llms.ChatMessageTypeSystem,
		Parts: []llms.ContentPart{
			llms.TextContent{Text: "Stop calling tools. Now produce final answer only. Return strict JSON with keys reply and ui_actions. ui_actions can only include: navigate_dashboard, open_component_info, switch_city, open_map_layer. reply MUST use real numbers and units from tool results above when tools returned data; if tools failed, say so honestly—do not invent statistics."},
		},
	})
	resp, err := twccModel.GenerateContent(
		ctx,
		s.currentMessages,
		append(s.options, llms.WithTools([]llms.Tool{}), llms.WithToolChoice("none"))...,
	)
	if err != nil {
		logs.FError("Force final response failed: %v", err)
		return
	}
	s.lastResp = resp
	s.updateTokens()
}

func (s *aiSession) injectInstructions() {
	toolNames := ""
	for i, t := range s.callOpts.Tools {
		if i > 0 {
			toolNames += ", "
		}
		toolNames += t.Function.Name
	}

	instruction := fmt.Sprintf(
		"\nSystem Instruction:\n1. Use ONLY: [%s].\n2. NEVER nest tool calls \n3. Arguments MUST be literal values (strings, integers, etc.), never function calls \n4. For dependent tasks, call tools sequentially in separate turns.\n5. If stuck, respond with text.\n6. For ANY statistics/metrics/trends/summary/compare question: you MUST answer ONLY from successful tool outputs. reply MUST quote concrete numbers, years, and units from chart_preview / summaries. NEVER invent figures, NEVER answer with generic outlines (e.g. \"includes daily waste, recycling…\") without actual values from tools.\n7. If a tool returns Error or empty data: state that clearly in reply; do NOT fabricate numbers or filler summaries.\n8. For get_dashboard_component_summary, dashboard_index MUST be a real string from resolve_navigation_target or manifest (e.g. environment_dashboard). NEVER use placeholders like {result1} or made-up tokens.\n9. Do NOT call get_current_ui_context unless the user's latest message clearly refers to the current screen, dashboard page, map view, open layers, or location (where am I). Backend may block that tool otherwise.",
		toolNames,
	)

	s.currentMessages = make([]llms.MessageContent, 0)
	merged := false
	for _, m := range s.req.Messages {
		if m.Role == llms.ChatMessageTypeSystem && !merged {
			s.currentMessages = append(s.currentMessages, mergeSystemMsg(m, instruction))
			merged = true
		} else {
			s.currentMessages = append(s.currentMessages, m)
		}
	}

	if !merged {
		s.currentMessages = append([]llms.MessageContent{{
			Role:  llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{llms.TextContent{Text: "Instruction: Use tools: [" + toolNames + "]."}},
		}}, s.currentMessages...)
	}
}

func (s *aiSession) finalize(ctx context.Context) (*models.AIChatLog, error) {
	log := &models.AIChatLog{
		SessionID: s.req.SessionID, UserID: s.req.UserID, IPAddress: s.req.IPAddress,
		Provider: "twcc", Model: global.TWCC.Model, LatencyMS: int(time.Since(s.startTime).Milliseconds()),
		Status: "success", Tools: "[]", CreatedAt: s.startTime,
	}

	if len(s.req.Messages) > 0 {
		log.Question = extractText(s.req.Messages[len(s.req.Messages)-1])
	}

	if s.lastErr != nil {
		log.Status, log.ErrorCode, log.ErrorMessage = "error", "MODEL_ERROR", s.lastErr.Error()
		models.CreateAIChatLog(log)
		return log, s.lastErr
	}

	if s.lastResp != nil && len(s.lastResp.Choices) > 0 {
		log.Answer = s.lastResp.Choices[0].Content
		if strings.TrimSpace(log.Answer) == "" {
			log.Answer = s.buildFallbackJSONAnswer()
		}
		log.Answer = s.finalizeAnswerJSON(ctx, log.Answer)
		log.InputTokens, log.OutputTokens = s.totalInput, s.totalOutput
		log.TotalTokens = s.totalInput + s.totalOutput
		if s.toolUsed {
			log.ToolUsed = true
			if toolJSON, err := json.Marshal(s.executedTools); err == nil {
				log.Tools = string(toolJSON)
			}
		}
	}

	if err := models.CreateAIChatLog(log); err != nil {
		logs.FError("DB Log Error: %v", err)
	}
	return log, nil
}

func (s *aiSession) buildFallbackJSONAnswer() string {
	if raw, ok := s.lastToolResults["get_component_facts"]; ok && raw != "" && !strings.HasPrefix(raw, "Error:") {
		var facts struct {
			Component struct {
				Index     string `json:"index"`
				Name      string `json:"name"`
				ShortDesc string `json:"short_desc"`
				City      string `json:"city"`
			} `json:"component"`
		}
		if err := json.Unmarshal([]byte(raw), &facts); err == nil && facts.Component.Index != "" {
			reply := fmt.Sprintf("%s（%s）重點：%s", facts.Component.Name, facts.Component.City, facts.Component.ShortDesc)
			payload := map[string]interface{}{
				"reply": reply,
				"ui_actions": []map[string]interface{}{
					{
						"type": "open_component_info",
						"params": map[string]interface{}{
							"component_index": facts.Component.Index,
							"city":            facts.Component.City,
						},
					},
				},
			}
			if b, err := json.Marshal(payload); err == nil {
				return string(b)
			}
		}
	}

	fallback := map[string]interface{}{
		"reply":      "目前已接收到你的問題，但 AI 回覆內容為空。請再試一次，或直接指定組件 index（例如 youbike_availability）。",
		"ui_actions": []interface{}{},
	}
	if b, err := json.Marshal(fallback); err == nil {
		return string(b)
	}
	return `{"reply":"AI 回覆暫時異常，請稍後再試。","ui_actions":[]}`
}

func toolsToParts(calls []llms.ToolCall) []llms.ContentPart {
	parts := make([]llms.ContentPart, len(calls))
	for i, c := range calls {
		parts[i] = c
	}
	return parts
}

func mergeSystemMsg(m llms.MessageContent, instruction string) llms.MessageContent {
	newParts := make([]llms.ContentPart, len(m.Parts))
	for i, p := range m.Parts {
		if tp, ok := p.(llms.TextContent); ok {
			newParts[i] = llms.TextContent{Text: tp.Text + instruction}
		} else {
			newParts[i] = p
		}
	}
	return llms.MessageContent{Role: m.Role, Parts: newParts}
}

func extractText(m llms.MessageContent) string {
	for _, p := range m.Parts {
		if t, ok := p.(llms.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

func parseUsageInt(val interface{}) int {
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

type normalizedAIResponse struct {
	Reply     string                   `json:"reply"`
	UIActions []map[string]interface{} `json:"ui_actions"`
}
