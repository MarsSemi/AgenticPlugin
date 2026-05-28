# Account Manager Plugin

Account Manager 是 AgenticPlugin 架構下的帳號管理外掛，提供帳號新增、修改、刪除、停用、密碼變更、帳密驗證，以及群組層級的 Plugin 存取權限設定。

## 主要功能

- 帳號 CRUD：建立、查詢、更新、刪除帳號。
- 群組 CRUD：建立、查詢、更新、刪除群組，帳號可加入多個群組。
- 密碼保護：密碼以 AES-GCM 格式儲存在設定檔中，不以明文回傳。
- 帳密驗證：`POST /api/account-manager/auth/verify` 可驗證帳號與密碼是否正確。
- Plugin 權限：可針對群組設定允許存取的 plugin、scope 與 enabled 狀態。
- Web UI：上方以 TAB 切換帳號管理與群組管理；帳號頁維護帳號內容，群組頁維護 Plugin 權限。

## 預設帳號

第一次啟動時會由 `plugins/account-manager/config.default.json` 建立 `config.json`。

- 帳號：admin
- 密碼：admin123

正式使用前應立即透過 Web UI 或 API 修改密碼，並替換 `encryption.key`。

## 常用 API

- `GET /api/account-manager/plugin/status`
- `POST /api/account-manager/plugin/auth`
- `POST /api/account-manager/plugin/load`
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

收到主系統注入的 TOKEN 後，`/api/account-manager/plugin/auth` 會呼叫主系統 `/api/reg_account_manager`，註冊本外掛為帳號管理服務。若主系統未在 auth payload 或 request header 中提供可辨識的 host URL，可設定環境變數 `ACCOUNT_MANAGER_HOST_URL`。

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
