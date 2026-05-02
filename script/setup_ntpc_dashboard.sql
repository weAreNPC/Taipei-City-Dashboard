-- Setup NTPC Green Stores component in dashboardmanager DB
-- Run against: postgres-manager (dashboardmanager DB)
-- Usage: docker exec -i postgres-manager psql -U postgres -d dashboardmanager < script/setup_ntpc_dashboard.sql

DO $$
DECLARE
  v_cm_id   integer;
  v_comp_id integer;
  v_dash_id integer;
BEGIN

  -- 1. component_maps ─ defines the Mapbox circle layer
  INSERT INTO component_maps (index, title, type, source, paint, property)
  VALUES (
    'ntpc_green_stores',
    '綠色商店',
    'circle',
    'geojson',
    '{
      "circle-color": ["match", ["get", "type"],
        "連鎖型綠色商店", "#22C55E",
        "個別型綠色商店", "#86EFAC",
        "#22C55E"
      ],
      "circle-radius": 6,
      "circle-opacity": 0.85,
      "circle-stroke-width": 1,
      "circle-stroke-color": "#ffffff"
    }',
    '[
      {"key": "name",             "name": "商店名稱"},
      {"key": "type",             "name": "商店類型"},
      {"key": "address",          "name": "地址"},
      {"key": "localcallservice", "name": "聯絡電話"}
    ]'
  )
  RETURNING id INTO v_cm_id;
  RAISE NOTICE 'component_maps id = %', v_cm_id;

  -- 2. component_charts ─ drives the MapLegend sidebar chart
  INSERT INTO component_charts (index, color, types, unit)
  VALUES (
    'ntpc_green_stores',
    ARRAY['#22C55E', '#86EFAC'],
    ARRAY['MapLegend'],
    '間'
  );

  -- 3. components ─ the logical component entry
  INSERT INTO components (index, name)
  VALUES ('ntpc_green_stores', '新北市綠色商店')
  RETURNING id INTO v_comp_id;
  RAISE NOTICE 'components id = %', v_comp_id;

  -- 4. query_charts ─ two rows (taipei + metrotaipei city contexts)
  INSERT INTO query_charts (
    index, map_config_ids, query_type, query_chart,
    time_from, source, short_desc, long_desc, use_case,
    links, contributors, city, created_at, updated_at
  ) VALUES
  (
    'ntpc_green_stores',
    ARRAY[v_cm_id]::integer[],
    'map_legend',
    'SELECT unnest(array[''連鎖型綠色商店'', ''個別型綠色商店'']) AS name, ''circle'' AS type',
    'static', '新北市政府環保局',
    '顯示新北市環保局認定之綠色商店分布位置。',
    '本圖層展示新北市環保局認定之連鎖型與個別型綠色商店地理分布，商家通過環保標準認定，可點選各點位查看詳細資訊。',
    '協助市民尋找鄰近的環保認證商店，促進綠色消費行為。',
    ARRAY[]::text[], ARRAY['新北市政府環保局'],
    'taipei', NOW(), NOW()
  ),
  (
    'ntpc_green_stores',
    ARRAY[v_cm_id]::integer[],
    'map_legend',
    'SELECT unnest(array[''連鎖型綠色商店'', ''個別型綠色商店'']) AS name, ''circle'' AS type',
    'static', '新北市政府環保局',
    '顯示新北市環保局認定之綠色商店分布位置。',
    '本圖層展示新北市環保局認定之連鎖型與個別型綠色商店地理分布，商家通過環保標準認定，可點選各點位查看詳細資訊。',
    '協助市民尋找鄰近的環保認證商店，促進綠色消費行為。',
    ARRAY[]::text[], ARRAY['新北市政府環保局'],
    'metrotaipei', NOW(), NOW()
  );

  -- 5. dashboards ─ new dashboard card
  INSERT INTO dashboards (index, name, components, icon, updated_at, created_at)
  VALUES (
    'ntpc_green_stores',
    '新北市綠色商店',
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
