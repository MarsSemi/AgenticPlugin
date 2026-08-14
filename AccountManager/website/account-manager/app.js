const accountList = document.getElementById("accountList");
const accountSummary = document.getElementById("accountSummary");
const groupSummary = document.getElementById("groupSummary");
const runtimeStatus = document.getElementById("runtimeStatus");
const selectedStatus = document.getElementById("selectedStatus");
const enabledSwitchLabel = document.getElementById("enabledSwitchLabel");
const permissionList = document.getElementById("permissionList");
const accountForm = document.getElementById("accountForm");
const saveAccountBtn = document.getElementById("saveAccountBtn");
const groupList = document.getElementById("groupList");
const groupForm = document.getElementById("groupForm");
const groupEnabledLabel = document.getElementById("groupEnabledLabel");
const permissionSettingsDialog = document.getElementById("permissionSettingsDialog");
const permissionSettingsTitle = document.getElementById("permissionSettingsTitle");
const permissionSettingsSummary = document.getElementById("permissionSettingsSummary");
const permissionSettingsList = document.getElementById("permissionSettingsList");
const accountBoundaryDialog = document.getElementById("accountBoundaryDialog");
const accountBoundaryTitle = document.getElementById("accountBoundaryTitle");
const accountBoundarySummary = document.getElementById("accountBoundarySummary");
const accountBoundaryBody = document.getElementById("accountBoundaryBody");
const apiKeyList = document.getElementById("apiKeyList");
const apiKeyResult = document.getElementById("apiKeyResult");
const apiKeyValue = document.getElementById("apiKeyValue");
const issueApiKeyBtn = document.getElementById("issueApiKeyBtn");
const busyDialog = document.getElementById("busyDialog");
const busyDialogTitle = document.getElementById("busyDialogTitle");
const busyDialogMessage = document.getElementById("busyDialogMessage");
const mcpSettingsStatus = document.getElementById("mcpSettingsStatus");
const saveMCPSettingsBtn = document.getElementById("saveMCPSettingsBtn");
const mcpFields = {
    read: document.getElementById("mcpReadEnabled"),
    write: document.getElementById("mcpWriteEnabled"),
    delete: document.getElementById("mcpDeleteEnabled")
};
const mcpFieldLabels = {
    read: document.getElementById("mcpReadEnabledLabel"),
    write: document.getElementById("mcpWriteEnabledLabel"),
    delete: document.getElementById("mcpDeleteEnabledLabel")
};
const fields = {
    accountId: document.getElementById("accountId"),
    displayName: document.getElementById("displayName"),
    email: document.getElementById("email"),
    role: document.getElementById("role"),
    groupIds: document.getElementById("groupIds"),
    password: document.getElementById("password"),
    confirmPassword: document.getElementById("confirmPassword"),
    enabled: document.getElementById("enabled"),
    note: document.getElementById("note"),
    apiKeyName: document.getElementById("apiKeyName"),
    groupId: document.getElementById("groupId"),
    groupName: document.getElementById("groupName"),
    groupEnabled: document.getElementById("groupEnabled"),
    groupWorkspaceIds: document.getElementById("groupWorkspaceIds"),
    groupNote: document.getElementById("groupNote")
};

let accounts = [];
let groups = [];
let selectedId = "";
let selectedGroupId = "";
let permissions = [];
let activePermissionSettingsIndex = -1;
let activeBoundaryAccountID = "";
let mcpSettings = { read: false, write: false, delete: false };
let mcpSettingsLoaded = false;
const smartQaPluginId = "talk";
const legacySmartQaPluginId = "smart-qa";
const permissionTargetAliases = {
    [legacySmartQaPluginId]: smartQaPluginId
};
const smartQaPermissionSettings = {
    title: "智慧問答配置",
    description: "可依群組權限控制智慧問答功能入口。",
    items: [
        {
            key: "work_history",
            label: "工作紀錄",
            description: "顯示工作紀錄與工具紀錄入口。",
            default: true
        },
        {
            key: "export_plan",
            label: "匯出規劃",
            description: "顯示對話上方的匯出規劃按鈕。",
            default: false
        },
        {
            key: "quick_settings",
            label: "快速設定",
            description: "顯示右側快速設定入口。",
            default: false
        },
        {
            key: "skill_management",
            label: "SKILL 管理",
            description: "顯示右側 Skill 管理入口。",
            default: true
        },
        {
            key: "session_status",
            label: "SESSION 狀態",
            description: "顯示右側 Session 狀態入口。",
            default: false
        },
        {
            key: "system_settings",
            label: "系統設定",
            description: "顯示右側系統設定入口。",
            default: true
        },
        {
            key: "flow_debug",
            label: "流程偵錯",
            description: "顯示流程偵錯入口與偵錯面板。",
            default: false
        }
    ]
};
const builtInPermissionPlugins = [
    { id: "system-settings", name: "系統設定" },
    { id: smartQaPluginId, name: "智慧問答", permission_settings: smartQaPermissionSettings }
];
let pluginOptions = [{ id: "*", name: "全部卡片" }, ...builtInPermissionPlugins];
const defaultWorkspaceID = "default";
let workspaceOptions = [{ id: defaultWorkspaceID, name: "預設工作空間" }];
let creating = false;
let creatingGroup = false;
const accountApiDefaultTimeoutMs = 60000;
const apiKeyOperationTimeoutMs = 60000;

function authHeadersFromCookie() {
    const runtimeHeaders = typeof window.AgenticTalkAPI?.authHeaders === "function" ? window.AgenticTalkAPI.authHeaders() : {};
    if (runtimeHeaders && Object.keys(runtimeHeaders).length) {
        return runtimeHeaders;
    }
    const names = ["agentic_auth_token", "auth_token", "authToken", "token", "Authentication", "Authorization"];
    const cookies = String(document.cookie || "").split(";").map((part) => part.trim()).filter(Boolean);
    for (const name of names) {
        const found = cookies.find((cookie) => cookie.startsWith(`${encodeURIComponent(name)}=`) || cookie.startsWith(`${name}=`));
        if (!found) continue;
        const raw = found.slice(found.indexOf("=") + 1);
        let token = "";
        try {
            token = decodeURIComponent(raw).trim();
        } catch {
            token = raw.trim();
        }
        if (!token) continue;
        const value = token.toLowerCase().startsWith("bearer ") ? token : `Bearer ${token}`;
        return { Authentication: value, Authorization: value };
    }
    return {};
}

function requestTimeoutMs(options = {}) {
    const timeout = Number(options.timeoutMs || accountApiDefaultTimeoutMs);
    return Number.isFinite(timeout) && timeout > 0 ? timeout : accountApiDefaultTimeoutMs;
}

function isAbortLikeError(error) {
    const message = String(error?.message || error || "").toLowerCase();
    return error?.name === "AbortError"
        || message.includes("signal is aborted")
        || message.includes("the operation was aborted")
        || message.includes("abort");
}

function readableApiError(error, fallbackMessage = "外掛 API 呼叫失敗") {
    if (isAbortLikeError(error)) {
        return "外掛 API 等待逾時，請稍後重新整理";
    }
    const message = String(error?.message || error || "").trim();
    return message || fallbackMessage;
}

function showBusyDialog(title, message) {
    busyDialogTitle.textContent = title || "處理中";
    busyDialogMessage.textContent = message || "請稍候";
    if (!busyDialog.open) {
        busyDialog.showModal();
    }
}

function closeBusyDialog() {
    if (busyDialog.open) {
        busyDialog.close();
    }
}

function setAPIKeyControlsDisabled(disabled) {
    issueApiKeyBtn.disabled = disabled;
    apiKeyList.querySelectorAll("[data-delete-api-key]").forEach((button) => {
        button.disabled = disabled;
    });
}

function withApiTimeout(promise, options = {}) {
    const timeout = requestTimeoutMs(options);
    let timer = 0;
    const timeoutPromise = new Promise((_, reject) => {
        timer = window.setTimeout(() => reject(new Error(`外掛 API 等待逾時 (${Math.round(timeout / 1000)} 秒)`)), timeout);
    });
    return Promise.race([promise, timeoutPromise]).finally(() => window.clearTimeout(timer));
}

async function rawFetchJSON(url, options = {}) {
    const { timeoutMs, ...fetchOptions } = options;
    const headers = { ...authHeadersFromCookie(), ...(fetchOptions.headers || {}) };
    if (!(options.body instanceof FormData)) headers["Content-Type"] = "application/json";
    const controller = fetchOptions.signal ? null : new AbortController();
    const timeout = requestTimeoutMs({ timeoutMs });
    const timer = controller ? window.setTimeout(() => {
        const timeoutError = new Error(`外掛 API 等待逾時 (${Math.round(timeout / 1000)} 秒)`);
        try {
            controller.abort(timeoutError);
        } catch {
            controller.abort();
        }
    }, timeout) : 0;
    try {
        const response = await fetch(url, {
            credentials: "same-origin",
            ...fetchOptions,
            headers,
            signal: fetchOptions.signal || controller?.signal
        });
        const text = await response.text();
        let data = {};
        try {
            data = text ? JSON.parse(text) : {};
        } catch (error) {
            data = { success: false, error: text || error.message };
        }
        if (!response.ok && !data.error) data.error = `HTTP ${response.status}`;
        return data;
    } catch (error) {
        throw new Error(readableApiError(error, "外掛 API 呼叫失敗"));
    } finally {
        if (timer) window.clearTimeout(timer);
    }
}

function assertSuccessfulPayload(data, fallbackMessage) {
    if (data?.success === false || data?.error) {
        throw new Error(data?.error || fallbackMessage);
    }
    return data;
}

function firstSuccessfulJSON(attempts, fallbackMessage) {
    return new Promise((resolve, reject) => {
        if (!attempts.length) {
            reject(new Error(fallbackMessage));
            return;
        }
        let settled = false;
        let pending = attempts.length;
        let lastError = null;
        attempts.forEach((attempt) => {
            Promise.resolve()
                .then(attempt)
                .then((data) => {
                    if (settled) return;
                    const successfulData = assertSuccessfulPayload(data, fallbackMessage);
                    settled = true;
                    resolve(successfulData);
                })
                .catch((error) => {
                    lastError = new Error(readableApiError(error, fallbackMessage));
                    pending -= 1;
                    if (!settled && pending <= 0) {
                        reject(lastError || new Error(fallbackMessage));
                    }
                });
        });
    });
}

async function firstSuccessfulJSONSequential(attempts, fallbackMessage) {
    let lastError = null;
    for (const attempt of attempts) {
        try {
            return assertSuccessfulPayload(await attempt(), fallbackMessage);
        } catch (error) {
            lastError = new Error(readableApiError(error, fallbackMessage));
        }
    }
    throw lastError || new Error(fallbackMessage);
}

async function accountApi(path, options = {}) {
    const normalized = String(path || "").startsWith("/") ? String(path || "") : `/${path || ""}`;
    const request = {
        timeoutMs: accountApiDefaultTimeoutMs,
        ...options,
        headers: {
            ...authHeadersFromCookie(),
            ...(options.headers || {})
        }
    };
    return assertSuccessfulPayload(
        await rawFetchJSON(`/api/plugin/account-manager/api/account-manager${normalized}`, request),
        "外掛 API 呼叫失敗"
    );
}

async function mainApi(path, options = {}) {
    const normalized = String(path || "").startsWith("/") ? String(path || "") : `/${path || ""}`;
    const request = {
        timeoutMs: 5000,
        ...options,
        headers: {
            ...authHeadersFromCookie(),
            ...(options.headers || {})
        }
    };
    const attempts = [
        () => rawFetchJSON(normalized, request)
    ];
    if (typeof window.AgenticTalkAPI?.apiFetch === "function") {
        attempts.push(() => withApiTimeout(window.AgenticTalkAPI.apiFetch(normalized, request), request));
    }
    return firstSuccessfulJSON(attempts, "主系統 API 呼叫失敗");
}

async function accountPluginControlFallbackFetch(path, options = {}) {
    const normalized = String(path || "").startsWith("/") ? String(path || "") : `/${path || ""}`;
    const request = {
        timeoutMs: 6000,
        ...options,
        headers: {
            ...authHeadersFromCookie(),
            ...(options.headers || {})
        }
    };
    return rawFetchJSON(`/api/plugin/account-manager/_plugin${normalized}`, request);
}

async function accountPluginControlFetch(path, options = {}) {
    const normalized = String(path || "").startsWith("/") ? String(path || "") : `/${path || ""}`;
    const request = {
        timeoutMs: 6000,
        ...options,
        headers: {
            ...authHeadersFromCookie(),
            ...(options.headers || {})
        }
    };
    if (typeof window.AgenticTalkAPI?.fetchPluginControl === "function") {
        return withApiTimeout(window.AgenticTalkAPI.fetchPluginControl("account-manager", normalized, request), request);
    }
    return accountPluginControlFallbackFetch(normalized, request);
}

async function accountPluginRuntimeControlFetch(command, options = {}) {
    const normalizedCommand = String(command || "status").replace(/^\/+/, "") || "status";
    const request = {
        timeoutMs: Number(options.timeoutMs || 6000),
        ...options,
        headers: {
            ...authHeadersFromCookie(),
            ...(options.headers || {})
        }
    };
    const attempts = [
        () => accountPluginControlFetch(`/${normalizedCommand}`, request),
        () => accountPluginControlFallbackFetch(`/${normalizedCommand}`, request),
        () => rawFetchJSON(`/api/account-manager/plugin/${normalizedCommand}`, request)
    ];
    return firstSuccessfulJSON(attempts, "外掛狀態讀取失敗");
}

function setRuntime(status, text, icon) {
    runtimeStatus.dataset.fullLoadDone = status ? "true" : "false";
    runtimeStatus.className = `status-pill ${status || ""}`.trim();
    runtimeStatus.innerHTML = `<i class="fa-solid ${icon || "fa-circle"}"></i> ${escapeHtml(text)}`;
}

function setSelectedStatus(account) {
    if (!account) {
        selectedStatus.classList.toggle("warn", !creating);
        fields.enabled.disabled = !creating;
        enabledSwitchLabel.textContent = creating ? "啟用" : "未選取";
        return;
    }
    fields.enabled.disabled = false;
    fields.enabled.checked = account.enabled !== false;
    enabledSwitchLabel.textContent = fields.enabled.checked ? "啟用" : "停用";
}

function printResult(title, data) {
    console.debug(title, data);
}

function showSaveConfirmation(button) {
    if (!button) return;
    const originalHTML = button.dataset.originalHtml || button.innerHTML;
    button.dataset.originalHtml = originalHTML;
    button.classList.remove("is-confirmed");
    void button.offsetWidth;
    button.classList.add("is-confirmed");
    button.innerHTML = `<i class="fa-solid fa-circle-check"></i> 已儲存`;
    window.clearTimeout(button._confirmTimer);
    button._confirmTimer = window.setTimeout(() => {
        button.classList.remove("is-confirmed");
        button.innerHTML = originalHTML;
    }, 1300);
}

function activateTab(tabName) {
    document.querySelectorAll("[data-tab]").forEach((button) => {
        const active = button.dataset.tab === tabName;
        button.classList.toggle("active", active);
        button.setAttribute("aria-selected", active ? "true" : "false");
    });
    document.querySelectorAll("[data-tab-panel]").forEach((panel) => {
        panel.hidden = panel.dataset.tabPanel !== tabName;
    });
}

function activateMainTab(tabName) {
    const requested = String(tabName || "").trim();
    const name = ["accounts", "groups", "settings"].includes(requested) ? requested : "accounts";
    document.querySelectorAll("[data-main-tab]").forEach((button) => {
        const active = button.dataset.mainTab === name;
        button.classList.toggle("active", active);
        button.setAttribute("aria-selected", active ? "true" : "false");
    });
    document.querySelectorAll("[data-main-panel]").forEach((panel) => {
        panel.hidden = panel.dataset.mainPanel !== name;
    });
}

function activateGroupTab(tabName) {
    const name = tabName === "permissions" ? "permissions" : "profile";
    document.querySelectorAll("[data-group-tab]").forEach((button) => {
        const active = button.dataset.groupTab === name;
        button.classList.toggle("active", active);
        button.setAttribute("aria-selected", active ? "true" : "false");
    });
    document.querySelectorAll("[data-group-tab-panel]").forEach((panel) => {
        panel.hidden = panel.dataset.groupTabPanel !== name;
    });
}

function normalizeMCPSettings(value = {}) {
    const input = value && typeof value === "object" && !Array.isArray(value) ? value : {};
    return {
        read: input.read === true,
        write: input.write === true,
        delete: input.delete === true
    };
}

function setMCPSettingsControlsDisabled(disabled) {
    Object.values(mcpFields).forEach((input) => {
        input.disabled = Boolean(disabled);
    });
    saveMCPSettingsBtn.disabled = Boolean(disabled);
}

function setMCPSettingsStatus(state, text) {
    const normalizedState = ["ok", "error", "warn"].includes(state) ? state : "warn";
    const icon = normalizedState === "ok"
        ? "fa-circle-check"
        : normalizedState === "error"
            ? "fa-triangle-exclamation"
            : "fa-circle-pause";
    mcpSettingsStatus.className = `status-pill ${normalizedState}`;
    mcpSettingsStatus.innerHTML = `<i class="fa-solid ${icon}"></i> ${escapeHtml(text)}`;
}

function renderMCPSettings(value = mcpSettings, error = "") {
    mcpSettings = normalizeMCPSettings(value);
    Object.entries(mcpFields).forEach(([key, input]) => {
        input.checked = mcpSettings[key];
        mcpFieldLabels[key].textContent = input.checked ? "開啟" : "關閉";
    });
    mcpSettingsLoaded = !error;
    setMCPSettingsControlsDisabled(Boolean(error));
    if (error) {
        setMCPSettingsStatus("error", error);
        return;
    }
    const enabledCount = Object.values(mcpSettings).filter(Boolean).length;
    setMCPSettingsStatus(enabledCount > 0 ? "ok" : "warn", enabledCount > 0 ? `已開啟 ${enabledCount} 項` : "全部關閉");
}

function readMCPSettings() {
    return normalizeMCPSettings({
        read: mcpFields.read.checked,
        write: mcpFields.write.checked,
        delete: mcpFields.delete.checked
    });
}

function markMCPSettingsDirty() {
    Object.entries(mcpFields).forEach(([key, input]) => {
        mcpFieldLabels[key].textContent = input.checked ? "開啟" : "關閉";
    });
    if (mcpSettingsLoaded) {
        setMCPSettingsStatus("warn", "尚未儲存");
    }
}

async function loadMCPSettings(options = {}) {
    const showWait = options.showBusy !== false;
    if (showWait) {
        showBusyDialog("讀取設定中", "正在讀取 MCP 功能設定，請稍候。");
    }
    setMCPSettingsControlsDisabled(true);
    try {
        const data = await accountApi("/settings", { method: "GET", cache: "no-store" });
        renderMCPSettings(data?.settings?.mcp);
        printResult("GET /settings", data);
    } catch (error) {
        const message = readableApiError(error, "MCP 設定讀取失敗");
        renderMCPSettings({ read: false, write: false, delete: false }, message);
        printResult("GET /settings", { success: false, error: message });
    } finally {
        if (showWait) closeBusyDialog();
    }
}

async function saveMCPSettings() {
    const nextSettings = readMCPSettings();
    setMCPSettingsControlsDisabled(true);
    showBusyDialog("儲存設定中", "正在更新 MCP 功能設定，請稍候。");
    try {
        const data = await accountApi("/settings", {
            method: "PUT",
            body: JSON.stringify({ mcp: nextSettings })
        });
        renderMCPSettings(data?.settings?.mcp || nextSettings);
        showSaveConfirmation(saveMCPSettingsBtn);
        printResult("PUT /settings", data);
    } catch (error) {
        const message = readableApiError(error, "MCP 設定儲存失敗");
        setMCPSettingsControlsDisabled(false);
        setMCPSettingsStatus("error", message);
        printResult("PUT /settings", { success: false, error: message });
    } finally {
        closeBusyDialog();
    }
}

function renderAccounts() {
    const query = document.getElementById("accountSearch").value.trim().toLowerCase();
    const filtered = accounts.filter((account) => {
        const text = [account.id, account.username, account.display_name, account.email, account.role, account.note].join(" ").toLowerCase();
        return !query || text.includes(query);
    });
    accountSummary.textContent = `${accounts.length} 個帳號`;
    if (!filtered.length) {
        accountList.innerHTML = `<div class="empty">沒有符合條件的帳號</div>`;
        return;
    }
    accountList.innerHTML = filtered.map((account) => `
        <div class="account-list-row ${account.id === selectedId ? "active" : ""}">
            <button class="account-item ${account.id === selectedId ? "active" : ""}" type="button" data-account-id="${escapeHtml(account.id)}">
                <span class="account-main">
                    <span class="account-name">${escapeHtml(account.display_name || account.username)}</span>
                    <span class="account-meta">${escapeHtml(account.username)} · ${escapeHtml(account.role || "user")} · ${account.enabled ? "啟用" : "停用"}</span>
                </span>
            </button>
            <button class="account-boundary-button" type="button" data-account-boundary-id="${escapeHtml(account.id)}" title="查看權限邊界" aria-label="查看 ${escapeHtml(account.display_name || account.username)} 的權限邊界">
                <i class="fa-solid fa-circle-info" aria-hidden="true"></i>
            </button>
        </div>
    `).join("");
}

function boundaryGroupForID(groupID) {
    const target = String(groupID || "").trim().toLowerCase();
    return groups.find((group) => String(group.id || "").trim().toLowerCase() === target) || null;
}

function boundaryWorkspaceLabel(workspaceID) {
    const id = String(workspaceID || "").trim().toLowerCase();
    const option = workspaceOptions.find((workspace) => String(workspace.id || "").trim().toLowerCase() === id);
    const name = String(option?.name || "").trim();
    return name && name.toLowerCase() !== id ? `${name} (${id})` : id;
}

function boundaryPermissionSourceMap(account) {
    const sources = new Map();
    const append = (items, source) => {
        (Array.isArray(items) ? items : []).forEach((permission) => {
            const pluginID = String(permission?.plugin_id || "").trim();
            const key = pluginID.toLowerCase();
            if (!key || sources.has(key)) return;
            sources.set(key, source);
        });
    };

    append(account?.permissions, { label: "帳號直接設定", detail: account.username || account.id || "帳號" });
    (Array.isArray(account?.group_ids) ? account.group_ids : []).forEach((groupID) => {
        const group = boundaryGroupForID(groupID);
        if (!group || group.enabled === false) return;
        append(group.permissions, {
            label: group.name || group.id,
            detail: `群組 ${group.id}`
        });
    });
    return sources;
}

function boundaryPluginLabel(permission) {
    const pluginID = String(permission?.plugin_id || "").trim();
    if (pluginID === "*") return "全部卡片";
    const option = mergePluginOptions(pluginOptions, [permission]).find((plugin) => plugin.id === pluginID);
    return String(permission?.plugin_name || option?.name || pluginID).trim();
}

function normalizeBoundaryPermissions(input) {
    const seen = new Set();
    return (Array.isArray(input) ? input : []).map((permission) => {
        const pluginID = String(permission?.plugin_id || "").trim();
        const key = pluginID.toLowerCase();
        if (!key || seen.has(key)) return null;
        seen.add(key);
        return {
            ...permission,
            plugin_id: pluginID,
            plugin_name: String(permission?.plugin_name || "").trim(),
            enabled: permission?.enabled !== false,
            scopes: [...new Set((Array.isArray(permission?.scopes) ? permission.scopes : [])
                .map((scope) => String(scope || "").trim().toLowerCase())
                .filter(Boolean))],
            settings: permission?.settings && typeof permission.settings === "object" && !Array.isArray(permission.settings)
                ? { ...permission.settings }
                : {}
        };
    }).filter(Boolean);
}

function renderBoundarySettings(permission) {
    const pluginID = String(permission?.plugin_id || "").trim();
    const settings = permission?.settings && typeof permission.settings === "object" && !Array.isArray(permission.settings)
        ? permission.settings
        : {};
    const definition = permissionSettingDefinitionForPlugin(pluginID);
    if (definition?.items?.length) {
        return `<div class="boundary-settings">${definition.items.map((item) => {
            const explicit = Object.prototype.hasOwnProperty.call(settings, item.key);
            const enabled = explicit ? Boolean(settings[item.key]) : Boolean(item.default);
            const suffix = explicit ? "" : "（預設）";
            return `<span class="boundary-chip ${enabled ? "ok" : "off"}">${escapeHtml(item.label)}：${enabled ? "啟用" : "停用"}${suffix}</span>`;
        }).join("")}</div>`;
    }
    const entries = Object.entries(settings);
    if (!entries.length) {
        return `<span class="boundary-secondary-text">未設定功能項</span>`;
    }
    return `<div class="boundary-settings">${entries.map(([key, value]) => {
        const display = typeof value === "boolean" ? (value ? "啟用" : "停用") : JSON.stringify(value);
        const stateClass = typeof value === "boolean" ? (value ? "ok" : "off") : "";
        return `<span class="boundary-chip ${stateClass}">${escapeHtml(key)}：${escapeHtml(display)}</span>`;
    }).join("")}</div>`;
}

function renderAccountBoundary(account, payload) {
    const effectivePermissions = normalizeBoundaryPermissions(payload?.plugin_permissions || payload?.permission_details || payload?.permissions || []);
    const effectiveWorkspaces = normalizeWorkspaceIDs(payload?.workspace_ids || []);
    const sourceMap = boundaryPermissionSourceMap(account);
    const assignedGroupIDs = Array.isArray(account.group_ids) ? account.group_ids : [];
    const effectiveGroupCount = assignedGroupIDs.filter((groupID) => {
        const group = boundaryGroupForID(groupID);
        return Boolean(group && group.enabled !== false);
    }).length;
    const enabledPermissionCount = effectivePermissions.filter((permission) => permission.enabled !== false).length;

    const groupRows = assignedGroupIDs.length ? assignedGroupIDs.map((groupID) => {
        const group = boundaryGroupForID(groupID);
        if (!group) {
            return `
                <tr>
                    <td><span class="boundary-primary-text">${escapeHtml(groupID)}</span><span class="boundary-secondary-text">群組不存在</span></td>
                    <td><span class="boundary-chip deny">不生效</span></td>
                    <td>—</td>
                    <td>—</td>
                </tr>
            `;
        }
        const enabled = group.enabled !== false;
        const workspaceLabels = normalizeWorkspaceIDs(group.workspace_ids).map(boundaryWorkspaceLabel);
        return `
            <tr>
                <td><span class="boundary-primary-text">${escapeHtml(group.name || group.id)}</span><span class="boundary-secondary-text">${escapeHtml(group.id)}</span></td>
                <td><span class="boundary-chip ${enabled ? "ok" : "off"}">${enabled ? "生效" : "群組停用"}</span></td>
                <td><div class="boundary-chips">${workspaceLabels.map((label) => `<span class="boundary-chip">${escapeHtml(label)}</span>`).join("")}</div></td>
                <td>${enabled ? (Array.isArray(group.permissions) ? group.permissions.length : 0) : 0} 項</td>
            </tr>
        `;
    }).join("") : `
        <tr>
            <td colspan="4"><span class="boundary-secondary-text">未指派群組；工作空間邊界僅保留 DEFAULT。</span></td>
        </tr>
    `;

    const permissionRows = effectivePermissions.length ? effectivePermissions.map((permission) => {
        const pluginID = String(permission.plugin_id || "").trim();
        const enabled = permission.enabled !== false;
        const scopes = Array.isArray(permission.scopes) ? permission.scopes : [];
        const source = sourceMap.get(pluginID.toLowerCase()) || { label: "後端有效權限", detail: "來源未標示" };
        return `
            <tr>
                <td><span class="boundary-primary-text">${escapeHtml(boundaryPluginLabel(permission))}</span><span class="boundary-secondary-text">${escapeHtml(pluginID)}</span></td>
                <td><span class="boundary-chip ${enabled ? "ok" : "deny"}">${enabled ? "允許" : "拒絕"}</span></td>
                <td>${scopes.length
                    ? `<div class="boundary-chips">${scopes.map((scope) => `<span class="boundary-chip">${escapeHtml(scope)}</span>`).join("")}</div>`
                    : `<span class="boundary-chip off">未指定 Scope</span>`}
                </td>
                <td><span class="boundary-primary-text">${escapeHtml(source.label)}</span><span class="boundary-secondary-text">${escapeHtml(source.detail)}</span></td>
                <td>${renderBoundarySettings(permission)}</td>
            </tr>
        `;
    }).join("") : `
        <tr>
            <td colspan="5"><span class="boundary-secondary-text">沒有有效 Plugin 權限規則，未明確允許的卡片皆不可存取。</span></td>
        </tr>
    `;

    accountBoundaryTitle.textContent = `${account.display_name || account.username} · 權限邊界`;
    accountBoundarySummary.textContent = `${account.username} · 依目前帳號與群組設定計算`;
    accountBoundaryBody.innerHTML = `
        ${account.enabled === false ? `
            <div class="boundary-alert">
                <i class="fa-solid fa-triangle-exclamation" aria-hidden="true"></i>
                <span>此帳號已停用。即使下列群組與 Plugin 規則允許存取，帳密驗證仍會拒絕登入。</span>
            </div>
        ` : ""}
        <div class="boundary-summary-grid">
            <div class="boundary-summary-item">
                <span class="boundary-summary-label">帳號狀態</span>
                <span class="boundary-summary-value">${account.enabled === false ? "停用" : "啟用"}</span>
            </div>
            <div class="boundary-summary-item">
                <span class="boundary-summary-label">角色</span>
                <span class="boundary-summary-value">${escapeHtml(account.role || "user")}</span>
            </div>
            <div class="boundary-summary-item">
                <span class="boundary-summary-label">生效群組</span>
                <span class="boundary-summary-value">${effectiveGroupCount} / ${assignedGroupIDs.length}</span>
            </div>
            <div class="boundary-summary-item">
                <span class="boundary-summary-label">允許規則</span>
                <span class="boundary-summary-value">${enabledPermissionCount} / ${effectivePermissions.length}</span>
            </div>
        </div>

        <section class="boundary-section">
            <div class="boundary-section-head">
                <strong>工作空間邊界</strong>
                <span>${effectiveWorkspaces.length} 個允許工作空間</span>
            </div>
            <div class="boundary-chips">
                ${effectiveWorkspaces.map((workspaceID) => `<span class="boundary-chip ok">${escapeHtml(boundaryWorkspaceLabel(workspaceID))}</span>`).join("")}
            </div>
            <div class="boundary-table-wrap">
                <table class="boundary-table">
                    <thead><tr><th>所屬群組</th><th>狀態</th><th>群組工作空間</th><th>有效規則</th></tr></thead>
                    <tbody>${groupRows}</tbody>
                </table>
            </div>
        </section>

        <section class="boundary-section">
            <div class="boundary-section-head">
                <strong>Plugin 與功能邊界</strong>
                <span>後端回傳的最終有效規則</span>
            </div>
            <div class="boundary-table-wrap">
                <table class="boundary-table">
                    <thead><tr><th>卡片 / Plugin</th><th>存取</th><th>Scope</th><th>規則來源</th><th>功能項</th></tr></thead>
                    <tbody>${permissionRows}</tbody>
                </table>
            </div>
        </section>

        <section class="boundary-section">
            <div class="boundary-section-head"><strong>邊界判定順序</strong></div>
            <ul class="boundary-policy">
                <li>帳號停用時，登入與帳密驗證直接拒絕。</li>
                <li>帳號直接權限優先，其後依帳號所屬群組順序採用第一筆同名 Plugin 規則；停用群組不參與計算。</li>
                <li>指定 Plugin 的規則優先於「全部卡片（*）」；未明確允許的 Plugin 或 Scope 預設拒絕。</li>
                <li>未設定工作空間時只允許 DEFAULT；群組工作空間會合併為帳號的有效工作空間。</li>
            </ul>
        </section>
    `;
}

async function openAccountBoundary(accountID) {
    const account = accounts.find((item) => item.id === accountID);
    if (!account) return;
    activeBoundaryAccountID = account.id;
    accountBoundaryTitle.textContent = `${account.display_name || account.username} · 權限邊界`;
    accountBoundarySummary.textContent = `${account.username} · 正在計算有效權限`;
    accountBoundaryBody.innerHTML = `
        <div class="boundary-loading">
            <span><i class="fa-solid fa-circle-notch fa-spin" aria-hidden="true"></i><br>正在讀取帳號的有效權限邊界…</span>
        </div>
    `;
    if (!accountBoundaryDialog.open) accountBoundaryDialog.showModal();
    try {
        const payload = await accountApi(`/plugins/permissions?account_id=${encodeURIComponent(account.id)}`, {
            method: "GET",
            cache: "no-store",
            timeoutMs: 10000
        });
        if (activeBoundaryAccountID !== account.id || !accountBoundaryDialog.open) return;
        renderAccountBoundary(account, payload);
    } catch (error) {
        if (activeBoundaryAccountID !== account.id || !accountBoundaryDialog.open) return;
        accountBoundarySummary.textContent = `${account.username} · 權限邊界讀取失敗`;
        accountBoundaryBody.innerHTML = `
            <div class="boundary-empty">
                <span><i class="fa-solid fa-triangle-exclamation" aria-hidden="true"></i><br>${escapeHtml(readableApiError(error, "權限邊界讀取失敗"))}</span>
            </div>
        `;
    }
}

function closeAccountBoundary() {
    activeBoundaryAccountID = "";
    if (accountBoundaryDialog.open) accountBoundaryDialog.close();
}

function renderGroups() {
    const query = document.getElementById("groupSearch").value.trim().toLowerCase();
    const filtered = groups.filter((group) => {
        const text = [group.id, group.name, group.note].join(" ").toLowerCase();
        return !query || text.includes(query);
    });
    groupSummary.textContent = `${groups.length} 個群組`;
    if (!filtered.length) {
        groupList.innerHTML = `<div class="empty">沒有符合條件的群組</div>`;
        return;
    }
    groupList.innerHTML = filtered.map((group) => `
        <button class="account-item ${group.id === selectedGroupId ? "active" : ""}" type="button" data-group-id="${escapeHtml(group.id)}">
            <span class="account-main">
                <span class="account-name">${escapeHtml(group.name || group.id)}</span>
                <span class="account-meta">${escapeHtml(group.id)} · ${group.enabled ? "啟用" : "停用"} · ${normalizeWorkspaceIDs(group.workspace_ids).length} 個工作空間 · ${(group.permissions || []).length} 項權限</span>
            </span>
        </button>
    `).join("");
}

function renderGroupOptions(selected = []) {
    const selectedSet = new Set((selected || []).map((id) => String(id)));
    if (!groups.length) {
        fields.groupIds.innerHTML = `<div class="checkbox-empty">尚未建立群組</div>`;
        return;
    }
    fields.groupIds.innerHTML = groups.map((group) => `
        <label class="checkbox-item">
            <input type="checkbox" value="${escapeHtml(group.id)}" ${selectedSet.has(group.id) ? "checked" : ""}>
            <span>${escapeHtml(group.name || group.id)}</span>
        </label>
    `).join("");
}

function normalizeWorkspaceIDs(values) {
    const normalized = [...new Set((Array.isArray(values) ? values : [])
        .map((value) => String(value || "").trim().toLowerCase())
        .filter(Boolean))];
    return [defaultWorkspaceID, ...normalized.filter((id) => id !== defaultWorkspaceID)];
}

function normalizeWorkspaceOption(item) {
    const id = String(item?.id || item?.workspace_id || "").trim().toLowerCase();
    if (!id) return null;
    return {
        id,
        name: String(item?.name || item?.title || item?.label || id).trim() || id
    };
}

function mergedWorkspaceOptions(selected = []) {
    const merged = new Map();
    [{ id: defaultWorkspaceID, name: "預設工作空間" }, ...workspaceOptions]
        .map(normalizeWorkspaceOption)
        .filter(Boolean)
        .forEach((item) => merged.set(item.id, item));
    normalizeWorkspaceIDs(selected).forEach((id) => {
        if (!merged.has(id)) merged.set(id, { id, name: id });
    });
    return [...merged.values()].sort((left, right) => {
        if (left.id === defaultWorkspaceID) return -1;
        if (right.id === defaultWorkspaceID) return 1;
        return left.name.localeCompare(right.name, "zh-Hant");
    });
}

function renderGroupWorkspaceOptions(selected = []) {
    const selectedSet = new Set(normalizeWorkspaceIDs(selected));
    fields.groupWorkspaceIds.innerHTML = mergedWorkspaceOptions(selected).map((workspace) => `
        <label class="checkbox-item">
            <input type="checkbox" value="${escapeHtml(workspace.id)}" ${selectedSet.has(workspace.id) ? "checked" : ""} ${workspace.id === defaultWorkspaceID ? "disabled" : ""}>
            <span>${escapeHtml(workspace.name)} (${escapeHtml(workspace.id)})</span>
        </label>
    `).join("");
}

function renderAPIKeys(account = null) {
    const keys = Array.isArray(account?.api_keys) ? account.api_keys : [];
    apiKeyResult.hidden = true;
    apiKeyValue.value = "";
    if (!account || creating) {
        apiKeyList.innerHTML = `<div class="empty">請先儲存帳號後再核發金鑰</div>`;
        return;
    }
    if (!keys.length) {
        apiKeyList.innerHTML = `<div class="empty">尚未核發金鑰</div>`;
        return;
    }
    apiKeyList.innerHTML = keys.map((key) => `
        <div class="api-key-row" data-api-key-id="${escapeHtml(key.id)}">
            <span class="api-key-main">
                <span class="api-key-name">${escapeHtml(key.name || key.id)}</span>
                <span class="api-key-meta">ID ${escapeHtml(key.id)} · ${key.enabled === false ? "停用" : "啟用"}</span>
            </span>
            <span class="api-key-meta">前綴 ${escapeHtml(key.prefix || "")}</span>
            <span class="api-key-meta">${key.last_used_at ? `最近使用 ${escapeHtml(key.last_used_at)}` : `建立 ${escapeHtml(key.created_at || "")}`}</span>
            <button class="icon danger" type="button" title="刪除金鑰" aria-label="刪除金鑰" data-delete-api-key="${escapeHtml(key.id)}">
                <i class="fa-solid fa-trash"></i>
            </button>
        </div>
    `).join("");
}

function renderPluginOptions(selectedID = "") {
    const options = mergePluginOptions(pluginOptions, permissions);
    const selected = String(selectedID || "").trim();
    return options.map((plugin) => `
        <option value="${escapeHtml(plugin.id)}" ${plugin.id === selected ? "selected" : ""}>
            ${escapeHtml(`${plugin.name || plugin.id} (${plugin.id})`)}
        </option>
    `).join("");
}

function emptyAccount() {
    return {
        id: "",
        username: "",
        display_name: "",
        email: "",
        role: "user",
        enabled: true,
        note: "",
        group_ids: [],
        password_set: false,
        metadata: {}
    };
}

function selectAccount(id) {
    creating = false;
    selectedId = id;
    const account = accounts.find((item) => item.id === id) || emptyAccount();
    fillForm(account);
    setSelectedStatus(account);
    renderAPIKeys(account);
    renderAccounts();
}

function startNewAccount() {
    creating = true;
    selectedId = "";
    fillForm(emptyAccount());
    setSelectedStatus(null);
    renderAPIKeys(null);
    renderAccounts();
    activateTab("profile");
    fields.accountId.focus();
}

function fillForm(account) {
    fields.accountId.value = account.id || "";
    fields.accountId.disabled = !creating && Boolean(account.id);
    fields.displayName.value = account.display_name || "";
    fields.email.value = account.email || "";
    fields.role.value = account.role || "user";
    fields.password.value = "";
    fields.confirmPassword.value = "";
    fields.enabled.checked = account.enabled !== false;
    enabledSwitchLabel.textContent = fields.enabled.checked ? "啟用" : "停用";
    fields.note.value = accountNote(account);
    fields.apiKeyName.value = "";
    renderGroupOptions(account.group_ids || []);
}

function readAccountPayload() {
    const accountID = fields.accountId.value.trim();
    const payload = {
        id: accountID,
        username: accountID,
        display_name: fields.displayName.value.trim(),
        email: fields.email.value.trim(),
        role: fields.role.value.trim(),
        enabled: fields.enabled.checked,
        note: fields.note.value.trim(),
        group_ids: [...fields.groupIds.querySelectorAll("input[type='checkbox']:checked")].map((input) => input.value),
        metadata: selectedAccountMetadata()
    };
    return payload;
}

function isReservedAccountId(value) {
    return String(value || "").trim().toLowerCase() === "system-admin";
}

function accountNote(account) {
    if (typeof account?.note === "string" && account.note.trim()) return account.note;
    const metadata = account?.metadata || {};
    if (typeof metadata.note === "string" && metadata.note.trim()) return metadata.note;
    if (typeof metadata.source === "string" && metadata.source.trim()) return metadata.source;
    return "";
}

function selectedAccountMetadata() {
    const account = accounts.find((item) => item.id === selectedId);
    return account?.metadata || {};
}

function emptyGroup() {
    return {
        id: "",
        name: "",
        enabled: true,
        note: "",
        workspace_ids: [defaultWorkspaceID],
        permissions: []
    };
}

function selectGroup(id) {
    creatingGroup = false;
    selectedGroupId = id;
    const group = groups.find((item) => item.id === id) || emptyGroup();
    fillGroupForm(group);
    permissions = normalizePermissions(group.permissions || []);
    renderPermissions();
    renderGroups();
}

function startNewGroup() {
    creatingGroup = true;
    selectedGroupId = "";
    permissions = [];
    fillGroupForm(emptyGroup());
    renderPermissions();
    renderGroups();
    activateGroupTab("profile");
    fields.groupName.focus();
}

function fillGroupForm(group) {
    fields.groupId.value = group.id || "";
    fields.groupId.disabled = !creatingGroup && Boolean(group.id);
    fields.groupName.value = group.name || "";
    fields.groupEnabled.checked = group.enabled !== false;
    groupEnabledLabel.textContent = fields.groupEnabled.checked ? "啟用" : "停用";
    renderGroupWorkspaceOptions(group.workspace_ids);
    fields.groupNote.value = group.note || "";
}

function readGroupPayload() {
    return {
        id: fields.groupId.value.trim(),
        name: fields.groupName.value.trim(),
        enabled: fields.groupEnabled.checked,
        note: fields.groupNote.value.trim(),
        workspace_ids: normalizeWorkspaceIDs(
            [...fields.groupWorkspaceIds.querySelectorAll("input[type='checkbox']:checked")].map((input) => input.value)
        ),
        permissions: normalizePermissions(permissions)
    };
}

function normalizePermissions(input) {
    return (Array.isArray(input) ? input : []).map((item) => ({
        plugin_id: String(item.plugin_id || "").trim(),
        plugin_name: String(item.plugin_name || "").trim(),
        enabled: item.enabled !== false,
        scopes: Array.isArray(item.scopes)
            ? item.scopes.map((scope) => String(scope || "").trim()).filter(Boolean)
            : String(item.scopes || "").split(",").map((scope) => scope.trim()).filter(Boolean),
        note: String(item.note || "").trim(),
        settings: normalizePermissionSettings(item.plugin_id, item.settings)
    })).filter((item) => item.plugin_id);
}

function permissionLevelFromScopes(scopes = []) {
    const set = new Set((Array.isArray(scopes) ? scopes : []).map((scope) => String(scope || "").trim().toLowerCase()).filter(Boolean));
    if (!set.size || set.has("none")) return "none";
    if (set.has("admin") || set.has("delete") || set.has("*")) return "full";
    if (set.has("write") || set.has("manage")) return "write";
    return "read";
}

function scopesFromPermissionLevel(level) {
    switch (String(level || "").trim().toLowerCase()) {
        case "read":
            return ["read"];
        case "write":
            return ["read", "write"];
        case "full":
            return ["read", "write", "delete", "admin"];
        case "none":
        default:
            return [];
    }
}

function renderScopeOptions(scopes = []) {
    const selected = permissionLevelFromScopes(scopes);
    const options = [
        ["none", "不顯示"],
        ["read", "唯讀"],
        ["write", "讀寫"],
        ["full", "完整權限"]
    ];
    return options.map(([value, label]) => `
        <option value="${value}" ${value === selected ? "selected" : ""}>${label}</option>
    `).join("");
}

function normalizePermissionSettingItem(input) {
    if (!input || typeof input !== "object") return null;
    const key = String(input.key || input.id || input.name || "").trim();
    if (!key) return null;
    const type = String(input.type || "boolean").trim().toLowerCase();
    if (type !== "boolean") return null;
    return {
        key,
        type,
        label: String(input.label || input.title || key).trim(),
        description: String(input.description || input.note || "").trim(),
        default: Boolean(input.default ?? input.default_enabled ?? input.defaultEnabled ?? false)
    };
}

function normalizePermissionSettingDefinition(input) {
    if (!input || typeof input !== "object") return null;
    const rawItems = Array.isArray(input) ? input : input.items;
    const items = (Array.isArray(rawItems) ? rawItems : []).map(normalizePermissionSettingItem).filter(Boolean);
    if (!items.length) return null;
    return {
        title: String(input.title || input.name || "詳細設定").trim(),
        description: String(input.description || input.note || "可依群組權限啟用或停用項目。").trim(),
        items
    };
}

function permissionSettingDefinitionForPlugin(pluginID) {
    const id = String(pluginID || "").trim();
    if (!id) return null;
    const options = mergePluginOptions(pluginOptions, permissions);
    const plugin = options.find((item) => item.id === id)
        || options.find((item) => item.id === permissionTargetAliases[id]);
    return plugin?.permission_settings || null;
}

function defaultPermissionSettings(pluginID) {
    const definition = permissionSettingDefinitionForPlugin(pluginID);
    if (!definition) return {};
    return Object.fromEntries(definition.items.map((item) => [item.key, Boolean(item.default)]));
}

function normalizePermissionSettings(pluginID, settings = {}) {
    const input = settings && typeof settings === "object" && !Array.isArray(settings) ? settings : {};
    const definition = permissionSettingDefinitionForPlugin(pluginID);
    if (!definition) {
        return Object.keys(input).length ? { ...input } : {};
    }
    const normalized = defaultPermissionSettings(pluginID);
    definition.items.forEach((item) => {
        if (Object.prototype.hasOwnProperty.call(input, item.key)) {
            normalized[item.key] = Boolean(input[item.key]);
        }
    });
    return normalized;
}

function permissionSettingsEnabledCount(pluginID, settings = {}) {
    const definition = permissionSettingDefinitionForPlugin(pluginID);
    if (!definition) return 0;
    const normalized = normalizePermissionSettings(pluginID, settings);
    return definition.items.filter((item) => normalized[item.key]).length;
}

function permissionSettingsSummaryText(pluginID, settings = {}) {
    const definition = permissionSettingDefinitionForPlugin(pluginID);
    if (!definition) return "尚未提供詳細設定。";
    return `${definition.description} 已啟用 ${permissionSettingsEnabledCount(pluginID, settings)} / ${definition.items.length} 個項目。`;
}

function normalizePluginOption(input) {
    const id = String(input?.id || input?.plugin_id || input?.name || input?.code || "").trim();
    if (!id) return null;
    const ui = input?.ui || {};
    const name = String(input?.display_name || input?.plugin_name || input?.title || ui.title || input?.name || id).trim();
    const permissionSettings = normalizePermissionSettingDefinition(
        input?.permission_settings ||
        input?.permissionSettings ||
        input?.permission_setting_schema ||
        input?.feature_settings
    );
    return permissionSettings ? { id, name, permission_settings: permissionSettings } : { id, name };
}

function normalizeModuleOption(input, pluginSettingsByID = {}) {
    const id = String(input?.id || input?.module_id || input?.card_id || input?.plugin_id || "").trim();
    if (!id) return null;
    const pluginID = String(input?.plugin_id || "").trim();
    const cardID = String(input?.card_id || "").trim();
    const name = String(input?.title || input?.display_name || input?.name || id).trim();
    const permissionSettings = normalizePermissionSettingDefinition(
        input?.permission_settings ||
        pluginSettingsByID[pluginID] ||
        pluginSettingsByID[id]
    );
    const option = permissionSettings ? { id, name, permission_settings: permissionSettings } : { id, name };
    if (pluginID) option.plugin_id = pluginID;
    if (cardID) option.card_id = cardID;
    return option;
}

function mergePluginOptions(base, permissionSource = []) {
    const merged = [];
    const byID = new Map();
    const upsert = (source) => {
        const plugin = normalizePluginOption(source);
        if (!plugin) return;
        const existing = byID.get(plugin.id);
        if (existing) {
            if (plugin.name && plugin.name !== plugin.id) existing.name = plugin.name;
            if (plugin.permission_settings) existing.permission_settings = plugin.permission_settings;
            return;
        }
        byID.set(plugin.id, plugin);
        merged.push(plugin);
    };
    upsert({ id: "*", name: "全部卡片" });
    for (const source of [...builtInPermissionPlugins, ...(base || []), ...(permissionSource || []).map((permission) => ({
        id: permission.plugin_id,
        name: permission.plugin_name
    }))]) {
        upsert(source);
    }
    return merged;
}

function extractPermissionTargetList(payload, normalizer = normalizePluginOption) {
    const candidates = [
        payload?.modules,
        payload?.plugins,
        payload?.items,
        payload?.data?.modules,
        payload?.data?.plugins,
        payload?.data?.items,
        payload?.module_list,
        payload?.plugin_list,
        Array.isArray(payload) ? payload : null
    ];
    for (const candidate of candidates) {
        if (Array.isArray(candidate)) {
            return candidate.map(normalizer).filter(Boolean);
        }
    }
    return [];
}

async function loadPluginOptions() {
    let pluginCatalog = [];
    for (const endpoint of ["/api/plugin", "/api/plugin/list"]) {
        try {
            const data = await mainApi(endpoint, { method: "GET", cache: "no-store", timeoutMs: 3000 });
            pluginCatalog = extractPermissionTargetList(data, normalizePluginOption);
            if (pluginCatalog.length) break;
        } catch {
            // 主系統版本可能使用不同 plugin list API，逐一嘗試後保留既有選項。
        }
    }
    const pluginSettingsByID = {};
    pluginCatalog.forEach((plugin) => {
        if (plugin.permission_settings) pluginSettingsByID[plugin.id] = plugin.permission_settings;
    });
    for (const endpoint of ["/api/modules", "/api/modules/list"]) {
        try {
            const data = await mainApi(endpoint, { method: "GET", cache: "no-store", timeoutMs: 5000 });
            const modules = extractPermissionTargetList(data, (item) => normalizeModuleOption(item, pluginSettingsByID));
            if (modules.length) {
                pluginOptions = mergePluginOptions(modules, permissions);
                return;
            }
        } catch {
            // 舊版主系統可能尚未提供 module list，退回 plugin catalog。
        }
    }
    pluginOptions = mergePluginOptions(pluginCatalog.length ? pluginCatalog : pluginOptions, permissions);
}

async function loadWorkspaceOptions() {
    try {
        const data = assertSuccessfulPayload(
            await accountPluginControlFetch("/config", { method: "GET", cache: "no-store", timeoutMs: 6000 }),
            "主系統工作空間讀取失敗"
        );
        const values = data?.data?.workspace_options || data?.workspace_options || [];
        const normalized = (Array.isArray(values) ? values : []).map(normalizeWorkspaceOption).filter(Boolean);
        const merged = new Map([[defaultWorkspaceID, { id: defaultWorkspaceID, name: "預設工作空間" }]]);
        normalized.forEach((item) => merged.set(item.id, item));
        workspaceOptions = [...merged.values()];
    } catch {
        workspaceOptions = [{ id: defaultWorkspaceID, name: "預設工作空間" }];
    }
}

function renderPermissions() {
    if (!permissions.length) {
        permissionList.innerHTML = `<div class="empty">尚未設定卡片權限</div>`;
        return;
    }
    permissionList.innerHTML = permissions.map((permission, index) => {
        const configurable = Boolean(permissionSettingDefinitionForPlugin(permission.plugin_id));
        const detailTitle = configurable ? "詳細設定" : "尚未提供詳細設定";
        return `
            <div class="permission-row" data-permission-index="${index}">
                <label>
                    卡片 ID
                    <select data-permission-field="plugin_id">
                        ${renderPluginOptions(permission.plugin_id)}
                    </select>
                </label>
                <label>
                    Scopes
                    <select data-permission-field="scopes">
                        ${renderScopeOptions(permission.scopes)}
                    </select>
                </label>
                <label class="check-row">
                    <input type="checkbox" data-permission-field="enabled" ${permission.enabled !== false ? "checked" : ""}>
                    啟用
                </label>
                <button class="icon permission-detail-button" type="button" title="${detailTitle}" aria-label="${detailTitle}" data-config-permission="${index}" ${configurable ? "" : "disabled"}>
                    <i class="fa-solid fa-gear"></i>
                </button>
                <button class="icon danger" type="button" title="刪除權限" aria-label="刪除權限" data-remove-permission="${index}">
                    <i class="fa-solid fa-trash"></i>
                </button>
            </div>
        `;
    }).join("");
}

function syncPermissionInputs() {
    const rows = [...permissionList.querySelectorAll("[data-permission-index]")];
    permissions = rows.map((row) => {
        const get = (field) => row.querySelector(`[data-permission-field="${field}"]`);
        const index = Number(row.dataset.permissionIndex);
        const pluginID = get("plugin_id")?.value.trim() || "";
        const previous = permissions[index] || {};
        const selectedPlugin = mergePluginOptions(pluginOptions, permissions).find((plugin) => plugin.id === pluginID);
        const previousSettings = previous.plugin_id === pluginID ? previous.settings : {};
        return {
            plugin_id: pluginID,
            plugin_name: selectedPlugin?.name || "",
            enabled: Boolean(get("enabled")?.checked),
            scopes: scopesFromPermissionLevel(get("scopes")?.value),
            settings: normalizePermissionSettings(pluginID, previousSettings)
        };
    }).filter((item) => item.plugin_id);
}

function renderPermissionSettings(pluginID, settings = {}) {
    const definition = permissionSettingDefinitionForPlugin(pluginID);
    if (!definition) {
        permissionSettingsTitle.textContent = "詳細設定";
        permissionSettingsSummary.textContent = "尚未提供詳細設定。";
        permissionSettingsList.innerHTML = "";
        return;
    }
    const normalized = normalizePermissionSettings(pluginID, settings);
    permissionSettingsTitle.textContent = definition.title;
    permissionSettingsSummary.textContent = permissionSettingsSummaryText(pluginID, normalized);
    permissionSettingsList.innerHTML = definition.items.map((option) => `
        <label class="settings-card">
            <input type="checkbox" data-permission-setting="${escapeHtml(option.key)}" ${normalized[option.key] ? "checked" : ""}>
            <span class="settings-card-text">
                <strong>${escapeHtml(option.label)}</strong>
                <span>${escapeHtml(option.description)}</span>
            </span>
        </label>
    `).join("");
}

function openPermissionSettings(index) {
    syncPermissionInputs();
    const permission = permissions[index];
    if (!permission || !permissionSettingDefinitionForPlugin(permission.plugin_id)) return;
    activePermissionSettingsIndex = index;
    renderPermissionSettings(permission.plugin_id, permission.settings);
    if (typeof permissionSettingsDialog.showModal === "function") {
        permissionSettingsDialog.showModal();
    } else {
        permissionSettingsDialog.setAttribute("open", "open");
    }
}

function closePermissionSettings() {
    activePermissionSettingsIndex = -1;
    if (permissionSettingsDialog.open) {
        permissionSettingsDialog.close();
    }
}

function readPermissionSettingsFromDialog() {
    const permission = permissions[activePermissionSettingsIndex];
    if (!permission) return {};
    const settings = defaultPermissionSettings(permission.plugin_id);
    permissionSettingsList.querySelectorAll("[data-permission-setting]").forEach((input) => {
        settings[input.dataset.permissionSetting] = Boolean(input.checked);
    });
    return normalizePermissionSettings(permission.plugin_id, settings);
}

function applyPermissionSettings() {
    if (activePermissionSettingsIndex < 0 || !permissions[activePermissionSettingsIndex]) {
        closePermissionSettings();
        return;
    }
    permissions[activePermissionSettingsIndex].settings = readPermissionSettingsFromDialog();
    closePermissionSettings();
    renderPermissions();
}

async function loadAccounts(options = {}) {
    const showWait = options.showBusy !== false;
    if (showWait) {
        showBusyDialog("重新整理中", "正在讀取帳號與群組資料，請稍候。");
    }
    try {
        setRuntime("", "檢查中", "fa-circle-notch fa-spin");
        try {
            const status = await accountPluginRuntimeControlFetch("status", { method: "GET", cache: "no-store", timeoutMs: 3000 });
            if (!status?.success) throw new Error(status?.error || "狀態讀取失敗");
            setRuntime("ok", "已連線", "fa-circle-check");
        } catch (error) {
            setRuntime("error", readableApiError(error, "連線失敗"), "fa-triangle-exclamation");
            printResult("狀態讀取失敗", { success: false, error: readableApiError(error, "連線失敗") });
            return;
        }

        const [accountResult, groupResult, settingsResult] = await Promise.allSettled([
            accountApi("/accounts", { method: "GET", cache: "no-store", timeoutMs: accountApiDefaultTimeoutMs }),
            accountApi("/groups", { method: "GET", cache: "no-store", timeoutMs: accountApiDefaultTimeoutMs }),
            accountApi("/settings", { method: "GET", cache: "no-store", timeoutMs: accountApiDefaultTimeoutMs })
        ]);
        const accountError = accountResult.status === "fulfilled" ? "" : readableApiError(accountResult.reason, "帳號讀取失敗");
        const groupError = groupResult.status === "fulfilled" ? "" : readableApiError(groupResult.reason, "群組讀取失敗");
        const settingsError = settingsResult.status === "fulfilled" ? "" : readableApiError(settingsResult.reason, "MCP 設定讀取失敗");
        accounts = accountResult.status === "fulfilled" && Array.isArray(accountResult.value.accounts) ? accountResult.value.accounts : [];
        groups = groupResult.status === "fulfilled" && Array.isArray(groupResult.value.groups) ? groupResult.value.groups : [];
        renderMCPSettings(settingsResult.status === "fulfilled" ? settingsResult.value?.settings?.mcp : {}, settingsError);
        await Promise.all([loadPluginOptions(), loadWorkspaceOptions()]);
        renderGroupOptions();
        if (accountError) {
            creating = false;
            selectedId = "";
            fillForm(emptyAccount());
            setSelectedStatus(null);
            renderAPIKeys(null);
            accountSummary.textContent = "帳號讀取失敗";
            accountList.innerHTML = `<div class="empty">${escapeHtml(accountError)}</div>`;
        } else if (selectedId && accounts.some((item) => item.id === selectedId)) {
            selectAccount(selectedId);
        } else if (accounts.length) {
            selectAccount(accounts[0].id);
        } else {
            startNewAccount();
        }
        if (groupError) {
            creatingGroup = false;
            selectedGroupId = "";
            permissions = [];
            fillGroupForm(emptyGroup());
            renderPermissions();
            groupSummary.textContent = "群組讀取失敗";
            groupList.innerHTML = `<div class="empty">${escapeHtml(groupError)}</div>`;
        } else if (selectedGroupId && groups.some((item) => item.id === selectedGroupId)) {
            selectGroup(selectedGroupId);
        } else if (groups.length) {
            selectGroup(groups[0].id);
        } else {
            startNewGroup();
        }
        activateMainTab(document.querySelector("[data-main-tab].active")?.dataset.mainTab || "accounts");
        printResult("GET /accounts + /groups + /settings", {
            accounts: accountResult.status === "fulfilled" ? accountResult.value : { success: false, error: accountError },
            groups: groupResult.status === "fulfilled" ? groupResult.value : { success: false, error: groupError },
            settings: settingsResult.status === "fulfilled" ? settingsResult.value : { success: false, error: settingsError }
        });
    } catch (error) {
        const message = readableApiError(error, "帳號或群組讀取失敗");
        setRuntime("ok", "已連線", "fa-circle-check");
        accountSummary.textContent = "帳號讀取失敗";
        groupSummary.textContent = "群組讀取失敗";
        accountList.innerHTML = `<div class="empty">${escapeHtml(message)}</div>`;
        groupList.innerHTML = `<div class="empty">${escapeHtml(message)}</div>`;
        renderMCPSettings({}, message);
        printResult("資料載入失敗", { success: false, error: message });
    } finally {
        if (showWait) {
            closeBusyDialog();
        }
    }
}

async function saveAccount(event) {
    event?.preventDefault();
    const payload = readAccountPayload();
    if (!payload.id) {
        window.alert("帳號 ID 不可空白。");
        return;
    }
    if (isReservedAccountId(payload.id)) {
        window.alert("system-admin 為系統保留帳號 ID，不可使用。");
        fields.accountId.focus();
        return;
    }
    const password = fields.password.value;
    const confirmPassword = fields.confirmPassword.value;
    if (password || confirmPassword) {
        if (password !== confirmPassword) {
            window.alert("密碼與確認密碼不一致。");
            fields.confirmPassword.focus();
            return;
        }
        payload.password = password;
    }
    const shouldCreate = creating || !selectedId;
    const path = shouldCreate ? "/accounts" : `/accounts/${encodeURIComponent(selectedId)}`;
    const method = shouldCreate ? "POST" : "PUT";
    let data = null;
    try {
        data = await accountApi(path, { method, body: JSON.stringify(payload) });
    } catch (error) {
        data = { success: false, error: error?.message || String(error) };
    }
    printResult(`${method} ${path}`, data);
    if (!data?.success) {
        window.alert(data?.error || "儲存失敗。");
        return;
    }
    creating = false;
    selectedId = data.account?.id || payload.id || selectedId;
    await loadAccounts();
    showSaveConfirmation(saveAccountBtn);
}

async function deleteAccount() {
    if (!selectedId || creating) return;
    const account = accounts.find((item) => item.id === selectedId);
    const label = account?.display_name || account?.username || selectedId;
    if (!window.confirm(`確定要刪除「${label}」？`)) return;
    const path = `/accounts/${encodeURIComponent(selectedId)}`;
    const data = await accountApi(path, { method: "DELETE" });
    printResult(`DELETE ${path}`, data);
    if (!data?.success) {
        window.alert(data?.error || "刪除失敗。");
        return;
    }
    selectedId = "";
    await loadAccounts();
}

async function issueAPIKey() {
    if (!selectedId || creating) {
        window.alert("請先儲存帳號後再核發金鑰。");
        return;
    }
    const name = fields.apiKeyName.value.trim();
    if (!name) {
        window.alert("金鑰名稱不可空白。");
        fields.apiKeyName.focus();
        return;
    }
    const path = `/accounts/${encodeURIComponent(selectedId)}/api-keys`;
    setAPIKeyControlsDisabled(true);
    showBusyDialog("核發金鑰中", "正在建立遠端 API 金鑰並更新清單，請稍候。");
    try {
        const data = await accountApi(path, {
            method: "POST",
            body: JSON.stringify({ name }),
            timeoutMs: apiKeyOperationTimeoutMs
        });
        printResult(`POST ${path}`, data);
        fields.apiKeyName.value = "";
        apiKeyValue.value = data.key || "";
        apiKeyResult.hidden = false;
        await loadAccounts({ showBusy: false });
        activateTab("apiKeys");
        apiKeyResult.hidden = false;
        apiKeyValue.value = data.key || "";
    } catch (error) {
        const message = readableApiError(error, "核發金鑰失敗。");
        printResult(`POST ${path}`, { success: false, error: message });
        window.alert(message);
    } finally {
        closeBusyDialog();
        setAPIKeyControlsDisabled(false);
    }
}

async function deleteAPIKey(keyID) {
    keyID = String(keyID || "").trim();
    if (!selectedId || !keyID) return;
    if (!window.confirm("確定要刪除此金鑰？刪除後遠端呼叫會立即失效。")) return;
    const path = `/accounts/${encodeURIComponent(selectedId)}/api-keys/${encodeURIComponent(keyID)}`;
    setAPIKeyControlsDisabled(true);
    showBusyDialog("刪除金鑰中", "正在刪除遠端 API 金鑰並更新清單，請稍候。");
    try {
        const data = await accountApi(path, { method: "DELETE", timeoutMs: apiKeyOperationTimeoutMs });
        printResult(`DELETE ${path}`, data);
        await loadAccounts({ showBusy: false });
        activateTab("apiKeys");
    } catch (error) {
        const message = readableApiError(error, "刪除金鑰失敗。");
        printResult(`DELETE ${path}`, { success: false, error: message });
        window.alert(message);
    } finally {
        closeBusyDialog();
        setAPIKeyControlsDisabled(false);
    }
}

async function copyAPIKey() {
    const value = apiKeyValue.value.trim();
    if (!value) return;
    try {
        await navigator.clipboard.writeText(value);
        window.alert("金鑰已複製。");
    } catch {
        apiKeyValue.focus();
        apiKeyValue.select();
    }
}

async function saveGroup(event) {
    event?.preventDefault();
    syncPermissionInputs();
    const payload = readGroupPayload();
    if (!payload.name) {
        window.alert("群組名稱不可空白。");
        return;
    }
    const shouldCreate = creatingGroup || !selectedGroupId;
    const path = shouldCreate ? "/groups" : `/groups/${encodeURIComponent(selectedGroupId)}`;
    const method = shouldCreate ? "POST" : "PUT";
    let data = null;
    try {
        data = await accountApi(path, { method, body: JSON.stringify(payload) });
    } catch (error) {
        data = { success: false, error: error?.message || String(error) };
    }
    printResult(`${method} ${path}`, data);
    if (!data?.success) {
        window.alert(data?.error || "群組儲存失敗。");
        return;
    }
    creatingGroup = false;
    selectedGroupId = data.group?.id || payload.id || selectedGroupId;
    await loadAccounts();
}

async function deleteGroup() {
    if (!selectedGroupId || creatingGroup) return;
    const group = groups.find((item) => item.id === selectedGroupId);
    const label = group?.name || selectedGroupId;
    if (!window.confirm(`確定要刪除「${label}」？`)) return;
    const path = `/groups/${encodeURIComponent(selectedGroupId)}`;
    const data = await accountApi(path, { method: "DELETE" });
    printResult(`DELETE ${path}`, data);
    if (!data?.success) {
        window.alert(data?.error || "群組刪除失敗。");
        return;
    }
    selectedGroupId = "";
    await loadAccounts();
}

async function savePermissions() {
    if (!selectedGroupId || creatingGroup) {
        window.alert("請先儲存群組。");
        return;
    }
    syncPermissionInputs();
    const path = `/groups/${encodeURIComponent(selectedGroupId)}/permissions`;
    const data = await accountApi(path, { method: "PUT", body: JSON.stringify({ permissions }) });
    printResult(`PUT ${path}`, data);
    if (!data?.success) {
        window.alert(data?.error || "權限儲存失敗。");
        return;
    }
    await loadAccounts();
}

function escapeHtml(value) {
    return String(value ?? "")
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;")
        .replaceAll("'", "&#039;");
}

document.querySelectorAll("[data-tab]").forEach((button) => {
    button.addEventListener("click", () => activateTab(button.dataset.tab));
});
document.querySelectorAll("[data-main-tab]").forEach((button) => {
    button.addEventListener("click", () => activateMainTab(button.dataset.mainTab));
});
document.querySelectorAll("[data-group-tab]").forEach((button) => {
    button.addEventListener("click", () => activateGroupTab(button.dataset.groupTab));
});

accountList.addEventListener("click", (event) => {
    const boundaryButton = event.target.closest("[data-account-boundary-id]");
    if (boundaryButton) {
        openAccountBoundary(boundaryButton.dataset.accountBoundaryId);
        return;
    }
    const button = event.target.closest("[data-account-id]");
    if (button) selectAccount(button.dataset.accountId);
});
groupList.addEventListener("click", (event) => {
    const button = event.target.closest("[data-group-id]");
    if (button) selectGroup(button.dataset.groupId);
});
apiKeyList.addEventListener("click", (event) => {
    const button = event.target.closest("[data-delete-api-key]");
    if (button) deleteAPIKey(button.dataset.deleteApiKey);
});

permissionList.addEventListener("click", (event) => {
    const configButton = event.target.closest("[data-config-permission]");
    if (configButton) {
        openPermissionSettings(Number(configButton.dataset.configPermission));
        return;
    }
    const button = event.target.closest("[data-remove-permission]");
    if (!button) return;
    syncPermissionInputs();
    permissions.splice(Number(button.dataset.removePermission), 1);
    renderPermissions();
});
permissionList.addEventListener("change", (event) => {
    const field = event.target.closest("[data-permission-field]");
    if (!field) return;
    syncPermissionInputs();
    if (field.dataset.permissionField === "plugin_id") renderPermissions();
});
permissionSettingsList.addEventListener("change", () => {
    const permission = permissions[activePermissionSettingsIndex];
    if (!permission) return;
    permissionSettingsSummary.textContent = permissionSettingsSummaryText(permission.plugin_id, readPermissionSettingsFromDialog());
});
busyDialog.addEventListener("cancel", (event) => event.preventDefault());
document.getElementById("accountSearch").addEventListener("input", renderAccounts);
document.getElementById("groupSearch").addEventListener("input", renderGroups);
document.getElementById("reloadBtn").addEventListener("click", loadAccounts);
document.getElementById("reloadGroupsBtn").addEventListener("click", loadAccounts);
document.getElementById("reloadSettingsBtn").addEventListener("click", loadMCPSettings);
saveMCPSettingsBtn.addEventListener("click", saveMCPSettings);
Object.values(mcpFields).forEach((input) => input.addEventListener("change", markMCPSettingsDirty));
document.getElementById("newAccountBtn").addEventListener("click", startNewAccount);
document.getElementById("newGroupBtn").addEventListener("click", startNewGroup);
document.getElementById("deleteBtn").addEventListener("click", deleteAccount);
document.getElementById("deleteGroupBtn").addEventListener("click", deleteGroup);
issueApiKeyBtn.addEventListener("click", issueAPIKey);
document.getElementById("copyApiKeyBtn").addEventListener("click", copyAPIKey);
document.getElementById("addPermissionBtn").addEventListener("click", () => {
    syncPermissionInputs();
    permissions.push({ plugin_id: "", plugin_name: "", enabled: true, scopes: [] });
    renderPermissions();
});
document.getElementById("savePermissionsBtn").addEventListener("click", savePermissions);
document.getElementById("closePermissionSettingsBtn").addEventListener("click", closePermissionSettings);
document.getElementById("cancelPermissionSettingsBtn").addEventListener("click", closePermissionSettings);
document.getElementById("savePermissionSettingsBtn").addEventListener("click", applyPermissionSettings);
document.getElementById("closeAccountBoundaryBtn").addEventListener("click", closeAccountBoundary);
document.getElementById("dismissAccountBoundaryBtn").addEventListener("click", closeAccountBoundary);
accountBoundaryDialog.addEventListener("close", () => {
    activeBoundaryAccountID = "";
});
permissionSettingsDialog.addEventListener("close", () => {
    activePermissionSettingsIndex = -1;
});
fields.enabled.addEventListener("change", () => {
    enabledSwitchLabel.textContent = fields.enabled.checked ? "啟用" : "停用";
});
fields.groupEnabled.addEventListener("change", () => {
    groupEnabledLabel.textContent = fields.groupEnabled.checked ? "啟用" : "停用";
});
accountForm.addEventListener("submit", saveAccount);
groupForm.addEventListener("submit", saveGroup);

async function startAccountManagerPage() {
    if (typeof window.AgenticTalkAPI?.ensureAuthOrRedirect === "function") {
        const allowed = await window.AgenticTalkAPI.ensureAuthOrRedirect();
        if (allowed === false) return;
    }
    loadAccounts();
}

if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", startAccountManagerPage, { once: true });
} else {
    startAccountManagerPage();
}
