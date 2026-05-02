# Agent 與儀表板組件整合指南（模板版）

本文件保留可重用的 Agent 能力接線方式，供後續新增任意儀表板組件沿用。

## 1. 保留中的核心能力

### 能力 A：回答單一組件資訊
- 後端工具：`get_component_facts`
- 用途：依 `component_id` 或 `component_index` 回傳組件基本資料與可選圖表預覽
- 入口：
  - 工具實作：`Taipei-City-Dashboard-BE/app/services/ai/tools/component_tools.go`
  - 工具註冊：`Taipei-City-Dashboard-BE/app/services/ai/tools/registry.go`
  - 前端 schema：`Taipei-City-Dashboard-FE/src/store/chatStore.js` 的 `AGENT_TOOLS`

### 能力 B：結合多個組件資訊回應
- 後端工具：`get_dashboard_component_summary`
- 用途：依 `dashboard_index` 回傳多組件摘要（含上限控制）
- 典型場景：回答「這個主題整體趨勢」或「跨組件比較」

### 能力 C：結合使用者地理位置與儀表板資訊回應
- 後端工具：`get_geo_nearby_for_component`（統一鄰近查詢；模式由既有資料推導：`youbike_availability` → YouBike 站點；否則若 `map_config` 綁定之 `component_maps` 含 `source=geojson` → 與前端相同之 `mapData/{index}.geojson` 點位）
- GET `/api/v1/ai/component-routing-manifest` 各組件列 `geo_nearby_supported`、`geo_nearby_provider`（皆為推導值，非額外 DB 欄位）。
- 輸入：`component_index`、`city`、`latitude`、`longitude`、`radius_meters`、`top_n`
- 輸出：`query_location`、`nearby_stations`、`nearest_stations`（YouBike 另有可借/可還加總欄位）
- 前端上下文：`buildUIContext()` 會帶入 `map_context.user_location`
- 前端定位流程：`requestCurrentLocationForAI()` 於位置型提問時嘗試更新 `mapStore.userLocation`
- 注意：若使用者拒絕定位，Agent 應回覆缺少定位授權，避免估算假資料

### 能力 D：根據需求操作儀表板（含跳轉、地圖圖層，可單一或多個）
- 回覆協議：Agent 必須輸出 JSON：`{"reply":"...","ui_actions":[...]}`
- 受控白名單（前端）：`navigate_dashboard`、`open_component_info`、`switch_city`、`open_map_layer`
- 執行器：`executeUIActions()` 逐一執行 action，可同時處理多個 action
- 地圖圖層：`open_map_layer` 會等待組件與地圖載入後開圖層，並回傳執行結果

## 2. 新增組件時的標準串接步驟

### 步驟 1：補齊資料面
1. 新組件需有穩定的 `index`、`city`、`query_type`、描述欄位。
2. 若需 Agent 回答數據，需可由現有 `models` 查詢鏈路取回。
3. 若屬地理型組件，需先定義可查詢資料來源與座標欄位格式。

### 步驟 2：決定工具策略
1. 若可抽象為通用查詢：優先延伸 `get_component_facts` 或 `get_dashboard_component_summary`。
2. **鄰近／距離**：統一走 `get_geo_nearby_for_component`；無需新增 DB 欄位——YouBike 用固定 `index`；其他主題須在 `component_maps` 有 `geojson` 圖層並提供與前端一致的 `public/mapData/{圖層 index}.geojson`。
3. 工具回傳一律 JSON，可被 LLM 直接引用，避免自然語言拼接資料。
4. 若是 `map_geojson`，優先沿用 `buildNearbySummary(...)`；若有領域聚合再在分支補欄位。

### 步驟 3：後端註冊工具（鄰近查詢免新增一支）
1. 鄰近查詢僅維護 `GetGeoNearbyForComponent`；新資料源可在 `deriveGeoNearbyMode`／載入鏈路擴充，或沿用「geojson 圖層 + mapData 檔」慣例。
2. 一般工具仍在 `app/services/ai/tools/` 實作 `func(ctx context.Context, args string) (string, error)` 並 `Register(...)`。
3. 確保工具錯誤訊息可讀且可回傳給 LLM（方便修正參數）。

### 步驟 4：前端宣告工具 schema
1. 在 `chatStore.js` 的 `AGENT_TOOLS` 新增同名 schema。
2. 明確定義 `required` 欄位與 `city` 合法值（目前為 `taipei` 或 `metrotaipei`）。
3. 不在前端做業務運算；前端只傳上下文與執行 action。

### 步驟 5：若需 UI 操作，新增 action 合約
1. 先定義 action type 與 `params` 契約。
2. 加入 `ALLOWED_UI_ACTIONS` 白名單。
3. 在 `executeUIActions()` 補實作，並保留失敗訊息。
4. 若要一次操作多個目標，讓 Agent 回傳多筆 `ui_actions`，前端逐筆執行。

### 步驟 6：更新 system prompt（必要）
1. 新工具名稱、用途與參數要寫清楚。
2. 新 action type 與參數要寫清楚。
3. 明確要求：資料問題優先用工具；`ui_actions` 不可輸出未授權 type。

## 3. 最小驗收清單（每次新增組件都跑）

- 單組件問答：Agent 會呼叫對應工具並回傳正確組件資訊。
- 多組件整合：Agent 可引用 `get_dashboard_component_summary` 做綜合回答。
- 定位整合：有座標時可呼叫位置型工具；無座標時會要求定位授權。
- UI 操作：
  - 可跳轉正確儀表板/組件。
  - 在地圖頁可開啟指定圖層。
  - 可連續執行一個以上 action。

## 4. 維運原則（避免後續失控）

- 工具邏輯放後端，前端只負責協議與執行。
- action 永遠白名單，禁止任意命令直通路由。
- 不要為單一組件硬編碼固定 dashboard；改由工具/索引解析。
- Agent 回傳格式固定 JSON，並保留 `reply` 與 `ui_actions` 雙軌。