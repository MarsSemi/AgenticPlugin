# Sample Plugin

此目錄示範 AgenticService plugin 應具備的基本結構。

## 主要檔案

- `plugin.json`：主服務讀取的外掛 manifest。
- `config.default.json`：runtime 設定範本。service 啟動或 load hook 發現缺少 `config.json` 時，會由此檔建立新的 runtime 設定。
- `skill/guide/SKILL.md`：Sample Plugin 專屬對話式 Skill guide，前端 CHAT 會透過 API 讀取後作為 SYSTEM PROMPT。
- `skill/skill-cards.default.json`：Sample Plugin 對話頁左側 Skill 卡片預設值。runtime 會在缺少 `skill-cards.json` 時用它初始化。
- `src/sampleplugin`：Sample Plugin 的 Go 原始碼。
- `website/sample/index.html`：Sample Plugin 前端呼叫範例，由 `plugin.json` 的 `website_path` 與 `ui` 註冊到主頁。

`plugin.json` 的 `type` 為 `service`，`auto_start` 為 `true`，因此主服務啟動時會預先啟動 Sample Plugin service 並呼叫 load hook。若外掛只有頁面沒有服務，請改用 `type: "web"`。

## 主頁卡片 manifest 規則

主頁卡片與 plugin 套件不是同一個 key：

- `id` 是 plugin 套件 ID，仍用於 `/api/plugin/{id}`、lifecycle、匯入、刪除與權限。
- `plugin_code` 是主頁卡片命名空間；省略時等同 `id`。
- `card_id` 是同一個 plugin 內的卡片 ID；單卡可省略，多卡建議必填。
- 主服務回傳的 `module.id` 是卡片唯一鍵：單卡為 `plugin_code`，多卡為 `plugin_code::card_id`。

Sample 目前保留單卡格式：

```json
{
  "id": "sample",
  "plugin_code": "sample",
  "website_path": "./website/sample/index.html",
  "ui": {
    "website_path": "./website/sample/index.html",
    "href": "/sample/index.html",
    "title": "Sample Plugin"
  }
}
```

如果同一個 plugin 要提供多個主頁入口，使用 `cards`：

```json
{
  "id": "sample",
  "plugin_code": "sample",
  "cards": [
    {
      "card_id": "main",
      "website_path": "./website/sample/index.html",
      "href": "/sample/index.html",
      "title": "Sample Plugin"
    },
    {
      "card_id": "ops",
      "website_path": "./website/sample/ops.html",
      "href": "/sample/ops.html",
      "title": "Sample 維運"
    }
  ]
}
```

多卡只是增加主頁入口，不代表拆成多個 plugin。前端仍使用 `window.AgenticTalkAPI.fetchPlugin("sample", "/api/sample/...")` 呼叫同一個後端。`card_id` 與 `plugin_code` 發布後應保持穩定，避免使用者已儲存的工作區塊群組與排序失效。

## 執行目錄規則

Sample service 必須以 plugin package 根目錄作為工作目錄。開發時是 `Sample` 根目錄；匯入主服務後是主服務根目錄。這個根目錄必須能用相對路徑找到 `plugins/sample/plugin.json`、`plugins/sample/config.json` 與 `website/sample/index.html`。

service 啟動時會從目前工作目錄或執行檔所在目錄往上尋找 `plugins/sample/plugin.json`，找到後切換到該根目錄再讀寫 runtime 檔案。若找不到根目錄，會停止啟動並要求從正確目錄執行，避免把 `config.json` 或 skill runtime 檔案寫到錯誤位置。

## 必備基礎功能

自定義 plugin 應保留下列基礎功能，再依需求擴充其他能力：

- 前端必須提供返回主畫面按鍵，例如 `回到主畫面` 連到 `/main.html`。
- 前端必須提供載入偵測，但只在頁面初始化時更新狀態；一般 API 操作不應反覆改寫右上角載入狀態。
- 後端必須提供 API catalog，例如 `GET /api/sample/apis` 或 `GET /api/sample`，讓主服務與使用者能查到所有 API 的路徑、方法、說明與範例。
- 後端必須提供 `GET /api/hello` 作為最小握手 API。
- 後端必須提供 `GET /api/health` 作為健康檢查 API。Sample 另支援 `GET /api/heatlth` 作為拼字相容別名。
- service plugin 應提供 lifecycle API：`status`、`load`、`unload`、`reload`、`registration`。`unload` 應採用 pending stop 流程：先回應主服務，再非同步關閉 service process，避免 Windows 更新時 exe 被舊 process 鎖住。

CRUD、串流、背景工作、定期任務、檔案 payload、即時訊息、工具呼叫與專屬 Skill 對話屬於可選能力，依 plugin 目的決定是否保留。

## Runtime 設定規則

`config.default.json` 是部署範本，`config.json` 是 runtime 檔案。package 不應包含 `config.json`，也不應在更新匯入時覆蓋既有 `config.json`。

Sample service 的標準流程是：

1. 讀取 `plugins/sample/config.json`。
2. 如果不存在，讀取 `plugins/sample/config.default.json`。
3. 補上目前 plugin 版本與必要預設欄位。
4. 寫出新的 `plugins/sample/config.json`。
5. 後續設定讀寫都以 `config.json` 為準。

## 呼叫方式

前端或主服務頁面應透過主服務 gateway 呼叫：

```js
window.AgenticTalkAPI.fetchPlugin("sample", "/api/sample/echo", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ message: "hello" })
})
```

外掛生命週期控制：

```js
window.AgenticTalkAPI.fetchPluginControl("sample", "/load", { method: "POST" })
window.AgenticTalkAPI.fetchPluginControl("sample", "/status", { method: "GET" })
window.AgenticTalkAPI.fetchPluginControl("sample", "/unload", { method: "POST" })
```

`/unload` 會清除 runtime 狀態、停止背景排程並在 response 寫回後關閉 service。更新部署包時主服務可透過這個 hook 釋放 `plugins/sample/bin/sample-service_windows_amd64.exe`，再覆蓋 plugin 檔案。

Sample 前端會在呼叫 gateway 或 fallback API 時，自動從 cookie 補上授權 header。支援 cookie 名稱包含 `agentic_auth_token`、`auth_token`、`authToken`、`token`、`Authentication` 與 `Authorization`；token 會同時轉成 `Authentication: Bearer ...` 與 `Authorization: Bearer ...`。若主服務已注入 `window.AgenticTalkAPI.authHeaders()`，仍可沿用主服務提供的 header。

主服務也會在 service 啟動後呼叫 host auth hook：

```text
POST /api/sample/plugin/auth
```

payload 範例：

```json
{
  "auth_token": "TOKEN",
  "token_type": "Bearer",
  "header": "Authentication",
  "account": "admin",
  "project": "default",
  "source": "service",
  "expires_at": "2026-05-22T12:00:00Z"
}
```

這個 token 是給 plugin 後端程式呼叫主服務 API 使用，與瀏覽器 cookie 分開。Sample 只保存在記憶體，狀態 API 只回報 `host_auth.available`，不輸出 token。

## 範例能力

- API catalog：`/api/sample/apis`，回傳所有 API 的路徑、方法、說明、request body 與 fetchPlugin/curl 範例。
- hello：`/api/hello`
- health：`/api/health`，`/api/heatlth` 為拼字相容別名。
- lifecycle：`/api/sample/plugin/status|auth|load|unload|reload|registration`
- host auth：接收主服務注入的 auth token，供 plugin 後端程式呼叫主服務 API。
- standard MCP：`POST /mcp`；前端盤點使用 `GET /api/sample/mcp`
- echo：`/api/sample/echo`
- config：`/api/sample/config`
- CRUD：`/api/sample/items`
- 專屬 Skill guide：`/api/sample/skills`、`/api/sample/skills/guide/content`
- 對話 Skill 卡片：`/api/sample/skills/cards`、`/api/sample/skills/cards/{id}`
- SSE stream：`/api/sample/stream`
- background job：`/api/sample/jobs`
- 定期任務：`/api/sample/scheduled-tasks`、`/api/sample/scheduled-tasks/{id}/run`
- 定期任務 LOG：`/api/sample/scheduled-logs?date=YYYY-MM-DD&task_id=TASK_ID`，依日期讀取執行紀錄。
- file payload：`/api/sample/files`
- 即時訊息：
  - `POST /api/sample/msg`：接收主服務 MessageHub 投遞的 webhook。
  - `GET /api/sample/msg/events`：查看 Sample 最近收到的 webhook 訊息。
  - `DELETE /api/sample/msg/events`：清空 Sample webhook 訊息紀錄。
  - `POST /api/sample/msg/publish`：由 Sample 後端使用 host auth token 呼叫主服務 `/api/msg/publish` 發布訊息。
- mock tool call：`/api/sample/tools/run`

## 即時訊息

Sample manifest 已宣告：

```json
{
  "messaging": {
    "webhook": "/api/sample/msg",
    "topics": ["sample.notice", "system.notice"]
  }
}
```

主服務收到符合 topic 的訊息後，會 POST 到 Sample service 的 `/api/sample/msg`，並在 header 帶入 `Authentication` 與 `Authorization`。Sample 會把最近 50 筆 webhook 訊息保存在記憶體，可透過頁面「即時訊息」分頁或 `GET /api/sample/msg/events` 查看。

Sample 後端也示範如何用主服務注入的 host auth token 發布訊息：

```js
window.AgenticTalkAPI.fetchPlugin("sample", "/api/sample/msg/publish", {
  method: "POST",
  body: JSON.stringify({
    topic: "sample.notice",
    event: "created",
    payload: { message: "hello from sample plugin" }
  })
})
```

Browser 端可訂閱主服務 SSE。若目前環境依賴 header auth，應使用 `fetch` stream，因為原生 `EventSource` 不能自訂 `Authentication` / `Authorization` header：

```js
const response = await fetch("/api/msg/subscribe?topics=sample.notice", {
  headers: {
    Accept: "text/event-stream",
    Authentication: "Bearer TOKEN",
    Authorization: "Bearer TOKEN"
  }
})
const reader = response.body.getReader()
```

若部署環境可穩定使用同源登入 cookie，才適合直接使用 `EventSource("/api/msg/subscribe?topics=sample.notice")`。

正式 plugin 若需要不可遺失的事件，應先把事件寫入自己的資料庫，再透過 MessageHub 發通知；MessageHub 的補發緩衝只存在主服務記憶體。

## 標準 MCP

Sample manifest 透過 `mcp_url` 宣告 `http://127.0.0.1:18182/mcp`，主系統會優先用 `2026-07-28` 的 `server/discover` 探索，無法使用時再回退舊版 `initialize`。`/mcp` 只接受無狀態 JSON-RPC POST；前端的 MCP 按鈕使用 `GET /api/sample/mcp` 查看協定與 tools/list 定義。

Sample 提供狀態、握手、項目 CRUD 與背景工作的 MCP 工具。讀取、寫入、刪除分別對應 `mcp_read`、`mcp_write`、`mcp_delete`，預設全部關閉。外部 MCP Client 應連 AgenticService `/mcp/`，不要直接連 Plugin service，以免繞過主系統授權與稽核。完整內容請看 package 根目錄 `MCP.md`。

## 對話式 Skill

Sample Plugin 頁面的「專屬 Skill 對話」會先透過 Sample Plugin API 取得目前 runtime 狀態、registration、config、items、MCP 協定與工具盤點及 skill 清單，再呼叫：

```js
window.AgenticTalkAPI.fetchPlugin("sample", "/api/sample/skills/guide/content")
```

取得 `plugins/sample/skill/guide/SKILL.md` 作為 CHAT 的 SYSTEM PROMPT。這個設計讓 plugin 自己維護對話行為與開發指引，主服務只負責透過 `CallPlugin` gateway 轉交 API。

左側 Skill 卡片由 Sample Plugin API 管理：

```js
window.AgenticTalkAPI.fetchPlugin("sample", "/api/sample/skills/cards")
window.AgenticTalkAPI.fetchPlugin("sample", "/api/sample/skills/cards", { method: "POST", body: JSON.stringify(card) })
window.AgenticTalkAPI.fetchPlugin("sample", `/api/sample/skills/cards/${id}`, { method: "PUT", body: JSON.stringify(card) })
window.AgenticTalkAPI.fetchPlugin("sample", `/api/sample/skills/cards/${id}`, { method: "DELETE" })
```

正式資料寫入 `plugins/sample/skill/skill-cards.json`，deploy package 不應覆蓋這個 runtime 檔案；預設範本只放在 `skill-cards.default.json`。
