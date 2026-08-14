(function () {
    function escapeHTML(value) {
        return String(value || "")
            .replaceAll("&", "&amp;")
            .replaceAll("<", "&lt;")
            .replaceAll(">", "&gt;")
            .replaceAll('"', "&quot;")
            .replaceAll("'", "&#039;");
    }
    function cookieValue(name) {
        var prefix = encodeURIComponent(name) + "=";
        var fallbackPrefix = name + "=";
        var found = document.cookie.split(";").map(function (item) {
            return item.trim();
        }).find(function (item) {
            return item.indexOf(prefix) === 0 || item.indexOf(fallbackPrefix) === 0;
        });
        return found ? found.slice(found.indexOf("=") + 1) : "";
    }
    function authHeaders() {
        var names = ["agentic_auth_token", "auth_token", "authToken", "token", "Authentication", "Authorization"];
        var raw = names.map(cookieValue).find(Boolean);
        if (!raw) return {};
        var decoded = "";
        try {
            decoded = decodeURIComponent(raw).trim();
        } catch {
            decoded = raw.trim();
        }
        if (!decoded) return {};
        var bearer = /^Bearer\s+/i.test(decoded) ? decoded : "Bearer " + decoded;
        return { "Authentication": bearer, "Authorization": bearer };
    }
    function setStatus(status, text, icon) {
        var el = document.getElementById("runtimeStatus");
        if (!el || el.dataset.fullLoadDone === "true") return;
        el.className = ("status-pill " + (status || "")).trim();
        el.innerHTML = '<i class="fa-solid ' + (icon || "fa-circle") + '"></i> ' + escapeHTML(text);
    }
    function fetchStatus(url) {
        var controller = new AbortController();
        var timer = window.setTimeout(function () {
            try {
                controller.abort(new Error("狀態檢查逾時"));
            } catch {
                controller.abort();
            }
        }, 3200);
        return fetch(url, {
            cache: "no-store",
            credentials: "same-origin",
            headers: Object.assign({ "Accept": "application/json" }, authHeaders()),
            signal: controller.signal
        }).then(function (response) {
            return response.ok ? response.json() : Promise.reject(new Error("HTTP " + response.status));
        }).finally(function () {
            window.clearTimeout(timer);
        });
    }
    var urls = [
        "/api/plugin/account-manager/_plugin/status"
    ];
    var done = false;
    urls.forEach(function (url) {
        fetchStatus(url).then(function (data) {
            if (done) return;
            if (!data || data.success === false) {
                return;
            }
            done = true;
            setStatus("ok", "已連線", "fa-circle-check");
        }).catch(function () {});
    });
}());
