-- 綠色商店儀表板完整設定
-- 目標 DB：dashboardmanager（postgres-manager container）
-- 用法：docker exec -i postgres-manager psql -U postgres -d dashboardmanager < db-sample-data/green_stores_setup.sql
--
-- 三種地圖資料（圖資）對應關係：
--   taipei_green_shop.geojson      → component_map id=1   → query_charts city=taipei
--   ntpc_green_stores.geojson      → component_map (新建) → query_charts city=newtaipei
--   metro_green_stores.geojson     → component_map id=103 → query_charts city=metrotaipei

DO $$
DECLARE
  v_ntpc_map_id   integer;
  v_comp_id       integer := 219;   -- green_stores
  v_dash_taipei   integer := 365;
  v_dash_metro    integer := 366;
BEGIN

  -- ── 1. component_charts ──────────────────────────────────────────────────────
  -- 顏色順序：台北連鎖 / 新北連鎖 / 台北個別 / 新北個別
  INSERT INTO component_charts (index, color, types, unit)
  VALUES (
    'green_stores',
    ARRAY['#4caf50','#22C55E','#a5d6a7','#86EFAC'],
    ARRAY['MapLegend'],
    '間'
  )
  ON CONFLICT (index) DO UPDATE
    SET color = EXCLUDED.color,
        types = EXCLUDED.types,
        unit  = EXCLUDED.unit;

  -- ── 2. component_maps ────────────────────────────────────────────────────────

  -- 2a. 台北市綠色商店（index=taipei_green_shop，geojson 屬性為中文）
  --     使用現有 id=1，僅更新 paint / property
  UPDATE component_maps SET
    title    = '台北市綠色商店',
    paint    = '{
      "circle-color": ["match", ["get", "類型"],
        "連鎖型綠色商店", "#4caf50",
        "個別型綠色商店", "#a5d6a7",
        "#4caf50"
      ],
      "circle-radius": 5,
      "circle-opacity": 0.85,
      "circle-stroke-color": "#ffffff",
      "circle-stroke-width": 1
    }',
    property = '[
      {"key": "名稱",   "name": "商店名稱"},
      {"key": "類型",   "name": "類型"},
      {"key": "地址",   "name": "地址"},
      {"key": "電話",   "name": "電話"},
      {"key": "商店編號", "name": "商店編號"}
    ]'
  WHERE id = 1;

  -- 2b. 新北市綠色商店（index=ntpc_green_stores，geojson 屬性為英文）
  INSERT INTO component_maps (index, title, type, source, paint, property)
  VALUES (
    'ntpc_green_stores',
    '新北市綠色商店',
    'circle',
    'geojson',
    '{
      "circle-color": ["match", ["get", "type"],
        "連鎖型綠色商店", "#22C55E",
        "個別型綠色商店", "#86EFAC",
        "#22C55E"
      ],
      "circle-radius": 5,
      "circle-opacity": 0.85,
      "circle-stroke-color": "#ffffff",
      "circle-stroke-width": 1
    }',
    '[
      {"key": "name",             "name": "商店名稱"},
      {"key": "type",             "name": "類型"},
      {"key": "address",          "name": "地址"},
      {"key": "localcallservice", "name": "電話"},
      {"key": "number",           "name": "商店編號"}
    ]'
  )
  RETURNING id INTO v_ntpc_map_id;
  RAISE NOTICE '新建 ntpc_green_stores component_map id = %', v_ntpc_map_id;

  -- 2c. 雙北綠色商店（index=metro_green_stores，geojson 屬性為英文，含 city 欄位）
  INSERT INTO component_maps (id, index, title, type, source, paint, property)
  VALUES (
    103,
    'metro_green_stores',
    '雙北綠色商店',
    'circle',
    'geojson',
    '{
      "circle-color": ["match", ["get", "city"],
        "台北市", "#4caf50",
        "新北市", "#22C55E",
        "#4caf50"
      ],
      "circle-radius": 5,
      "circle-opacity": 0.85,
      "circle-stroke-color": "#ffffff",
      "circle-stroke-width": 1
    }',
    '[
      {"key": "name",    "name": "商店名稱"},
      {"key": "type",    "name": "類型"},
      {"key": "address", "name": "地址"},
      {"key": "phone",   "name": "電話"},
      {"key": "number",  "name": "商店編號"},
      {"key": "city",    "name": "城市"}
    ]'
  )
  ON CONFLICT (id) DO UPDATE
    SET title    = EXCLUDED.title,
        paint    = EXCLUDED.paint,
        property = EXCLUDED.property;

  -- ── 3. components ─────────────────────────────────────────────────────────────
  INSERT INTO components (id, index, name)
  VALUES (219, 'green_stores', '綠色商店')
  ON CONFLICT (id) DO UPDATE
    SET index = EXCLUDED.index,
        name  = EXCLUDED.name;

  -- ── 4. query_charts（三種城市情境）────────────────────────────────────────────
  -- 4a. 台北市：使用 id=1 (taipei_green_shop.geojson)，更新 map_config_ids 和 query
  UPDATE query_charts SET
    map_config_ids = ARRAY[1]::integer[],
    query_chart    = 'SELECT unnest(array[''連鎖型綠色商店'',''個別型綠色商店'']) AS name, ''circle'' AS type',
    source         = '台北市政府環保局',
    short_desc     = '顯示台北市環保局認證之綠色商店點位分布。',
    long_desc      = '顯示台北市政府環保局認證之綠色商店點位，包含連鎖型與個別型商家。綠色商店為通過環保標章認證、積極推動環保理念的商業場所。',
    use_case       = '可查詢台北市各行政區的綠色商店分布，提供市民選購友善環境商品的參考資訊。',
    updated_at     = NOW()
  WHERE index = 'green_stores' AND city = 'taipei';

  -- 4b. 新北市：新增 newtaipei 情境（使用 ntpc_green_stores.geojson）
  INSERT INTO query_charts (
    index, map_config_ids, query_type, query_chart,
    time_from, source, short_desc, long_desc, use_case,
    links, contributors, city, created_at, updated_at
  ) VALUES (
    'green_stores',
    ARRAY[v_ntpc_map_id]::integer[],
    'map_legend',
    'SELECT unnest(array[''連鎖型綠色商店'',''個別型綠色商店'']) AS name, ''circle'' AS type',
    'static', '新北市政府環保局',
    '顯示新北市環保局認定之綠色商店分布位置。',
    '本圖層展示新北市環保局認定之連鎖型與個別型綠色商店地理分布，可點選各點位查看詳細資訊。',
    '協助市民尋找鄰近的環保認證商店，促進綠色消費行為。',
    ARRAY[]::text[], ARRAY['新北市政府環保局'],
    'newtaipei', NOW(), NOW()
  )
  ON CONFLICT DO NOTHING;

  -- 4c. 雙北：使用 id=103 (metro_green_stores.geojson)，更新 map_config_ids 和 query
  UPDATE query_charts SET
    map_config_ids = ARRAY[103]::integer[],
    query_chart    = 'SELECT unnest(array[''台北市'',''新北市'']) AS name, ''circle'' AS type',
    source         = '台北市政府環保局、新北市政府環保局',
    short_desc     = '顯示雙北綠色商店點位分布。',
    long_desc      = '合併顯示台北市與新北市之綠色商店分布，可依城市顏色區分。',
    use_case       = '提供雙北市民查詢鄰近環保認證商店，促進綠色消費。',
    updated_at     = NOW()
  WHERE index = 'green_stores' AND city = 'metrotaipei';

  -- ── 5. dashboards ─────────────────────────────────────────────────────────────
  INSERT INTO dashboards (id, index, name, components, icon, updated_at, created_at)
  VALUES
    (v_dash_taipei, 'green_stores_taipei',      '台北市綠色商店', ARRAY[v_comp_id]::integer[], 'eco', NOW(), NOW()),
    (v_dash_metro,  'green_stores_metrotaipei', '雙北綠色商店',   ARRAY[v_comp_id]::integer[], 'eco', NOW(), NOW())
  ON CONFLICT (id) DO UPDATE
    SET name       = EXCLUDED.name,
        components = EXCLUDED.components,
        icon       = EXCLUDED.icon,
        updated_at = NOW();

  -- ── 6. dashboard_groups ───────────────────────────────────────────────────────
  -- 365 (台北市) → group 1 (public) + group 2 (taipei)
  -- 366 (雙北)   → group 1 (public) + group 3 (metrotaipei)
  INSERT INTO dashboard_groups (dashboard_id, group_id)
  VALUES
    (v_dash_taipei, 1), (v_dash_taipei, 2),
    (v_dash_metro,  1), (v_dash_metro,  3)
  ON CONFLICT (dashboard_id, group_id) DO NOTHING;

  -- ── 7. 更新 sequence ─────────────────────────────────────────────────────────
  PERFORM pg_catalog.setval(
    'public.dashboards_id_seq',
    (SELECT COALESCE(MAX(id), 0) FROM public.dashboards), true
  );
  PERFORM pg_catalog.setval(
    'public.component_maps_id_seq',
    (SELECT COALESCE(MAX(id), 0) FROM public.component_maps), true
  );

  RAISE NOTICE 'Done. component=%, dashboards=(%, %), ntpc_map=%', v_comp_id, v_dash_taipei, v_dash_metro, v_ntpc_map_id;
END $$;
