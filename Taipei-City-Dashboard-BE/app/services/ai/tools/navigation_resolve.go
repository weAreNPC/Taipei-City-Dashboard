package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"TaipeiCityDashboardBE/app/models"
)

type resolveNavigationArgs struct {
	Query string `json:"query"`
	City  string `json:"city"`
	Kind  string `json:"kind"`
	Limit int    `json:"limit"`
}

// ResolveNavigationTarget 依組件／儀表板「名稱或 index 片段」與可选縣市，回傳候選清單（與目前使用者可見側欄一致）。
func ResolveNavigationTarget(ctx context.Context, args string) (string, error) {
	var p resolveNavigationArgs
	if err := parseArgs(args, &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(p.Query) == "" {
		return "", fmt.Errorf("query is required")
	}
	accountID := AccountIDFromContext(ctx)
	kind := strings.ToLower(strings.TrimSpace(p.Kind))
	if kind == "" {
		kind = "all"
	}
	lim := p.Limit
	if lim <= 0 {
		lim = 5
	}

	out := make(map[string]interface{})
	if kind == "all" || kind == "component" {
		cm, err := models.ResolveComponentMatches(accountID, p.Query, p.City, lim)
		if err != nil {
			return "", err
		}
		out["component_matches"] = cm
	}
	if kind == "all" || kind == "dashboard" {
		dm, err := models.ResolveDashboardMatches(accountID, p.Query, p.City, lim)
		if err != nil {
			return "", err
		}
		out["dashboard_matches"] = dm
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
