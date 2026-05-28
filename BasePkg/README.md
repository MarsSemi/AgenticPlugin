# Plugin BasePkg

`BasePkg` 提供通用的 plugin 獨立開發集成，目標是讓 plugin 不需要同時啟動主服務，也能完成前端與 plugin service 的本機開發。

## 內容

- `web/host-adapter.js`：瀏覽器端 Host API adapter。主服務已注入 `window.AgenticTalkAPI` 時不覆蓋；獨立開發時會補上 `fetchPlugin`、`fetchPluginControl`、`apiFetch`、`authHeaders` 與 `startAuthMonitor`。
- `cmd/plugin-dev-host`：通用開發宿主。負責掛載 plugin website、代理 plugin API、提供 BasePkg 前端資源，並以 mock API/SSE 回應 `/talk/models`、`/talk/session/create`、`/talk/rag/*`、`/talk/ask/direct_stream`、`/data/*` 與 `/api/calendar_query` 等常見主服務端點。

## Sample 開發流程

先啟動 Sample plugin service：

```bash
cd Sample
go run ./src/sampleplugin/service
```

再啟動通用 dev host：

```bash
cd ../BasePkg
go run ./cmd/plugin-dev-host \
  -plugin-root ../Sample \
  -plugin-id sample \
  -service http://127.0.0.1:18182
```

開啟：

```text
http://127.0.0.1:19090/sample/index.html
```

## Host Auth Token 注入

瀏覽器端呼叫主服務 gateway 時，頁面通常會帶有主服務設定的 auth token cookie。通用前端規則如下：

- `host-adapter.js` 會依序讀取 `config.authToken`、`window.AgenticPluginDev.authToken`、`localStorage.agentic_auth_token` 與 cookie token。
- cookie 支援名稱：`agentic_auth_token`、`auth_token`、`authToken`、`token`、`Authentication`、`Authorization`。
- 若 token 內容不是 `Bearer ...`，adapter 會自動補成 `Bearer {token}`，並同時送出 `Authentication` 與 `Authorization` header。
- `cmd/plugin-dev-host` 在開發模式會把 `-auth-token` 寫入 `agentic_auth_token` cookie，讓 plugin 頁面重新整理後仍能透過同源 gateway 帶入授權。

正式匯入主服務後，service plugin 若需要由後端程式呼叫主服務 API，不能依賴瀏覽器 cookie。plugin manifest 可在 `runtime.auth` 宣告接收 token 的 hook：

```json
{
  "runtime": {
    "auth": "/api/sample/plugin/auth",
    "load": "/api/sample/plugin/load"
  }
}
```

主服務在 plugin service 啟動後，會先呼叫 `runtime.auth`，再呼叫 `runtime.load`。當使用者重新登入或手動 load/reload plugin 時，也會重新注入目前可用的 token。

`runtime.auth` 建議接收 `POST application/json`：

```json
{
  "auth_token": "TOKEN",
  "token_type": "Bearer",
  "header": "Authentication",
  "account": "admin",
  "project": "default",
  "source": "service|login|local-login|request",
  "expires_at": "2026-05-22T12:00:00Z"
}
```

plugin service 應只在記憶體保存 token，不要寫入 `config.json` 或 log。後續 plugin 程式呼叫主服務 API 時，可使用：

```text
Authentication: Bearer {auth_token}
Authorization: Bearer {auth_token}
```

`cmd/plugin-dev-host` 也提供開發模式注入，預設會送出 `dev-auth-token`：

```bash
go run ./cmd/plugin-dev-host \
  -plugin-root ../Sample \
  -plugin-id sample \
  -service http://127.0.0.1:18182 \
  -auth-token dev-auth-token
```

若要關閉開發模式注入：

```bash
go run ./cmd/plugin-dev-host -inject-auth=false
```

## Plugin 執行目錄規則

Plugin service 必須以自己的 package 根目錄作為工作目錄執行。開發時是 `Sample` 這類 plugin package 根目錄；匯入主服務後是主服務根目錄，也就是同時包含 `plugins/{plugin-id}` 與 `website/{plugin-id}` 的目錄。

這個規則讓 manifest、runtime 設定與前端路徑都能使用同一套相對路徑，例如：

```text
plugins/sample/plugin.json
plugins/sample/config.json
website/sample/index.html
```

不要從 `BasePkg`、`src/...`、`plugins/{plugin-id}/bin` 或其他任意目錄直接啟動 plugin service，除非 service 本身會先定位並切換到 plugin package 根目錄。

`cmd/plugin-dev-host` 只負責以 `-plugin-root` 掛載 plugin website、代理 plugin API 與提供本機 mock host API；它不會代替 plugin service 設定工作目錄。因此獨立開發時應先在 plugin package 根目錄啟動 service，再由 BasePkg dev host 指向同一個 plugin 根目錄。

## Runtime 設定通用規則

所有可獨立部署的 service plugin 都應採用同一套設定檔規則：

- package 只提供 `plugins/{plugin-id}/config.default.json` 作為預設設定範本。
- plugin service 必須先確認工作目錄是 plugin package 根目錄，或自行定位並切換到該根目錄後，才讀寫相對路徑。
- package 不放入 `plugins/{plugin-id}/config.json`，也不應在匯入主服務時覆蓋既有 runtime 設定。
- plugin service 啟動或處理 load hook 時，如果 `config.json` 不存在，必須讀取 `config.default.json`，正規化版本與必要欄位後寫出新的 `config.json`。
- 後續所有設定讀寫都只操作 `config.json`；`config.default.json` 保持唯讀範本用途。
- build script 必須排除 `config.json`、runtime data、cache、state、storage 等部署後產物。
- 主服務匯入 plugin 時必須保留既有 `config.json` 與 runtime 目錄，只有刪除 plugin 才徹底清除。

Go service 可採用以下模式：

```go
func readPluginConfig() (Config, time.Time, error) {
	data, err := os.ReadFile(ConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return initializePluginConfigFromDefault()
		}
		return Config{}, time.Time{}, err
	}
	return parseAndNormalizeConfig(data, ConfigPath)
}

func initializePluginConfigFromDefault() (Config, time.Time, error) {
	cfg := Config{Version: PluginVersion}
	if data, err := os.ReadFile(DefaultConfigPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, time.Time{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, time.Time{}, err
	}
	normalizeConfig(&cfg)
	if err := writePluginConfig(cfg); err != nil {
		return Config{}, time.Time{}, err
	}
	return cfg, configModifiedTime(ConfigPath), nil
}
```

## 匯入主服務

Sample 目前保留可同步回主服務的 overlay 結構：

```text
Sample/
├── plugins/sample
├── website/sample
└── src/sampleplugin
```

匯入主服務時可把上述三個目錄同步到主服務根目錄。正式載入時仍由主服務的 `window.AgenticTalkAPI`、plugin gateway 與 lifecycle 控制接管，BasePkg adapter 不會覆蓋既有宿主 API。

## Web Plugin 開發

純前端 plugin 沒有 service 時，可直接使用 dev host 掛載 website：

```bash
cd BasePkg
go run ./cmd/plugin-dev-host \
  -plugin-root ../FactoryScheduler \
  -plugin-id factory-scheduler \
  -index website/factory/sch/ai_sch.html
```

頁面若在獨立開發時需要主服務 API，可在載入 `/assets/js/api.js` 後依需要注入 BasePkg adapter：

```html
<script>
  if (typeof window.AgenticTalkAPI?.apiFetch !== "function") {
    window.AgenticPluginDev = { pluginId: "factory-scheduler" };
    document.write('<script src="/basepkg/host-adapter.js"><\/script>');
  }
</script>
```

匯入主服務後，`window.AgenticTalkAPI` 已由主服務提供，adapter 不會覆蓋正式宿主 API。
