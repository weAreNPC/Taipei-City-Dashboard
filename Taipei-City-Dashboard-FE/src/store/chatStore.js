import { ref, watch } from 'vue'
import { defineStore } from 'pinia'
import http from "../router/axios";
import router from "../router";
import { useAuthStore } from "./authStore";
import { useContentStore } from "./contentStore";
import { useMapStore } from "./mapStore";

const AGENT_TOOLS = [
	{
		type: "function",
		function: {
			name: "get_current_ui_context",
			description:
				"ONLY when the user explicitly asks about the current screen/page, map view, open layers, or \"where am I\" / device location—not for general statistics or follow-ups like \"give me numbers\". Returns compact ui_context JSON; backend adds map_context.reverse_geocode when user_location exists. For index lookup or chart data use resolve_navigation_target / get_component_facts / get_dashboard_component_summary instead. Calling without clear UI intent may be blocked server-side.",
			parameters: {
				type: "object",
				properties: {},
			},
		},
	},
	{
		type: "function",
		function: {
			name: "resolve_coordinates_zh",
			description:
				"Forward geocode for Taiwan: converts a place name or address to WGS84 lat/lng (OpenStreetMap Nominatim). Use when the user names a specific location (not \"here\"/current GPS). Then call get_geo_nearby_for_component with location_anchor explicit and those coordinates; same-turn parallel calls are OK—backend injects coords from this tool if geo args use 0.",
			parameters: {
				type: "object",
				properties: {
					query: {
						type: "string",
						description:
							"Landmark, district, or address e.g. 台北101、信義區市政府、新北板橋車站",
					},
				},
				required: ["query"],
			},
		},
	},
	{
		type: "function",
		function: {
			name: "get_component_facts",
			description: "Get structured facts and chart preview of one component by id or index.",
			parameters: {
				type: "object",
				properties: {
					component_id: { type: "integer", description: "Component ID" },
					component_index: { type: "string", description: "Component index" },
					city: { type: "string", description: "taipei or metrotaipei" },
					include_chart: { type: "boolean", description: "Include chart preview data" },
				},
			},
		},
	},
	{
		type: "function",
		function: {
			name: "get_geo_nearby_for_component",
			description:
				"Proximity search for components with geo_nearby_supported. NOT for “how many X in the whole district/city”—that needs get_component_facts (chart). This tool always counts points within radius_meters of one anchor (GPS or geocoded place). Two modes: (1) location_anchor user_device (default)—browser GPS from ui_context overwrites lat/lng; use for 我附近／這裡. (2) location_anchor explicit—uses lat/lng for a named place; call resolve_coordinates_zh first or pass known coordinates; never invent. YouBike responses include query_location_description when relevant; map_geojson returns interpretation_hint_zh—read it. Geojson uses merged Taipei+New Taipei layers where applicable. See manifest geo_nearby_supported.",
			parameters: {
				type: "object",
				properties: {
					location_anchor: {
						type: "string",
						enum: ["user_device", "explicit"],
						description:
							"user_device: use device GPS (default). explicit: query at given latitude/longitude (foreign-named location).",
					},
					component_index: {
						type: "string",
						description:
							"English component index e.g. green_stores, youbike_availability, bike_network — must match the topic.",
					},
					component_id: { type: "integer", description: "Optional alternative to component_index" },
					city: { type: "string", description: "taipei or metrotaipei — DB row scope for the component" },
					latitude: {
						type: "number",
						description:
							"With user_device: placeholder OK (server overwrites). With explicit: real WGS84 lat from resolve_coordinates_zh or known coords.",
					},
					longitude: {
						type: "number",
						description:
							"With user_device: placeholder OK. With explicit: real WGS84 lon.",
					},
					radius_meters: { type: "integer", description: "Search radius in meters, default 500" },
					top_n: { type: "integer", description: "Nearest N items to list, default 5" },
				},
				required: ["city", "latitude", "longitude"],
			},
		},
	},
	{
		type: "function",
		function: {
			name: "get_dashboard_component_summary",
			description:
				"Fetch chart_preview for multiple components on one dashboard in a single call. Use when the user asks to combine, synthesize, interpret (意味著／統整／整合／實際數據), or the whole board (e.g. 長照關懷所有資訊). Prefer this over many get_component_facts for the same dashboard. Pass max_components=10 to include all typical boards; use optional component_indexes to limit to named components (English index strings).",
			parameters: {
				type: "object",
				properties: {
					dashboard_index: { type: "string", description: "Dashboard index" },
					city: { type: "string", description: "taipei or metrotaipei" },
					max_components: { type: "integer", description: "Cap after filter, up to 10, default 5" },
					component_indexes: {
						type: "array",
						items: { type: "string" },
						description:
							"Optional: only these component index strings (e.g. dependency_aging, aging_workforce_trend). Omit to include all components on the dashboard up to max_components.",
					},
				},
				required: ["dashboard_index"],
			},
		},
	},
	{
		type: "function",
		function: {
			name: "resolve_navigation_target",
			description:
				"Look up component_index / dashboard_index from Chinese or English name fragments and optional city (taipei/metrotaipei). Same visibility as user sidebar. Use when unsure of exact index. For topic-based dashboard routing (e.g. 環保／環境／交通／長照), call with kind \"dashboard\" or \"all\" so dashboard_matches lists the board index before you navigate.",
			parameters: {
				type: "object",
				properties: {
					query: { type: "string", description: "Component or dashboard name / keyword" },
					city: { type: "string", description: "taipei, metrotaipei, or empty" },
					kind: {
						type: "string",
						description: "component | dashboard | all",
						enum: ["component", "dashboard", "all"],
					},
					limit: { type: "integer", description: "Max matches per category, default 5" },
				},
				required: ["query"],
			},
		},
	},
];

const ALLOWED_UI_ACTIONS = {
	navigate_dashboard: true,
	open_component_info: true,
	switch_city: true,
	open_map_layer: true,
};

const normalizeUIActions = (actions) => {
	let candidates = [];
	if (Array.isArray(actions)) {
		const expanded = [];
		for (let i = 0; i < actions.length; i++) {
			const el = actions[i];
			if (
				typeof el === "string" &&
				ALLOWED_UI_ACTIONS[el] &&
				i + 1 < actions.length &&
				actions[i + 1] &&
				typeof actions[i + 1] === "object" &&
				!Array.isArray(actions[i + 1])
			) {
				expanded.push({ type: el, params: { ...actions[i + 1] } });
				i++;
				continue;
			}
			expanded.push(el);
		}
		candidates = expanded;
	} else if (actions && typeof actions === "object") {
		if (actions.type) {
			candidates = [actions];
		} else {
			// Compatibility: object map format, e.g. { open_component_info: {...} }
			candidates = Object.entries(actions).map(([type, params]) => ({ type, params }));
		}
	}

	return candidates
		.map((action) => {
			if (!action || typeof action !== "object") return null;
			const type =
				action.type || action.action || action.function_name;
			if (!type) return null;
			if (action.params && typeof action.params === "object") {
				return { type, params: action.params };
			}
			// Compatibility: some LLM replies place params at top level.
			const { type: _type, action: _a, ...rest } = action;
			return { type, params: rest };
		})
		.filter(Boolean);
};

const normalizeText = (value) => String(value || "").toLowerCase().trim();
const COMPONENT_ALIAS_MAP = {
	ubike: "youbike_availability",
	youbike: "youbike_availability",
};

/** digest 尚未載入或缺少 youbike 列時之後備（與後端 ubikeFallback* 一致） */
const UBIKE_MAP_LAYER_FALLBACK_ENTRY = {
	preferred_dashboard_index: "practical_transportation_newtpe",
	preferred_navigate_city: "metrotaipei",
	city: "metrotaipei",
	has_map_layer: true,
	placement_dashboard_indexes: ["practical_transportation_newtpe"],
};

const navigateDashboardIndexRaw = (params) =>
	params?.index || params?.dashboard_index || params?.dashboard || "";

const isMapLayersDashboardIndex = (idx) => normalizeText(idx).includes("map-layers");

const firstNonEmptyParam = (...vals) => {
	for (const v of vals) {
		const s = String(v ?? "").trim();
		if (s) return s;
	}
	return "";
};

/** 與後端 GetComponentRoutingManifest 對齊之精簡表（條目數上限；越小越省模型上下文）。 */
const MAX_ROUTING_DIGEST = 120;
/** mapview_layer_catalog 最多送入筆數（每輪 tool／請求會重複附帶 ui_context）。 */
const MAX_MAPVIEW_LAYER_CATALOG = 24;
/** 全站 component_index|city|dashboard 索引字元上限（system 內嵌）。 */
const MAX_SITE_CATALOG_CHARS = 10000;

/** 與 digest／全站目錄一致：優先非 map-layers-* 的側欄儀表板。 */
const pickPreferredDashboardFromPlacements = (placements) => {
	const pl = placements || [];
	if (!pl.length) return null;
	return (
		pl.find(
			(p) =>
				p?.dashboard_index &&
				!normalizeText(p.dashboard_index).includes("map-layers"),
		) || pl[0]
	);
};

const buildCompactSiteComponentCatalog = (components, maxChars) => {
	if (!Array.isArray(components) || components.length === 0) {
		return { text: "", truncated: false, totalLines: 0, shownLines: 0 };
	}
	const lines = [];
	for (const e of components) {
		const preferred = pickPreferredDashboardFromPlacements(e.placements);
		const dash = preferred?.dashboard_index || "";
		const ci = String(e.component_index || "").trim();
		const city = String(e.city || "").trim();
		if (!ci || !city || !dash) continue;
		lines.push(`${ci}|${city}|${dash}`);
	}
	const unique = [...new Set(lines)].sort();
	const totalLines = unique.length;
	let text = unique.join("\n");
	let truncated = false;
	let shownLines = totalLines;
	const footer = (n, total) =>
		`\n…(全站共${total}筆，此處僅列前${n}筆以省 token；其餘請 resolve_navigation_target)`;
	if (text.length > maxChars) {
		truncated = true;
		let lo = 0;
		let hi = unique.length;
		while (lo < hi) {
			const mid = Math.floor((lo + hi + 1) / 2);
			const chunk = unique.slice(0, mid).join("\n");
			if (chunk.length + footer(mid, totalLines).length <= maxChars) lo = mid;
			else hi = mid - 1;
		}
		shownLines = lo;
		text = unique.slice(0, lo).join("\n") + footer(lo, totalLines);
	}
	return { text, truncated, totalLines, shownLines };
};

const buildRoutingDigestFromAPIComponents = (components) => {
	if (!Array.isArray(components)) return [];
	const out = [];
	for (const e of components) {
		const pl = e.placements || [];
		if (!pl.length) continue;
		const preferred = pickPreferredDashboardFromPlacements(pl);
		if (!preferred?.dashboard_index) continue;
		const row = {
			component_index: e.component_index,
			name: e.name,
			city: e.city,
			has_map_layer: !!e.has_map_layer,
			preferred_dashboard_index: preferred.dashboard_index,
			preferred_navigate_city: e.city,
		};
		if (e.geo_nearby_supported != null) {
			row.geo_nearby_supported = !!e.geo_nearby_supported;
			if (e.geo_nearby_provider)
				row.geo_nearby_provider = String(e.geo_nearby_provider);
		}
		const placeIdx = [
			...new Set(pl.map((p) => p.dashboard_index).filter(Boolean)),
		].slice(0, 8);
		if (placeIdx.length) row.placement_dashboard_indexes = placeIdx;
		out.push(row);
		if (out.length >= MAX_ROUTING_DIGEST) break;
	}
	return out;
};

const collectOpenMapLayerTargetsFromActions = (actions) => {
	if (!Array.isArray(actions)) return [];
	const out = [];
	for (const a of actions) {
		if (a?.type !== "open_map_layer") continue;
		const p = a.params || {};
		const raw = firstNonEmptyParam(
			p.component_index,
			p.index,
			p.component,
			p.layer,
		);
		const ci = COMPONENT_ALIAS_MAP[normalizeText(raw)] || raw;
		if (ci) {
			out.push({ component_index: ci, city: String(p.city || "").trim() });
		}
	}
	return out;
};

const findRoutingDigestEntry = (digest, componentIndex, city) => {
	if (!Array.isArray(digest) || !componentIndex) return null;
	const cx = normalizeText(componentIndex);
	const cty = normalizeText(city);
	let fallback = null;
	for (const e of digest) {
		if (normalizeText(e.component_index) !== cx) continue;
		if (!cty) return e;
		if (normalizeText(e.city) === cty) return e;
		if (!fallback) fallback = e;
	}
	return fallback;
};

const isOnPreferredMapviewDashboard = (contentStore, entry) => {
	const mode = contentStore.currentDashboard?.mode || "";
	if (!mode.includes("mapview")) return false;
	const wantCity = entry.preferred_navigate_city || entry.city || "";
	return (
		normalizeText(contentStore.currentDashboard?.index) ===
			normalizeText(entry.preferred_dashboard_index) &&
		(!wantCity ||
			normalizeText(contentStore.currentDashboard?.city) === normalizeText(wantCity))
	);
};

const needsPreferredMapviewFirst = (contentStore, entry) => {
	if (!entry?.preferred_dashboard_index || !entry?.has_map_layer) return false;
	if (isOnPreferredMapviewDashboard(contentStore, entry)) return false;
	return true;
};

/** 先切到 manifest 建議的 mapview 儀表板；已在該頁則略過。 */
const ensurePreferredMapviewDashboard = async (contentStore, entry) => {
	if (!entry?.preferred_dashboard_index) return "";
	if (isOnPreferredMapviewDashboard(contentStore, entry)) {
		return "";
	}
	const resolved = resolveDashboardTarget(
		contentStore.dashboards,
		entry.preferred_dashboard_index,
		entry.preferred_navigate_city || entry.city,
	);
	const index = resolved?.index || entry.preferred_dashboard_index;
	const navCity = resolved?.city || entry.preferred_navigate_city || entry.city;
	await router.push({ path: "/mapview", query: { index, city: navCity } });
	const deadline = Date.now() + 20000;
	while (Date.now() < deadline) {
		if (isOnPreferredMapviewDashboard(contentStore, entry) && !contentStore.loading) {
			break;
		}
		await sleep(150);
	}
	return `已切換至地圖儀表板（${index}，${navCity}）`;
};

const rewriteNavigateForMapLayerBatch = (actions, digest) => {
	if (!Array.isArray(actions)) return actions;
	const layerTargets = collectOpenMapLayerTargetsFromActions(actions);
	const digestList = Array.isArray(digest) ? digest : [];
	return actions.map((a) => {
		if (a?.type !== "navigate_dashboard") return a;
		let comp = firstNonEmptyParam(
			a.params?.component_index,
			a.params?.map_layer_component_index,
		);
		let cty = String(a.params?.city || "").trim();
		if (!comp && layerTargets.length === 1) {
			comp = layerTargets[0].component_index;
			cty = layerTargets[0].city || cty;
		}
		if (!comp) return a;
		comp = COMPONENT_ALIAS_MAP[normalizeText(comp)] || comp;
		let entry =
			digestList.length > 0 ? findRoutingDigestEntry(digestList, comp, cty) : null;
		if (
			!entry?.preferred_dashboard_index &&
			normalizeText(comp) === "youbike_availability"
		) {
			entry = UBIKE_MAP_LAYER_FALLBACK_ENTRY;
		}
		if (!entry?.preferred_dashboard_index) return a;
		const cur = navigateDashboardIndexRaw(a.params);
		const wrongBoard =
			isMapLayersDashboardIndex(cur) ||
			(entry.placement_dashboard_indexes?.length > 0 &&
				!entry.placement_dashboard_indexes.some(
					(x) => normalizeText(x) === normalizeText(cur),
				));
		if (!wrongBoard) return a;
		return {
			...a,
			params: {
				...a.params,
				index: entry.preferred_dashboard_index,
				city: entry.preferred_navigate_city || entry.city,
				mode: "mapview",
			},
		};
	});
};

/** 自行車「道／路網」設施圖，勿與 YouBike 站點圖層混淆 */
const isBikeLaneInfrastructureIntent = (value) => {
	const raw = String(value || "");
	const n = normalizeText(raw);
	return (
		n.includes("自行車道") ||
		n.includes("自行車道路") ||
		n.includes("單車道") ||
		n.includes("自行車路網") ||
		n.includes("自行車路線") ||
		n.includes("單車路網") ||
		n.includes("車道圖") ||
		n.includes("自行車專用道") ||
		/(自行車|單車).{0,8}(道|路網|路線)/.test(raw) ||
		/(道|路網).{0,8}(自行車|單車)/.test(raw)
	);
};

const isUbikeKeyword = (value) => {
	const text = normalizeText(value);
	if (!text) return false;
	if (isBikeLaneInfrastructureIntent(value)) return false;
	return (
		text.includes("ubkie") ||
		text.includes("ubike") ||
		text.includes("youbike") ||
		text.includes("微笑單車") ||
		text.includes("自行車") ||
		text.includes("單車")
	);
};

/** 使用者明確要看地圖／圖層時（與「只要數字／文字資訊」區隔） */
const MAP_VISUALIZATION_HINT =
	/地圖|圖層|mapview|開.*圖層|顯示.*圖層|切到.*地圖|地圖介面|地圖模式|在地圖|看.*分布/i;

const userWantsMapVisualization = (q) => {
	const s = String(q || "").trim();
	if (MAP_VISUALIZATION_HINT.test(s)) return true;
	if (isUbikeKeyword(s) && /\bmap\b|地圖|圖層|layer/i.test(s)) return true;
	return false;
};

const cloneUiActionsForMutation = (actions) =>
	Array.isArray(actions)
		? actions.map((a) => ({
				...a,
				params: { ...(a.params || {}) },
			}))
		: [];

const inferCityFromNavigateActions = (actions) => {
	for (let i = actions.length - 1; i >= 0; i--) {
		const a = actions[i];
		if (a?.type === "navigate_dashboard" && a.params?.city) {
			return String(a.params.city).trim();
		}
	}
	return "";
};

const batchReferencesUbikeLayer = (actions) =>
	actions.some((a) => {
		if (a.type === "open_map_layer") {
			const raw = firstNonEmptyParam(
				a.params?.component_index,
				a.params?.index,
				a.params?.layer,
			);
			const n = COMPONENT_ALIAS_MAP[normalizeText(raw)] || raw;
			return normalizeText(n) === "youbike_availability";
		}
		if (a.type === "navigate_dashboard") {
			const raw = firstNonEmptyParam(
				a.params?.component_index,
				a.params?.map_layer_component_index,
				a.params?.layer,
			);
			const n = COMPONENT_ALIAS_MAP[normalizeText(raw)] || raw;
			return normalizeText(n) === "youbike_availability";
		}
		return false;
	});

/**
 * 模型常省略 mode=mapview 或未附 open_map_layer。當使用者話術明确要求「地圖／圖層」時補齊。
 */
const ensureMapVisualizationUiActions = (actions, userQuestion, digest) => {
	if (!userWantsMapVisualization(userQuestion)) return actions;
	const list = cloneUiActionsForMutation(actions);
	for (const a of list) {
		if (a.type === "navigate_dashboard") {
			a.params.mode = "mapview";
		}
	}
	const wantsUbike =
		isUbikeKeyword(userQuestion) || batchReferencesUbikeLayer(list);
	const hasExplicitMapLayerStep =
		list.some((a) => a.type === "open_map_layer") ||
		list.some((a) => {
			if (a.type !== "navigate_dashboard") return false;
			return !!firstNonEmptyParam(
				a.params?.component_index,
				a.params?.map_layer_component_index,
			);
		});
	if (wantsUbike && !hasExplicitMapLayerStep) {
		const cy =
			inferCityFromNavigateActions(list) ||
			findRoutingDigestEntry(digest, "youbike_availability", "")?.preferred_navigate_city ||
			findRoutingDigestEntry(digest, "youbike_availability", "")?.city ||
			UBIKE_MAP_LAYER_FALLBACK_ENTRY.preferred_navigate_city;
		list.push({
			type: "open_map_layer",
			params: { component_index: "youbike_availability", city: cy },
		});
	}
	return list;
};

const collectDashboards = (dashboardsMap) => {
	if (!(dashboardsMap instanceof Map)) return [];
	const results = [];
	for (const [city, dashboards] of dashboardsMap.entries()) {
		if (!Array.isArray(dashboards)) continue;
		for (const dashboard of dashboards) {
			results.push({
				city,
				index: dashboard?.index || "",
				name: dashboard?.name || "",
			});
		}
	}
	return results;
};

const resolveDashboardTarget = (dashboardsMap, rawIndex, cityHint = "") => {
	const allDashboards = collectDashboards(dashboardsMap);
	if (!rawIndex || allDashboards.length === 0) return null;

	const candidate = normalizeText(rawIndex);
	const city = normalizeText(cityHint);

	const byExactIndex = allDashboards.find(
		(item) => normalizeText(item.index) === candidate && (!city || normalizeText(item.city) === city)
	) || allDashboards.find((item) => normalizeText(item.index) === candidate);
	if (byExactIndex) return byExactIndex;

	const byContains = allDashboards.filter((item) => {
		const name = normalizeText(item.name);
		const index = normalizeText(item.index);
		return name.includes(candidate) || index.includes(candidate);
	});
	if (byContains.length === 0) return null;
	return (
		byContains.find((item) => city && normalizeText(item.city) === city) ||
		byContains[0]
	);
};

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** 若有載入過 /component 全表，可把側欄的數字 id 對應成 component_index + city */
const buildComponentIdLookupFromStore = (contentStore) => {
	const list = contentStore.components;
	if (!Array.isArray(list) || list.length === 0) return null;
	const byId = {};
	for (const c of list) {
		if (c?.id == null) continue;
		byId[String(c.id)] = {
			component_index: c.index || "",
			city: c.city || "",
		};
	}
	return Object.keys(byId).length ? byId : null;
};

const enrichComponentIdsForAgent = (componentIds, lookup, maxItems = 16) => {
	if (!lookup || !Array.isArray(componentIds)) return undefined;
	const slice = componentIds.slice(0, maxItems);
	return slice.map((id) => {
		const hit = lookup[String(id)];
		return hit?.component_index
			? {
					id,
					component_index: hit.component_index,
					city: hit.city || "",
				}
			: { id };
	});
};

/** 側欄：各城市儀表板清單（精簡：不附完整 component_ids 陣列以省 token；詳情用 digest／resolve） */
const buildSidebarCatalogForAgent = (contentStore) => {
	const cities =
		contentStore.cityManager?.activeCities?.length > 0
			? contentStore.cityManager.activeCities
			: ["taipei", "metrotaipei"];
	const lookup = buildComponentIdLookupFromStore(contentStore);
	const sidebar_by_city = {};
	for (const city of cities) {
		const boards = contentStore.getDashboardsByCity(city) || [];
		sidebar_by_city[city] = boards.map((d) => {
			const component_ids = Array.isArray(d.components) ? d.components : [];
			const row = {
				index: d.index || "",
				name: d.name || "",
				n_components: component_ids.length,
			};
			const enriched = enrichComponentIdsForAgent(component_ids, lookup);
			if (enriched?.length) row.components_with_index = enriched;
			return row;
		});
	}
	return sidebar_by_city;
};

/** 目前儀表板 index 底下已載入的組件（跨 city 彙總於 cityDashboard） */
const buildCurrentDashboardComponentsForAgent = (contentStore) => {
	const raw = contentStore.cityDashboard?.components;
	if (!Array.isArray(raw)) return [];
	return raw.map((c) => ({
		index: c.index || "",
		name: c.name || "",
		id: c.id,
		city: c.city || "",
		has_map_layer: !!(c.map_config && c.map_config.length),
	}));
};

const mergeMapLayerCatalog = (contentStore) => {
	const out = [];
	const seen = new Set();
	for (const list of [contentStore.allMapLayers, contentStore.mapLayers]) {
		if (!Array.isArray(list)) continue;
		for (const item of list) {
			const key = `${item?.id ?? ""}:${item?.city ?? ""}`;
			if (seen.has(key)) continue;
			seen.add(key);
			out.push(item);
		}
	}
	return out;
};

/** 圖資模式已載入的主題圖層 component index（供 Agent 對齊「地圖上有哪些」） */
const buildThematicLayerIndexesHint = (contentStore) => {
	const catalog = mergeMapLayerCatalog(contentStore);
	if (!catalog.length) return [];
	const uniq = [...new Set(catalog.map((c) => c.index).filter(Boolean))];
	return uniq.slice(0, 100);
};

const pickComponentWithMapLayer = (candidates, normalizedComponent, requestedCity) => {
	if (!Array.isArray(candidates)) return null;
	return (
		candidates.find(
			(item) =>
				normalizeText(item?.index) === normalizeText(normalizedComponent) &&
				(!requestedCity ||
					normalizeText(item?.city) === normalizeText(requestedCity)) &&
				Array.isArray(item?.map_config) &&
				item.map_config.length > 0,
		) || null
	);
};

const fetchComponentWithMapLayerByIndex = async (normalizedComponent, requestedCity) => {
	try {
		const res = await http.get(`/component/`, {
			params: {
				filtermode: "eq",
				filterby: "index",
				filtervalue: normalizedComponent,
				city: requestedCity,
			},
		});
		const rows = res.data?.data || [];
		let hit = pickComponentWithMapLayer(rows, normalizedComponent, requestedCity);
		if (!hit) {
			hit = rows.find(
				(item) =>
					normalizeText(item?.index) === normalizeText(normalizedComponent) &&
					Array.isArray(item?.map_config) &&
					item.map_config.length > 0,
			);
		}
		return hit || null;
	} catch {
		return null;
	}
};

export const useChatStore = defineStore('chat', () => {
	const authStore = useAuthStore();
	const contentStore = useContentStore();
	const mapStore = useMapStore();

	const routingManifestDigest = ref([]);
	/** GET /ai/component-routing-manifest 原始 components（供每則 system 附全站索引） */
	const routingManifestRawComponents = ref([]);
	/** 與 manifest 一併下發：各 mapview 儀表板×city 下可 open_map_layer 的組件表 */
	const agentMapviewLayerCatalog = ref([]);
	const mapviewLayerCatalogTruncated = ref(false);
	const routingManifestFetched = ref(false);

	const ensureRoutingManifestDigest = async () => {
		if (routingManifestFetched.value) return;
		try {
			const res = await http.get("/ai/component-routing-manifest");
			const data = res.data?.data;
			routingManifestRawComponents.value = Array.isArray(data?.components)
				? data.components
				: [];
			routingManifestDigest.value = buildRoutingDigestFromAPIComponents(
				data?.components,
			);
			const rawCat = data?.mapview_layer_catalog;
			if (Array.isArray(rawCat) && rawCat.length > MAX_MAPVIEW_LAYER_CATALOG) {
				agentMapviewLayerCatalog.value = rawCat.slice(0, MAX_MAPVIEW_LAYER_CATALOG);
				mapviewLayerCatalogTruncated.value = true;
			} else {
				agentMapviewLayerCatalog.value = Array.isArray(rawCat) ? rawCat : [];
				mapviewLayerCatalogTruncated.value =
					!!data?.mapview_layer_catalog_truncated;
			}
		} catch (e) {
			console.warn("component-routing-manifest fetch failed", e);
		} finally {
			routingManifestFetched.value = true;
		}
	};

  	// 預設訊息
  	const defaultChatData = [
    	{
      		id: 1,
      		role: 'bot',
	  		isDefault: true,
      		content:
        	'您好，我是【臺北城市儀表板】小幫手，很高興為您服務！\n 您可以： \n\n • 點擊左側既有的儀表板主題，快速查看各主題內容 \n • 輸入您感興趣的主題描述，我會自動為您組建最適合的儀表板 \n\n 如果有想了解的內容，歡迎直接告訴我，我會盡力協助！\n\n 📩 聯絡信箱：tuic@gov.taipei \n 🏢 臺北大數據中心 \n\n',
    	},
  	];

	const recommendComponents = ref(null)
	const lastMapLayerAction = ref({
		componentIndex: "",
		city: "",
		timestamp: 0,
	});

  	// 從 sessionStorage 讀取
  	const savedChatData = JSON.parse(sessionStorage.getItem('chatData')) || [];

  	// 拼接預設訊息 + sessionStorage 的聊天紀錄
  	const chatData = ref([...defaultChatData, ...savedChatData]);

  	// 監聽 chatData 的變化，自動同步到 sessionStorage
  	watch(
    	chatData,
    	(newVal) => {
      	// 只存使用者與機器人的聊天訊息，不存重複的預設訊息
      	const userBotMessages = newVal.filter((item) => !item.isDefault)
      	sessionStorage.setItem('chatData', JSON.stringify(userBotMessages))
    	},
    	{ deep: true }
  	);

  	const addChatData = (newChatData) => {
    	chatData.value.push({ id: chatData.value.length + 1, isDefault: false, ...newChatData });
  	};

	const isAwaitingBotReply = ref(false);

  	const addQueryData = async (newChatData) => {

    	chatData.value.push({ id: chatData.value.length + 1, isDefault: false, ...newChatData });
		isAwaitingBotReply.value = true;
		try {
			const aiResponse = await askAgent(newChatData.content);
			if (aiResponse) {
				const actionResults = await executeUIActions(
					aiResponse.ui_actions,
					newChatData.content,
				);
				chatData.value.push({
					id: chatData.value.length + 1,
					role: 'bot',
					isDefault: false,
					content: aiResponse.reply || aiResponse.raw,
				});
				if (actionResults.length > 0) {
					chatData.value.push({
						id: chatData.value.length + 1,
						role: 'bot',
						isDefault: false,
						content: `已執行操作：\n- ${actionResults.join("\n- ")}`,
					});
				}
				saveChatLog(newChatData.content, {
					reply: aiResponse.reply || aiResponse.raw,
					ui_actions: aiResponse.ui_actions,
					action_results: actionResults,
				});
				return;
			}

			// Reset broken agent session before fallback flow.
			sessionStorage.removeItem("agentSessionId");
			await runVectorRecommendation(newChatData.content);
		} finally {
			isAwaitingBotReply.value = false;
		}
  	};

	const requestCurrentLocationForAI = async () => {
		if (!navigator?.geolocation) return;
		return new Promise((resolve) => {
			navigator.geolocation.getCurrentPosition(
				(position) => {
					mapStore.userLocation = {
						latitude: position.coords.latitude,
						longitude: position.coords.longitude,
					};
					resolve();
				},
				() => {
					mapStore.userLocation = { latitude: null, longitude: null };
					resolve();
				},
				{
					enableHighAccuracy: true,
					timeout: 10000,
					maximumAge: 0,
				}
			);
		});
	};

	const snapshotUserLocationForAgent = () => {
		const u = mapStore.userLocation;
		if (!u) return null;
		const { latitude: lat, longitude: lng } = u;
		if (typeof lat !== "number" || typeof lng !== "number") return null;
		if (Number.isNaN(lat) || Number.isNaN(lng)) return null;
		return { latitude: lat, longitude: lng };
	};

	const buildUIContext = () => ({
		current_path: authStore.currentPath,
		current_dashboard: {
			index: contentStore.currentDashboard?.index || "",
			name: contentStore.currentDashboard?.name || "",
			city: contentStore.currentDashboard?.city || "",
			mode: contentStore.currentDashboard?.mode || "",
			component_count: contentStore.currentDashboard?.components?.length || 0,
		},
		/** 使用者可見組件路由摘要（與 GET /ai/component-routing-manifest 一致；優先非 map-layers 儀表板） */
		component_routing_digest: routingManifestDigest.value,
		component_routing_digest_truncated:
			routingManifestDigest.value.length >= MAX_ROUTING_DIGEST,
		/**
		 * 地圖可開圖層小目錄（後端由 manifest 動態產生）：每筆 { dashboard_index, mapview_city, openable_layers: [{ component_index, name, component_city }] }。
		 * 意義：在 /mapview?index=dashboard_index&city=mapview_city 時，可對列管組件執行 open_map_layer（params 帶 component_index 與 city=component_city）。
		 */
		mapview_layer_catalog: agentMapviewLayerCatalog.value,
		mapview_layer_catalog_truncated: mapviewLayerCatalogTruncated.value,
		/** 左側欄位各城市儀表板；component_ids 為後端整數 id；若有逛過組件列表頁可能會多出 components_with_index */
		sidebar_by_city: buildSidebarCatalogForAgent(contentStore),
		/** 僅「目前這個儀表板」已載入的組件（index／name／id／city／has_map_layer）；換頁即變 */
		current_dashboard_components: buildCurrentDashboardComponentsForAgent(contentStore),
		/**
		 * 圖資 map-layers-* 頁面上已載入的主題圖層 index；僅反映「圖資」情境。
		 * 與 YouBike、務實交通等非圖資儀表板無對應關係，不可用它推斷 YouBike 該開在哪個儀表板。
		 */
		thematic_map_component_indexes_loaded: buildThematicLayerIndexesHint(
			contentStore,
		).slice(0, 24),
		map_context: {
			visible_layers: mapStore.currentVisibleLayers || [],
			user_location: snapshotUserLocationForAgent(),
		},
	});

	const askAgent = async (question) => {
		try {
			await ensureRoutingManifestDigest();
			// 每次送 AI 前更新 GPS，讓 ui_context.map_context.user_location 與後端 geo／反向地理一致；
			// 僅 YouBike 關鍵字才請定位會導致「我在哪」等題永遠拿不到座標。
			await requestCurrentLocationForAI();
			const uiContext = buildUIContext();
			const siteCat = buildCompactSiteComponentCatalog(
				routingManifestRawComponents.value,
				MAX_SITE_CATALOG_CHARS,
			);
			const siteCatalogBlock = siteCat.text
				? `【全站組件索引】使用者可見範圍內，每行格式為 component_index|city|dashboard_index（city 僅 taipei／metrotaipei；dashboard_index 為 navigate_dashboard／開地圖建議對齊之儀表板；同一組件若兩區皆有資料會各一行）。${siteCat.truncated ? `已截斷（列${siteCat.shownLines}/${siteCat.totalLines}筆）。` : `共${siteCat.totalLines}筆。`}
${siteCat.text}`
				: routingManifestRawComponents.value.length > 0
					? `【全站組件索引】無法產生縮寫列（請 resolve_navigation_target）。`
					: `【全站組件索引】尚未載入（請仍可用 resolve_navigation_target 依關鍵字查詢）。`;
			const storedSession = sessionStorage.getItem("agentSessionId");
			const response = await http.post("/ai/chat/twai", {
				session: storedSession || "",
				stream: false,
				ui_context: uiContext,
				messages: [
					{
						role: "system",
						content: `你是臺北城市儀表板 agent。
${siteCatalogBlock}

【核心】凡詢問統計、指標、趨勢、整理、比較、雙北／單一縣市資料者，皆視為資料題：必呼叫工具取得資料後再作答；禁止憑常識、臆測或僅列指標名搪塞。reply 只能依工具成功回傳之內容撰寫，須寫出 chart_preview／彙總內的具體數字、年份與單位；禁止只複製 short_desc、禁止「某某包括…等」式空泛總覽而無實際數值。若工具失敗、報錯或查無資料，reply 須誠實說明原因並請使用者換問法或稍後再試，禁止捏造數字。get_dashboard_component_summary 的 dashboard_index 必須來自 resolve_navigation_target（或 manifest／digest 中的真實 index 字串），絕對禁止填占位符、變數名或諸如「結果1／{結果1}」等非真實 index。回覆 JSON：{"reply":"…","ui_actions":[…]}；不需操作則 ui_actions=[]。city 僅 taipei／metrotaipei。
【UI】僅用 navigate_dashboard、open_component_info、switch_city、open_map_layer。除下列「主題同步」外：使用者明確說「前往／打開／切到／看哪一頁」才需 navigate／open_map_layer。
【主題同步畫面】當使用者以領域／主題／某類統計發問或延續該主題（例如環保統計、環境指標、交通景況、長照／社福），且 resolve_navigation_target（建議 kind 含 dashboard）或 sidebar／digest 可對應到**單一明確**的 dashboard_index 時，應在 ui_actions 加入 navigate_dashboard：params.index 為該 dashboard_index，params.city 為該板適用之 taipei／metrotaipei（工具結果 sidebar_source／PickNavigateCity 或 match 列之 city；双北未指定時依該板資料慣用範圍，勿臆造）。使回覆的資料與畫面上的儀表板一致。若無法對應單一儀表板、或使用者明示只要口頭／文字答案不要換頁，則 ui_actions 可為 []。
【工具選擇】名稱／index 不明→resolve_navigation_target。單組件→get_component_facts；整板／綜合→get_dashboard_component_summary（max_components≤10，可 component_indexes）。get_current_ui_context：僅在使用者明示「目前畫面／這頁／地圖視窗／我在哪／定位／已開圖層」等與介面狀態相關時才可呼叫；一般統計、分析、延續上一則主題的追問（例如「給我實際數字」）一律禁止呼叫，應延續對話主題並用 facts／summary 取數。digest／catalog 若 truncated 則其餘用 resolve。
【ui_context 欄位（精簡版）】component_routing_digest：組件×city＋has_map_layer＋preferred_dashboard＋geo_nearby_*。mapview_layer_catalog：可 open_map_layer 的板×層。sidebar_by_city：index／name／n_components／components_with_index（無完整 id 清單）。current_dashboard_components：畫面上組件。current_dashboard.city：僅儀表板資料範圍（taipei／metrotaipei），不可當成使用者實際所在縣市。map_context.user_location：GPS。map_context.reverse_geocode：服務端以國土測繪中心村里界查詢附帶之縣市區村里（admin_line 為一行簡述）；「我在哪」類問題必優先照抄 reverse_geocode，禁只用 current_dashboard.city 推測。visible_layers：已開層。thematic_map_*：僅圖資頁，勿當一般導覽唯一依據。
【雙北】未指定單一城市且組件兩區皆有資料時，分次 get_component_facts（taipei、metrotaipei）；數字旁必標來源口徑，禁混談。
【綜合分析題】禁套話；摘錄工具數字後須另段「解讀」：指標對照、（若有）年度趨勢、（若有）空間差異；解讀篇幅須大於摘錄。「附近」須延續話題組件，禁無理由改 YouBike。
【地圖】要看地圖／圖層→mode="mapview"＋open_map_layer（對齊 digest 之板與 city）。
【綠色商家地圖】凡出現「綠色商家／綠色商店／綠商店＋地圖或圖層」：最後一則回覆**必須**是合法 JSON，且 ui_actions 至少含 open_map_layer（component_index=green_stores、city 依台北／新北或 digest）；禁止只回附近幾家、距離等純文字而無 JSON。可搭配 navigate_dashboard（index=environment_dashboard 等 digest 之板、mode=mapview）以確保地圖頁能開圖層。工具 open_map_layer 若曾呼叫，僅為取得提示，仍須把 open_map_layer 寫入最終 JSON 的 ui_actions。
【地理鄰近】get_geo_nearby_for_component：僅 geo_nearby_supported；永遠是「單一錨點＋半徑」的點位數，不是「某行政區／全臺共有幾家」—後者用 get_component_facts（圖表彙總）並在 reply 拆兩種口徑。location_anchor：預設 user_device—以瀏覽器 GPS 覆寫經緯度（我附近／這裡）。使用者指明地名、景點、地址時設 explicit：先 resolve_coordinates_zh，再同一輪 geo＋explicit（可並行；座標 0 時後端自 resolve 注入）。無 GPS 且非 explicit 時 error=no_gps。map_geojson 已合併雙北；必讀 interpretation_hint_zh、radius_semantics（半徑內筆數 vs nearest 可能越界）。僅問數量時 ui_actions=[]。
【YouBike 附近】工具回傳若含 query_location_description：先看頂層 location_anchor。user_device（裝置 GPS）→ 首段才可描述「您／使用者推定所在」（admin_line）。explicit（使用者問某地名／地址）→ admin_line 僅表示該詢問地點錨點所在行政區，首段須用「詢問地點位於…／該一带…」，禁止使用「您位於」「您人在」。再列站點數、最近站與車輛／車位。
【自行車道 vs YouBike】車道圖資≠YouBike 站位。
【導覽歧義】resolve_navigation_target 或 ui_actions 帶名稱＋city；細節仍靠 get_component_facts／summary。`,
					},
					{
						role: "user",
						content: question,
					},
				],
				tools: AGENT_TOOLS,
				tool_choice: "auto",
			});

			if (response.data?.data?.session) {
				sessionStorage.setItem("agentSessionId", response.data.data.session);
			}
			return parseAgentPayload(response.data?.data?.content || "");
		} catch (error) {
			console.error("AgentChatError :", error);
			return null;
		}
	};

	const parseAgentPayload = (rawContent) => {
		if (!rawContent) return null;
		const content = rawContent
			.trim()
			.replace(/^```json\s*/i, "")
			.replace(/```$/, "")
			.trim();
		try {
			const parsed = JSON.parse(content);
			return {
				reply: parsed.reply || "",
				ui_actions: normalizeUIActions(parsed.ui_actions),
				raw: rawContent,
			};
		} catch (error) {
			return {
				reply: rawContent,
				ui_actions: [],
				raw: rawContent,
			};
		}
	};

	const executeUIActions = async (actions, userQuestion = "") => {
		if (!Array.isArray(actions) || actions.length === 0) return [];

		let normalizedActions = ensureMapVisualizationUiActions(
			actions,
			userQuestion,
			routingManifestDigest.value,
		);
		normalizedActions = rewriteNavigateForMapLayerBatch(
			normalizedActions,
			routingManifestDigest.value,
		);

		const openMapLayerFromParams = async (params = {}) => {
			const requestedCity = params.city || contentStore.currentDashboard?.city || "taipei";
			const requestedComponent =
				params.component_index ||
				params.index ||
				params.component ||
				params.layer ||
				"";
			const normalizedComponent =
				COMPONENT_ALIAS_MAP[normalizeText(requestedComponent)] || requestedComponent;
			if (!normalizedComponent) {
				return "open_map_layer 失敗：缺少 component_index";
			}

			let routingEntry = findRoutingDigestEntry(
				routingManifestDigest.value,
				normalizedComponent,
				requestedCity,
			);
			if (
				!routingEntry?.preferred_dashboard_index &&
				normalizeText(normalizedComponent) === "youbike_availability"
			) {
				routingEntry = UBIKE_MAP_LAYER_FALLBACK_ENTRY;
			}
			let prefixMsg = "";
			if (needsPreferredMapviewFirst(contentStore, routingEntry)) {
				const navHint = await ensurePreferredMapviewDashboard(
					contentStore,
					routingEntry,
				);
				if (navHint) prefixMsg = `${navHint}。`;
			}

			const componentWaitDeadline = Date.now() + 16000;
			let targetComponent = null;
			while (Date.now() < componentWaitDeadline) {
				const currentComponents = Array.isArray(contentStore.currentDashboard?.components)
					? contentStore.currentDashboard.components
					: [];
				targetComponent = currentComponents.find(
					(item) =>
						normalizeText(item?.index) === normalizeText(normalizedComponent) &&
						(!requestedCity || normalizeText(item?.city) === normalizeText(requestedCity))
				);
				if (targetComponent?.map_config?.length) break;
				targetComponent = pickComponentWithMapLayer(
					mergeMapLayerCatalog(contentStore),
					normalizedComponent,
					requestedCity,
				);
				if (targetComponent?.map_config?.length) break;
				await sleep(200);
			}

			if (!targetComponent?.map_config?.length) {
				targetComponent = pickComponentWithMapLayer(
					mergeMapLayerCatalog(contentStore),
					normalizedComponent,
					"",
				);
			}
			if (!targetComponent?.map_config?.length) {
				targetComponent = await fetchComponentWithMapLayerByIndex(
					normalizedComponent,
					requestedCity,
				);
			}

			if (!targetComponent?.map_config?.length) {
				return `${prefixMsg}open_map_layer 失敗：找不到可開啟圖層的組件 (${normalizedComponent})`;
			}

			const mapWaitDeadline = Date.now() + 10000;
			while (Date.now() < mapWaitDeadline) {
				if (mapStore.map?.loaded()) break;
				await sleep(200);
			}
			if (!mapStore.map?.loaded()) {
				return "open_map_layer 失敗：地圖尚未完成載入";
			}

			mapStore.addToMapLayerList(targetComponent.map_config);
			lastMapLayerAction.value = {
				componentIndex: normalizedComponent,
				city: targetComponent.city || requestedCity,
				timestamp: Date.now(),
			};
			return `${prefixMsg}已開啟地圖圖層 ${normalizedComponent} (${targetComponent.city || requestedCity})`;
		};

		const results = [];
		for (const action of normalizedActions) {
			if (!action?.type || !ALLOWED_UI_ACTIONS[action.type]) {
				results.push(`忽略未授權操作：${action?.type || "unknown"}`);
				continue;
			}

			try {
				if (action.type === "navigate_dashboard") {
					const requestedIndex =
						action.params?.index ||
						action.params?.dashboard_index ||
						action.params?.dashboard ||
						"";
					const requestedCity = action.params?.city || contentStore.currentDashboard?.city || "taipei";
					const resolvedTarget = resolveDashboardTarget(
						contentStore.dashboards,
						requestedIndex,
						requestedCity
					);
					let index = resolvedTarget?.index || "";
					let city = resolvedTarget?.city || requestedCity;
					const mode = action.params?.mode === "mapview" ? "mapview" : "dashboard";
					const requestedComponent =
						action.params?.component_index ||
						action.params?.map_layer_component_index ||
						"";
					if (!index) {
						results.push(`navigate_dashboard 失敗：找不到對應儀表板 (${requestedIndex || "unknown"})`);
						continue;
					}
					await router.push({ path: `/${mode}`, query: { index, city } });
					results.push(`已切換到 ${mode}，儀表板 ${index} (${city})`);
					if (mode === "mapview" && requestedComponent) {
						const mapLayerResult = await openMapLayerFromParams({
							component_index: requestedComponent,
							city,
						});
						results.push(mapLayerResult);
					}
					continue;
				}

				if (action.type === "open_component_info") {
					let componentIndex = action.params?.component_index || action.params?.index;
					if (!componentIndex && action.params?.component_id && Array.isArray(contentStore.currentDashboard?.components)) {
						const matched = contentStore.currentDashboard.components.find(
							(item) => String(item?.id) === String(action.params.component_id)
						);
						componentIndex = matched?.index;
					}
					const city = action.params?.city || contentStore.currentDashboard?.city || "taipei";
					if (!componentIndex) {
						results.push("open_component_info 失敗：缺少 component_index");
						continue;
					}
					await router.push({ path: `/component/${componentIndex}`, query: { city } });
					results.push(`已開啟組件 ${componentIndex} (${city})`);
					continue;
				}

				if (action.type === "switch_city") {
					const city = action.params?.city;
					const index = action.params?.index || contentStore.currentDashboard?.index;
					const mode = contentStore.currentDashboard?.mode?.includes("mapview") ? "mapview" : "dashboard";
					if (!city || !index) {
						results.push("switch_city 失敗：缺少 city 或 index");
						continue;
					}
					await router.push({ path: `/${mode}`, query: { index, city } });
					results.push(`已切換城市到 ${city}`);
					continue;
				}

				if (action.type === "open_map_layer") {
					const mapLayerResult = await openMapLayerFromParams(action.params || {});
					results.push(mapLayerResult);
				}
			} catch (error) {
				results.push(`${action.type} 執行失敗：${error?.message || "unknown error"}`);
			}
		}

		return results;
	};

	const runVectorRecommendation = async (question) => {
		recommendComponents.value = [];
		let topK = null;

		try {
			const response = await http.post(
				"/vector/component",
				new URLSearchParams({
					query: question,
					limit: 10,
					score: 0.8,
				}),
				{
					headers: {
						"Content-Type": "application/x-www-form-urlencoded",
					},
				}
			);
			if (response.data?.data?.length > 0) {
				recommendComponents.value = response.data.data;
			}

			const result = Array.from(
				recommendComponents.value.reduce((map, item) => {
					const key = item.index
					const exist = map.get(key)
					if (!exist) {
						map.set(key, item)
						return map
					}
					if (item.city === 'metrotaipei') {
						map.set(key, item)
					}
					return map
				}, new Map()).values()
			)
			recommendComponents.value = result
		} catch (error) {
			console.error("VectorAnalysisError :", error);
		}

		if (recommendComponents.value && recommendComponents.value?.length > 0) {
			topK = [...recommendComponents.value].sort((a, b) => b.score - a.score);
			chatData.value.push({ id: chatData.value.length + 1, role: 'bot', isDefault: false, button: [{ id:1, text:'建立儀表板' }], content: `您好 😊 \n 以下是根據您的問題，自動為您推薦的「組件清單」。您可以將這些組件整批加入「個人儀表板」，方便日後快速查看與使用。\n`, relations: topK });
			chatData.value.push({ id: chatData.value.length + 1, role: 'bot', isDefault: false, content: `若您有任何新的查詢或想深入探索的內容，都可以隨時在對話框告訴我～\n 我很樂意再協助您 💬✨` });
		} else {
			chatData.value.push({ id: chatData.value.length + 1, role: 'bot', isDefault: false, content: `很抱歉，您提供的描述沒有相似組件，請繼續提問 ! ` });
		}

		saveChatLog(question, recommendComponents.value);
	};

	const saveChatLog = async(question, answer) => {
		try {
        	const formData = new FormData();
        	const d = new Date();
        	const todayId =
          		d.getFullYear() +
          		String(d.getMonth() + 1).padStart(2, "0") +
          		String(d.getDate()).padStart(2, "0");

        	formData.append("session", "session_" + todayId);
        	formData.append("question", question);
        	formData.append("answer", JSON.stringify(answer));

        	await http.post("/chatlog/", formData, {
          		headers: {
            		"Content-Type": "multipart/form-data",
          		},
        	});
      	} catch (error) {
        	console.error("saveChatLog error:", error);
      	}
	};

	return { chatData, isAwaitingBotReply, addChatData, addQueryData, saveChatLog, lastMapLayerAction }
})
