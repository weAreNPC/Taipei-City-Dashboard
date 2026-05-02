package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type resolveCoordinatesZhArgs struct {
	Query string `json:"query"`
}

// ResolveCoordinatesZh 將中文／英文地名、地址轉成 WGS84 座標（OpenStreetMap Nominatim，限台灣）。
// 結果應搭配 get_geo_nearby_for_component，並設定 location_anchor=explicit。
func ResolveCoordinatesZh(ctx context.Context, args string) (string, error) {
	var p resolveCoordinatesZhArgs
	if err := parseArgs(args, &p); err != nil {
		return "", fmt.Errorf("invalid arguments: %v", err)
	}
	q := strings.TrimSpace(p.Query)
	if q == "" {
		return "", fmt.Errorf("query is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	subCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	vals := url.Values{}
	vals.Set("q", q)
	vals.Set("format", "json")
	vals.Set("limit", "3")
	vals.Set("countrycodes", "tw")
	reqURL := "https://nominatim.openstreetmap.org/search?" + vals.Encode()

	req, err := http.NewRequestWithContext(subCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "TaipeiCityDashboardAgent/1.0 (city-dashboard; contact: https://github.com/tpe-doit)")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("geocode request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("geocode HTTP %d", resp.StatusCode)
	}

	var results []struct {
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return "", fmt.Errorf("geocode parse: %w", err)
	}
	if len(results) == 0 {
		out, _ := json.Marshal(map[string]interface{}{
			"error":   "not_found",
			"message": "查無座標，請縮小範圍或改用較完整的地名／地址再試。",
		})
		return string(out), nil
	}

	best := results[0]
	lat, err := strconv.ParseFloat(best.Lat, 64)
	if err != nil {
		return "", fmt.Errorf("invalid lat in response")
	}
	lon, err := strconv.ParseFloat(best.Lon, 64)
	if err != nil {
		return "", fmt.Errorf("invalid lon in response")
	}

	candidates := make([]map[string]interface{}, 0, len(results))
	for _, r := range results {
		latF, e1 := strconv.ParseFloat(r.Lat, 64)
		lonF, e2 := strconv.ParseFloat(r.Lon, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		candidates = append(candidates, map[string]interface{}{
			"latitude":     latF,
			"longitude":    lonF,
			"display_name": r.DisplayName,
		})
	}

	out, err := json.Marshal(map[string]interface{}{
		"source":              "nominatim_openstreetmap",
		"query":               q,
		"latitude":            lat,
		"longitude":           lon,
		"display_name":        best.DisplayName,
		"candidates":          candidates,
		"usage_note_zh":       "將 latitude／longitude 填入 get_geo_nearby_for_component，並設 location_anchor 為 explicit（勿填 0）。資料來源為 OpenStreetMap／Nominatim，僅供概略定位。",
		"attribution_note_zh": "Geocoding © OpenStreetMap contributors (Nominatim)",
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}
