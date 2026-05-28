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

## Runtime 設定初始化

Sample 採用通用 plugin 設定檔規則：

- service 會先定位並切換到 plugin package 根目錄；開發時是 `Sample`，匯入主服務後是主服務根目錄。
- 原始碼與部署包只保留 `plugins/sample/config.default.json`。
- `plugins/sample/config.json` 是 runtime 檔案，不納入部署包，也不應提交到源碼管理。
- service 啟動或執行 load hook 時，如果缺少 `config.json`，會讀取 `config.default.json`，補上目前版本與必要欄位，再寫出新的 `config.json`。
- 後續 `/api/sample/config` 的讀寫只操作 `config.json`。
- 匯入主服務更新 plugin 時，主服務應保留既有 `config.json`；只有刪除 plugin 時才清除 runtime 設定。

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
