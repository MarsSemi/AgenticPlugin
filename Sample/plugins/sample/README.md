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

## 執行目錄規則

Sample service 必須以 plugin package 根目錄作為工作目錄。開發時是 `Sample` 根目錄；匯入主服務後是主服務根目錄。這個根目錄必須能用相對路徑找到 `plugins/sample/plugin.json`、`plugins/sample/config.json` 與 `website/sample/index.html`。

service 啟動時會從目前工作目錄或執行檔所在目錄往上尋找 `plugins/sample/plugin.json`，找到後切換到該根目錄再讀寫 runtime 檔案。若找不到根目錄，會停止啟動並要求從正確目錄執行，避免把 `config.json` 或 skill runtime 檔案寫到錯誤位置。

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

- lifecycle：`/api/sample/plugin/status|auth|load|unload|reload|registration`
- host auth：接收主服務注入的 auth token，供 plugin 後端程式呼叫主服務 API。
- MCP metadata：`/mcp`
- echo：`/api/sample/echo`
- config：`/api/sample/config`
- CRUD：`/api/sample/items`
- 專屬 Skill guide：`/api/sample/skills`、`/api/sample/skills/guide/content`
- 對話 Skill 卡片：`/api/sample/skills/cards`、`/api/sample/skills/cards/{id}`
- SSE stream：`/api/sample/stream`
- background job：`/api/sample/jobs`
- file payload：`/api/sample/files`
- mock tool call：`/api/sample/tools/run`

## 對話式 Skill

Sample Plugin 頁面的「專屬 Skill 對話」會先透過 Sample Plugin API 取得目前 runtime 狀態、registration、config、items、MCP metadata 與 skill 清單，再呼叫：

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
