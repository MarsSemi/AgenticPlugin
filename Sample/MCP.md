# Sample Plugin MCP 開發指南

Sample 提供可被新版 AgenticService 探索的標準、無狀態 MCP JSON-RPC Server，主要用來示範自定義 Plugin 如何宣告工具、區分讀寫刪除權限，並與主系統 MCP Gateway 整合。

## 連線邊界

| 使用者 | 入口 | 說明 |
| --- | --- | --- |
| 外部 MCP Client | AgenticService `/mcp/` | 使用主系統建立的 MCP key，由主系統統一驗證 scope、工具白名單、寫入與刪除權限。|
| AgenticService | Sample service `/mcp` | 主系統在本機探索並呼叫 Sample 工具；不把外部 MCP key 傳給 Plugin。|
| Sample 前端 | `GET /api/sample/mcp` | 只回傳協定版本、方法、權限及工具定義，供 UI 盤點；不是 JSON-RPC transport。|

Sample service 只監聽 `127.0.0.1:18182`。正式環境不應把 Plugin 的 `/mcp` 直接公開到網際網路，也不應讓瀏覽器直接呼叫本機 service URL。

## Manifest 契約

`plugins/sample/plugin.json` 必須保留：

```json
{
  "mcp_url": "http://127.0.0.1:18182/mcp",
  "capabilities": ["mcp", "standard-mcp"],
  "permission_settings": {
    "items": [
      { "key": "mcp_read", "type": "boolean", "default": false },
      { "key": "mcp_write", "type": "boolean", "default": false },
      { "key": "mcp_delete", "type": "boolean", "default": false }
    ]
  }
}
```

三項細部權限預設都關閉。主系統仍會另外套用帳號 Plugin 權限、MCP key scopes、`allow_write`、`allow_delete` 與工具白名單；Plugin 的宣告不能取代主系統授權。

## 協定版本

Sample 支援：

- `2026-07-28`：新版無狀態協定，使用 `server/discover`，每個 request 都帶版本 metadata 與鏡像 header。
- `2025-11-25`、`2025-06-18`、`2025-03-26`：相容舊版 `initialize` 流程。

所有請求使用 `POST /mcp`，`GET /mcp` 固定回 `405 Method Not Allowed`，且不建立 SSE session。單次 request 上限 1 MiB，工具結構化輸出上限 2 MiB；不接受 JSON-RPC batch。

### 新版探索

```http
POST /mcp HTTP/1.1
Content-Type: application/json
MCP-Protocol-Version: 2026-07-28
Mcp-Method: server/discover
```

```json
{
  "jsonrpc": "2.0",
  "id": "discover-1",
  "method": "server/discover",
  "params": {
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": {
        "name": "sample-client",
        "version": "1.0"
      },
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

新版的 `MCP-Protocol-Version`、`Mcp-Method` 必須與 body 一致。呼叫 `tools/call` 時還要提供與 `params.name` 相同的 `Mcp-Name`。

### 舊版初始化

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-11-25",
    "capabilities": {},
    "clientInfo": { "name": "sample-client", "version": "1.0" }
  }
}
```

## MCP 工具

| 工具 | 類型 | 權限鍵 | 說明 |
| --- | --- | --- | --- |
| `sample.status` | 讀取 | `mcp_read` | 讀取 Plugin、設定、項目、工作及 Host Auth 可用狀態，不回傳 token。|
| `sample.hello` | 讀取 | `mcp_read` | 取得 Plugin 識別、版本與目前時間。|
| `sample.items.list` | 讀取 | `mcp_read` | 列出記憶體中的示範項目。|
| `sample.items.get` | 讀取 | `mcp_read` | 依 ID 讀取項目。|
| `sample.items.create` | 寫入 | `mcp_write` | 建立項目。|
| `sample.items.update` | 寫入 | `mcp_write` | 完整更新項目。|
| `sample.items.delete` | 刪除 | `mcp_delete` | 刪除項目，`destructiveHint` 為 `true`。|
| `sample.jobs.start` | 寫入 | `mcp_write` | 啟動非同步示範工作。|
| `sample.jobs.get` | 讀取 | `mcp_read` | 輪詢背景工作狀態。|

每個工具都提供封閉的 `inputSchema`、`outputSchema`、四項 MCP annotations，以及 `_meta["io.agenticservice/permission"]`。寫入與刪除是獨立維度，不能由 `readOnlyHint` 反推 `destructiveHint`。

## 呼叫範例

新版列出工具：

```json
{
  "jsonrpc": "2.0",
  "id": "tools-1",
  "method": "tools/list",
  "params": {
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28"
    }
  }
}
```

新版讀取狀態：

```json
{
  "jsonrpc": "2.0",
  "id": "call-1",
  "method": "tools/call",
  "params": {
    "name": "sample.status",
    "arguments": {},
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28"
    }
  }
}
```

對應 headers：

```text
MCP-Protocol-Version: 2026-07-28
Mcp-Method: tools/call
Mcp-Name: sample.status
```

工具回應同時提供 `content`、`structuredContent` 與 `isError`，讓一般 MCP Client 與主系統結構化工具流程都能使用。

## 實作位置

- `src/sampleplugin/mcp_server.go`：HTTP transport、版本協商、JSON-RPC dispatch 與 protocol validation。
- `src/sampleplugin/mcp_tools.go`：工具 Schema、annotations、權限 metadata 與實際操作。
- `src/sampleplugin/http_api.go`：`GET /api/sample/mcp` 盤點 API、registration 與 config/permission settings。
- `website/sample/index.html`：MCP 盤點按鈕與專屬 Skill 對話的即時 context。

新增 MCP 工具時，必須同步更新工具定義、執行 switch、權限鍵、API 盤點、README 與本文件。涉及外部網路、檔案、程序或資料庫的工具，要把 `openWorldHint`、`destructiveHint` 與執行限制寫清楚。
