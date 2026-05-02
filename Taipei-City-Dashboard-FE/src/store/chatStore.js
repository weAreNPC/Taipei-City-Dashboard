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
				"Returns the user's current frontend UI snapshot as JSON: route, current dashboard index/name/city/mode, components on screen, sidebar hints, mapview_layer_catalog, component_routing_digest, visible map layers, optional user_location. Call ONLY when the answer depends on where the user is now or what they see (e.g. 'this page', 'here', 'what layer is on', current map). Skip for generic facts that resolve_navigation_target or get_component_facts alone can answer.",
			parameters: {
				type: "object",
				properties: {},
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
			name: "get_nearby_ubike_summary",
			description:
				"Get nearby YouBike stations by latitude/longitude. When get_current_ui_context is used in the same turn, you MUST use map_context.user_location from that tool result for latitude/longitude (do not guess or use landmark defaults). Call get_current_ui_context first in a separate tool round if needed; do not invent coordinates.",
			parameters: {
				type: "object",
				properties: {
					latitude: { type: "number", description: "User latitude in WGS84" },
					longitude: { type: "number", description: "User longitude in WGS84" },
					radius_meters: { type: "integer", description: "Search radius in meters, default 500" },
					top_n: { type: "integer", description: "How many nearest stations to return, default 5" },
				},
				required: ["latitude", "longitude"],
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
				"Look up component_index / dashboard_index from Chinese or English name fragments and optional city (taipei/metrotaipei). Same visibility as user sidebar. Use when unsure of exact index.",
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
		candidates = actions;
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
			const type = action.type;
			if (!type) return null;
			if (action.params && typeof action.params === "object") {
				return { type, params: action.params };
			}
			// Compatibility: some LLM replies place params at top level.
			const { type: _type, ...rest } = action;
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

/** 與後端 GetComponentRoutingManifest 對齊之精簡表，供 prompt 與執行 ui_actions 前修正導航。 */
const MAX_ROUTING_DIGEST = 500;

const buildRoutingDigestFromAPIComponents = (components) => {
	if (!Array.isArray(components)) return [];
	const out = [];
	for (const e of components) {
		const pl = e.placements || [];
		if (!pl.length) continue;
		const preferred =
			pl.find(
				(p) =>
					p?.dashboard_index &&
					!normalizeText(p.dashboard_index).includes("map-layers"),
			) || pl[0];
		if (!preferred?.dashboard_index) continue;
		out.push({
			component_index: e.component_index,
			name: e.name,
			city: e.city,
			has_map_layer: !!e.has_map_layer,
			preferred_dashboard_index: preferred.dashboard_index,
			preferred_navigate_city: e.city,
			placement_dashboard_indexes: pl.map((p) => p.dashboard_index).filter(Boolean),
		});
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

const enrichComponentIdsForAgent = (componentIds, lookup, maxItems = 80) => {
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

/** 側欄：各城市儀表板清單（component_ids 為後端整數；若有 components_with_index 則已對應 index／city） */
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
				component_ids,
			};
			const enriched = enrichComponentIdsForAgent(component_ids, lookup);
			if (enriched) row.components_with_index = enriched;
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
	/** 與 manifest 一併下發：各 mapview 儀表板×city 下可 open_map_layer 的組件表 */
	const agentMapviewLayerCatalog = ref([]);
	const mapviewLayerCatalogTruncated = ref(false);
	const routingManifestFetched = ref(false);

	const ensureRoutingManifestDigest = async () => {
		if (routingManifestFetched.value) return;
		try {
			const res = await http.get("/ai/component-routing-manifest");
			const data = res.data?.data;
			routingManifestDigest.value = buildRoutingDigestFromAPIComponents(
				data?.components,
			);
			agentMapviewLayerCatalog.value = Array.isArray(data?.mapview_layer_catalog)
				? data.mapview_layer_catalog
				: [];
			mapviewLayerCatalogTruncated.value = !!data?.mapview_layer_catalog_truncated;
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

  	const addQueryData = async (newChatData) => {

    	chatData.value.push({ id: chatData.value.length + 1, isDefault: false, ...newChatData });
		if (isUbikeKeyword(newChatData.content)) {
			await requestCurrentLocationForAI();
		}
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
		),
		map_context: {
			visible_layers: mapStore.currentVisibleLayers || [],
			user_location: mapStore.userLocation || null,
		},
	});

	const askAgent = async (question) => {
		try {
			await ensureRoutingManifestDigest();
			const uiContext = buildUIContext();
			const storedSession = sessionStorage.getItem("agentSessionId");
			const response = await http.post("/ai/chat/twai", {
				session: storedSession || "",
				stream: false,
				ui_context: uiContext,
				messages: [
					{
						role: "system",
						content: `你是臺北城市儀表板 agent。請優先使用工具回覆資料型問題，引用工具結果，不要臆測。
你可建議 UI 操作，但只能使用以下 action type：navigate_dashboard、open_component_info、switch_city、open_map_layer。
若使用者明確要「前往/切換/打開某個儀表板／地圖頁」，必須回傳對應 navigate_dashboard（或搭配 open_map_layer）；僅詢問統計或文字說明時不得為滿足此規則而強制導覽。
請以 JSON 回覆，格式必須是：{"reply":"文字回覆","ui_actions":[{"type":"action_type","params":{...}}]}。
若不需要操作，ui_actions 請回傳空陣列。
工具參數 city 僅可使用小寫：taipei 或 metrotaipei。
【介面快照】路由、目前儀表板、側欄、地圖可見層、定位、digest、catalog 等完整 JSON 須透過工具 get_current_ui_context（無參數）取得；與 GET /ai/component-routing-manifest 內 mapview_layer_catalog_note 之語意一致。僅在問題依賴「目前頁／畫面上有什麼／已開圖層／定位」時呼叫；純名稱／index 不確定時優先 resolve_navigation_target。工具若回 error（未附 ui_context）依錯誤提示處理。
【get_current_ui_context 欄位速覽（mapview_layer_catalog_truncated 或 component_routing_digest_truncated 為 true 時，未列項目一律改 resolve_navigation_target）】
• mapview_layer_catalog：每筆 dashboard_index + mapview_city = 一個 mapview URL 情境；openable_layers 為該板側欄可開之圖層（component_index、中文 name、open_map_layer 之 component_city）。開層前 navigate_dashboard 須對齊該組 index、city、mode=mapview。
• component_routing_digest：側欄可見組件彙總（component_index、city、has_map_layer、preferred_dashboard_index 開圖層建議板且已避開 map-layers-*、placement_dashboard_indexes）。
• current_dashboard_components：僅「目前畫面」儀表板已載入組件之 index／name／city／has_map_layer（換頁即變）。
• sidebar_by_city：儀表板 index／name／component_ids；有 components_with_index 才有 component_index。
• thematic_map_component_indexes_loaded：僅 map-layers-* 圖資頁主題層；不可替代 digest 決定一般組件應開在哪個板，勿僅因在此列表就 navigate 到 map-layers。
• map_context.visible_layers／user_location：目前地圖已開層與定位（YouBike 附近站點見下）。
【說明／資訊類問題】問「資訊／說明／有哪些／統計／分布」等除非確定無資料，須先工具查詢再在 reply 摘要重點；禁空話導覽。建議：resolve_navigation_target → get_component_facts 或 get_dashboard_component_summary；無結果時 reply 明說並建議換關鍵字。僅在使用者明確「帶我去／打開／切換」時才填 navigate_dashboard／open_map_layer。
【區域／城市】使用者未明確指定僅「臺北市」或僅「雙北／北北基／新北」等範圍時，若該組件經工具確認同時存在 taipei 與 metrotaipei 資料，應依規定分次呼叫 get_component_facts（city 先後為 taipei、metrotaipei），並在 reply 「並列」兩區重點；每一組數字、年份區間或趨勢都須緊鄰標示來自「臺北市（taipei）」或「雙北—臺北市與新北市合計／行政區劃範圍依平台定義（metrotaipei）」，禁止混在同一句而不標區域，亦禁止只引用單一 city 卻未說明另一區是否存在資料。使用者已明確只要其中一區時，僅摘要該區並開頭標示區域名稱即可。
【綜合分析／多組件】使用者若要求「結合／整合／統整／意味著什麼／有什麼關聯／一起解讀／用實際數據回答」，或點名整板儀表板（例：長照關懷所有資訊、這一頁所有組件），必須以工具結果中的數字作答，不可只用組件 use_case／short_desc 套話或介紹儀表板功能代答。
(1) 儀表板與範圍：優先 get_current_ui_context 取得 current_dashboard.index 與 city；必要時 resolve_navigation_target kind=dashboard。
(2) 資料一次拉齊：優先 get_dashboard_component_summary，帶齊 dashboard_index、city，max_components 設 10（或該板實際組件數）；使用者若只點名部分組件，傳 component_indexes（英文 component_index 陣列）篩選。同一題若需臺北與雙北並列，依【區域／城市】分兩次呼叫（不同 city），再綜合。
(3) reply 結構（使用者問「分析／意味著／帶給我們什麼訊息」時為強制）：①「數據摘錄」—依組件逐段列出工具 JSON 可核對的數值，須標組件名、區域，有時間序列則標年份（不可把不同組件、不同年份的數字混在一起卻不註明）；②「綜合解讀」—須另起一段或多段，**不得**只用換句話重述①的數字當作分析；必須明確回答「這些指標一起看，傳達了什麼訊息」，且內容只能由①已出現的數據推論，並至少包含：**(a)** 兩項以上指標的**對照**（例如扶養負擔與老化程度是否同向、與就業年齡結構變化是否一致或形成張力）；**(b)** 若有多個年度，簡述**趨勢**與對長照／勞動力寓意的白話涵義；**(c)** 若有行政區／分區統計，簡述**空間差異**代表什麼（何區幼年或高齡人口相對突出、對資源配置可能的啟示）。篇幅上「綜合解讀」應明顯多於單純摘錄句。③ 嚴禁離題：未問交通／定位時，不得用 YouBike、自行車道、或「系統會提供哪些服務」等填充分析。
【只要資訊 vs 要開地圖】僅要數據／說明時 ui_actions 可 []。使用者要求看地圖／圖層／地圖模式時：navigate_dashboard.params.mode 必為字串 "mapview"（省略則成一般儀表板、非全幅地圖頁），並 open_map_layer（或 navigate 同帶 map_layer_component_index）；通常先對齊正確儀表板 mapview 再開層。
【YouBike】附近站點／可借數：一律先 get_current_ui_context，再以回傳之 map_context.user_location 經緯度呼叫 get_nearby_ubike_summary（禁止並行、禁止臆測座標或套用景點預設點）；無定位則請使用者開定位，勿捏造距離。僅回答「資訊／附近／有多少」時 ui_actions 必為 []，不得 navigate_dashboard／open_map_layer。若使用者明確要看地圖／圖層／在地圖上找站點，才可 navigate practical_transportation_newtpe + metrotaipei + mode=mapview，並 open_map_layer youbike_availability；文字回覆仍勿導向「圖資」或 map-layers-taipei／map-layers-metrotaipei。
【自行車道／路網】為車道／路線主題，非 YouBike 站位；用 resolve_navigation_target 找 component_index，勿與 youbike_availability 混淆。
【導覽】不必背 index：(1) resolve_navigation_target；(2) 或 ui_actions params 給 component_name／name／dashboard_name + city，後端會比對側欄補齊 index／component_index。仍應盡量給正確 taipei／metrotaipei。資料細節用 get_component_facts、get_dashboard_component_summary；介面細節按需 get_current_ui_context。`,
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

	return { chatData, addChatData, addQueryData, saveChatLog, lastMapLayerAction }
})
