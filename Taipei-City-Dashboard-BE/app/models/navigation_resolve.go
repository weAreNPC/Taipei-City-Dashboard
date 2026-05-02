package models

import (
	"sort"
	"strings"
)

// NormalizeAgentCity 將使用者／模型可能輸入的縣市描述收斂為 taipei 或 metrotaipei（其餘小寫原樣）。
func NormalizeAgentCity(c string) string {
	x := strings.TrimSpace(strings.ToLower(c))
	switch x {
	case "臺北市", "台北市", "台北", "北市", "taipei", "tpe":
		return "taipei"
	case "新北市", "新北", "板橋", "metrotaipei", "metro taipei", "new taipei":
		return "metrotaipei"
	case "雙北", "双北":
		return ""
	default:
		return x
	}
}

func scoreComponentMatch(queryNorm, cityNorm string, e ComponentRoutingManifestEntry) float64 {
	if queryNorm == "" {
		return 0
	}
	s := 0.0
	ix := strings.ToLower(e.ComponentIndex)
	nm := strings.ToLower(strings.TrimSpace(e.Name))
	if ix == queryNorm {
		s += 1000
	}
	if nm == queryNorm {
		s += 900
	}
	if strings.Contains(ix, queryNorm) {
		s += 120
	}
	if strings.Contains(nm, queryNorm) {
		s += 100
	}
	for _, tok := range strings.Fields(queryNorm) {
		if len(tok) < 2 {
			continue
		}
		if strings.Contains(nm, tok) || strings.Contains(ix, tok) {
			s += 35
		}
	}
	if cityNorm != "" && strings.EqualFold(e.City, cityNorm) {
		s += 80
	}
	return s
}

// ComponentMatchScored 與 manifest 相同之組件列，附比對分數（供高信心自動補 open_map_layer）。
type ComponentMatchScored struct {
	ComponentRoutingManifestEntry
	Score float64 `json:"score"`
}

// ResolveComponentMatchesWithScores 與 ResolveComponentMatches 相同資料來源，但保留分數。
func ResolveComponentMatchesWithScores(accountID int, query string, cityHint string, limit int) ([]ComponentMatchScored, error) {
	manifest, err := GetComponentRoutingManifest(accountID)
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return nil, nil
	}
	cityNorm := NormalizeAgentCity(cityHint)
	var hits []ComponentMatchScored
	for _, e := range manifest {
		sc := scoreComponentMatch(q, cityNorm, e)
		if sc > 0 {
			hits = append(hits, ComponentMatchScored{ComponentRoutingManifestEntry: e, Score: sc})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ComponentIndex < hits[j].ComponentIndex
	})
	if limit <= 0 {
		limit = 5
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// ResolveComponentMatches 依中文／英文／index 片段與可选縣市，自路由清單回傳最可能組件（同 GetComponentRoutingManifest 可見範圍）。
func ResolveComponentMatches(accountID int, query string, cityHint string, limit int) ([]ComponentRoutingManifestEntry, error) {
	hits, err := ResolveComponentMatchesWithScores(accountID, query, cityHint, limit)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}
	out := make([]ComponentRoutingManifestEntry, 0, len(hits))
	for i := range hits {
		out = append(out, hits[i].ComponentRoutingManifestEntry)
	}
	return out, nil
}

// UserQuestionHintsBikeLaneInfrastructure 使用者指的是自行車「道／路網／路線」等設施圖，而非 YouBike 租借站點。
func UserQuestionHintsBikeLaneInfrastructure(q string) bool {
	q = strings.TrimSpace(q)
	if q == "" {
		return false
	}
	if strings.Contains(q, "自行車道") || strings.Contains(q, "自行車道路") || strings.Contains(q, "單車道") {
		return true
	}
	if strings.Contains(q, "自行車路網") || strings.Contains(q, "自行車路線") || strings.Contains(q, "單車路網") {
		return true
	}
	if strings.Contains(q, "車道圖") || strings.Contains(q, "自行車專用道") || strings.Contains(q, "單車路線") {
		return true
	}
	return false
}

// PickUniqueMapLayerComponentFromQuestion 從整句使用者問題推一個「唯一高信心」且有地圖圖層的組件；不符則 ok=false（寧缺勿錯）。
// 規則：僅 HasMapLayer；最高分須 ≥ minTopScore；若第二名同樣有圖層，須領先 ≥ minLeadOverSecond。
func PickUniqueMapLayerComponentFromQuestion(accountID int, question string, cityHint string) (componentIndex string, city string, ok bool) {
	const (
		minTopScore          = 160.0
		minLeadOverSecond    = 72.0
		candidatePool        = 48
	)
	scored, err := ResolveComponentMatchesWithScores(accountID, question, cityHint, candidatePool)
	if err != nil || len(scored) == 0 {
		return "", "", false
	}
	var withMap []ComponentMatchScored
	bikeLane := UserQuestionHintsBikeLaneInfrastructure(question)
	for _, s := range scored {
		if !s.HasMapLayer {
			continue
		}
		if bikeLane && strings.EqualFold(strings.TrimSpace(s.ComponentIndex), "youbike_availability") {
			continue
		}
		withMap = append(withMap, s)
	}
	if len(withMap) == 0 {
		return "", "", false
	}
	top := withMap[0]
	if top.Score < minTopScore {
		return "", "", false
	}
	if len(withMap) >= 2 && (top.Score-withMap[1].Score) < minLeadOverSecond {
		return "", "", false
	}
	return top.ComponentIndex, top.City, true
}

// DashboardNavMatch 儀表板名稱／index 模糊搜尋結果。
type DashboardNavMatch struct {
	DashboardIndex string `json:"dashboard_index"`
	DashboardName  string `json:"dashboard_name"`
	SidebarSource  string `json:"sidebar_source"`
}

func scoreDashboardMatch(queryNorm, cityNorm, sidebarSource string, d Dashboard) float64 {
	if queryNorm == "" {
		return 0
	}
	s := 0.0
	name := strings.ToLower(strings.TrimSpace(d.Name))
	idx := strings.ToLower(strings.TrimSpace(d.Index))
	if idx == queryNorm {
		s += 800
	}
	if name == queryNorm {
		s += 750
	}
	if strings.Contains(name, queryNorm) {
		s += 120
	}
	if strings.Contains(idx, queryNorm) {
		s += 100
	}
	for _, tok := range strings.Fields(queryNorm) {
		if len(tok) < 2 {
			continue
		}
		if strings.Contains(name, tok) {
			s += 40
		}
	}
	if cityNorm != "" {
		if cityNorm == "taipei" && sidebarSource == "taipei" {
			s += 90
		}
		if cityNorm == "metrotaipei" && sidebarSource == "metrotaipei" {
			s += 90
		}
	}
	return s
}

// sidebarCityDefault 由側欄來源推斷 navigate 常用 query city。
func sidebarCityDefault(sidebarSource string) string {
	switch sidebarSource {
	case "taipei":
		return "taipei"
	case "metrotaipei":
		return "metrotaipei"
	default:
		return "taipei"
	}
}

// ResolveDashboardMatches 依儀表板名稱或 index 片段搜尋（使用者可見儀表板）。
func ResolveDashboardMatches(accountID int, query string, cityHint string, limit int) ([]DashboardNavMatch, error) {
	data, err := GetAllDashboards(accountID)
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return nil, nil
	}
	cityNorm := NormalizeAgentCity(cityHint)
	type scored struct {
		row   DashboardNavMatch
		score float64
	}
	var hits []scored
	add := func(list []Dashboard, sidebarSource string) {
		for _, d := range list {
			sc := scoreDashboardMatch(q, cityNorm, sidebarSource, d)
			if sc <= 0 {
				continue
			}
			hits = append(hits, scored{
				row: DashboardNavMatch{
					DashboardIndex: d.Index,
					DashboardName:  d.Name,
					SidebarSource:  sidebarSource,
				},
				score: sc,
			})
		}
	}
	add(data.Taipei, "taipei")
	add(data.MetroTaipei, "metrotaipei")
	add(data.Public, "public")
	add(data.Personal, "personal")

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].row.DashboardIndex < hits[j].row.DashboardIndex
	})

	seen := make(map[string]struct{})
	out := make([]DashboardNavMatch, 0, limit)
	if limit <= 0 {
		limit = 5
	}
	for _, h := range hits {
		key := h.row.DashboardIndex + "|" + h.row.SidebarSource
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, h.row)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// PickNavigateCityForDashboard 補齊 navigate_dashboard 的 city query。
func PickNavigateCityForDashboard(cityHint string, match DashboardNavMatch) string {
	if ch := NormalizeAgentCity(cityHint); ch != "" {
		return ch
	}
	return sidebarCityDefault(match.SidebarSource)
}

// PickPreferredDashboardForMapLayer 依側欄 placements 選導航儀表板：優先非 map-layers-*（避免圖資誤導），否则第一個。
func PickPreferredDashboardForMapLayer(entry ComponentRoutingManifestEntry) (dashboardIndex, navigateCity string) {
	navigateCity = strings.TrimSpace(entry.City)
	for _, pl := range entry.Placements {
		di := strings.ToLower(strings.TrimSpace(pl.DashboardIndex))
		if di != "" && !strings.Contains(di, "map-layers") {
			return pl.DashboardIndex, navigateCity
		}
	}
	if len(entry.Placements) > 0 {
		return entry.Placements[0].DashboardIndex, navigateCity
	}
	return "", ""
}

// ManifestHasDashboardPlacement 檢查 navigate 的儀表板 index 是否為該組件（此 city）實際所屬側欄儀表板之一。
func ManifestHasDashboardPlacement(entry *ComponentRoutingManifestEntry, dashboardIndex string) bool {
	if entry == nil {
		return false
	}
	want := strings.ToLower(strings.TrimSpace(dashboardIndex))
	if want == "" {
		return false
	}
	for _, pl := range entry.Placements {
		if strings.ToLower(strings.TrimSpace(pl.DashboardIndex)) == want {
			return true
		}
	}
	return false
}

// FindManifestEntryForComponent 自 manifest 找 component_index；city 非空時優先同 city，否則取第一筆同 index。
func FindManifestEntryForComponent(manifest []ComponentRoutingManifestEntry, componentIndex, city string) *ComponentRoutingManifestEntry {
	ci := strings.ToLower(strings.TrimSpace(componentIndex))
	if ci == "" {
		return nil
	}
	cy := strings.ToLower(strings.TrimSpace(city))
	var fallback *ComponentRoutingManifestEntry
	for i := range manifest {
		e := &manifest[i]
		if strings.ToLower(strings.TrimSpace(e.ComponentIndex)) != ci {
			continue
		}
		if cy == "" {
			return e
		}
		if strings.ToLower(strings.TrimSpace(e.City)) == cy {
			return e
		}
		if fallback == nil {
			fallback = e
		}
	}
	return fallback
}
