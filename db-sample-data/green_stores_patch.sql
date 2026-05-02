-- Green stores patch: insert only new records

-- component_charts
INSERT INTO public.component_charts (index, color, types, unit)
VALUES ('green_stores', '{#4CAF50,#81C784}', '{MapLegend}', '間')
ON CONFLICT (index) DO NOTHING;

-- component_maps
INSERT INTO public.component_maps (id, index, title, type, source, size, icon, paint, property)
VALUES
  (102, 'taipei_green_shop', '台北市綠色商店', 'circle', 'geojson', NULL, NULL,
   '{"circle-color":"#4CAF50"}',
   '[{"key":"名稱","name":"名稱"},{"key":"地址","name":"地址"},{"key":"商店編號","name":"商店編號"},{"key":"類型","name":"類型"}]'),
  (103, 'metro_green_stores', '雙北綠色商店', 'circle', 'geojson', NULL, NULL,
   '{"circle-color":"#4CAF50"}',
   '[{"key":"name","name":"名稱"},{"key":"address","name":"地址"},{"key":"number","name":"商店編號"},{"key":"type","name":"類型"},{"key":"city","name":"城市"}]')
ON CONFLICT (id) DO NOTHING;

-- components
INSERT INTO public.components (id, index, name)
VALUES (219, 'green_stores', '綠色商店')
ON CONFLICT (id) DO NOTHING;

-- dashboards
INSERT INTO public.dashboards (id, index, name, components, icon, updated_at, created_at)
VALUES
  (365, 'green_stores_taipei', '台北市綠色商店', '{219}', 'eco', '2026-05-02 00:00:00+00', '2026-05-02 00:00:00+00'),
  (366, 'green_stores_metrotaipei', '雙北綠色商店', '{219}', 'eco', '2026-05-02 00:00:00+00', '2026-05-02 00:00:00+00')
ON CONFLICT (id) DO NOTHING;

-- dashboard_groups
INSERT INTO public.dashboard_groups (dashboard_id, group_id)
VALUES (365, 1), (365, 2), (366, 1), (366, 3)
ON CONFLICT (dashboard_id, group_id) DO NOTHING;

-- update sequence
SELECT pg_catalog.setval('public.dashboards_id_seq', (SELECT COALESCE(MAX(id), 0) FROM public.dashboards), true);
SELECT pg_catalog.setval('public.component_maps_id_seq', (SELECT COALESCE(MAX(id), 0) FROM public.component_maps), true);
