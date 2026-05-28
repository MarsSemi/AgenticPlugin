(function () {
    function normalizePath(path) {
        const text = String(path || "");
        return text.startsWith("/") ? text : `/${text}`;
    }

    function joinURL(base, path) {
        const cleanBase = String(base || window.location.origin).replace(/\/+$/, "");
        return `${cleanBase}${normalizePath(path)}`;
    }

    function isPlainObject(value) {
        return value && typeof value === "object" && !Array.isArray(value);
    }

    function createHostAPI(config = {}) {
        const pluginId = String(config.pluginId || window.AgenticPluginDev?.pluginId || "sample").trim();
        const serviceURL = String(config.serviceURL || window.AgenticPluginDev?.serviceURL || window.location.origin).replace(/\/+$/, "");
        const useGateway = config.useGateway === true || window.AgenticPluginDev?.useGateway === true;
        const pluginApiBase = normalizePath(config.pluginApiBase || window.AgenticPluginDev?.pluginApiBase || `/api/${pluginId}`);
        const authToken = normalizeAuthToken(
            config.authToken ||
            window.AgenticPluginDev?.authToken ||
            window.localStorage?.getItem("agentic_auth_token") ||
            readAuthTokenCookie() ||
            "dev-auth-token"
        );

        async function readJSONResponse(resp) {
            const text = await resp.text();
            if (!text) return {};
            try {
                return JSON.parse(text);
            } catch (error) {
                return { success: false, error: text };
            }
        }

        async function request(url, options = {}) {
            const headers = {
                "Content-Type": "application/json",
                ...authHeaders(),
                ...(options.headers || {})
            };
            if (options.body instanceof FormData) {
                delete headers["Content-Type"];
            }
            const resp = await fetch(url, { credentials: "same-origin", ...options, headers });
            const data = await readJSONResponse(resp);
            if (!resp.ok && !data.error) data.error = `HTTP ${resp.status}`;
            return data;
        }

        function controlPath(commandPath) {
            const command = normalizePath(commandPath).replace(/^\/+/, "");
            if (!command || command === "status") return `${pluginApiBase}/plugin/status`;
            if (command === "mcp") return "/mcp";
            return `${pluginApiBase}/plugin/${command}`;
        }

        return {
            pluginId,
            devMode: !useGateway,
            authHeaders,
            ensureAuthOrRedirect() {
                return true;
            },
            startAuthMonitor() {},
            apiFetch(path, options = {}) {
                return request(joinURL(serviceURL, path), options);
            },
            fetchPlugin(id, pluginPath = "", options = {}) {
                const targetId = String(id || pluginId).trim();
                const path = normalizePath(pluginPath);
                if (useGateway) {
                    return request(`/api/plugin/${encodeURIComponent(targetId)}${path}`, options);
                }
                return request(joinURL(serviceURL, path), options);
            },
            fetchPluginControl(id, commandPath = "", options = {}) {
                const targetId = String(id || pluginId).trim();
                const path = normalizePath(commandPath);
                if (useGateway) {
                    return request(`/api/plugin/${encodeURIComponent(targetId)}/_plugin${path}`, options);
                }
                return request(joinURL(serviceURL, controlPath(path)), options);
            },
            configure(next = {}) {
                if (!isPlainObject(next)) return this;
                return createHostAPI({ pluginId, serviceURL, useGateway, pluginApiBase, authToken, ...next });
            }
        };

        function authHeaders() {
            if (!authToken) return {};
            const value = authToken.toLowerCase().startsWith("bearer ") ? authToken : `Bearer ${authToken}`;
            return {
                Authentication: value,
                Authorization: value
            };
        }
    }

    function readAuthTokenCookie() {
        if (typeof document === "undefined" || !document.cookie) return "";
        const names = ["agentic_auth_token", "auth_token", "authToken", "token", "Authentication", "Authorization"];
        const cookies = document.cookie.split(";").map((part) => part.trim()).filter(Boolean);
        for (const name of names) {
            const prefix = `${encodeURIComponent(name)}=`;
            const found = cookies.find((cookie) => cookie.startsWith(prefix) || cookie.startsWith(`${name}=`));
            if (!found) continue;
            const raw = found.slice(found.indexOf("=") + 1);
            try {
                return decodeURIComponent(raw);
            } catch {
                return raw;
            }
        }
        return "";
    }

    function normalizeAuthToken(value) {
        return String(value || "").trim();
    }

    window.AgenticPluginBase = window.AgenticPluginBase || {};
    window.AgenticPluginBase.createHostAPI = createHostAPI;

    const existing = window.AgenticTalkAPI || {};
    const hasHostFetch = typeof existing.fetchPlugin === "function" && !window.AgenticPluginDev?.forceAdapter;
    if (hasHostFetch) return;

    window.AgenticTalkAPI = createHostAPI(window.AgenticPluginDev || {});
})();
