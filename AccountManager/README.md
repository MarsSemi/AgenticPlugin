# Account Manager Plugin

Account Manager 是 AgenticPlugin 架構下的帳號管理外掛，提供帳號新增、修改、刪除、停用、密碼變更、帳密驗證，以及群組層級的 Plugin 存取權限設定。

## 主要功能

- 帳號 CRUD：建立、查詢、更新、刪除帳號。
- 群組 CRUD：建立、查詢、更新、刪除群組，帳號可加入多個群組。
- 工作空間權限：群組可複選主系統工作空間；`default` 固定保留，未設定時只允許 `default`，帳號加入多個群組時取允許清單聯集。
- 密碼保護：密碼以 AES-GCM 格式儲存在設定檔中，不以明文回傳。
- 帳密驗證：`POST /api/account-manager/auth/verify` 可驗證帳號與密碼是否正確。
- 卡片權限：可針對群組設定允許存取的工作台卡片、scope、enabled 狀態與 plugin 自訂功能項。
- MCP 控制：讀取、寫入、刪除三類 MCP 工具可分別開關，預設全部關閉；寫入與刪除工具彼此分離。
- Web UI：上方以 TAB 切換帳號管理、群組管理與設定；設定頁可管理 MCP 工具範圍。

## 預設帳號

第一次啟動時會由 `plugins/account-manager/config.default.json` 建立 `config.json`。

- 帳號：admin
- 密碼：admin123

正式使用前應立即透過 Web UI 或 API 修改密碼，並替換 `encryption.key`。

## 常用 API

- `GET /api/account-manager/plugin/status`
- `POST /api/account-manager/plugin/auth`
- `POST /api/account-manager/plugin/load`
- `GET|PUT /api/account-manager/settings`
- `GET /api/account-manager/accounts`
- `POST /api/account-manager/accounts`
- `GET /api/account-manager/accounts/{id}`
- `PUT /api/account-manager/accounts/{id}`
- `DELETE /api/account-manager/accounts/{id}`
- `PUT /api/account-manager/accounts/{id}/password`
- `GET /api/account-manager/groups`
- `POST /api/account-manager/groups`
- `GET /api/account-manager/groups/{id}`
- `PUT /api/account-manager/groups/{id}`
- `DELETE /api/account-manager/groups/{id}`
- `GET /api/account-manager/groups/{id}/permissions`
- `PUT /api/account-manager/groups/{id}/permissions`
- `POST /api/account-manager/auth/verify`
- `GET /api/account-manager/plugins/permissions`
- `POST /api/account/verify`
- `GET|POST /api/account/permissions`

## MCP 設定

`plugins/account-manager/config.json` 的 `mcp` 區塊控制 Account Manager 發布的 MCP 工具，缺少欄位時會採用 `false`：

```json
{
  "mcp": {
    "read": false,
    "write": false,
    "delete": false
  }
}
```

- `read`：發布帳號、群組與權限查詢，以及不修改資料的帳密驗證工具。
- `write`：發布帳號、密碼、群組與權限的新增或更新工具，不包含刪除。
- `delete`：只發布帳號與群組刪除工具。

三項皆關閉時 `/mcp` 仍可回報停用狀態，但 `tools` 為空陣列；開啟後只會發布對應類別的工具。

收到主系統注入的 TOKEN 後，`/api/account-manager/plugin/auth` 只會依據請求的 `RemoteAddr` 建立主系統 host URL，並呼叫主系統 `/api/reg_account_manager` 註冊本外掛為帳號管理服務；不接受 auth payload、request header、Origin 或 Referer 推導 host URL。

註冊資訊包含 `port: 18186`，供主系統辨識 AccountManager service 的連線埠。

`/api/account/verify` 接受主系統格式：

```json
{
  "account": "user",
  "password": "password",
  "project": "default"
}
```

成功時回傳 `success`、`account`、`project`、`roles`、`permissions`、`expires_in`。

## Extended JWT

帳號可透過 `metadata.jwt` 明確要求主系統簽發 MARS Extended JWT；未啟用的帳號維持既有 opaque session token。

```json
{
  "metadata": {
    "jwt": {
      "enabled": true,
      "claims": {
        "tenant": "factory-1"
      },
      "extensions": [
        {"policy": {"level": 2}},
        {"features": ["audit"]}
      ]
    }
  }
}
```

Account Manager 會把標準帳號、project、角色、權限與有效期 claims 加入第二段，再合併 `claims`；`extensions` 最多兩個。主系統只呼叫 MARS SDK 編碼與簽章，第四、五段由 SDK 使用 AES-GCM 加密，不是明文 Base64。

主系統也只對 Account Manager 的 stdio 通道提供 `auth.token.encode` JSON-RPC method，供後續以新的 payload 或 extensions 重新簽發 token。

### 使用限制

- Account Manager 修改後必須重新編譯；其他只轉送 Bearer token 或呼叫主系統 `/auth/introspect` 的 Plugin 不需更新。
- 同一安全信任邊界且已有正式金鑰供應機制的 Go 元件，若需本機驗證，必須使用 MARS SDK `v0.1.19` 以上；一般 Plugin 應走 introspection，且不得自行依三段／五段解析。
- Extension 的 SDK 明文上限為單段 16 KiB，但正式 token 建議控制在 3 KiB 以內，避免超過常見約 4 KiB 的 Cookie 限制。
- JWT 第二段不是加密資料；不得放密碼、API key 或其他秘密。
- Extension 在 token 傳輸時會加密，但 `metadata.jwt.extensions` 仍是 Account Manager 設定資料，必須另外考量設定檔的存取權限與靜態資料保護。
- 其他 Plugin 若需要 extension 內容，應由主系統驗證後透過授權資料投影 API 提供，不應取得主系統 AES/RSA 金鑰。

## Plugin 功能項註冊結構

各 Plugin 可在 `plugin.json` 或 `_plugin/registration` 回傳 `permission_settings`，讓 Account Manager 在群組的卡片權限列顯示詳細設定按鈕。設定值會保存在群組權限的 `settings` 欄位，帳號驗證與權限查詢時會跟著群組權限一起回傳；主系統或 plugin 可依回傳的 `settings` / `plugin_settings` 決定功能入口是否顯示。

權限物件為了相容既有主系統仍使用欄位名 `plugin_id`，但建議填入 `/api/modules` 回傳的卡片 `id`。舊資料若填 plugin id 仍可相容，主系統會同時比對卡片 id、plugin id、plugin code 與既有 alias。

```json
{
  "permission_settings": {
    "title": "智慧問答配置",
    "description": "可依群組權限控制智慧問答功能入口。",
    "items": [
      {
        "key": "work_history",
        "label": "工作紀錄",
        "description": "顯示工作紀錄與工具紀錄入口。",
        "type": "boolean",
        "default": true
      }
    ]
  }
}
```

目前支援 `type: "boolean"`，用於 ENABLE/DISABLE 類型項目。未註冊 `permission_settings.items` 的 Plugin，詳細設定按鈕會維持停用。

群組權限保存範例：

```json
{
  "plugin_id": "talk",
  "plugin_name": "智慧問答",
  "enabled": true,
  "scopes": ["read"],
  "settings": {
    "work_history": true,
    "export_plan": false
  }
}
```

帳密驗證或權限查詢回應會保留 session 層權限字串，並額外提供工作空間與 plugin 功能項設定：

```json
{
  "success": true,
  "workspace_ids": ["default", "workspace_factory"],
  "permissions": ["smart-qa.read"],
  "settings": {
    "smart-qa": {
      "work_history": true,
      "export_plan": false
    }
  },
  "plugin_settings": {
    "smart-qa": {
      "work_history": true,
      "export_plan": false
    }
  }
}
```

## 建置

```bash
./build.sh
```

## 原始碼結構

- `src/accountmanager/service`：service 啟動入口、工作目錄偵測與 HTTP listen。
- `src/accountmanager/internal/accountmanager`：帳號、群組、權限、AES 密碼、主系統註冊與 HTTP API 實作。

## 清理

```bash
./cleanBuild.sh
```
