# SampleR Plugin Developer Guide

本文件面向 plugin 開發者，整理 SampleR Plugin 的架構、必備功能、開發注意事項與移植自定義 plugin 時應保留的通用規則。一般使用與建置方式請先看 `README.md`；本文件補充更細的實作規範。

## 目錄結構

```text
plugins/sample-r
website/sample-r
src
```

- `plugins/sample-r/plugin.json`：主服務讀取的 manifest，定義 plugin id、UI、service entry、routes、runtime hooks 與 capabilities。
- `plugins/sample-r/config.default.json`：部署用預設設定。runtime 實際使用 `config.json`，部署包不應覆蓋使用者既有 `config.json`。
- `plugins/sample-r/skill/guide/SKILL.md`：專屬 Skill 對話的 system guide。
- `plugins/sample-r/skill/skill-cards.default.json`：對話 Skill 卡片預設值。
- `website/sample-r/index.html`：plugin UI。必須透過主服務 gateway 呼叫 API。
- `src/main.rs`：service 啟動入口，以及 plugin HTTP API、runtime 狀態、config、catalog、定期任務等主要實作。
- `src/mcp.rs`：標準無狀態 MCP JSON-RPC transport、版本協商、工具 Schema、權限 metadata 與實際操作。

## 原始碼分層建議

SampleR 為了示範與封裝方便，目前多數後端邏輯集中在 `src/main.rs`。自定義 plugin 成長後，建議依功能與職責分門別類放在 `src` 下的對應模組，避免單一檔案過大，後續不好管理、測試與拆分。

建議方向：

- `src/{plugin}/service`：service 啟動入口、flag、工作目錄定位。
- `src/{plugin}/api`：HTTP route、request/response、API catalog。
- `src/{plugin}/config`：config 讀寫、預設值、runtime migration。
- `src/{plugin}/scheduler`：定期任務、背景 ticker、防重入控制。
- `src/{plugin}/auth`：host auth、token 保存與 header 組裝。
- `src/{plugin}/skill`：Skill guide、Skill card 管理。
- `src/{plugin}/domain`：實際業務資料模型與服務邏輯。

拆分時仍要維持對外 API 契約穩定；檔案位置可以調整，但 manifest、routes、catalog 與前端呼叫路徑不能無故改變。

## Website 分層建議

SampleR 目前為了方便展示，將 HTML、CSS、JavaScript 集中在 `website/sample-r/index.html`。自定義 plugin 成長後，建議依功能拆分 `website` 下的前端檔案，避免單一 HTML 過大，後續不好維護、重用與拆分。

建議方向：

- `website/{plugin}/index.html`：頁面骨架、主要容器、必要 script/style 引用。
- `website/{plugin}/css/`：樣式檔。可依版面或功能拆成 `layout.css`、`components.css`、`tasks.css` 等。
- `website/{plugin}/js/`：互動邏輯。可依功能拆成 `api.js`、`runtime-status.js`、`tasks.js`、`skill-chat.js` 等。
- `website/{plugin}/assets/`：圖片、圖示、下載範本或其他靜態資源。

拆分 HTML、JS、CSS 時要注意：

- `plugin.json` 的 `website_path` 與 `ui.website_path` 仍要指向入口 HTML。
- 入口 HTML 引用的 CSS/JS 路徑必須能在主服務部署路徑下正確解析。
- JS 模組應依功能分層，不要把 API client、UI render、表單狀態、排程任務、Skill 對話全部混在同一段 script。
- CSS 應依元件或頁面區塊拆分，避免全域樣式互相覆蓋。
- 即使拆分檔案，前端對後端 API 的呼叫路徑與 catalog 說明仍要保持一致。

## 必備基礎功能

所有 plugin 不論業務功能多寡，都應保留下列基礎能力：

- 返回主畫面按鍵：前端第一層操作區必須提供返回主畫面的按鍵，例如連到 `/main.html`。
- 載入偵測：頁面初始化時執行一次狀態偵測即可。一般 API 操作不應持續改寫右上角載入狀態，避免缺少 `plugin.loaded` 的回應被誤判為未載入。
- API catalog：後端必須提供查詢所有 API 的 catalog。SampleR 使用 `GET /api/sample-r/apis` 與 `GET /api/sample-r`。
- `GET /api/hello`：最小握手 API，回傳 plugin id、name、version、time。
- `GET /api/health`：健康檢查 API，回傳 service 健康狀態與 plugin runtime 狀態。SampleR 另保留 `GET /api/heatlth` 作為拼字相容別名。
- lifecycle API：service plugin 應提供 `status`、`load`、`unload`、`reload`、`registration`。
- manifest routes：新增根路徑 API 時，必須同步更新 `plugin.json` 的 `routes`。

其他能力如 CRUD、串流、背景工作、定期任務、檔案 payload、工具呼叫、專屬 Skill 對話，依 plugin 需求保留或移除。

## Manifest 注意事項

`plugin.json` 是主服務載入 plugin 的主要契約。修改時應同步確認：

- `id` 必須穩定，前端 `fetchPlugin("sample-r", ...)`、後端路由、資料目錄都會依賴它。
- `type: "service"` 表示有後端 service；純前端 plugin 才改成 `web`。
- `auto_start: true` 代表主服務啟動時會自動啟動 service。
- `website_path` 與 `ui.website_path` 必須指向實際頁面。
- `entry` 與 `platform_entries` 必須對應建置後 binary。
- `service_url`、`runtime.addr`、`runtime.listen_addr` 要與 service listen address 一致。
- `routes` 必須包含所有主服務要 gateway 的根路徑，例如 `/api/sample-r`、`/api/hello`、`/api/health`。
- `runtime.health` 建議指向 `/api/health`，詳細 runtime 狀態則由 `/api/sample-r/plugin/status` 回報。
- `runtime.auth`、`load`、`unload`、`registration` 要與後端實作一致。
- `runtime.preserve_paths` 要列出更新套件時不可覆蓋的 runtime 資料；SampleR 保留 `config.json`、`runtime/` 與 `skill/skill-cards.json`。
- `mcp_url` 應指向 Plugin service 的標準 MCP 端點；實作後在 `capabilities` 同時宣告 `mcp` 與 `standard-mcp`。
- `permission_settings` 可宣告 Account Manager 管理的細部 boolean 權限；MCP 讀取、寫入與刪除應分開，安全預設為關閉。
- `capabilities` 應反映實際保留能力，避免主服務或使用者誤判。

## 主頁卡片與多卡規則

主服務會把 plugin manifest 展開成主頁工作區塊卡片。新版規則不再把 `plugin_id` 當成唯一卡片 key，而是採用 `plugin_code + card_id`：

- `id`：外掛套件 ID，也就是舊文件常說的 `plugin_id`。它仍是 API gateway、生命週期、匯入、刪除、host auth 與權限設定的主要 ID。
- `plugin_code`：主頁卡片命名空間。可省略；省略時主服務 fallback 使用 `id`。
- `card_id`：同一個 plugin 內單張卡片的 ID。單卡可省略；多卡建議一定要填。
- 主頁回傳的 `module.id`：卡片唯一鍵。單卡預設為 `plugin_code`；多卡為 `plugin_code::card_id`。

相容原則：

- 既有單卡 plugin 保留 `ui` 即可，不需要改成 `cards`。
- 單卡若沒有 `card_id`，卡片 ID 會維持原本的 plugin ID，使用者既有工作區塊群組與排序不會失效。
- 多卡時應使用頂層 `cards`；主服務也相容 `ui.cards`，但為了 manifest 可讀性，正式 plugin 建議使用頂層 `cards`。
- 多卡仍屬於同一個 plugin。前端 API 呼叫、lifecycle 控制與權限仍使用 `plugin_id`，不要拿 `module.id` 呼叫 `/api/plugin/{id}`。

SampleR 實際 manifest 採用保守相容格式：

```json
{
  "id": "sample-r",
  "plugin_code": "sample-r",
  "website_path": "./website/sample-r/index.html",
  "ui": {
    "website_path": "./website/sample-r/index.html",
    "href": "/sample-r/index.html",
    "title": "SampleR Plugin"
  }
}
```

這會產生一張卡，`module.id` 仍是 `sample-r`。

多卡 manifest 範例：

```json
{
  "id": "sample-r",
  "plugin_code": "sample-r",
  "type": "service",
  "cards": [
    {
      "card_id": "main",
      "order": 90,
      "website_path": "./website/sample-r/index.html",
      "href": "/sample-r/index.html",
      "title": "SampleR Plugin",
      "description": "外掛開發參考頁。",
      "icon": "fa-solid fa-puzzle-piece"
    },
    {
      "card_id": "ops",
      "order": 91,
      "website_path": "./website/sample-r/ops.html",
      "href": "/sample-r/ops.html",
      "title": "SampleR 維運",
      "description": "檢查生命週期、狀態與排程。",
      "icon": "fa-solid fa-screwdriver-wrench"
    }
  ]
}
```

多卡設計方法：

- 先以使用者工作流切分入口，而不是以程式碼模組切分。常見入口是「操作」、「查詢」、「報表」、「設定」、「維運」。
- 每張卡都要有不同的 `href` 或清楚不同的頁內工作目的；不要只為同一頁換不同名稱。
- `card_id` 一旦發布後不要任意更名，否則使用者持久化的群組、排序與收合狀態會視為新卡。
- `plugin_code` 一旦發布後也應固定；若需要品牌或套件名稱變更，優先改 `title`，不要改 `plugin_code`。
- 每張卡的 `website_path` 都必須指向部署包內存在的 HTML。若卡片共用同一 HTML，可透過 query string 或 hash 分流，但文件要寫清楚，例如 `/sample-r/index.html#ops`。
- `order` 只決定初始排序。使用者拖曳後會以自己的 layout 設定為準。
- 權限設計應以 plugin 為主要邊界，例如 `sample.read`、`sample.write`、`sample.admin`。若未來需要細分到卡片，應使用完整卡片鍵 `sample::ops`，不要只使用裸 `ops`。
- 文件、API catalog 與 Skill guide 要同步說明多卡入口，避免使用者只看到其中一張卡時不知道它們屬於同一個 plugin。

## 後端 API 規範

所有 JSON API 建議固定回傳：

```json
{
  "success": true
}
```

失敗時固定回傳：

```json
{
  "success": false,
  "error": "錯誤訊息"
}
```

重要 API：

- `/api/hello`：最小握手。
- `/api/health`：健康檢查。
- `/api/sample-r/apis`：API catalog，必須包含 `path`、`method`、`description`、`examples`，有 request body 的 API 應提供 `request_body`。
- `/api/sample-r/plugin/status`：runtime 狀態，包含 `loaded`、`loaded_at`、`last_error`、`host_auth` 等。
- `/api/sample-r/plugin/load`：載入 config、初始化 runtime 狀態、啟動必要背景流程。
- `/api/sample-r/plugin/unload`：清除 runtime 狀態、停止背景流程，並標記 service pending stop。HTTP response 寫回後才非同步關閉 service process。
- `/api/sample-r/plugin/reload`：重新載入 config。
- `/api/sample-r/plugin/registration`：回傳 manifest 類 metadata。

新增 API 時要同步更新：

- `process_request()` route match。
- `api_catalog()`。
- `mcp::tool_definitions()` 與 MCP 工具執行 match，如果該能力要讓主服務工具化使用。
- `plugins/sample-r/README.md`、`README.md` 或 `developer.md`，視 API 重要性更新。
- 前端呼叫點與錯誤處理。

## Service Lifecycle 與 Windows 更新

service plugin 必須讓 `unload` 能釋放執行中的 service binary，否則 Windows 匯入新版 plugin 時可能因 exe 仍被 process 持有而無法刪除。

SampleR 採用 Axum/Tokio graceful shutdown 流程：

- `AppState` 保存 `oneshot::Sender` 與 scheduler `JoinHandle`。
- `/api/sample-r/plugin/unload` 先 abort scheduler、清除 runtime 與 host auth，再回傳 `should_shutdown = true`。
- `handle_request` 先建立完整 JSON response，之後才非同步送出 oneshot shutdown signal。
- `axum::serve(...).with_graceful_shutdown(...)` 收到 signal 後結束 listener，讓 Windows 可釋放正在執行的 exe。
- 不在 handler 內直接 `process::exit`，也不在 response 建立前關閉 listener。

自定義 plugin 若有 Tokio task、DB connection 或檔案 handle，必須在送出 shutdown signal 前先釋放，確保 service process 結束時不再持有 runtime 資源。

## 打包規範

SampleR 的打包流程以 `build.sh` 為唯一主流程：

- 建立乾淨 staging 目錄。
- 複製 `plugins/sample-r`、`website/sample-r` 與根目錄 `README.md`。
- 排除 runtime 檔案，例如 `config.json`、`runtime/`、`skill/skill-cards.json` 與已存在的 `bin/`。
- 交叉編譯目前開發機平台、Linux ARM64、Linux AMD64 與 Windows AMD64。
- 同步更新 staging 內 `plugin.json.version`、`platform_entries` 與 `config.default.json.version`。
- 寫出 `build-info.json` 後建立 zip。

Windows `build.bat` 僅切換到專案根目錄並呼叫 `bash build.sh %*`，避免 Windows 與 macOS/Linux 維護兩套不一致的 staging、manifest 與 zip 邏輯。

## Runtime Config 規則

SampleR 採用通用 runtime config 規則：

- 原始碼與部署包只保留 `config.default.json`。
- `config.json` 是 runtime 檔案，不應提交、不應包進部署包、不應在更新匯入時覆蓋。
- service 啟動或 load hook 發現 `config.json` 不存在時，才由 `config.default.json` 建立。
- 後續設定讀寫都以 `config.json` 為準。
- 定期任務寫在 `scheduled_tasks`，更新部署包時必須保留使用者既有任務。

新增 config 欄位時，請同步更新：

- `sampleConfig` struct。
- `normalizeSampleConfig()` 預設值與相容處理。
- `config.default.json`。
- 相關 API catalog request body 範例。

## 前端開發規範

前端頁面必須符合以下規則：

- 第一層操作區保留返回主畫面按鍵。
- 右上角載入狀態只在頁面初始化時更新。
- 一般 API 操作只更新操作結果區，不應改寫載入狀態。
- 正式環境呼叫 plugin API 時使用 `window.AgenticTalkAPI.fetchPlugin("sample-r", "/api/sample-r/...")`。
- lifecycle 控制使用 `window.AgenticTalkAPI.fetchPluginControl("sample-r", "/status|/load|/unload|/reload")`。
- 若獨立開發環境沒有 `AgenticTalkAPI`，可以 fallback 到同源 API，但正式文件仍應建議 gateway。
- 需要認證時，沿用主服務注入的 auth headers；SampleR 也會從 cookie 補 `Authentication` 與 `Authorization`。
- 新增 tab 或功能區時，確保手機寬度不重疊、不溢出。

## 載入狀態設計

載入狀態的資料來源應限於：

- `/api/sample-r/plugin/status`
- `/api/sample-r/plugin/load`
- `/api/sample-r/plugin/reload`

不要用一般 API 回應推導載入狀態。像 `/api/sample-r/items`、`/api/sample-r/jobs`、`/api/sample-r/scheduled-tasks` 這些 API 不一定包含 `plugin.loaded`，若用 `Boolean(data.plugin?.loaded)` 會把缺欄位誤判成 `false`。

## Host Auth 設計

瀏覽器 cookie 只能供前端使用；後端 service 若需要代表主服務呼叫 API，必須透過 host auth hook 接收 token。

- manifest 宣告 `runtime.auth`。
- 主服務啟動或 reload 後呼叫 `/api/sample-r/plugin/auth`。
- 後端只把 token 保存在記憶體，不寫入 config。
- 狀態 API 只能回報 `host_auth.available`、來源與時間，不可回傳 token 本文。

## 標準 MCP 設計

SampleR 的 `/mcp` 是標準無狀態 JSON-RPC 端點，不是舊式 metadata GET API。前端需要盤點工具時呼叫 `GET /api/sample-r/mcp`；AgenticService 會依 manifest `mcp_url` 直接探索 Plugin service。

- `2026-07-28` 使用 `server/discover`，每次 request 都必須讓 `MCP-Protocol-Version`、`Mcp-Method`、必要的 `Mcp-Name` 與 body `_meta` 一致。
- `2025-11-25`、`2025-06-18`、`2025-03-26` 保留 `initialize` 相容流程。
- `GET /mcp` 回 `405`，不建立 SSE session，也不接受 JSON-RPC batch。
- 工具輸入 Schema 應使用 `additionalProperties: false`，並限制字串長度、ID 格式與輸出大小。
- 每個工具都要明確設定 `readOnlyHint`、`destructiveHint`、`idempotentHint`、`openWorldHint`；寫入不等於刪除，不可互相推導。
- `_meta["io.agenticservice/permission"]` 對應 manifest `permission_settings.items[].key`；SampleR 使用 `mcp_read`、`mcp_write`、`mcp_delete`。
- 計算或記憶體密集工具應設輸入量、並行數、timeout 與輸出限制；長時間工作改用 job ID 與狀態輪詢。
- Plugin `/mcp` 只供本機主系統呼叫。外部 MCP Client 一律使用 AgenticService `/mcp/`，由主系統執行帳號、MCP key、scope、工具白名單與寫刪限制。

詳細 request 範例與工具表請看 `MCP.md`。

## 定期任務設計

定期任務是可選能力。SampleR 的實作重點：

- 任務資料存在 `config.json` 的 `scheduled_tasks`。
- 執行 LOG 依日期寫入 `plugins/sample-r/runtime/scheduled-logs/YYYY-MM-DD.jsonl`。
- `enabled` 控制是否排程。
- `interval_minutes` 限制在合理範圍內，SampleR 上限為 1440。
- `next_run_at`、`last_run_at`、`running_until`、`run_token` 用於排程與防重入。
- 手動執行使用 `/api/sample-r/scheduled-tasks/{id}/run`。
- LOG 讀取使用 `/api/sample-r/scheduled-logs?date=YYYY-MM-DD&task_id=TASK_ID`，前端應提供日期選擇、LOG 數量與上一則/下一則切換。
- 背景 ticker 啟動於 load，unload 時停止。
- 寫入任務時要正規化 ID、時間欄位、payload 與文字長度。

如果自定義 plugin 不需要排程，可以移除前端 tab 與後端 scheduled task API，但應保留基礎 health、hello、catalog 與 lifecycle。

## Skill 對話設計

專屬 Skill 對話是可選能力，但若保留，應符合：

- `SKILL.md` 由 plugin 自己維護。
- 前端送出對話前先取得即時 context，例如 status、registration、config、items、MCP 協定與工具盤點、API catalog。
- system prompt 必須要求使用繁體中文，且回答要對應 live API context。
- Skill 卡片 runtime 檔案 `skill-cards.json` 不應覆蓋；部署包只提供 `skill-cards.default.json`。

## 建置與部署

建置使用：

```bash
./build.sh
```

注意事項：

- build 會清除並重建 `build/` 與 `dist/`。
- zip 內包含 `plugins/sample-r`、`website/sample-r`、service binaries 與 `build-info.json`。
- 不要把 `config.json`、`skill-cards.json`、runtime 目錄包進部署包。
- 更新 Rust 程式後，主服務實際使用的是已編譯 binary；只改原始碼不會影響已匯入的舊 binary。
- 變更 manifest、config default、前端頁面後，重新建置部署包才能讓匯入包包含新內容。

## 語法檢查

一般修改後至少確認：

```bash
cargo check
node -e "JSON.parse(require('fs').readFileSync('plugins/sample-r/plugin.json','utf8')); JSON.parse(require('fs').readFileSync('plugins/sample-r/config.default.json','utf8'));"
```

若修改 `website/sample-r/index.html` 內嵌 script，可額外用 Node 解析 script 語法。除非有特別要求，不需要啟動 service 或打 API；實際功能驗證可由使用者執行。

## 常見錯誤

- 忘記更新 `routes`，導致新增 API 無法透過主服務 gateway。
- 一般 API 回應缺少 `plugin.loaded`，前端卻拿它更新載入狀態。
- 把 `config.json` 包進部署包，導致更新時覆蓋使用者設定。
- 修改 Rust 原始碼後沒有重新建置 binary。
- 修改可工具化能力但忘記同步 MCP tools/list、執行 match、權限設定與文件。
- 在 plugin 後端輸出 auth token 本文。
- 定期任務沒有防重入，導致同一任務重複執行。
