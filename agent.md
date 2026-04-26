# Agent 整合與擴充指南
本文件記錄本次將 chatbot 升級為可呼叫工具、可受控操作介面的 Agent 化改動，並提供未來新增組件時的接入方式。
## 1. 本次修改清單
### 1.1 後端
- 新增：`Taipei-City-Dashboard-BE/app/services/ai/tools/component_tools.go`
  - `get_component_facts`
    - 依 `component_id` 或 `component_index` 取得組件結構化資訊
    - 可附帶 `chart_preview`（重用既有 query/data 函式）
  - `get_dashboard_component_summary`
    - 依 `dashboard_index` 取得多組件摘要（可設定 `max_components`）
- 修改：`Taipei-City-Dashboard-BE/app/services/ai/tools/registry.go`
  - 註冊新工具：
    - `get_component_facts`
    - `get_dashboard_component_summary`
### 1.2 前端
- 修改：`Taipei-City-Dashboard-FE/src/store/chatStore.js`
  - 新增 Agent tools schema（前端傳給 `/ai/chat/twai`）
  - 新增 UI context 打包（目前頁面、dashboard、地圖狀態）
  - 對話流程改為：
    1. 優先呼叫 Agent API `/ai/chat/twai`
    2. 失敗時 fallback 至既有 `/vector/component`
  - 新增 Phase 2 受控 UI 操作：
    - 白名單 action type
    - action 解析與執行
    - 執行結果回饋到聊天訊息
## 2. 目前已具備能力
## 2.1 Agent 資訊能力
- 可用工具回答單一組件資訊：
  - 組件基本欄位（index/name/city/query_type/source/desc/use_case）
  - 圖表資料預覽（依 query type 走既有資料函式）
- 可用工具回答多組件摘要：
  - 同一 dashboard 內多個組件的簡化摘要
  - 讓 Agent 可做跨組件整合回答
## 2.2 Agent 介面操作能力（受控）
目前只允許以下白名單操作（避免 Agent 任意操作）：

- `navigate_dashboard`
- `open_component_info`
- `switch_city`
執行原則：
- 非白名單 action 一律忽略
- 缺必要參數會回報失敗原因
- 每次執行結果都回寫到聊天訊息與 log
## 3. 目前資料流（簡化）
1. 使用者輸入問題
2. `chatStore` 組合 `ui_context` + `tools schema`
3. 呼叫 `/api/v1/ai/chat/twai`
4. LLM 決定是否呼叫 tools（由後端 tool loop 執行）
5. 前端解析 Agent 回覆：
   - 文字回覆 `reply`
   - 介面操作 `ui_actions`
6. `chatStore` 驗證白名單後執行 action
7. 回饋 action 執行結果給使用者
8. 若 Agent 失敗，fallback 走既有向量推薦流程
## 4. 未來新增組件時，如何讓 Agent 能取得資訊
建議原則：**優先擴充後端 tool，不增加前端複雜度**。
### 步驟 A：先確認組件資料可由既有模型取得
新增組件後，確認以下資料鏈路可用：
- `components` / `query_charts` 有完整 metadata（index、desc、query_type）
- chart 查詢可透過既有函式取得（如 `GetComponentChartDataQuery` + 對應解析函式）
### 步驟 B：決定是「通用工具」還是「領域工具」
- 若需求可抽象化（例如多數組件都能用）：
  - 優先擴充 `get_component_facts`
- 若需求是領域特化（例如特定運算邏輯）：
  - 新增一個新 tool（例如 `get_xxx_domain_insight`）
### 步驟 C：在後端新增/註冊工具
1. 在 `app/services/ai/tools/` 新增 tool 函式檔案  
2. 實作 `ToolFunc` 簽名：`func(ctx context.Context, args string) (string, error)`  
3. 內部重用 `models` 層資料查詢函式  
4. 在 `registry.go` `Register("tool_name", ToolFunc)` 註冊
### 步驟 D：前端只加 schema（最小變更）
在 `chatStore.js` 的 `AGENT_TOOLS` 增加新 tool schema，讓 LLM 可呼叫該工具。
## 5. 未來新增組件時，如何讓 Agent 能操作該組件介面
建議原則：**前端操作能力採白名單，逐步增加，不直接放開任意命令**。
### 步驟 A：定義操作意圖（action type）
先定義固定 action type，例如：
- `open_component_info`
- `navigate_dashboard`
- `switch_city`
- （未來可加）`focus_map_layer`
- （未來可加）`set_time_range`
### 步驟 B：把 action 加入白名單與執行器
在 `chatStore.js`：
1. 加入 `ALLOWED_UI_ACTIONS`
2. 在 `executeUIActions` 中實作該 action 的參數驗證與路由/狀態操作
3. 保留錯誤訊息回饋（便於 debug 與使用者理解）
### 步驟 C：更新 system prompt 的 action 合約
明確告訴 LLM：
- 可用 action type
- 每個 action 需要哪些 `params`
- 回覆 JSON 格式固定：`reply` + `ui_actions`
## 6. 建議的擴充準則（保持簡潔）
- 複雜邏輯放後端 tools，不堆在前端
- 前端只做三件事：送上下文、渲染回覆、受控執行 action
- 每新增一個 domain tool，不修改主聊天流程
- 每新增一個 action，先白名單、再執行器、再回饋訊息
## 7. 後續建議
- 增加 `/ai/tools` schema 端點（由後端提供工具清單，前端不用硬編）
- 為 action 增加風險等級（高風險操作需二次確認）
- 增加 tool 執行觀測欄位（latency、成功率、error type）