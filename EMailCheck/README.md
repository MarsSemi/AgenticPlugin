# EMAIL 檢查 Plugin

這是依照 `../SAMPLE` overlay 結構建立的 EMAIL Plugin，提供 IMAP/POP3 登入、信件讀取、SMTP 回覆與郵件檢查。

IMAP/POP3/SMTP TLS 連線最低使用 TLS 1.2，並支援仍在企業信箱常見的 TLS 1.2 RSA key exchange cipher，以相容較舊但仍要求 SSL/TLS 的 mail server。IMAP 登入會依伺服器能力自動選用 `AUTH=PLAIN`、`AUTH=LOGIN` 或傳統 `LOGIN`；POP3 登入使用 `USER/PASS`。

```text
plugins/email-check        # plugin manifest、config、skill
website/email-check        # 前端頁面
src/emailcheck/api         # HTTP API、config、lifecycle、帳號管理
src/emailcheck/mail        # IMAP、POP3、SMTP、MIME 解析
src/emailcheck/model       # 共用資料模型
src/emailcheck/service     # service entrypoint
cmd/imap-probe             # 獨立 IMAP 測試工具
```

## 獨立啟動

```bash
cd EMailCheck
go run ./src/emailcheck/service
```

另一個終端機啟動 BasePkg dev host：

```bash
cd ../BasePkg
go run ./cmd/plugin-dev-host -plugin-root ../EMailCheck -plugin-id email-check -service http://127.0.0.1:18185
```

開啟：

```text
http://127.0.0.1:19090/email-check/index.html
```

## 主要 API

- `GET /api/email-check/plugin/status`：外掛狀態
- `GET|PUT /api/email-check/config`：讀寫設定
- `GET|POST /api/email-check/accounts`：列出或新增信箱帳號
- `GET|PUT|DELETE /api/email-check/accounts/{id}`：讀取、更新或刪除信箱帳號
- `POST /api/email-check/login`：依帳號收信協定檢查 IMAP 或 POP3 登入，並檢查 SMTP 登入
- `GET|POST /api/email-check/messages`：讀取信件清單
- `GET /api/email-check/messages/{uid}`：讀取單封信件
- `POST /api/email-check/check`：檢查信件；IMAP 支援未讀條件，POP3 會讀取最新信件
- `POST /api/email-check/reply`：回覆指定信件或寄出回覆
- `GET|POST /mcp`：MCP metadata

## Runtime 設定

原始碼只保留 `plugins/email-check/config.default.json`。service 啟動或執行 load hook 時，如果缺少 `plugins/email-check/config.json`，會從預設設定初始化 runtime 設定檔。信箱密碼目前依照通用 config 機制保存在本機 `config.json`，請搭配作業系統檔案權限與信箱 app password 使用。

## 建置部署包

macOS / Linux：

```bash
./build.sh
```

Windows：

```bat
build.bat
```

封裝會輸出到：

```text
dist/email-check-plugin_1.YY.MMDD_build_HHmm.zip
```
