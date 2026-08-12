use axum::{
    body::Body,
    http::{HeaderMap, HeaderValue, Method, StatusCode, header},
    response::{IntoResponse, Response},
};
use base64::Engine;
use serde::Deserialize;
use serde_json::{Map, Value, json};

use super::{
    AppState, PLUGIN_NAME, SampleItem, SampleJob, VERSION, ensure_loaded, list_items, next_id,
    now_rfc3339, prune_jobs, spawn_job, status_payload, with_cors,
};

const MCP_PROTOCOL_LATEST: &str = "2026-07-28";
const MCP_PROTOCOL_LEGACY_LATEST: &str = "2025-11-25";
const MCP_PROTOCOL_METADATA_KEY: &str = "io.modelcontextprotocol/protocolVersion";
const MCP_MAX_REQUEST_BYTES: usize = 1024 * 1024;
const MCP_MAX_RESPONSE_BYTES: usize = 2 * 1024 * 1024;
const SUPPORTED_PROTOCOLS: [&str; 4] = [
    MCP_PROTOCOL_LATEST,
    MCP_PROTOCOL_LEGACY_LATEST,
    "2025-06-18",
    "2025-03-26",
];

#[derive(Debug, Deserialize)]
struct McpRpcRequest {
    jsonrpc: String,
    #[serde(default)]
    id: Value,
    method: String,
    #[serde(default)]
    params: Value,
}

struct ProtocolError {
    status: StatusCode,
    code: i64,
    message: &'static str,
    data: Value,
}

pub(crate) fn is_mcp_path(path: &str) -> bool {
    path.trim_matches('/').eq_ignore_ascii_case("mcp")
}

pub(crate) fn metadata_response(method: &Method) -> Value {
    if method != Method::GET {
        return json!({"success": false, "error": "method not allowed"});
    }
    json!({"success": true, "mcp": metadata()})
}

pub(crate) fn permission_settings() -> Value {
    json!({
        "title": "SampleR MCP 權限",
        "description": "示範 Account Manager 如何保存 Rust Plugin 的 MCP 讀取、寫入與刪除能力偏好；實際呼叫仍由主系統帳號權限、MCP key scopes、allow_write、allow_delete 與工具白名單共同限制。",
        "items": [
            {"key":"mcp_read","label":"MCP 讀取","description":"允許讀取 SampleR 狀態、項目與背景工作結果。","type":"boolean","default":false},
            {"key":"mcp_write","label":"MCP 寫入","description":"允許建立或更新 SampleR 項目，以及啟動背景工作。","type":"boolean","default":false},
            {"key":"mcp_delete","label":"MCP 刪除","description":"允許刪除 SampleR 項目。","type":"boolean","default":false}
        ]
    })
}

pub(crate) fn config_settings() -> Value {
    json!({
        "title": "SampleR Plugin 設定",
        "description": "示範新版主系統以參數表單修改 Rust Plugin 基本設定，並保留未顯示的完整 JSON。",
        "mode": "json",
        "reload_after_save": true,
        "basic_parameters": [
            {"key":"title","label":"顯示名稱","type":"string","default":PLUGIN_NAME,"help":"主頁主要卡片顯示名稱。"},
            {"key":"default_group","label":"預設群組","type":"string","default":"開發工具","help":"使用者尚未自行編排主頁時採用的建議群組。"},
            {"key":"message","label":"範例訊息","type":"string","default":"Hello from SampleR Plugin","help":"SampleR 頁面與 API 使用的示範訊息。"}
        ]
    })
}

pub(crate) fn handle_http(
    state: &AppState,
    method: &Method,
    headers: &HeaderMap,
    body: &str,
) -> Response {
    if method != Method::POST {
        let mut response = mcp_json_response(
            StatusCode::METHOD_NOT_ALLOWED,
            json!({"error":"此 MCP Server 採無狀態 HTTP；請使用 POST。"}),
            MCP_PROTOCOL_LATEST,
        );
        response
            .headers_mut()
            .insert(header::ALLOW, HeaderValue::from_static("POST"));
        return response;
    }
    if body.len() > MCP_MAX_REQUEST_BYTES {
        return mcp_json_response(
            StatusCode::PAYLOAD_TOO_LARGE,
            json!({"error":"MCP request exceeds 1 MiB"}),
            MCP_PROTOCOL_LATEST,
        );
    }
    let trimmed = body.trim();
    if trimmed.is_empty() || trimmed.starts_with('[') {
        return rpc_error(
            StatusCode::OK,
            Value::Null,
            -32600,
            "Invalid Request",
            json!("不接受空白或 batch request"),
            MCP_PROTOCOL_LATEST,
        );
    }
    let request: McpRpcRequest = match serde_json::from_str(trimmed) {
        Ok(request) => request,
        Err(err) => {
            return rpc_error(
                StatusCode::OK,
                Value::Null,
                -32700,
                "Parse error",
                json!(err.to_string()),
                MCP_PROTOCOL_LATEST,
            );
        }
    };
    if request.jsonrpc != "2.0" || request.method.trim().is_empty() {
        return rpc_error(
            StatusCode::OK,
            request.id,
            -32600,
            "Invalid Request",
            Value::Null,
            MCP_PROTOCOL_LATEST,
        );
    }
    let (protocol, modern) = match validate_protocol(headers, &request) {
        Ok(result) => result,
        Err(err) => {
            return rpc_error(
                err.status,
                request.id,
                err.code,
                err.message,
                err.data,
                MCP_PROTOCOL_LATEST,
            );
        }
    };
    dispatch(state, request, &protocol, modern)
}

fn dispatch(state: &AppState, request: McpRpcRequest, protocol: &str, modern: bool) -> Response {
    let method = request.method.trim();
    if method.starts_with("notifications/") {
        return mcp_empty_response(StatusCode::ACCEPTED, protocol);
    }
    match method {
        "server/discover" => {
            if !modern {
                return rpc_error(
                    StatusCode::OK,
                    request.id,
                    -32601,
                    "Method not found",
                    json!({"method":method}),
                    protocol,
                );
            }
            rpc_result(
                request.id,
                json!({
                    "resultType":"complete",
                    "supportedVersions":SUPPORTED_PROTOCOLS,
                    "capabilities":server_capabilities(),
                    "instructions":instructions(),
                    "ttlMs":60000,
                    "cacheScope":"private",
                    "_meta":{"io.modelcontextprotocol/serverInfo":{"name":"SampleR Plugin MCP","version":VERSION}}
                }),
                protocol,
            )
        }
        "initialize" => {
            if modern {
                return rpc_error(
                    StatusCode::NOT_FOUND,
                    request.id,
                    -32601,
                    "Method not found",
                    json!({"method":method,"supported":SUPPORTED_PROTOCOLS}),
                    protocol,
                );
            }
            let requested = request
                .params
                .get("protocolVersion")
                .and_then(Value::as_str)
                .unwrap_or("");
            let selected = if is_supported_protocol(requested) && requested != MCP_PROTOCOL_LATEST {
                requested
            } else {
                MCP_PROTOCOL_LEGACY_LATEST
            };
            rpc_result(
                request.id,
                json!({
                    "protocolVersion":selected,
                    "capabilities":server_capabilities(),
                    "serverInfo":{"name":"SampleR Plugin MCP","version":VERSION},
                    "instructions":instructions()
                }),
                selected,
            )
        }
        "ping" => rpc_result(request.id, json!({}), protocol),
        "tools/list" => {
            let mut result = json!({"tools":tool_definitions()});
            if modern {
                result["resultType"] = json!("complete");
            }
            rpc_result(request.id, result, protocol)
        }
        "tools/call" => {
            let (name, arguments) = match tool_call_params(&request.params) {
                Ok(params) => params,
                Err(err) => {
                    return rpc_error(
                        StatusCode::OK,
                        request.id,
                        -32602,
                        "Invalid tools/call params",
                        json!(err),
                        protocol,
                    );
                }
            };
            match call_tool(state, &name, &arguments) {
                Ok(payload) => rpc_result(request.id, call_envelope(payload, false), protocol),
                Err(err) => rpc_result(
                    request.id,
                    call_envelope(json!({"success":false,"error":err}), true),
                    protocol,
                ),
            }
        }
        _ => rpc_error(
            if modern {
                StatusCode::NOT_FOUND
            } else {
                StatusCode::OK
            },
            request.id,
            -32601,
            "Method not found",
            json!({"method":method}),
            protocol,
        ),
    }
}

fn validate_protocol(
    headers: &HeaderMap,
    request: &McpRpcRequest,
) -> Result<(String, bool), ProtocolError> {
    let header_version = header_text(headers, "MCP-Protocol-Version");
    let body_version = request
        .params
        .get("_meta")
        .and_then(Value::as_object)
        .and_then(|metadata| metadata.get(MCP_PROTOCOL_METADATA_KEY))
        .and_then(Value::as_str)
        .unwrap_or("")
        .trim()
        .to_string();
    let requested = if body_version.is_empty() {
        header_version.as_str()
    } else {
        body_version.as_str()
    };
    if !requested.is_empty() && !is_supported_protocol(requested) {
        return Err(ProtocolError {
            status: StatusCode::BAD_REQUEST,
            code: -32022,
            message: "Unsupported protocol version",
            data: json!({"supported":SUPPORTED_PROTOCOLS,"requested":requested}),
        });
    }
    let modern = header_version == MCP_PROTOCOL_LATEST || body_version == MCP_PROTOCOL_LATEST;
    if modern {
        if header_version.is_empty() || body_version.is_empty() || header_version != body_version {
            return Err(header_mismatch(
                "MCP-Protocol-Version 與 request _meta 不一致",
            ));
        }
        if header_text(headers, "Mcp-Method") != request.method {
            return Err(header_mismatch("Mcp-Method 與 JSON-RPC method 不一致"));
        }
        if request.method == "tools/call" {
            let expected_name = request
                .params
                .get("name")
                .and_then(Value::as_str)
                .unwrap_or("")
                .trim();
            let header_name =
                decode_mirrored_header(&header_text(headers, "Mcp-Name")).unwrap_or_default();
            if expected_name.is_empty() || header_name != expected_name {
                return Err(header_mismatch("Mcp-Name 與 JSON-RPC params 不一致"));
            }
        }
        return Ok((body_version, true));
    }
    if header_version.is_empty() {
        Ok(("2025-03-26".to_string(), false))
    } else {
        Ok((header_version, false))
    }
}

fn header_mismatch(reason: &str) -> ProtocolError {
    ProtocolError {
        status: StatusCode::BAD_REQUEST,
        code: -32020,
        message: "Header mismatch",
        data: json!({"reason":reason}),
    }
}

fn header_text(headers: &HeaderMap, name: &str) -> String {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .unwrap_or("")
        .trim()
        .to_string()
}

fn decode_mirrored_header(value: &str) -> Result<String, String> {
    let value = value.trim();
    if !value.starts_with("=?base64?") || !value.ends_with("?=") {
        return Ok(value.to_string());
    }
    let encoded = value
        .strip_prefix("=?base64?")
        .and_then(|text| text.strip_suffix("?="))
        .unwrap_or("");
    base64::engine::general_purpose::STANDARD
        .decode(encoded)
        .map_err(|err| err.to_string())
        .and_then(|bytes| String::from_utf8(bytes).map_err(|err| err.to_string()))
}

fn is_supported_protocol(version: &str) -> bool {
    SUPPORTED_PROTOCOLS.contains(&version.trim())
}

fn server_capabilities() -> Value {
    json!({"tools":{"listChanged":false}})
}

fn instructions() -> &'static str {
    "此 Server 是 SampleR Plugin 的標準 MCP 開發範例，適合示範高計算量或高記憶體用量的 Rust Plugin。請先用 tools/list 取得封閉 Schema；讀取、寫入與刪除工具具獨立 annotations 與 permission key。SampleR 資料只供示範，不應當成正式持久化資料庫。"
}

fn metadata() -> Value {
    json!({
        "name":"SampleR Plugin MCP",
        "description":"標準無狀態 MCP JSON-RPC Rust 開發範例。",
        "version":VERSION,
        "language":"rust",
        "endpoint":"/mcp",
        "transport":"streamable-http-stateless-json",
        "supported_protocols":SUPPORTED_PROTOCOLS,
        "methods":["server/discover","initialize","ping","tools/list","tools/call"],
        "permission_settings":permission_settings(),
        "tools":tool_definitions()
    })
}

fn tool_definitions() -> Value {
    json!([
        tool(
            "sample-r.status",
            "讀取 SampleR 狀態",
            "讀取 Plugin 載入、設定、項目、工作及 Host Auth 可用狀態；不回傳 token。",
            object_schema(json!({}), &[]),
            output_schema("status"),
            true,
            false,
            true,
            false,
            "mcp_read"
        ),
        tool(
            "sample-r.hello",
            "SampleR 握手",
            "取得 SampleR Plugin ID、名稱、版本、語言及目前時間。",
            object_schema(json!({}), &[]),
            output_schema("hello"),
            true,
            false,
            true,
            false,
            "mcp_read"
        ),
        tool(
            "sample-r.items.list",
            "列出 SampleR 項目",
            "列出目前 runtime 記憶體中的 SampleR 項目。",
            object_schema(json!({}), &[]),
            output_schema("items"),
            true,
            false,
            true,
            false,
            "mcp_read"
        ),
        tool(
            "sample-r.items.get",
            "讀取 SampleR 項目",
            "依穩定 ID 讀取一筆 SampleR 項目。",
            object_schema(json!({"id":id_schema("項目 ID。")}), &["id"]),
            output_schema("item"),
            true,
            false,
            true,
            false,
            "mcp_read"
        ),
        tool(
            "sample-r.items.create",
            "建立 SampleR 項目",
            "建立一筆記憶體內 SampleR 項目；省略 ID 時由服務產生。",
            object_schema(item_properties(true), &["name"]),
            output_schema("item"),
            false,
            false,
            false,
            false,
            "mcp_write"
        ),
        tool(
            "sample-r.items.update",
            "更新 SampleR 項目",
            "依 ID 完整更新一筆 SampleR 項目的名稱、值與 metadata。",
            object_schema(item_properties(false), &["id", "name"]),
            output_schema("item"),
            false,
            false,
            true,
            false,
            "mcp_write"
        ),
        tool(
            "sample-r.items.delete",
            "刪除 SampleR 項目",
            "依 ID 刪除一筆 SampleR 項目。",
            object_schema(json!({"id":id_schema("要刪除的項目 ID。")}), &["id"]),
            output_schema("delete"),
            false,
            true,
            true,
            false,
            "mcp_delete"
        ),
        tool(
            "sample-r.jobs.start",
            "啟動 SampleR 背景工作",
            "啟動非同步 Rust 示範工作並立即回傳 job ID；使用 sample-r.jobs.get 輪詢進度。",
            object_schema(
                json!({"message":{"type":"string","maxLength":500,"default":"sample-r background job"}}),
                &[]
            ),
            output_schema("job"),
            false,
            false,
            false,
            false,
            "mcp_write"
        ),
        tool(
            "sample-r.jobs.get",
            "讀取 SampleR 背景工作",
            "依 job ID 讀取背景工作的狀態與進度。",
            object_schema(json!({"id":id_schema("背景工作 ID。")}), &["id"]),
            output_schema("job"),
            true,
            false,
            true,
            false,
            "mcp_read"
        )
    ])
}

#[allow(clippy::too_many_arguments)]
fn tool(
    name: &str,
    title: &str,
    description: &str,
    input_schema: Value,
    output_schema: Value,
    read_only: bool,
    destructive: bool,
    idempotent: bool,
    open_world: bool,
    permission: &str,
) -> Value {
    json!({
        "name":name,
        "title":title,
        "description":description,
        "inputSchema":input_schema,
        "outputSchema":output_schema,
        "annotations":{
            "title":title,
            "readOnlyHint":read_only,
            "destructiveHint":destructive,
            "idempotentHint":idempotent,
            "openWorldHint":open_world
        },
        "_meta":{
            "io.agenticservice/permission":permission,
            "io.agenticservice.sample-r/executionLimits":{
                "request_max_bytes":MCP_MAX_REQUEST_BYTES,
                "response_max_bytes":MCP_MAX_RESPONSE_BYTES,
                "storage":"runtime_memory",
                "language":"rust"
            }
        }
    })
}

fn object_schema(properties: Value, required: &[&str]) -> Value {
    let mut schema = json!({"type":"object","properties":properties,"additionalProperties":false});
    if !required.is_empty() {
        schema["required"] = json!(required);
    }
    schema
}

fn id_schema(description: &str) -> Value {
    json!({"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9._-]+$","description":description})
}

fn item_properties(id_optional: bool) -> Value {
    json!({
        "id":id_schema(if id_optional {"項目 ID；省略時由服務產生。"} else {"項目 ID。"}),
        "name":{"type":"string","minLength":1,"maxLength":200,"description":"項目名稱。"},
        "value":{"description":"任意 JSON 相容示範值。"},
        "metadata":{"type":"object","additionalProperties":true,"description":"選填的 JSON metadata。"}
    })
}

fn output_schema(kind: &str) -> Value {
    let mut properties = Map::from_iter([("success".to_string(), json!({"type":"boolean"}))]);
    match kind {
        "status" => {
            properties.insert(
                "plugin".to_string(),
                json!({"type":"object","additionalProperties":true}),
            );
        }
        "hello" => {
            properties.insert(
                "hello".to_string(),
                json!({"type":"object","additionalProperties":true}),
            );
        }
        "items" => {
            properties.insert(
                "items".to_string(),
                json!({"type":"array","items":{"type":"object","additionalProperties":true}}),
            );
        }
        "item" => {
            properties.insert(
                "item".to_string(),
                json!({"type":"object","additionalProperties":true}),
            );
        }
        "delete" => {
            properties.insert("id".to_string(), json!({"type":"string"}));
            properties.insert("deleted".to_string(), json!({"type":"boolean"}));
        }
        "job" => {
            properties.insert(
                "job".to_string(),
                json!({"type":"object","additionalProperties":true}),
            );
        }
        _ => {}
    }
    json!({"type":"object","properties":properties,"required":["success"],"additionalProperties":false})
}

fn tool_call_params(params: &Value) -> Result<(String, Map<String, Value>), String> {
    let params = params
        .as_object()
        .ok_or_else(|| "params must be an object".to_string())?;
    ensure_allowed_keys(params, &["name", "arguments", "_meta"])?;
    let name = params
        .get("name")
        .and_then(Value::as_str)
        .unwrap_or("")
        .trim()
        .to_string();
    if name.is_empty() {
        return Err("tool name is required".to_string());
    }
    let arguments = match params.get("arguments") {
        None | Some(Value::Null) => Map::new(),
        Some(Value::Object(arguments)) => arguments.clone(),
        Some(_) => return Err("arguments must be an object".to_string()),
    };
    Ok((name, arguments))
}

fn call_tool(
    state: &AppState,
    name: &str,
    arguments: &Map<String, Value>,
) -> Result<Value, String> {
    let payload = match name.trim() {
        "sample-r.status" => {
            require_empty(arguments)?;
            json!({"success":true,"plugin":status_payload(state)})
        }
        "sample-r.hello" => {
            require_empty(arguments)?;
            json!({"success":true,"hello":{"id":"sample-r","name":PLUGIN_NAME,"version":VERSION,"language":"rust","time":now_rfc3339()}})
        }
        "sample-r.items.list" => {
            require_empty(arguments)?;
            ensure_loaded(state)?;
            json!({"success":true,"items":list_items(state)})
        }
        "sample-r.items.get" => {
            ensure_allowed_keys(arguments, &["id"])?;
            let id = required_id(arguments)?;
            ensure_loaded(state)?;
            let item = state
                .runtime
                .lock()
                .map_err(|_| "runtime lock failed".to_string())?
                .items
                .get(&id)
                .cloned()
                .ok_or_else(|| "item not found".to_string())?;
            json!({"success":true,"item":item})
        }
        "sample-r.items.create" => create_item(state, arguments)?,
        "sample-r.items.update" => update_item(state, arguments)?,
        "sample-r.items.delete" => delete_item(state, arguments)?,
        "sample-r.jobs.start" => start_job(state, arguments)?,
        "sample-r.jobs.get" => {
            ensure_allowed_keys(arguments, &["id"])?;
            let id = required_id(arguments)?;
            let job = state
                .runtime
                .lock()
                .map_err(|_| "runtime lock failed".to_string())?
                .jobs
                .get(&id)
                .cloned()
                .ok_or_else(|| "job not found".to_string())?;
            json!({"success":true,"job":job})
        }
        _ => return Err(format!("unknown MCP tool: {name}")),
    };
    let encoded = serde_json::to_vec(&payload).map_err(|err| err.to_string())?;
    if encoded.len() > MCP_MAX_RESPONSE_BYTES {
        return Err("MCP result exceeds 2 MiB".to_string());
    }
    Ok(payload)
}

fn create_item(state: &AppState, arguments: &Map<String, Value>) -> Result<Value, String> {
    ensure_allowed_keys(arguments, &["id", "name", "value", "metadata"])?;
    ensure_loaded(state)?;
    let name = required_text(arguments, "name", 200)?;
    let id = match arguments.get("id").and_then(Value::as_str) {
        Some(value) if !value.trim().is_empty() => validate_id(value)?,
        _ => next_id("item"),
    };
    let metadata = optional_metadata(arguments)?;
    let now = now_rfc3339();
    let item = SampleItem {
        id: id.clone(),
        name,
        value: arguments.get("value").cloned().unwrap_or(Value::Null),
        metadata,
        created_at: now.clone(),
        updated_at: now,
    };
    let mut runtime = state
        .runtime
        .lock()
        .map_err(|_| "runtime lock failed".to_string())?;
    if runtime.items.contains_key(&id) {
        return Err("item already exists".to_string());
    }
    runtime.items.insert(id, item.clone());
    Ok(json!({"success":true,"item":item}))
}

fn update_item(state: &AppState, arguments: &Map<String, Value>) -> Result<Value, String> {
    ensure_allowed_keys(arguments, &["id", "name", "value", "metadata"])?;
    ensure_loaded(state)?;
    let id = required_id(arguments)?;
    let name = required_text(arguments, "name", 200)?;
    let metadata = optional_metadata(arguments)?;
    let mut runtime = state
        .runtime
        .lock()
        .map_err(|_| "runtime lock failed".to_string())?;
    let old = runtime
        .items
        .get(&id)
        .cloned()
        .ok_or_else(|| "item not found".to_string())?;
    let item = SampleItem {
        id: id.clone(),
        name,
        value: arguments.get("value").cloned().unwrap_or(Value::Null),
        metadata,
        created_at: old.created_at,
        updated_at: now_rfc3339(),
    };
    runtime.items.insert(id, item.clone());
    Ok(json!({"success":true,"item":item}))
}

fn delete_item(state: &AppState, arguments: &Map<String, Value>) -> Result<Value, String> {
    ensure_allowed_keys(arguments, &["id"])?;
    ensure_loaded(state)?;
    let id = required_id(arguments)?;
    let deleted = state
        .runtime
        .lock()
        .map_err(|_| "runtime lock failed".to_string())?
        .items
        .remove(&id)
        .is_some();
    if !deleted {
        return Err("item not found".to_string());
    }
    Ok(json!({"success":true,"id":id,"deleted":true}))
}

fn start_job(state: &AppState, arguments: &Map<String, Value>) -> Result<Value, String> {
    ensure_allowed_keys(arguments, &["message"])?;
    let message = match arguments.get("message") {
        None | Some(Value::Null) => "sample-r background job".to_string(),
        Some(Value::String(value)) if value.trim().is_empty() => {
            "sample-r background job".to_string()
        }
        Some(Value::String(value)) if value.chars().count() <= 500 => value.trim().to_string(),
        Some(Value::String(_)) => return Err("message exceeds 500 characters".to_string()),
        Some(_) => return Err("message must be a string".to_string()),
    };
    let now = now_rfc3339();
    let job = SampleJob {
        id: next_id("job"),
        status: "queued".to_string(),
        progress: 0,
        message,
        created_at: now.clone(),
        updated_at: now,
    };
    {
        let mut runtime = state
            .runtime
            .lock()
            .map_err(|_| "runtime lock failed".to_string())?;
        runtime.jobs.insert(job.id.clone(), job.clone());
        prune_jobs(&mut runtime);
    }
    spawn_job(state.clone(), job.id.clone());
    Ok(json!({"success":true,"job":job}))
}

fn required_id(arguments: &Map<String, Value>) -> Result<String, String> {
    let value = arguments
        .get("id")
        .and_then(Value::as_str)
        .ok_or_else(|| "id is required".to_string())?;
    validate_id(value)
}

fn validate_id(value: &str) -> Result<String, String> {
    let id = value.trim();
    if id.is_empty() {
        return Err("id is required".to_string());
    }
    if id.len() > 128 {
        return Err("id exceeds 128 characters".to_string());
    }
    if !id
        .bytes()
        .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
    {
        return Err("id contains unsupported characters".to_string());
    }
    Ok(id.to_string())
}

fn required_text(
    arguments: &Map<String, Value>,
    key: &str,
    max_chars: usize,
) -> Result<String, String> {
    let value = arguments
        .get(key)
        .and_then(Value::as_str)
        .ok_or_else(|| format!("{key} is required"))?
        .trim();
    if value.is_empty() {
        return Err(format!("{key} is required"));
    }
    if value.chars().count() > max_chars {
        return Err(format!("{key} exceeds {max_chars} characters"));
    }
    Ok(value.to_string())
}

fn optional_metadata(arguments: &Map<String, Value>) -> Result<Map<String, Value>, String> {
    match arguments.get("metadata") {
        None | Some(Value::Null) => Ok(Map::new()),
        Some(Value::Object(metadata)) => Ok(metadata.clone()),
        Some(_) => Err("metadata must be an object".to_string()),
    }
}

fn require_empty(arguments: &Map<String, Value>) -> Result<(), String> {
    if arguments.is_empty() {
        Ok(())
    } else {
        Err("this tool does not accept arguments".to_string())
    }
}

fn ensure_allowed_keys(arguments: &Map<String, Value>, allowed: &[&str]) -> Result<(), String> {
    if let Some(key) = arguments
        .keys()
        .find(|key| !allowed.contains(&key.as_str()))
    {
        return Err(format!("unknown argument: {key}"));
    }
    Ok(())
}

fn call_envelope(structured: Value, is_error: bool) -> Value {
    json!({
        "content":[{"type":"text","text":structured.to_string()}],
        "structuredContent":structured,
        "isError":is_error
    })
}

fn rpc_result(id: Value, result: Value, protocol: &str) -> Response {
    mcp_json_response(
        StatusCode::OK,
        json!({"jsonrpc":"2.0","id":id,"result":result}),
        protocol,
    )
}

fn rpc_error(
    status: StatusCode,
    id: Value,
    code: i64,
    message: &str,
    data: Value,
    protocol: &str,
) -> Response {
    let mut error = json!({"code":code,"message":message});
    if !data.is_null() {
        error["data"] = data;
    }
    mcp_json_response(
        status,
        json!({"jsonrpc":"2.0","id":id,"error":error}),
        protocol,
    )
}

fn mcp_json_response(status: StatusCode, payload: Value, protocol: &str) -> Response {
    let mut response = Response::new(Body::from(payload.to_string()));
    *response.status_mut() = status;
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    apply_mcp_headers(&mut response, protocol);
    with_cors(response)
}

fn mcp_empty_response(status: StatusCode, protocol: &str) -> Response {
    let mut response = StatusCode::NO_CONTENT.into_response();
    *response.status_mut() = status;
    apply_mcp_headers(&mut response, protocol);
    with_cors(response)
}

fn apply_mcp_headers(response: &mut Response, protocol: &str) {
    let headers = response.headers_mut();
    headers.insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    headers.insert(
        "MCP-Protocol-Version",
        HeaderValue::from_str(protocol)
            .unwrap_or_else(|_| HeaderValue::from_static(MCP_PROTOCOL_LATEST)),
    );
    headers.insert(
        header::X_CONTENT_TYPE_OPTIONS,
        HeaderValue::from_static("nosniff"),
    );
}
