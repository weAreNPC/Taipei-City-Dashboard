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
			description: "Get nearby YouBike stations summary by latitude and longitude.",
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
			description: "Summarize multiple components in one dashboard.",
			parameters: {
				type: "object",
				properties: {
					dashboard_index: { type: "string", description: "Dashboard index" },
					city: { type: "string", description: "taipei or metrotaipei" },
					max_components: { type: "integer", description: "Max components in summary, up to 10" },
				},
				required: ["dashboard_index"],
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
const isUbikeKeyword = (value) => {
	const text = normalizeText(value);
	if (!text) return false;
	return (
		text.includes("ubike") ||
		text.includes("youbike") ||
		text.includes("自行車") ||
		text.includes("單車") ||
		text.includes("bike")
	);
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

export const useChatStore = defineStore('chat', () => {
	const authStore = useAuthStore();
	const contentStore = useContentStore();
	const mapStore = useMapStore();

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
			const actionResults = await executeUIActions(aiResponse.ui_actions);
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
				() => resolve(),
				{
					enableHighAccuracy: true,
					timeout: 10000,
					maximumAge: 60000,
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
		map_context: {
			visible_layers: mapStore.currentVisibleLayers || [],
			user_location: mapStore.userLocation || null,
		},
	});

	const askAgent = async (question) => {
		try {
			const uiContext = buildUIContext();
			const storedSession = sessionStorage.getItem("agentSessionId");
			const response = await http.post("/ai/chat/twai", {
				session: storedSession || "",
				stream: false,
				messages: [
					{
						role: "system",
						content: `你是臺北城市儀表板 agent。請優先使用工具回覆資料型問題，引用工具結果，不要臆測。
你可建議 UI 操作，但只能使用以下 action type：navigate_dashboard、open_component_info、switch_city、open_map_layer。
若使用者的意圖明確是「前往/切換/打開某個儀表板」，必須回傳 navigate_dashboard，不可省略 ui_actions。
請以 JSON 回覆，格式必須是：{"reply":"文字回覆","ui_actions":[{"type":"action_type","params":{...}}]}。
若不需要操作，ui_actions 請回傳空陣列。
工具參數 city 僅可使用小寫：taipei 或 metrotaipei。
若使用者詢問 ubike/YouBike 使用情況且需要附近站點或可借數量，若 map_context.user_location 有座標，優先呼叫工具 get_nearby_ubike_summary，不要憑空估計數字；若缺少座標請明確請使用者提供定位授權。
以下是目前前端介面狀態：${JSON.stringify(uiContext)}`,
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

	const executeUIActions = async (actions) => {
		if (!Array.isArray(actions) || actions.length === 0) return [];

		const openMapLayerFromParams = async (params = {}) => {
			const requestedCity = params.city || contentStore.currentDashboard?.city || "taipei";
			const requestedComponent =
				params.component_index || params.index || params.component || "";
			const normalizedComponent =
				COMPONENT_ALIAS_MAP[normalizeText(requestedComponent)] || requestedComponent;
			if (!normalizedComponent) {
				return "open_map_layer 失敗：缺少 component_index";
			}

			const componentWaitDeadline = Date.now() + 10000;
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
				await sleep(200);
			}

			if (!targetComponent?.map_config?.length) {
				return `open_map_layer 失敗：找不到可開啟圖層的組件 (${normalizedComponent})`;
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
			return `已開啟地圖圖層 ${normalizedComponent} (${targetComponent.city || requestedCity})`;
		};

		const results = [];
		for (const action of actions) {
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
