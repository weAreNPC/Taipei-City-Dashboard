-- 台北市綠色商店 component 插入語句
-- 執行對象：dashboardmanager DB (postgres-manager container)
-- 用法：docker exec -i postgres-manager psql -U postgres -d dashboardmanager < db-sample-data/taipei_green_shop.sql

DO $$
DECLARE
  v_cm_id   integer;
  v_comp_id integer;
  v_dash_id integer;
BEGIN

  -- 1. component_maps ─ Mapbox circle layer styling
  INSERT INTO component_maps (index, title, type, source, paint, property)
  VALUES (
    'taipei_green_shop',
    '台北市綠色商店',
    'circle',
    'geojson',
    '{
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
    '[
      {"key": "名稱",   "name": "商店名稱"},
      {"key": "類型",   "name": "類型"},
      {"key": "地址",   "name": "地址"},
      {"key": "電話",   "name": "電話"},
      {"key": "商店編號", "name": "商店編號"}
    ]'
  )
  RETURNING id INTO v_cm_id;
  RAISE NOTICE 'component_maps id = %', v_cm_id;

  -- 2. component_charts ─ drives the MapLegend sidebar chart
  INSERT INTO component_charts (index, color, types, unit)
  VALUES (
    'taipei_green_shop',
    ARRAY['#4caf50', '#a5d6a7'],
    ARRAY['MapLegend'],
    '間'
  );

  -- 3. components ─ logical component entry
  INSERT INTO components (index, name)
  VALUES ('taipei_green_shop', '台北市綠色商店')
  RETURNING id INTO v_comp_id;
  RAISE NOTICE 'components id = %', v_comp_id;

  -- 4. query_charts ─ taipei city context
  INSERT INTO query_charts (
    index, map_config_ids, query_type, query_chart,
    time_from, source, short_desc, long_desc, use_case,
    links, contributors, city, created_at, updated_at
  ) VALUES
  (
    'taipei_green_shop',
    ARRAY[v_cm_id]::integer[],
    'map_legend',
    'SELECT unnest(array[''連鎖型綠色商店'', ''個別型綠色商店'']) AS name, ''circle'' AS type',
    'static', '台北市政府環保局',
    '顯示台北市環保局認證之綠色商店點位分布。',
    '顯示台北市政府環保局認證之綠色商店點位，包含連鎖型與個別型商家，共 906 處。綠色商店為通過環保標章認證、積極推動環保理念的商業場所。',
    '可查詢台北市各行政區的綠色商店分布，提供市民選購友善環境商品的參考資訊，並可結合其他環保指標進行分析。',
    ARRAY[]::text[], ARRAY['tuic'],
    'taipei', NOW(), NOW()
  ),
  (
    'taipei_green_shop',
    ARRAY[v_cm_id]::integer[],
    'map_legend',
    'SELECT unnest(array[''連鎖型綠色商店'', ''個別型綠色商店'']) AS name, ''circle'' AS type',
    'static', '台北市政府環保局',
    '顯示台北市環保局認證之綠色商店點位分布。',
    '顯示台北市政府環保局認證之綠色商店點位，包含連鎖型與個別型商家，共 906 處。綠色商店為通過環保標章認證、積極推動環保理念的商業場所。',
    '可查詢台北市各行政區的綠色商店分布，提供市民選購友善環境商品的參考資訊，並可結合其他環保指標進行分析。',
    ARRAY[]::text[], ARRAY['tuic'],
    'metrotaipei', NOW(), NOW()
  );

  -- 5. dashboards ─ new dashboard card
  INSERT INTO dashboards (index, name, components, icon, updated_at, created_at)
  VALUES (
    'taipei_green_shop',
    '台北市綠色商店',
    ARRAY[v_comp_id]::integer[],
    'eco',
    NOW(), NOW()
  )
  RETURNING id INTO v_dash_id;
  RAISE NOTICE 'dashboards id = %', v_dash_id;

  -- 6. dashboard_groups ─ visible in both taipei (2) and metrotaipei (3) sidebars
  INSERT INTO dashboard_groups (dashboard_id, group_id) VALUES (v_dash_id, 2);
  INSERT INTO dashboard_groups (dashboard_id, group_id) VALUES (v_dash_id, 3);

  RAISE NOTICE 'Done. component_maps=%, component=%, dashboard=%', v_cm_id, v_comp_id, v_dash_id;
END $$;
