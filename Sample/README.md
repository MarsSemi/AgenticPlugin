# Sample Plugin

這是從主服務抽出的 Sample Plugin 獨立開發包。

目前保留與主服務相容的 overlay 結構：

```text
plugins/sample        # plugin manifest、config、skill
website/sample        # 前端頁面
src/sampleplugin      # Go service 原始碼
```

## 獨立啟動

Sample service 必須在 plugin package 根目錄執行，也就是此目錄。service 啟動時會檢查並切換到包含 `plugins/sample/plugin.json` 的根目錄，確保 `plugins/sample/config.json`、`plugins/sample/skill/...` 與 `website/sample/...` 等相對路徑都以同一個根目錄解析。

```bash
cd Sample
go run ./src/sampleplugin/service
```

另一個終端機啟動 BasePkg dev host：

```bash
cd ../BasePkg
go run ./cmd/plugin-dev-host -plugin-root ../Sample -plugin-id sample -service http://127.0.0.1:18182
```

開啟：

```text
http://127.0.0.1:19090/sample/index.html
```

## 建置部署包

macOS / Linux：

```bash
./build.sh
```

macOS Finder 可雙擊：

```text
build.command
```

Windows：

```bat
build.bat
```

Windows 的 `build.bat` 只負責切到專案根目錄後呼叫 `bash build.sh`，讓 macOS / Linux / Windows 都走同一套 staging、manifest 版本同步、`platform_entries` 產生與 zip 打包邏輯。Windows 環境需可執行 `bash`。

build 會產生：

```text
dist/sample-plugin_1.YY.MMDD_build_HHmm.zip
```

封裝內只包含部署主服務所需的 `plugins/sample`、`website/sample`、已編譯 service binaries 與 `build-info.json`。版號格式為 `1.YY.MMDD build HHmm`，並會同步寫入 zip 內的 `plugin.json`、`config.default.json` 與 service binary 回報版本。

封裝名稱與 staging 目錄不區分 OS；同一個 zip 會包含目前開發機平台，加上 Linux ARM64、Linux x64 與 Windows x64。Go 的 x64 架構名稱為 `amd64`，平台差異由執行檔名稱區分，例如：

```text
plugins/sample/bin/sample-service_darwin_arm64
plugins/sample/bin/sample-service_linux_arm64
plugins/sample/bin/sample-service_linux_amd64
plugins/sample/bin/sample-service_windows_amd64.exe
```

主服務匯入後會依 `plugin.json` 的 `platform_entries`，按照目前 OS/ARCH 選擇對應執行檔。

清除建置與 runtime 產物：

```bash
./cleanBuild.sh
```

## Plugin 必備基礎功能

所有 plugin 不論是否有額外業務功能，都應保留以下基礎功能：

- 返回主畫面按鍵：前端頁面第一層操作區必須提供返回主畫面的按鍵，例如 Sample 頁面的 `回到主畫面` 連到 `/main.html`。
- 載入偵測：頁面初始化時檢查 plugin 載入狀態即可；載入狀態不應在每次一般 API 操作後反覆改寫，避免一般 API 回應缺少 `plugin.loaded` 時誤判為未載入。
- API catalog：必須提供可查詢所有 API 的 catalog，例如 `GET /api/sample/apis` 與 `GET /api/sample`，回傳 API 路徑、方法、說明、request body 與呼叫範例。
- `GET /api/hello`：基礎握手 API，用來確認 plugin service 可回應最小資訊。
- `GET /api/health`：基礎健康檢查 API，用來確認 service 存活與 runtime 狀態。Sample 也保留 `GET /api/heatlth` 作為拼字相容別名。
- lifecycle：service plugin 應提供 `status`、`load`、`unload`、`reload`、`registration` 等生命週期 API。`unload` 必須先回應主服務，再非同步關閉 service process，避免 Windows 更新 plugin 時因 exe 仍被執行中程序鎖住而無法刪除。
- messaging：service plugin 若需要即時訊息，可在 `plugin.json` 宣告 `messaging.webhook` 與 `messaging.topics`，並提供 webhook API 接收主服務 MessageHub 投遞。
- standard MCP：若要讓主系統或外部 MCP Client 使用 Plugin 能力，manifest 應宣告 `mcp_url` 與 `standard-mcp`，並提供標準無狀態 JSON-RPC `POST /mcp`。外部 Client 應連主系統 `/mcp/`，由主系統統一授權。

其餘能力，例如 CRUD、串流、背景工作、定期任務、檔案上傳、即時訊息、工具呼叫、專屬 Skill 對話等，依 plugin 實際需求實作。

## Runtime 設定初始化

Sample 採用通用 plugin 設定檔規則：

- service 會先定位並切換到 plugin package 根目錄；開發時是 `Sample`，匯入主服務後是主服務根目錄。
- 原始碼與部署包只保留 `plugins/sample/config.default.json`。
- `plugins/sample/config.json` 是 runtime 檔案，不納入部署包，也不應提交到源碼管理。
- service 啟動或執行 load hook 時，如果缺少 `config.json`，會讀取 `config.default.json`，補上目前版本與必要欄位，再寫出新的 `config.json`。
- 後續 `/api/sample/config` 的讀寫只操作 `config.json`。
- 定期任務清單寫在 `config.json` 的 `scheduled_tasks`，可透過 `/api/sample/scheduled-tasks` 管理；部署更新不覆蓋既有 runtime 任務。
- 匯入主服務更新 plugin 時，主服務應保留既有 `config.json`；只有刪除 plugin 時才清除 runtime 設定。
- `runtime.preserve_paths` 明確列出 `config.json`、`runtime/` 與 `skill/skill-cards.json`，讓新版主系統替換 Plugin 目錄時仍保留使用者資料。

這個規則讓 plugin 可以獨立開發，也能在匯入主服務後保留使用者既有設定。

## 匯入主服務

同步以下目錄到主服務根目錄即可：

```text
plugins/sample
website/sample
src/sampleplugin
```

正式運作時頁面會載入主服務的 `/assets/js/api.js`，使用 `window.AgenticTalkAPI` 自動帶入 authentication；獨立開發時若沒有完整 API client，頁面會 fallback 到同源 `/api/sample` 與 `/api/plugin/sample/_plugin` 路徑。

Sample 前端也會在呼叫 `fetchPlugin`、`fetchPluginControl`、`apiFetch` 或 fallback `fetch` 時，從 cookie 補上 auth header。支援的 cookie 名稱包含 `agentic_auth_token`、`auth_token`、`authToken`、`token`、`Authentication` 與 `Authorization`。若 cookie value 不是 `Bearer ...`，頁面會自動補成 `Bearer {token}`，並同時帶出 `Authentication` 與 `Authorization`。

## 標準 MCP

Sample 已實作新版 AgenticService 使用的標準 MCP Server：

- Plugin 內部端點：`POST /mcp`。
- 前端盤點端點：`GET /api/sample/mcp`，只回傳協定與工具定義。
- 支援協定：`2026-07-28`、`2025-11-25`、`2025-06-18`、`2025-03-26`。
- 支援方法：新版 `server/discover`、舊版 `initialize`，以及 `ping`、`tools/list`、`tools/call`。
- 權限分成 `mcp_read`、`mcp_write`、`mcp_delete`，預設全部關閉。

外部 MCP Client 不應直接連 `127.0.0.1:18182/mcp`，而是連 AgenticService `/mcp/`，使用主系統 MCP key、scope、工具白名單、寫入與刪除限制。完整協定與工具清單請看 `MCP.md`。

## 主頁卡片與多卡設計

主服務的主頁工作區塊已將「外掛套件」與「主頁卡片」分開處理：

- `plugin_id`：外掛套件 ID，對應 `plugins/{plugin_id}`，用於 API gateway、生命週期、匯入、刪除與權限。
- `plugin_code`：主頁卡片命名空間，可省略；省略時主服務使用 `id`。
- `card_id`：單張卡片在同一個 `plugin_code` 下的 ID，可省略。省略時代表預設卡片，舊版單卡外掛會維持原本卡片 ID。
- `module.id`：主頁卡片唯一鍵。單卡預設為 `plugin_code`；多卡為 `plugin_code::card_id`。

Sample 的 `plugin.json` 保留舊版單卡 `ui` 格式，並加上 `plugin_code: "sample"` 作為新規格示範；因為未設定 `card_id`，主頁卡片 ID 仍是 `sample`，不會破壞既有工作區塊排序與群組設定。

同一個 plugin 要顯示多張主頁卡時，建議改用頂層 `cards`，每張卡明確設定穩定的 `card_id`：

```json
{
  "id": "sample",
  "plugin_code": "sample",
  "type": "service",
  "cards": [
    {
      "card_id": "main",
      "order": 90,
      "website_path": "./website/sample/index.html",
      "href": "/sample/index.html",
      "title": "Sample Plugin",
      "icon": "fa-solid fa-puzzle-piece"
    },
    {
      "card_id": "ops",
      "order": 91,
      "website_path": "./website/sample/ops.html",
      "href": "/sample/ops.html",
      "title": "Sample 維運",
      "icon": "fa-solid fa-screwdriver-wrench"
    }
  ]
}
```

設計多卡時，應讓每張卡代表一個清楚入口，例如「管理」、「報表」、「設定」或「現場操作」。不要為同一頁面建立多張只換名稱的卡；這會讓權限、群組排序與使用者理解都變複雜。多卡只改主頁入口，不改 API gateway 規則；前端仍應使用 `fetchPlugin("sample", "/api/sample/...")` 呼叫同一個 plugin 後端。

## API Catalog

Sample 提供通用 API catalog：

```text
GET /api/hello
GET /api/health
GET /api/sample/apis
GET /api/sample
```

回傳內容包含 `apis` 清單，每筆會提供 `path`、`method`、`description`、選用的 `request_body`，以及 `examples.fetch_plugin` 與 `examples.curl`。主服務頁面建議使用：

```js
window.AgenticTalkAPI.fetchPlugin("sample", "/api/sample/apis", { method: "GET" })
```

## Host Auth Token

Sample manifest 已宣告：

```json
{
  "runtime": {
    "auth": "/api/sample/plugin/auth"
  }
}
```

主服務啟動或手動 load/reload Sample service 後，會先呼叫 `/api/sample/plugin/auth` 將目前可用的 auth token 傳入 plugin 程式，再呼叫 `/api/sample/plugin/load`。這補足瀏覽器 cookie 只能供前端使用的限制，讓 plugin 後端程式也能代表主服務呼叫需要授權的 API。

Sample 只把 token 保存在記憶體，不寫入 `config.json`。程式需要呼叫主服務 API 時，可使用：

```go
headers := api.HostAuthHeaders()
```

回傳會包含 `Authentication: Bearer ...` 與 `Authorization: Bearer ...`。狀態 API 只會顯示 `host_auth.available`、來源與時間，不會回傳 token 本文。

獨立開發時，BasePkg dev host 預設會注入 `dev-auth-token`；可用 `-auth-token` 指定，或用 `-inject-auth=false` 關閉。

## 即時訊息範例

Sample 已在 `plugins/sample/plugin.json` 宣告：

```json
{
  "messaging": {
    "webhook": "/api/sample/msg",
    "topics": ["sample.notice", "system.notice"]
  }
}
```

主服務會把符合 topic 的訊息 POST 到 `/api/sample/msg`。Sample 會保留最近 50 筆 webhook 訊息在記憶體中，可用：

```text
GET    /api/sample/msg/events
DELETE /api/sample/msg/events
```

Sample 頁面的「即時訊息」分頁提供三個示範：

- `Plugin 發布`：呼叫 `POST /api/sample/msg/publish`，由 Sample 後端用 host auth token 轉呼叫主服務 `/api/msg/publish`。
- `Browser 發布`：由頁面直接呼叫主服務 `/api/msg/publish`。
- `訂閱 SSE`：由頁面使用 `fetch` 讀取 `/api/msg/subscribe?topics=sample.notice` 的 SSE stream，並顯示 browser 收到的即時事件。Sample UI 使用 `fetch` 是為了能帶 `Authentication` / `Authorization` header；若環境只靠同源 cookie 認證，也可以改用原生 `EventSource`。

後端發布範例：

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

若 plugin 後端尚未收到 `runtime.auth` 注入的 `host_url` 與 auth token，`/api/sample/msg/publish` 會回傳錯誤。正式環境應由主服務啟動或 load plugin，讓 host auth 先注入後再發布。

## Service 停止流程

Sample 採用 AccountManager 同款 service 停止流程：

1. 主服務呼叫 `POST /api/sample/plugin/unload`。
2. 後端清除 runtime 狀態、停止背景排程，並標記 pending stop。
3. HTTP response 完整寫回並 flush 後，才非同步呼叫 `http.Server.Shutdown()`。
4. 若 graceful shutdown 在 1 秒內失敗，才 fallback `server.Close()`。

這個流程可確保安裝或更新 plugin 時，Windows 不會因 `plugins/sample/bin/sample-service_windows_amd64.exe` 仍被舊 process 持有而出現 `Access is denied`。
