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
};

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

		await runVectorRecommendation(newChatData.content);
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
你可建議 UI 操作，但只能使用以下 action type：navigate_dashboard、open_component_info、switch_city。
請以 JSON 回覆，格式必須是：{"reply":"文字回覆","ui_actions":[{"type":"action_type","params":{...}}]}。
若不需要操作，ui_actions 請回傳空陣列。
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
				ui_actions: Array.isArray(parsed.ui_actions) ? parsed.ui_actions : [],
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

		const results = [];
		for (const action of actions) {
			if (!action?.type || !ALLOWED_UI_ACTIONS[action.type]) {
				results.push(`忽略未授權操作：${action?.type || "unknown"}`);
				continue;
			}

			try {
				if (action.type === "navigate_dashboard") {
					const index = action.params?.index || contentStore.currentDashboard?.index;
					const city = action.params?.city || contentStore.currentDashboard?.city || "taipei";
					const mode = action.params?.mode === "mapview" ? "mapview" : "dashboard";
					if (!index) {
						results.push("navigate_dashboard 失敗：缺少 index");
						continue;
					}
					await router.push({ path: `/${mode}`, query: { index, city } });
					results.push(`已切換到 ${mode}，儀表板 ${index} (${city})`);
					continue;
				}

				if (action.type === "open_component_info") {
					const componentIndex = action.params?.component_index;
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

	return { chatData, addChatData, addQueryData, saveChatLog }
})
