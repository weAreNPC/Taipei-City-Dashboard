package ai

import (
	"TaipeiCityDashboardBE/app/models"
	"TaipeiCityDashboardBE/logs"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

const uiActionsRepairSystemPrompt = `你是 JSON 修正器。使用者會提供一個物件，含 reply（字串）與 ui_actions（陣列）。
請輸出同一結構的合法 JSON：不要 markdown、不要註解、不要額外文字。
規則（務必遵守）：
- open_map_layer：params 必須含 component_index（字串，例如 youbike_availability）。禁止使用 layer 代替 component_index；若原本只有 layer，請改寫為 component_index。
- open_component_info：params 必須含 component_index（可用 index 語意者請改為 component_index）。
- navigate_dashboard：params 必須含 index（若只有 dashboard_index 請複製為 index）。
- 若使用者要在畫面上「開地圖／看圖層／地圖模式」：navigate_dashboard 必須含 mode 為 mapview（字串），且須配合 open_map_layer 或於 navigate 的 params 帶 map_layer_component_index／component_index 指向要開的圖層組件；不可只用預設（省略 mode 會被前端當成一般儀表板而非全幅地圖）。
- YouBike／自行車即時站點：請導向「務實交通」類儀表板（practical_transportation_newtpe，metrotaipei）的 mapview + open_map_layer youbike_availability；絕對不可建議「圖資資訊」或 map-layers-taipei／map-layers-metrotaipei。
- 「自行車道／自行車道路／路網」為車道設施圖層，不可開 youbike_availability；應以 resolve_navigation_target 解析正確 component_index。
- switch_city：params 必須含 city 與 index。
保留原 reply 的語意與主要文字，僅修正 ui_actions 結構與欄位名。
也可接受 component_name／dashboard_name 等寬鬆欄位，請盡量改為標準 index／component_index。
若使用者問題是在問「資訊／說明／有哪些」而非明确要求前往畫面，請勿為了補 ui_actions 而把 reply 改成只有「請自行查看」；應保留或補上實質摘要（模型應已透過工具取得）。`

func stripAnswerMarkdownFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```JSON")
	trimmed = strings.TrimSuffix(trimmed, "```")
	return strings.TrimSpace(trimmed)
}

func paramString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%.0f", x)
		}
		return strings.TrimSpace(fmt.Sprint(x))
	case json.Number:
		return strings.TrimSpace(x.String())
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func layerAliasComponentIndex(layer string) string {
	switch strings.ToLower(strings.TrimSpace(layer)) {
	case "ubike", "youbike":
		return "youbike_availability"
	default:
		return ""
	}
}

func componentIndexFromUbikeTool(toolResults map[string]string) string {
	if toolResults == nil {
		return ""
	}
	raw := toolResults["get_nearby_ubike_summary"]
	if raw == "" || strings.HasPrefix(raw, "Error:") {
		return ""
	}
	var tr struct {
		ComponentIndex string `json:"component_index"`
	}
	if err := json.Unmarshal([]byte(raw), &tr); err != nil {
		return ""
	}
	return strings.TrimSpace(tr.ComponentIndex)
}

func ensureParamsMap(action map[string]interface{}) map[string]interface{} {
	raw, ok := action["params"].(map[string]interface{})
	if ok && raw != nil {
		return raw
	}
	p := make(map[string]interface{})
	action["params"] = p
	return p
}

// machineFixUIActions fills canonical param keys from aliases and recent tool payloads.
func machineFixUIActions(payload *normalizedAIResponse, toolResults map[string]string) {
	if payload == nil || len(payload.UIActions) == 0 {
		return
	}
	ubikeIdx := componentIndexFromUbikeTool(toolResults)

	for _, action := range payload.UIActions {
		if action == nil {
			continue
		}
		typ, _ := action["type"].(string)
		params := ensureParamsMap(action)

		switch typ {
		case "open_map_layer":
			idx := firstNonEmpty(
				paramString(params["component_index"]),
				paramString(params["index"]),
				paramString(params["component"]),
			)
			if idx == "" {
				layerHint := firstNonEmpty(
					paramString(params["layer"]),
					paramString(params["map_layer"]),
				)
				if layerHint != "" {
					if mapped := layerAliasComponentIndex(layerHint); mapped != "" {
						idx = mapped
					}
				}
			}
			if idx == "" && ubikeIdx != "" {
				idx = ubikeIdx
			}
			if idx != "" {
				params["component_index"] = idx
			}

		case "open_component_info":
			idx := firstNonEmpty(
				paramString(params["component_index"]),
				paramString(params["index"]),
				paramString(params["component"]),
			)
			if idx == "" {
				layerHint := paramString(params["layer"])
				if layerHint != "" {
					if mapped := layerAliasComponentIndex(layerHint); mapped != "" {
						idx = mapped
					}
				}
			}
			if idx != "" {
				params["component_index"] = idx
			}

		case "navigate_dashboard":
			if paramString(params["index"]) == "" {
				if di := paramString(params["dashboard_index"]); di != "" {
					params["index"] = di
				} else if d := paramString(params["dashboard"]); d != "" {
					params["index"] = d
				}
			}
			if paramString(params["map_layer_component_index"]) != "" && paramString(params["component_index"]) == "" {
				params["component_index"] = paramString(params["map_layer_component_index"])
			}
		}
	}
}

type mapLayerTarget struct {
	ComponentIndex string
	City           string
}

const ubikeFallbackDashboardIndex = "practical_transportation_newtpe"
const ubikeFallbackDashboardCity = "metrotaipei"

func extractLastUserPlainText(messages []llms.MessageContent) string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != llms.ChatMessageTypeHuman {
			continue
		}
		for _, p := range m.Parts {
			if tc, ok := p.(llms.TextContent); ok {
				return strings.TrimSpace(tc.Text)
			}
		}
	}
	return ""
}

// userQuestionHintsUbikeMapLayer：最後一句使用者問題是否像在要 YouBike「地圖／圖層」（模型常只回 navigate 而無 open_map_layer）。
func userQuestionHintsUbikeMapLayer(q string) bool {
	q = strings.TrimSpace(q)
	if q == "" {
		return false
	}
	if models.UserQuestionHintsBikeLaneInfrastructure(q) {
		return false
	}
	ql := strings.ToLower(q)
	hasBike := strings.Contains(ql, "ubike") || strings.Contains(ql, "youbike") ||
		strings.Contains(q, "微笑單車") ||
		strings.Contains(q, "自行車") || strings.Contains(q, "單車")
	mapCue := strings.Contains(q, "地圖") || strings.Contains(q, "圖層") ||
		strings.Contains(ql, "mapview") || strings.Contains(ql, "map ") ||
		strings.HasSuffix(ql, "map")
	return hasBike && mapCue
}

func stripUbikeLayerWhenBikeLaneIntent(payload *normalizedAIResponse, lastUserQuestion string) {
	if payload == nil || !models.UserQuestionHintsBikeLaneInfrastructure(lastUserQuestion) {
		return
	}
	var kept []map[string]interface{}
	for _, action := range payload.UIActions {
		if action == nil {
			continue
		}
		typ, _ := action["type"].(string)
		if typ == "open_map_layer" {
			params := ensureParamsMap(action)
			idx := strings.ToLower(strings.TrimSpace(firstNonEmpty(
				paramString(params["component_index"]),
				paramString(params["index"]),
			)))
			if idx == "youbike_availability" {
				continue
			}
		}
		if typ == "navigate_dashboard" {
			params := ensureParamsMap(action)
			idx := strings.ToLower(strings.TrimSpace(firstNonEmpty(
				paramString(params["component_index"]),
				paramString(params["map_layer_component_index"]),
			)))
			if idx == "youbike_availability" {
				delete(params, "component_index")
				delete(params, "map_layer_component_index")
			}
		}
		kept = append(kept, action)
	}
	payload.UIActions = kept
}

func extractMapLayerTargets(payload *normalizedAIResponse, toolResults map[string]string, lastUserQuestion string) []mapLayerTarget {
	if payload == nil {
		return nil
	}
	var out []mapLayerTarget
	seen := map[string]struct{}{}
	add := func(ci, cy string) {
		ci = strings.TrimSpace(ci)
		cy = strings.TrimSpace(cy)
		if ci == "" {
			return
		}
		key := strings.ToLower(ci) + "|" + strings.ToLower(cy)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, mapLayerTarget{ComponentIndex: ci, City: cy})
	}

	for _, action := range payload.UIActions {
		if action == nil {
			continue
		}
		t, _ := action["type"].(string)
		params := ensureParamsMap(action)
		switch t {
		case "open_map_layer":
			idx := strings.TrimSpace(firstNonEmpty(
				paramString(params["component_index"]),
				paramString(params["index"]),
				paramString(params["component"]),
			))
			if idx == "" {
				if lh := paramString(params["layer"]); lh != "" {
					if mapped := layerAliasComponentIndex(lh); mapped != "" {
						idx = mapped
					}
				}
			}
			add(idx, paramString(params["city"]))
		case "navigate_dashboard":
			idx := strings.TrimSpace(firstNonEmpty(
				paramString(params["component_index"]),
				paramString(params["map_layer_component_index"]),
			))
			if idx == "" {
				if lh := paramString(params["layer"]); lh != "" {
					idx = layerAliasComponentIndex(lh)
				}
			}
			if idx != "" {
				add(idx, paramString(params["city"]))
			}
		}
	}
	if tb := strings.TrimSpace(componentIndexFromUbikeTool(toolResults)); tb != "" {
		add(tb, "")
	}
	if len(out) == 0 && userQuestionHintsUbikeMapLayer(lastUserQuestion) {
		add("youbike_availability", "")
	}
	return out
}

func ensureUbikeOpenMapLayerFromUserQuestion(payload *normalizedAIResponse, lastUserQuestion string) {
	if payload == nil || !userQuestionHintsUbikeMapLayer(lastUserQuestion) {
		return
	}
	for _, action := range payload.UIActions {
		if action == nil {
			continue
		}
		if typ, _ := action["type"].(string); typ == "open_map_layer" {
			return
		}
	}
	payload.UIActions = append(payload.UIActions, map[string]interface{}{
		"type": "open_map_layer",
		"params": map[string]interface{}{
			"component_index": "youbike_availability",
			"city":            ubikeFallbackDashboardCity,
		},
	})
}

func uiActionsHasOpenMapLayer(payload *normalizedAIResponse) bool {
	if payload == nil {
		return false
	}
	for _, action := range payload.UIActions {
		if action == nil {
			continue
		}
		if typ, _ := action["type"].(string); typ == "open_map_layer" {
			return true
		}
	}
	return false
}

// userQuestionHintsGenericMapVisualization 使用者想開「地圖／圖層」（非僅查數據）；與 YouBike 專用判斷分開。
func userQuestionHintsGenericMapVisualization(q string) bool {
	q = strings.TrimSpace(q)
	if q == "" {
		return false
	}
	ql := strings.ToLower(q)
	if strings.Contains(q, "地圖") || strings.Contains(q, "圖層") {
		return true
	}
	if strings.Contains(ql, "mapview") {
		return true
	}
	if strings.Contains(ql, "map ") || strings.HasSuffix(ql, "map") || strings.Contains(ql, "layer") {
		return true
	}
	return false
}

// ensureGenericOpenMapLayerFromUserQuestion 以 manifest 分數唯一性推斷 component_index（僅 HasMapLayer）；信心不足則不注入。
func ensureGenericOpenMapLayerFromUserQuestion(payload *normalizedAIResponse, lastUserQuestion string, accountID int) {
	if payload == nil || strings.TrimSpace(lastUserQuestion) == "" {
		return
	}
	if !userQuestionHintsGenericMapVisualization(lastUserQuestion) {
		return
	}
	if uiActionsHasOpenMapLayer(payload) {
		return
	}
	ci, cy, ok := models.PickUniqueMapLayerComponentFromQuestion(accountID, lastUserQuestion, "")
	if !ok {
		return
	}
	payload.UIActions = append(payload.UIActions, map[string]interface{}{
		"type": "open_map_layer",
		"params": map[string]interface{}{
			"component_index": ci,
			"city":            cy,
		},
	})
	logs.FInfo("ui_actions: generic map intent — injected open_map_layer component=%s city=%s", ci, cy)
}

func openMapLayerTargetsIndex(payload *normalizedAIResponse) map[string]struct{} {
	out := map[string]struct{}{}
	if payload == nil {
		return out
	}
	for _, action := range payload.UIActions {
		if action == nil || action["type"] != "open_map_layer" {
			continue
		}
		params := ensureParamsMap(action)
		idx := strings.TrimSpace(firstNonEmpty(
			paramString(params["component_index"]),
			paramString(params["index"]),
			paramString(params["component"]),
		))
		if idx == "" {
			if lh := paramString(params["layer"]); lh != "" {
				idx = layerAliasComponentIndex(lh)
			}
		}
		if idx != "" {
			out[strings.ToLower(idx)] = struct{}{}
		}
	}
	return out
}

// rewriteNavigateDashboardsForMapLayerComponents 依使用者可見 manifest，將錯誤的 navigate_dashboard（如 map-layers-* 或非組件所屬儀表板）改為建議儀表板與 city。
func rewriteNavigateDashboardsForMapLayerComponents(payload *normalizedAIResponse, accountID int, toolResults map[string]string, lastUserQuestion string) {
	targets := extractMapLayerTargets(payload, toolResults, lastUserQuestion)
	if len(targets) == 0 {
		return
	}
	manifest, err := models.GetComponentRoutingManifest(accountID)
	if err != nil || len(manifest) == 0 {
		logs.FInfo("rewriteNavigateDashboardsForMapLayerComponents: manifest unavailable, ubike fallback only: %v", err)
		manifest = nil
	}
	openMapFor := openMapLayerTargetsIndex(payload)
	userUbikeMap := userQuestionHintsUbikeMapLayer(lastUserQuestion)

	for _, action := range payload.UIActions {
		if action == nil || action["type"] != "navigate_dashboard" {
			continue
		}
		params := ensureParamsMap(action)
		ci := strings.TrimSpace(firstNonEmpty(
			paramString(params["component_index"]),
			paramString(params["map_layer_component_index"]),
		))
		if lh := paramString(params["layer"]); ci == "" && lh != "" {
			ci = layerAliasComponentIndex(lh)
		}
		cy := strings.TrimSpace(paramString(params["city"]))
		if ci == "" {
			if len(targets) == 1 {
				ci = targets[0].ComponentIndex
				if cy == "" {
					cy = targets[0].City
				}
			} else {
				continue
			}
		}

		entry := models.FindManifestEntryForComponent(manifest, ci, cy)
		prefDash, prefCity := "", ""
		if entry != nil {
			prefDash, prefCity = models.PickPreferredDashboardForMapLayer(*entry)
		}
		if prefDash == "" && strings.EqualFold(strings.TrimSpace(ci), "youbike_availability") {
			prefDash, prefCity = ubikeFallbackDashboardIndex, ubikeFallbackDashboardCity
		}
		if prefDash == "" {
			continue
		}
		curIdx := strings.TrimSpace(firstNonEmpty(
			paramString(params["index"]),
			paramString(params["dashboard_index"]),
			paramString(params["dashboard"]),
		))
		mode := strings.TrimSpace(strings.ToLower(paramString(params["mode"])))
		wantsMapview := mode == "mapview" || paramString(params["map_layer_component_index"]) != ""
		_, openMap := openMapFor[strings.ToLower(strings.TrimSpace(ci))]

		needRewrite := false
		if strings.Contains(strings.ToLower(curIdx), "map-layers") {
			needRewrite = true
		} else if entry != nil && !models.ManifestHasDashboardPlacement(entry, curIdx) && (openMap || wantsMapview || userUbikeMap) {
			needRewrite = true
		}
		if !needRewrite {
			continue
		}
		params["index"] = prefDash
		params["city"] = prefCity
		if wantsMapview || openMap || userUbikeMap {
			if strings.TrimSpace(paramString(params["mode"])) == "" {
				params["mode"] = "mapview"
			}
		}
		logs.FInfo("ui_actions: navigate corrected for component=%s -> dashboard=%s city=%s", ci, prefDash, prefCity)
	}
}

func uiActionsViolations(payload *normalizedAIResponse) []string {
	if payload == nil {
		return nil
	}
	var out []string
	for i, action := range payload.UIActions {
		if action == nil {
			continue
		}
		typ, _ := action["type"].(string)
		params, ok := action["params"].(map[string]interface{})
		if !ok || params == nil {
			params = map[string]interface{}{}
		}

		switch typ {
		case "open_map_layer":
			if firstNonEmpty(
				paramString(params["component_index"]),
				paramString(params["index"]),
				paramString(params["component"]),
			) == "" {
				out = append(out, fmt.Sprintf("open_map_layer[%d]: missing component_index", i))
			}
		case "open_component_info":
			if firstNonEmpty(paramString(params["component_index"]), paramString(params["index"])) == "" {
				out = append(out, fmt.Sprintf("open_component_info[%d]: missing component_index", i))
			}
		case "navigate_dashboard":
			if firstNonEmpty(
				paramString(params["index"]),
				paramString(params["dashboard_index"]),
				paramString(params["dashboard"]),
			) == "" {
				out = append(out, fmt.Sprintf("navigate_dashboard[%d]: missing index", i))
			}
		case "switch_city":
			if paramString(params["city"]) == "" {
				out = append(out, fmt.Sprintf("switch_city[%d]: missing city", i))
			}
			if paramString(params["index"]) == "" {
				out = append(out, fmt.Sprintf("switch_city[%d]: missing index", i))
			}
		}
	}
	return out
}

func (s *aiSession) tryRepairUIActions(ctx context.Context, payload *normalizedAIResponse) bool {
	if payload == nil {
		return false
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false
	}

	repairMsgs := []llms.MessageContent{
		{
			Role: llms.ChatMessageTypeSystem,
			Parts: []llms.ContentPart{
				llms.TextContent{Text: uiActionsRepairSystemPrompt},
			},
		},
		{
			Role: llms.ChatMessageTypeHuman,
			Parts: []llms.ContentPart{
				llms.TextContent{
					Text: fmt.Sprintf(
						"下列 JSON 的 ui_actions 不符合前端規範，請輸出修正後的完整 JSON（僅 JSON）：\n%s",
						string(raw),
					),
				},
			},
		},
	}

	opts := append(
		append([]llms.CallOption(nil), s.options...),
		llms.WithTools([]llms.Tool{}),
		llms.WithToolChoice("none"),
		llms.WithStreamingFunc(nil),
		llms.WithStreamingReasoningFunc(nil),
	)
	resp, err := twccModel.GenerateContent(ctx, repairMsgs, opts...)
	if err != nil {
		logs.FError("ui_actions repair model call failed: %v", err)
		return false
	}
	if resp == nil || len(resp.Choices) == 0 {
		return false
	}

	s.addUsageFromResponse(resp)

	content := strings.TrimSpace(resp.Choices[0].Content)
	fixed := stripAnswerMarkdownFence(content)
	var repaired normalizedAIResponse
	if err := json.Unmarshal([]byte(fixed), &repaired); err != nil {
		logs.FError("ui_actions repair parse failed: %v", err)
		return false
	}

	if strings.TrimSpace(repaired.Reply) != "" {
		payload.Reply = repaired.Reply
	}
	if repaired.UIActions != nil {
		payload.UIActions = repaired.UIActions
	}
	logs.FInfo("ui_actions repair pass applied")
	return true
}

// enrichUIActionsFromLooseNames 將 component_name、dashboard_name + city 等解析為標準 index（與使用者可見側欄一致）。
func enrichUIActionsFromLooseNames(payload *normalizedAIResponse, accountID int) {
	if payload == nil || len(payload.UIActions) == 0 {
		return
	}
	for _, action := range payload.UIActions {
		if action == nil {
			continue
		}
		typ, _ := action["type"].(string)
		params := ensureParamsMap(action)
		switch typ {
		case "open_map_layer", "open_component_info":
			if firstNonEmpty(paramString(params["component_index"]), paramString(params["index"])) != "" {
				continue
			}
			q := firstNonEmpty(
				paramString(params["component_name"]),
				paramString(params["name"]),
				paramString(params["query"]),
				paramString(params["component"]),
			)
			if q == "" {
				continue
			}
			cityHint := paramString(params["city"])
			hits, err := models.ResolveComponentMatches(accountID, q, cityHint, 1)
			if err != nil || len(hits) == 0 {
				continue
			}
			params["component_index"] = hits[0].ComponentIndex
			if paramString(params["city"]) == "" {
				params["city"] = hits[0].City
			}
		case "navigate_dashboard":
			if paramString(params["index"]) != "" {
				continue
			}
			dq := firstNonEmpty(
				paramString(params["dashboard_name"]),
				paramString(params["dashboard_title"]),
				paramString(params["name"]),
			)
			if dq == "" {
				continue
			}
			cityHint := paramString(params["city"])
			dhits, err := models.ResolveDashboardMatches(accountID, dq, cityHint, 1)
			if err != nil || len(dhits) == 0 {
				continue
			}
			params["index"] = dhits[0].DashboardIndex
			if paramString(params["city"]) == "" {
				params["city"] = models.PickNavigateCityForDashboard(cityHint, dhits[0])
			}
		}
	}
}

func (s *aiSession) addUsageFromResponse(resp *llms.ContentResponse) {
	if resp == nil || len(resp.Choices) == 0 {
		return
	}
	if usage, ok := resp.Choices[0].GenerationInfo["usage"].(map[string]interface{}); ok {
		s.totalInput += parseUsageInt(usage["input_tokens"])
		s.totalOutput += parseUsageInt(usage["output_tokens"])
	}
}

// finalizeAnswerJSON strips fences, applies machine fixes to ui_actions, and optionally runs one repair model pass.
func (s *aiSession) finalizeAnswerJSON(ctx context.Context, rawAnswer string) string {
	if strings.TrimSpace(rawAnswer) == "" {
		return rawAnswer
	}
	trimmed := stripAnswerMarkdownFence(rawAnswer)
	if trimmed == "" {
		return rawAnswer
	}

	var payload normalizedAIResponse
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return normalizeAIAnswerLegacy(rawAnswer)
	}
	if payload.UIActions == nil {
		payload.UIActions = []map[string]interface{}{}
	}

	lastUser := extractLastUserPlainText(s.req.Messages)

	machineFixUIActions(&payload, s.lastToolResults)
	accID, _ := strconv.Atoi(strings.TrimSpace(s.req.UserID))
	if accID < 0 {
		accID = 0
	}
	enrichUIActionsFromLooseNames(&payload, accID)
	machineFixUIActions(&payload, s.lastToolResults)
	stripUbikeLayerWhenBikeLaneIntent(&payload, lastUser)
	rewriteNavigateDashboardsForMapLayerComponents(&payload, accID, s.lastToolResults, lastUser)
	ensureUbikeOpenMapLayerFromUserQuestion(&payload, lastUser)
	ensureGenericOpenMapLayerFromUserQuestion(&payload, lastUser, accID)
	rewriteNavigateDashboardsForMapLayerComponents(&payload, accID, s.lastToolResults, lastUser)
	machineFixUIActions(&payload, s.lastToolResults)

	violations := uiActionsViolations(&payload)
	if len(violations) > 0 && s.callOpts.StreamingFunc == nil {
		logs.FInfo("ui_actions violations before repair: %v", violations)
		s.tryRepairUIActions(ctx, &payload)
		machineFixUIActions(&payload, s.lastToolResults)
		enrichUIActionsFromLooseNames(&payload, accID)
		machineFixUIActions(&payload, s.lastToolResults)
		stripUbikeLayerWhenBikeLaneIntent(&payload, lastUser)
		rewriteNavigateDashboardsForMapLayerComponents(&payload, accID, s.lastToolResults, lastUser)
		ensureUbikeOpenMapLayerFromUserQuestion(&payload, lastUser)
		ensureGenericOpenMapLayerFromUserQuestion(&payload, lastUser, accID)
		rewriteNavigateDashboardsForMapLayerComponents(&payload, accID, s.lastToolResults, lastUser)
		machineFixUIActions(&payload, s.lastToolResults)
		if remain := uiActionsViolations(&payload); len(remain) > 0 {
			logs.FInfo("ui_actions violations after repair: %v", remain)
		}
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return rawAnswer
	}
	return string(out)
}

// normalizeAIAnswerLegacy only trims markdown fences and ensures ui_actions is an array; used when JSON parse fails.
func normalizeAIAnswerLegacy(rawAnswer string) string {
	trimmed := stripAnswerMarkdownFence(rawAnswer)
	if trimmed == "" {
		return rawAnswer
	}

	var payload normalizedAIResponse
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return rawAnswer
	}
	if payload.UIActions == nil {
		payload.UIActions = []map[string]interface{}{}
	}

	normalized, err := json.Marshal(payload)
	if err != nil {
		return rawAnswer
	}
	return string(normalized)
}
