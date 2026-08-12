use axum::{
    Router,
    body::{Body, to_bytes},
    extract::State,
    http::{HeaderMap, Method, Request, StatusCode, header},
    response::{IntoResponse, Response},
};
use base64::Engine;
use chrono::{DateTime, Local, SecondsFormat, Utc};
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use std::{
    borrow::Cow,
    collections::{HashMap, HashSet},
    env, fs,
    io::{BufRead, BufReader, Read, Write},
    net::TcpStream,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::{sync::oneshot, task::JoinHandle};

mod mcp;

const PLUGIN_ID: &str = "sample-r";
const PLUGIN_CODE: &str = "sample-r";
const PLUGIN_NAME: &str = "SampleR Plugin";
const SERVICE_NAME: &str = "sampler-service";
const DEFAULT_ADDR: &str = "127.0.0.1:18183";
const DEFAULT_CONFIG_PATH: &str = "plugins/sample-r/config.json";
const MANIFEST_PATH: &str = "plugins/sample-r/plugin.json";
const VERSION: &str = env!("CARGO_PKG_VERSION");
const MAX_REQUEST_BODY_BYTES: usize = 32 * 1024 * 1024;
const MAX_MESSAGE_LOG: usize = 50;
const MAX_JOBS: usize = 100;

#[derive(Clone)]
struct AppState {
    runtime: Arc<Mutex<Runtime>>,
    shutdown_tx: Arc<Mutex<Option<oneshot::Sender<()>>>>,
    scheduler: Arc<Mutex<Option<JoinHandle<()>>>>,
}

#[derive(Clone)]
struct Runtime {
    loaded: bool,
    config: SampleConfig,
    items: HashMap<String, SampleItem>,
    jobs: HashMap<String, SampleJob>,
    scheduled_running: HashMap<String, bool>,
    host_auth: HostAuth,
    message_log: Vec<HubMessage>,
    loaded_at: Option<DateTime<Utc>>,
    last_error: String,
    last_modified: Option<DateTime<Utc>>,
    config_path: PathBuf,
    skill_root_path: PathBuf,
}

impl Runtime {
    fn new(config_path: PathBuf) -> Self {
        Self {
            loaded: false,
            config: SampleConfig::default(),
            items: HashMap::new(),
            jobs: HashMap::new(),
            scheduled_running: HashMap::new(),
            host_auth: HostAuth::default(),
            message_log: Vec::new(),
            loaded_at: None,
            last_error: String::new(),
            last_modified: None,
            skill_root_path: PathBuf::from("plugins/sample-r/skill"),
            config_path,
        }
    }
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
struct HostAuth {
    token: String,
    token_type: String,
    header: String,
    account: String,
    project: String,
    source: String,
    host_url: String,
    base_url: String,
    expires_at: String,
    updated_at: String,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
struct HostAuthRequest {
    #[serde(default)]
    auth_token: String,
    #[serde(default)]
    token_type: String,
    #[serde(default)]
    header: String,
    #[serde(default)]
    account: String,
    #[serde(default)]
    project: String,
    #[serde(default)]
    source: String,
    #[serde(default)]
    host_url: String,
    #[serde(default)]
    base_url: String,
    #[serde(default)]
    expires_at: String,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
struct SampleConfig {
    #[serde(default)]
    version: String,
    #[serde(default)]
    title: String,
    #[serde(default)]
    default_group: String,
    #[serde(default)]
    message: String,
    #[serde(default)]
    features: HashMap<String, bool>,
    #[serde(default)]
    items: Vec<SampleItem>,
    #[serde(default)]
    scheduled_tasks: Vec<ScheduledTask>,
}

impl Default for SampleConfig {
    fn default() -> Self {
        Self {
            version: VERSION.to_string(),
            title: PLUGIN_NAME.to_string(),
            default_group: "開發工具".to_string(),
            message: "Hello from SampleR Plugin".to_string(),
            features: default_features(),
            items: Vec::new(),
            scheduled_tasks: Vec::new(),
        }
    }
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
struct SampleItem {
    #[serde(default)]
    id: String,
    #[serde(default)]
    name: String,
    #[serde(default, skip_serializing_if = "Value::is_null")]
    value: Value,
    #[serde(default, skip_serializing_if = "Map::is_empty")]
    metadata: Map<String, Value>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    created_at: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    updated_at: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
struct SkillCard {
    #[serde(default)]
    id: String,
    #[serde(default)]
    title: String,
    #[serde(default)]
    description: String,
    #[serde(default)]
    icon: String,
    #[serde(default)]
    prompt: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
struct SampleJob {
    id: String,
    status: String,
    progress: u8,
    message: String,
    created_at: String,
    updated_at: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
struct ScheduledTask {
    #[serde(default)]
    id: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    enabled: bool,
    #[serde(default)]
    interval_minutes: i64,
    #[serde(default)]
    action: String,
    #[serde(default)]
    payload: Map<String, Value>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    last_run_at: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    next_run_at: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    running_until: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    run_token: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    last_started_at: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    last_result: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    last_error: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    created_at: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    updated_at: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
struct ScheduledLogEntry {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    id: String,
    task_id: String,
    #[serde(default)]
    task_name: String,
    #[serde(default)]
    action: String,
    status: String,
    #[serde(default)]
    manual: bool,
    #[serde(default)]
    payload: Map<String, Value>,
    #[serde(default)]
    result: String,
    #[serde(default)]
    error: String,
    #[serde(default)]
    started_at: String,
    #[serde(default)]
    finished_at: String,
    #[serde(default)]
    created_at: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
struct HubMessage {
    #[serde(default)]
    seq: u64,
    #[serde(default)]
    msg_id: String,
    topic: String,
    #[serde(default)]
    event: String,
    #[serde(default)]
    source: String,
    #[serde(default)]
    payload: Map<String, Value>,
    #[serde(default)]
    ts: String,
    #[serde(default)]
    received_at: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
struct PublishMessageRequest {
    topic: String,
    #[serde(default)]
    event: String,
    #[serde(default)]
    msg_id: String,
    #[serde(default)]
    payload: Map<String, Value>,
}

#[tokio::main(flavor = "current_thread")]
async fn main() {
    let args = parse_args();
    let addr = args
        .get("addr")
        .cloned()
        .unwrap_or_else(|| DEFAULT_ADDR.to_string());
    let config_path = args
        .get("config")
        .cloned()
        .unwrap_or_else(|| DEFAULT_CONFIG_PATH.to_string());

    let plugin_root = ensure_plugin_workdir().unwrap_or_else(|err| {
        eprintln!("{err}");
        std::process::exit(1);
    });

    let (shutdown_tx, shutdown_rx) = oneshot::channel::<()>();
    let state = AppState {
        runtime: Arc::new(Mutex::new(Runtime::new(PathBuf::from(&config_path)))),
        shutdown_tx: Arc::new(Mutex::new(Some(shutdown_tx))),
        scheduler: Arc::new(Mutex::new(None)),
    };
    if let Err(err) = load_config(&state, true) {
        eprintln!("sampler-service initial load failed: {err}");
    } else {
        start_scheduler(&state);
    }

    let app = Router::new().fallback(handle_request).with_state(state);
    let listener = tokio::net::TcpListener::bind(&addr)
        .await
        .unwrap_or_else(|err| {
            eprintln!("bind {addr} failed: {err}");
            std::process::exit(1);
        });

    eprintln!(
        "sampler-service listening on {addr}, root={}, config={config_path}",
        plugin_root.display()
    );
    if let Err(err) = axum::serve(listener, app)
        .with_graceful_shutdown(async {
            let _ = shutdown_rx.await;
        })
        .await
    {
        eprintln!("sampler-service failed: {err}");
        std::process::exit(1);
    }
}

async fn handle_request(State(state): State<AppState>, req: Request<Body>) -> Response {
    let (parts, body) = req.into_parts();
    let method = parts.method.clone();
    let path = parts.uri.path().to_string();
    let query = parts.uri.query().unwrap_or("").to_string();
    let headers = parts.headers.clone();

    if method == Method::OPTIONS {
        return with_cors(StatusCode::NO_CONTENT.into_response());
    }

    let body_bytes = match to_bytes(body, MAX_REQUEST_BODY_BYTES).await {
        Ok(bytes) => bytes,
        Err(err) => return json_response(json!({"success": false, "error": err.to_string()})),
    };
    let body_text: Cow<'_, str> = match std::str::from_utf8(&body_bytes) {
        Ok(text) => Cow::Borrowed(text),
        Err(_) => Cow::Owned(String::from_utf8_lossy(&body_bytes).into_owned()),
    };

    if mcp::is_mcp_path(&path) {
        return mcp::handle_http(&state, &method, &headers, body_text.as_ref());
    }

    if is_stream_path(&path) && (method == Method::GET || method == Method::POST) {
        return stream_response(body_text.as_ref());
    }

    let (payload, should_shutdown) =
        process_request(&state, method, &path, &query, &headers, body_text.as_ref()).await;
    let response = json_response(payload);
    if should_shutdown {
        let tx = state
            .shutdown_tx
            .lock()
            .ok()
            .and_then(|mut guard| guard.take());
        tokio::spawn(async move {
            tokio::time::sleep(Duration::from_millis(150)).await;
            if let Some(tx) = tx {
                let _ = tx.send(());
            }
        });
    }
    response
}

async fn process_request(
    state: &AppState,
    method: Method,
    raw_path: &str,
    query: &str,
    headers: &HeaderMap,
    body: &str,
) -> (Value, bool) {
    let path = normalize_api_path(raw_path);
    if path.is_empty() {
        return (catalog_response(state), false);
    }
    match path[0].as_str() {
        "plugin" => handle_plugin_api(state, &method, &path[1..], headers, &body).await,
        "hello" => (handle_hello(&method), false),
        "health" | "heatlth" => (handle_health(state, &method), false),
        "apis" => (catalog_response(state), false),
        "mcp" => (mcp::metadata_response(&method), false),
        "config" => (handle_config(state, &method, &body), false),
        "echo" => (handle_echo(&method, raw_path, query, headers, &body), false),
        "items" => (handle_items(state, &method, &path[1..], &body), false),
        "skills" => (handle_skills(state, &method, &path[1..], &body), false),
        "jobs" => (handle_jobs(state, &method, &path[1..], &body), false),
        "scheduled-tasks" => (
            handle_scheduled_tasks(state, &method, &path[1..], &body),
            false,
        ),
        "scheduled-logs" => (
            handle_scheduled_logs(state, &method, &path[1..], query),
            false,
        ),
        "files" => (handle_files(&method, headers, &body), false),
        "msg" => handle_message(state, &method, &path[1..], &body).await,
        "tools" => (handle_tools(&method, &path[1..], &body), false),
        _ => (catalog_response(state), false),
    }
}

async fn handle_plugin_api(
    state: &AppState,
    method: &Method,
    path: &[String],
    headers: &HeaderMap,
    body: &str,
) -> (Value, bool) {
    let cmd = path.last().map(String::as_str).unwrap_or("status");
    match cmd {
        "status" => {
            if method != Method::GET {
                return (method_not_allowed(), false);
            }
            (
                json!({"success": true, "plugin": status_payload(state)}),
                false,
            )
        }
        "load" => {
            if method != Method::POST {
                return (method_not_allowed(), false);
            }
            (load_response(state), false)
        }
        "auth" => {
            if method != Method::POST {
                return (method_not_allowed(), false);
            }
            (auth_response(state, headers, body), false)
        }
        "reload" => {
            if method != Method::POST {
                return (method_not_allowed(), false);
            }
            if let Ok(mut rt) = state.runtime.lock() {
                rt.loaded = false;
            }
            (load_response(state), false)
        }
        "unload" => {
            if method != Method::POST {
                return (method_not_allowed(), false);
            }
            if let Ok(mut handle) = state.scheduler.lock() {
                if let Some(task) = handle.take() {
                    task.abort();
                }
            }
            if let Ok(mut rt) = state.runtime.lock() {
                rt.loaded = false;
                rt.config = SampleConfig::default();
                rt.items = HashMap::new();
                rt.jobs = HashMap::new();
                rt.scheduled_running = HashMap::new();
                rt.message_log = Vec::new();
                rt.host_auth = HostAuth::default();
                rt.loaded_at = None;
                rt.last_error.clear();
                rt.last_modified = None;
                compact_runtime(&mut rt);
            }
            (
                json!({"success": true, "plugin": status_payload(state)}),
                true,
            )
        }
        "registration" => {
            if method != Method::GET {
                return (method_not_allowed(), false);
            }
            (
                json!({"success": true, "plugin": registration_payload()}),
                false,
            )
        }
        _ => (catalog_response(state), false),
    }
}

fn handle_hello(method: &Method) -> Value {
    if method != Method::GET {
        return method_not_allowed();
    }
    json!({
        "success": true,
        "hello": {
            "id": PLUGIN_ID,
            "name": PLUGIN_NAME,
            "version": VERSION,
            "language": "rust",
            "time": now_rfc3339()
        }
    })
}

fn handle_health(state: &AppState, method: &Method) -> Value {
    if method != Method::GET {
        return method_not_allowed();
    }
    json!({"success": true, "healthy": true, "status": "ok", "plugin": status_payload(state)})
}

fn handle_config(state: &AppState, method: &Method, body: &str) -> Value {
    match *method {
        Method::GET => {
            if let Err(err) = ensure_loaded(state) {
                return json!({"success": false, "error": err, "plugin": status_payload(state)});
            }
            let rt = state.runtime.lock().unwrap();
            json!({"success": true, "path": rt.config_path, "config": rt.config})
        }
        Method::POST | Method::PUT | Method::PATCH => {
            let cfg: Result<SampleConfig, _> = serde_json::from_str(body);
            let mut cfg = match cfg {
                Ok(cfg) => cfg,
                Err(_) => return json!({"success": false, "error": "invalid config json"}),
            };
            normalize_config(&mut cfg);
            let path = state.runtime.lock().unwrap().config_path.clone();
            if let Err(err) = write_config_path(&path, &cfg) {
                return json!({"success": false, "error": err});
            }
            if let Ok(mut rt) = state.runtime.lock() {
                rt.loaded = false;
            }
            load_response(state)
        }
        _ => method_not_allowed(),
    }
}

fn handle_echo(
    method: &Method,
    raw_path: &str,
    query: &str,
    headers: &HeaderMap,
    body: &str,
) -> Value {
    let parsed: Value = serde_json::from_str(body).unwrap_or_else(|_| json!({}));
    json!({
        "success": true,
        "echo": {
            "method": method.as_str(),
            "path": raw_path,
            "query": query_to_map(query),
            "headers": selected_headers(headers),
            "body": parsed,
            "raw": body,
            "time": now_rfc3339()
        }
    })
}

fn handle_items(state: &AppState, method: &Method, path: &[String], body: &str) -> Value {
    if let Err(err) = ensure_loaded(state) {
        return json!({"success": false, "error": err, "plugin": status_payload(state)});
    }
    if path.is_empty() {
        return match *method {
            Method::GET => json!({"success": true, "items": list_items(state)}),
            Method::POST => {
                let mut item = match parse_item(body) {
                    Ok(item) => item,
                    Err(err) => return json!({"success": false, "error": err}),
                };
                if item.id.trim().is_empty() {
                    item.id = next_id("item");
                }
                let now = now_rfc3339();
                item.created_at = now.clone();
                item.updated_at = now;
                state
                    .runtime
                    .lock()
                    .unwrap()
                    .items
                    .insert(item.id.clone(), item.clone());
                json!({"success": true, "item": item})
            }
            _ => method_not_allowed(),
        };
    }
    let id = path[0].trim();
    if id.is_empty() {
        return json!({"success": false, "error": "item id is required"});
    }
    match *method {
        Method::GET => match state.runtime.lock().unwrap().items.get(id).cloned() {
            Some(item) => json!({"success": true, "item": item}),
            None => json!({"success": false, "error": "item not found", "id": id}),
        },
        Method::PUT | Method::PATCH => {
            let mut item = match parse_item(body) {
                Ok(item) => item,
                Err(err) => return json!({"success": false, "error": err}),
            };
            let mut rt = state.runtime.lock().unwrap();
            item.id = id.to_string();
            item.updated_at = now_rfc3339();
            if let Some(old) = rt.items.get(id) {
                if item.created_at.is_empty() {
                    item.created_at = old.created_at.clone();
                }
            }
            rt.items.insert(id.to_string(), item.clone());
            json!({"success": true, "item": item})
        }
        Method::DELETE => {
            state.runtime.lock().unwrap().items.remove(id);
            json!({"success": true, "id": id})
        }
        _ => method_not_allowed(),
    }
}

fn handle_skills(state: &AppState, method: &Method, path: &[String], body: &str) -> Value {
    if path.is_empty() {
        if method != Method::GET {
            return method_not_allowed();
        }
        return match list_skills(state) {
            Ok(skills) => json!({"success": true, "root": skill_root(state), "skills": skills}),
            Err(err) => json!({"success": false, "error": err, "root": skill_root(state)}),
        };
    }
    if path[0].eq_ignore_ascii_case("cards") {
        return handle_skill_cards(state, method, &path[1..], body);
    }
    if path.len() == 2 && path[1].eq_ignore_ascii_case("content") {
        if method != Method::GET {
            return method_not_allowed();
        }
        return match read_skill_content(state, &path[0]) {
            Ok((content, entry)) => {
                json!({"success": true, "id": sanitize_skill_id(&path[0]), "entry": entry, "content": content})
            }
            Err(err) => json!({"success": false, "error": err, "id": path[0]}),
        };
    }
    json!({"success": false, "error": "unknown sample-r skill endpoint"})
}

fn handle_skill_cards(state: &AppState, method: &Method, path: &[String], body: &str) -> Value {
    if path.is_empty() {
        return match *method {
            Method::GET => match read_skill_cards(state) {
                Ok(cards) => {
                    json!({"success": true, "path": skill_cards_path(state), "cards": cards})
                }
                Err(err) => {
                    json!({"success": false, "error": err, "path": skill_cards_path(state)})
                }
            },
            Method::POST => {
                let mut card = match parse_skill_card(body) {
                    Ok(card) => card,
                    Err(err) => return json!({"success": false, "error": err}),
                };
                let mut cards = match read_skill_cards(state) {
                    Ok(cards) => cards,
                    Err(err) => return json!({"success": false, "error": err}),
                };
                if card.id.trim().is_empty() {
                    card.id = next_id("skill");
                }
                upsert_skill_card(&mut cards, card.clone());
                match write_skill_cards(state, &cards) {
                    Ok(()) => json!({"success": true, "card": card, "cards": cards}),
                    Err(err) => json!({"success": false, "error": err}),
                }
            }
            _ => method_not_allowed(),
        };
    }
    let id = path[0].trim();
    let mut cards = match read_skill_cards(state) {
        Ok(cards) => cards,
        Err(err) => return json!({"success": false, "error": err}),
    };
    match *method {
        Method::GET => cards
            .into_iter()
            .find(|card| card.id == id)
            .map(|card| json!({"success": true, "card": card}))
            .unwrap_or_else(
                || json!({"success": false, "error": "skill card not found", "id": id}),
            ),
        Method::PUT | Method::PATCH => {
            let mut card = match parse_skill_card(body) {
                Ok(card) => card,
                Err(err) => return json!({"success": false, "error": err}),
            };
            card.id = id.to_string();
            upsert_skill_card(&mut cards, card.clone());
            match write_skill_cards(state, &cards) {
                Ok(()) => json!({"success": true, "card": card, "cards": cards}),
                Err(err) => json!({"success": false, "error": err}),
            }
        }
        Method::DELETE => {
            cards.retain(|card| card.id != id);
            match write_skill_cards(state, &cards) {
                Ok(()) => json!({"success": true, "id": id, "cards": cards}),
                Err(err) => json!({"success": false, "error": err}),
            }
        }
        _ => method_not_allowed(),
    }
}

fn handle_jobs(state: &AppState, method: &Method, path: &[String], body: &str) -> Value {
    if path.is_empty() {
        if method != Method::POST {
            return method_not_allowed();
        }
        let payload = parse_object(body);
        let now = now_rfc3339();
        let job = SampleJob {
            id: next_id("job"),
            status: "queued".to_string(),
            progress: 0,
            message: string_from_map(&payload, "message", "sample-r background job"),
            created_at: now.clone(),
            updated_at: now,
        };
        {
            let mut rt = state.runtime.lock().unwrap();
            rt.jobs.insert(job.id.clone(), job.clone());
            prune_jobs(&mut rt);
        }
        spawn_job(state.clone(), job.id.clone());
        return json!({"success": true, "job": job});
    }
    if method != Method::GET {
        return method_not_allowed();
    }
    match state.runtime.lock().unwrap().jobs.get(&path[0]).cloned() {
        Some(job) => json!({"success": true, "job": job}),
        None => json!({"success": false, "error": "job not found", "id": path[0]}),
    }
}

fn handle_scheduled_tasks(state: &AppState, method: &Method, path: &[String], body: &str) -> Value {
    if let Err(err) = ensure_loaded(state) {
        return json!({"success": false, "error": err, "plugin": status_payload(state)});
    }
    if path.is_empty() {
        return match *method {
            Method::GET => json!({"success": true, "scheduled_tasks": list_scheduled_tasks(state)}),
            Method::POST | Method::PUT | Method::PATCH => {
                let value: Value = serde_json::from_str(body).unwrap_or_else(|_| json!({}));
                let tasks_value = value
                    .get("scheduled_tasks")
                    .cloned()
                    .unwrap_or_else(|| json!([]));
                let tasks: Result<Vec<ScheduledTask>, _> = serde_json::from_value(tasks_value);
                let mut tasks = match tasks {
                    Ok(tasks) => tasks,
                    Err(_) => {
                        return json!({"success": false, "error": "invalid scheduled tasks json"});
                    }
                };
                if let Err(err) = sanitize_tasks(&mut tasks) {
                    return json!({"success": false, "error": err});
                }
                match save_scheduled_tasks(state, tasks) {
                    Ok(()) => {
                        start_scheduler(state);
                        json!({"success": true, "scheduled_tasks": list_scheduled_tasks(state)})
                    }
                    Err(err) => {
                        json!({"success": false, "error": err, "plugin": status_payload(state)})
                    }
                }
            }
            _ => method_not_allowed(),
        };
    }
    if path.len() == 2 && path[1].eq_ignore_ascii_case("run") {
        if method != Method::POST {
            return method_not_allowed();
        }
        return match start_scheduled_task_run(state.clone(), path[0].clone(), true) {
            Ok(task) => {
                json!({"success": true, "accepted": true, "scheduled_task": task, "started_at": task.last_started_at, "scheduled_tasks": list_scheduled_tasks(state)})
            }
            Err(err) => json!({"success": false, "error": err, "plugin": status_payload(state)}),
        };
    }
    if path.len() != 1 {
        return json!({"success": false, "error": "unknown scheduled task endpoint"});
    }
    let task_id = path[0].trim();
    match *method {
        Method::GET => list_scheduled_tasks(state)
            .into_iter()
            .find(|task| task.id.eq_ignore_ascii_case(task_id))
            .map(|task| json!({"success": true, "scheduled_task": task}))
            .unwrap_or_else(
                || json!({"success": false, "error": "scheduled task not found", "id": task_id}),
            ),
        Method::DELETE => {
            let mut tasks = list_scheduled_tasks(state);
            let before = tasks.len();
            tasks.retain(|task| !task.id.eq_ignore_ascii_case(task_id));
            if before == tasks.len() {
                return json!({"success": false, "error": "scheduled task not found", "id": task_id});
            }
            match save_scheduled_tasks(state, tasks) {
                Ok(()) => {
                    start_scheduler(state);
                    json!({"success": true, "scheduled_tasks": list_scheduled_tasks(state)})
                }
                Err(err) => {
                    json!({"success": false, "error": err, "plugin": status_payload(state)})
                }
            }
        }
        _ => method_not_allowed(),
    }
}

fn handle_scheduled_logs(state: &AppState, method: &Method, path: &[String], query: &str) -> Value {
    if !path.is_empty() {
        return json!({"success": false, "error": "unknown scheduled log endpoint"});
    }
    if method != Method::GET {
        return method_not_allowed();
    }
    let query = query_to_map(query);
    let date = query
        .get("date")
        .and_then(|v| v.first())
        .cloned()
        .unwrap_or_default();
    let task_id = query
        .get("task_id")
        .and_then(|v| v.first())
        .cloned()
        .unwrap_or_default();
    match read_scheduled_logs(state, &date, &task_id) {
        Ok(logs) => {
            json!({"success": true, "date": normalize_log_date(&date), "task_id": task_id, "count": logs.len(), "logs": logs})
        }
        Err(err) => {
            json!({"success": false, "error": err, "date": normalize_log_date(&date), "task_id": task_id})
        }
    }
}

fn handle_files(method: &Method, headers: &HeaderMap, body: &str) -> Value {
    if method != Method::POST && method != Method::PUT {
        return method_not_allowed();
    }
    let content_type = headers
        .get(header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("")
        .to_ascii_lowercase();
    if content_type.starts_with("multipart/") {
        return json!({"success": true, "files": [{"file_name": "multipart-payload", "size": body.len(), "source": "multipart"}]});
    }
    let value: Value = serde_json::from_str(body).unwrap_or_else(|_| json!({}));
    let file_name = value.get("file_name").and_then(Value::as_str).unwrap_or("");
    let text = value.get("text").and_then(Value::as_str).unwrap_or("");
    let data = value
        .get("data_base64")
        .and_then(Value::as_str)
        .unwrap_or("");
    let size = if data.trim().is_empty() {
        text.as_bytes().len()
    } else {
        let encoded = data.trim_start_matches("base64,");
        base64::engine::general_purpose::STANDARD
            .decode(encoded)
            .map(|bytes| bytes.len())
            .unwrap_or_else(|_| data.as_bytes().len())
    };
    json!({"success": true, "files": [{"file_name": file_name, "size": size, "source": "json"}]})
}

async fn handle_message(
    state: &AppState,
    method: &Method,
    path: &[String],
    body: &str,
) -> (Value, bool) {
    if path.is_empty() {
        if method != Method::POST {
            return (method_not_allowed(), false);
        }
        let mut msg: HubMessage = match serde_json::from_str(body) {
            Ok(msg) => msg,
            Err(_) => {
                return (
                    json!({"success": false, "error": "invalid message json"}),
                    false,
                );
            }
        };
        msg.topic = msg.topic.trim().to_string();
        if msg.topic.is_empty() {
            return (
                json!({"success": false, "error": "topic is required"}),
                false,
            );
        }
        if msg.event.trim().is_empty() {
            msg.event = "message".to_string();
        }
        msg.received_at = Utc::now().to_rfc3339_opts(SecondsFormat::Nanos, true);
        let count = {
            let mut rt = state.runtime.lock().unwrap();
            rt.message_log.push(msg.clone());
            if rt.message_log.len() > MAX_MESSAGE_LOG {
                let drain_count = rt.message_log.len() - MAX_MESSAGE_LOG;
                rt.message_log.drain(0..drain_count);
                rt.message_log.shrink_to_fit();
            }
            rt.message_log.len()
        };
        return (
            json!({"success": true, "received": msg, "message_count": count}),
            false,
        );
    }
    match path[0].as_str() {
        "events" => match *method {
            Method::GET => {
                let mut events = state.runtime.lock().unwrap().message_log.clone();
                events.reverse();
                (
                    json!({"success": true, "events": events, "count": events.len()}),
                    false,
                )
            }
            Method::DELETE => {
                state.runtime.lock().unwrap().message_log = Vec::new();
                (json!({"success": true, "events": [], "count": 0}), false)
            }
            _ => (method_not_allowed(), false),
        },
        "publish" => {
            if method != Method::POST {
                return (method_not_allowed(), false);
            }
            let req: PublishMessageRequest = match serde_json::from_str(body) {
                Ok(req) => req,
                Err(_) => {
                    return (
                        json!({"success": false, "error": "invalid publish json", "plugin": status_payload(state)}),
                        false,
                    );
                }
            };
            match publish_message_to_host(state, req).await {
                Ok(result) => (json!({"success": true, "published": result}), false),
                Err(err) => (
                    json!({"success": false, "error": err, "plugin": status_payload(state)}),
                    false,
                ),
            }
        }
        _ => (
            json!({"success": false, "error": "unknown sample-r msg endpoint"}),
            false,
        ),
    }
}

fn handle_tools(method: &Method, path: &[String], body: &str) -> Value {
    if path.first().map(String::as_str) != Some("run") {
        return catalog_response_empty();
    }
    if method != Method::POST {
        return method_not_allowed();
    }
    let payload = parse_object(body);
    let tool_name = string_from_map(&payload, "tool", "sample-r.tool");
    json!({
        "success": true,
        "tool": {
            "name": tool_name,
            "input": payload.get("input").cloned().unwrap_or(Value::Null),
            "output": format!("mock result from {tool_name}"),
            "started_at": now_rfc3339()
        }
    })
}

fn load_response(state: &AppState) -> Value {
    match load_config(state, true) {
        Ok(()) => {
            start_scheduler(state);
            json!({"success": true, "plugin": status_payload(state)})
        }
        Err(err) => {
            if let Ok(mut rt) = state.runtime.lock() {
                rt.last_error = err.clone();
            }
            json!({"success": false, "error": err, "plugin": status_payload(state)})
        }
    }
}

fn auth_response(state: &AppState, headers: &HeaderMap, body: &str) -> Value {
    let req: HostAuthRequest = serde_json::from_str(body).unwrap_or(HostAuthRequest {
        auth_token: String::new(),
        token_type: String::new(),
        header: String::new(),
        account: String::new(),
        project: String::new(),
        source: String::new(),
        host_url: String::new(),
        base_url: String::new(),
        expires_at: String::new(),
    });
    let mut token = req.auth_token.trim().to_string();
    if token.is_empty() {
        token = bearer_token_from_header(headers, "Authentication")
            .or_else(|| bearer_token_from_header(headers, "Authorization"))
            .unwrap_or_default();
    }
    if token.is_empty() {
        return json!({"success": false, "error": "auth_token is required", "plugin": status_payload(state)});
    }
    let host_url = first_non_empty(&[&req.host_url, &req.base_url]);
    let base_url = first_non_empty(&[&req.base_url, &req.host_url]);
    let auth = HostAuth {
        token,
        token_type: first_non_empty(&[&req.token_type, "Bearer"]),
        header: first_non_empty(&[&req.header, "Authentication"]),
        account: req.account.trim().to_string(),
        project: req.project.trim().to_string(),
        source: first_non_empty(&[&req.source, "host"]),
        host_url: host_url.trim_end_matches('/').to_string(),
        base_url: base_url.trim_end_matches('/').to_string(),
        expires_at: req.expires_at.trim().to_string(),
        updated_at: now_rfc3339(),
    };
    state.runtime.lock().unwrap().host_auth = auth;
    json!({"success": true, "plugin": status_payload(state)})
}

fn catalog_response(state: &AppState) -> Value {
    json!({"success": true, "service": PLUGIN_ID, "plugin": status_payload(state), "apis": api_catalog()})
}

fn catalog_response_empty() -> Value {
    json!({"success": true, "service": PLUGIN_ID, "apis": api_catalog()})
}

fn api_catalog() -> Vec<Value> {
    vec![
        api_entry(
            "/api/sample-r/apis",
            "GET",
            "list every SampleR Plugin API with descriptions and examples",
            None,
        ),
        api_entry("/api/hello", "GET", "minimal plugin hello handshake", None),
        api_entry("/api/health", "GET", "minimal plugin health check", None),
        api_entry(
            "/api/heatlth",
            "GET",
            "compatibility alias for /api/health",
            None,
        ),
        api_entry(
            "/api/sample-r/plugin/status",
            "GET",
            "show plugin runtime status",
            None,
        ),
        api_entry(
            "/api/sample-r/plugin/registration",
            "GET",
            "show plugin registration metadata",
            None,
        ),
        api_entry(
            "/api/sample-r/plugin/auth",
            "POST",
            "receive host auth token for plugin service program calls",
            Some(
                json!({"auth_token": "TOKEN", "token_type": "Bearer", "header": "Authentication", "source": "host"}),
            ),
        ),
        api_entry(
            "/api/sample-r/plugin/load",
            "POST",
            "load config and initialize runtime state",
            Some(json!({})),
        ),
        api_entry(
            "/api/sample-r/plugin/unload",
            "POST",
            "clear runtime state and stop service process after response",
            Some(json!({})),
        ),
        api_entry(
            "/api/sample-r/plugin/reload",
            "POST",
            "reload config",
            Some(json!({})),
        ),
        api_entry(
            "/api/sample-r/config",
            "GET|PUT",
            "read or update sample-r config",
            Some(
                json!({"version": VERSION, "title": PLUGIN_NAME, "default_group": "開發工具", "message": "Hello from SampleR Plugin", "features": default_features(), "items": [{"id":"demo-1","name":"Demo Item","value":"demo value"}]}),
            ),
        ),
        api_entry(
            "/api/sample-r/echo",
            "GET|POST",
            "return method, query, headers and JSON body",
            Some(json!({"message": "hello sample-r plugin"})),
        ),
        api_entry(
            "/api/sample-r/items",
            "GET|POST",
            "list or create demo item",
            Some(json!({"name": "demo item", "value": {"enabled": true}})),
        ),
        api_entry(
            "/api/sample-r/items/{id}",
            "GET|PUT|DELETE",
            "read, update or delete demo item",
            Some(json!({"name": "updated item", "value": "updated value"})),
        ),
        api_entry(
            "/api/sample-r/skills",
            "GET",
            "list sample-r plugin skills",
            None,
        ),
        api_entry(
            "/api/sample-r/skills/{id}/content",
            "GET",
            "read sample-r plugin skill markdown content",
            None,
        ),
        api_entry(
            "/api/sample-r/skills/cards",
            "GET|POST",
            "list or create sample-r chat skill card",
            Some(
                json!({"title": "外掛狀態診斷", "description": "檢查 service、config、items 與載入狀態。", "icon": "fa-heart-pulse", "prompt": "請根據目前 SampleR Plugin 的即時狀態進行診斷。"}),
            ),
        ),
        api_entry(
            "/api/sample-r/skills/cards/{id}",
            "GET|PUT|DELETE",
            "read, update or delete sample-r chat skill card",
            Some(
                json!({"title": "API 串接建議", "description": "產生 fetchPlugin 呼叫範例。", "icon": "fa-route", "prompt": "請整理 SampleR Plugin API 呼叫範例。"}),
            ),
        ),
        api_entry(
            "/api/sample-r/stream",
            "GET|POST",
            "server-sent events stream example",
            Some(json!({"message": "stream progress"})),
        ),
        api_entry(
            "/api/sample-r/jobs",
            "POST",
            "start background job",
            Some(json!({"message": "sample-r background job"})),
        ),
        api_entry(
            "/api/sample-r/jobs/{id}",
            "GET",
            "read background job status",
            None,
        ),
        api_entry(
            "/api/sample-r/scheduled-tasks",
            "GET|PUT",
            "list or save scheduled tasks",
            Some(
                json!({"scheduled_tasks": [{"id":"sample_r_hourly_job","name":"SampleR 每小時背景任務","enabled":false,"interval_minutes":60,"action":"job","payload":{"message":"sample-r scheduled background job"}}]}),
            ),
        ),
        api_entry(
            "/api/sample-r/scheduled-tasks/{id}",
            "GET|DELETE",
            "read or delete scheduled task",
            None,
        ),
        api_entry(
            "/api/sample-r/scheduled-tasks/{id}/run",
            "POST",
            "run scheduled task immediately",
            Some(json!({})),
        ),
        api_entry(
            "/api/sample-r/scheduled-logs",
            "GET",
            "read scheduled task logs by date and optional task_id",
            None,
        ),
        api_entry(
            "/api/sample-r/files",
            "POST",
            "receive JSON base64 or multipart file payload",
            Some(json!({"file_name": "hello.txt", "text": "hello sample-r plugin"})),
        ),
        api_entry(
            "/api/sample-r/msg",
            "POST",
            "receive MessageHub webhook deliveries from the main service",
            Some(
                json!({"seq":42,"topic":"sample-r.notice","event":"created","source":"other-plugin","payload":{"id":"demo"}}),
            ),
        ),
        api_entry(
            "/api/sample-r/msg/events",
            "GET|DELETE",
            "list or clear recent MessageHub webhook deliveries stored by SampleR Plugin",
            None,
        ),
        api_entry(
            "/api/sample-r/msg/publish",
            "POST",
            "publish a MessageHub event through the main service using host auth",
            Some(
                json!({"topic":"sample-r.notice","event":"created","payload":{"message":"hello from sample-r plugin"}}),
            ),
        ),
        api_entry(
            "/api/sample-r/tools/run",
            "POST",
            "mock tool invocation endpoint",
            Some(json!({"tool":"sample-r.tool","input":{"message":"hello"}})),
        ),
        api_entry(
            "/api/sample-r/mcp",
            "GET",
            "show SampleR MCP protocol metadata and tool definitions",
            None,
        ),
        api_entry(
            "/mcp",
            "POST",
            "standard stateless MCP JSON-RPC endpoint",
            Some(json!({"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}})),
        ),
    ]
}

fn api_entry(path: &str, method: &str, description: &str, body: Option<Value>) -> Value {
    let mut entry = json!({
        "path": path,
        "method": method,
        "description": description,
        "examples": {
            "fetch_plugin": fetch_plugin_example(path, method, body.as_ref()),
            "curl": curl_example(path, method, body.as_ref())
        }
    });
    if let Some(body) = body {
        entry["request_body"] = body;
    }
    entry
}

fn fetch_plugin_example(path: &str, method: &str, body: Option<&Value>) -> String {
    let method = preferred_method(method);
    if path == "/mcp" {
        let empty_body = json!({});
        let request_body = body.unwrap_or(&empty_body);
        return format!(
            "window.AgenticTalkAPI.fetchPlugin(\"sample-r\", \"/mcp\", {{ method: \"{method}\", headers: {{ \"Content-Type\": \"application/json\", \"MCP-Protocol-Version\": \"2025-11-25\" }}, body: JSON.stringify({}) }})",
            compact_json(request_body)
        );
    }
    if body.is_none() || method == "GET" || method == "DELETE" {
        return format!(
            "window.AgenticTalkAPI.fetchPlugin(\"sample-r\", \"{path}\", {{ method: \"{method}\" }})"
        );
    }
    format!(
        "window.AgenticTalkAPI.fetchPlugin(\"sample-r\", \"{path}\", {{ method: \"{method}\", body: JSON.stringify({}) }})",
        compact_json(body.unwrap())
    )
}

fn curl_example(path: &str, method: &str, body: Option<&Value>) -> String {
    let method = preferred_method(method);
    let url = if path == "/mcp" {
        "http://127.0.0.1:18183/mcp".to_string()
    } else {
        format!("http://127.0.0.1:18183{path}")
    };
    if body.is_none() || method == "GET" || method == "DELETE" {
        return format!("curl -X {method} \"{url}\"");
    }
    format!(
        "curl -X {method} \"{url}\" -H \"Content-Type: application/json\" -d '{}'",
        compact_json(body.unwrap())
    )
}

fn preferred_method(method: &str) -> &str {
    for candidate in ["POST", "PUT", "PATCH", "DELETE", "GET"] {
        if method.contains(candidate) {
            return candidate;
        }
    }
    "GET"
}

fn registration_payload() -> Value {
    json!({
        "id": PLUGIN_ID,
        "plugin_code": PLUGIN_CODE,
        "name": PLUGIN_NAME,
        "version": VERSION,
        "language": "rust",
        "type": "service",
        "auto_start": true,
        "default_group": "開發工具",
        "service": SERVICE_NAME,
        "service_url": "http://127.0.0.1:18183",
        "api_base": "/api/plugin/sample-r",
        "plugin_api_base": "/api/sample-r",
        "api_catalog": "/api/sample-r/apis",
        "routes": ["/mcp", "/api/sample-r", "/api/hello", "/api/health", "/api/heatlth"],
        "mcp_url": "http://127.0.0.1:18183/mcp",
        "website_path": "./website/sample-r/index.html",
        "runtime": {
            "service": SERVICE_NAME,
            "addr": DEFAULT_ADDR,
            "listen_addr": DEFAULT_ADDR,
            "health": "/api/health",
            "auth": "/api/sample-r/plugin/auth",
            "load": "/api/sample-r/plugin/load",
            "reload": "/api/sample-r/plugin/reload",
            "unload": "/api/sample-r/plugin/unload",
            "registration": "/api/sample-r/plugin/registration",
            "start_args": ["--config", "plugins/sample-r/config.json"],
            "preserve_paths": ["config.json", "runtime/", "skill/skill-cards.json"]
        },
        "messaging": {"webhook": "/api/sample-r/msg", "topics": ["sample-r.notice", "system.notice"]},
        "ui": {
            "enabled": true,
            "order": 90,
            "website_path": "./website/sample-r/index.html",
            "href": "/sample-r/index.html",
            "code": "SAMPLER",
            "class": "sample-r",
            "title": PLUGIN_NAME,
            "description": "Rust 版外掛開發參考頁，示範生命週期、設定、CRUD、串流與背景工作呼叫。",
            "action": "進入 SampleR Plugin",
            "icon": "fa-solid fa-puzzle-piece"
        },
        "invoke": {
            "type": "CallPlugin",
            "api_base": "/api/plugin/sample-r",
            "plugin_api_base": "/api/sample-r"
        },
        "permission_settings": mcp::permission_settings(),
        "config_settings": mcp::config_settings(),
        "business_capabilities": ["rust-plugin-development-reference", "compute-intensive-plugin-reference"],
        "technical_capabilities": ["rust", "standard-mcp", "host-auth", "message-hub", "runtime-config", "background-job"],
        "capabilities": ["lifecycle","registration","host-auth","mcp","standard-mcp","config","crud","skill-guide","chat-guide","stream","background-job","scheduled-task","file-payload","messaging","tool-call"]
    })
}

fn status_payload(state: &AppState) -> Value {
    let rt = state.runtime.lock().unwrap();
    json!({
        "id": PLUGIN_ID,
        "name": PLUGIN_NAME,
        "version": VERSION,
        "language": "rust",
        "loaded": rt.loaded,
        "loaded_at": rt.loaded_at.map(|t| t.to_rfc3339()).unwrap_or_default(),
        "last_error": rt.last_error,
        "config_path": rt.config_path,
        "last_modified": rt.last_modified.map(|t| t.to_rfc3339()).unwrap_or_default(),
        "item_count": rt.items.len(),
        "job_count": rt.jobs.len(),
        "scheduled_task_count": rt.config.scheduled_tasks.len(),
        "message_count": rt.message_log.len(),
        "host_auth": host_auth_status(&rt.host_auth)
    })
}

fn host_auth_status(auth: &HostAuth) -> Value {
    json!({
        "available": !auth.token.trim().is_empty(),
        "account": auth.account,
        "project": auth.project,
        "source": auth.source,
        "host_url": first_non_empty(&[&auth.host_url, &auth.base_url]),
        "updated_at": auth.updated_at,
        "expires_at": auth.expires_at
    })
}

fn load_config(state: &AppState, force: bool) -> Result<(), String> {
    let config_path = state.runtime.lock().unwrap().config_path.clone();
    if !force {
        let rt = state.runtime.lock().unwrap();
        if rt.loaded && rt.last_error.is_empty() {
            return Ok(());
        }
    }
    let (mut cfg, modified) = read_config_path(&config_path)?;
    normalize_config(&mut cfg);
    let now = now_rfc3339();
    let mut items = HashMap::new();
    for mut item in cfg.items.clone() {
        if item.id.trim().is_empty() {
            item.id = next_id("item");
        }
        if item.created_at.is_empty() {
            item.created_at = now.clone();
        }
        if item.updated_at.is_empty() {
            item.updated_at = item.created_at.clone();
        }
        items.insert(item.id.clone(), item);
    }
    let mut rt = state.runtime.lock().unwrap();
    rt.config = cfg;
    rt.items = items;
    rt.loaded = true;
    rt.loaded_at = Some(Utc::now());
    rt.last_error.clear();
    rt.last_modified = Some(modified);
    compact_runtime(&mut rt);
    Ok(())
}

fn compact_runtime(rt: &mut Runtime) {
    rt.items.shrink_to_fit();
    rt.jobs.shrink_to_fit();
    rt.scheduled_running.shrink_to_fit();
    rt.message_log.shrink_to_fit();
    rt.config.features.shrink_to_fit();
    rt.config.items.shrink_to_fit();
    rt.config.scheduled_tasks.shrink_to_fit();
    rt.last_error.shrink_to_fit();
}

fn prune_jobs(rt: &mut Runtime) {
    if rt.jobs.len() <= MAX_JOBS {
        return;
    }
    let mut ordered: Vec<_> = rt
        .jobs
        .iter()
        .map(|(id, job)| (id.clone(), job.updated_at.clone(), job.created_at.clone()))
        .collect();
    ordered.sort_by(|a, b| b.1.cmp(&a.1).then_with(|| b.2.cmp(&a.2)));
    let keep: HashSet<String> = ordered
        .into_iter()
        .take(MAX_JOBS)
        .map(|(id, _, _)| id)
        .collect();
    rt.jobs.retain(|id, _| keep.contains(id));
    rt.jobs.shrink_to_fit();
}

fn ensure_loaded(state: &AppState) -> Result<(), String> {
    load_config(state, false)
}

fn read_config_path(path: &Path) -> Result<(SampleConfig, DateTime<Utc>), String> {
    if !path.exists() {
        return initialize_config_from_default(path);
    }
    let text = fs::read_to_string(path).map_err(|err| err.to_string())?;
    let cfg: SampleConfig =
        serde_json::from_str(&text).map_err(|err| format!("invalid sample-r config: {err}"))?;
    let modified = fs::metadata(path)
        .and_then(|m| m.modified())
        .map(DateTime::<Utc>::from)
        .unwrap_or_else(|_| Utc::now());
    Ok((cfg, modified))
}

fn initialize_config_from_default(path: &Path) -> Result<(SampleConfig, DateTime<Utc>), String> {
    let default_path = path
        .parent()
        .unwrap_or_else(|| Path::new("."))
        .join("config.default.json");
    let mut cfg = if default_path.exists() {
        let text = fs::read_to_string(&default_path).map_err(|err| err.to_string())?;
        serde_json::from_str::<SampleConfig>(&text)
            .map_err(|err| format!("invalid sample-r default config: {err}"))?
    } else {
        SampleConfig::default()
    };
    normalize_config(&mut cfg);
    write_config_path(path, &cfg)?;
    Ok((cfg, Utc::now()))
}

fn write_config_path(path: &Path, cfg: &SampleConfig) -> Result<(), String> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|err| err.to_string())?;
    }
    let text = serde_json::to_string_pretty(cfg).map_err(|err| err.to_string())?;
    let tmp = path.with_extension("json.tmp");
    fs::write(&tmp, format!("{text}\n")).map_err(|err| err.to_string())?;
    fs::rename(tmp, path).map_err(|err| err.to_string())
}

fn normalize_config(cfg: &mut SampleConfig) {
    if cfg.version.trim().is_empty() {
        cfg.version = VERSION.to_string();
    }
    if cfg.title.trim().is_empty() {
        cfg.title = PLUGIN_NAME.to_string();
    }
    if cfg.default_group.trim().is_empty() {
        cfg.default_group = "開發工具".to_string();
    }
    if cfg.message.trim().is_empty() {
        cfg.message = "Hello from SampleR Plugin".to_string();
    }
    for (name, enabled) in default_features() {
        cfg.features.entry(name).or_insert(enabled);
    }
    let _ = sanitize_tasks(&mut cfg.scheduled_tasks);
}

fn default_features() -> HashMap<String, bool> {
    HashMap::from([
        ("echo".to_string(), true),
        ("items".to_string(), true),
        ("stream".to_string(), true),
        ("jobs".to_string(), true),
        ("scheduled_tasks".to_string(), true),
        ("files".to_string(), true),
        ("messaging".to_string(), true),
        ("tools".to_string(), true),
        ("mcp".to_string(), true),
    ])
}

fn list_items(state: &AppState) -> Vec<SampleItem> {
    let mut items: Vec<_> = state
        .runtime
        .lock()
        .unwrap()
        .items
        .values()
        .cloned()
        .collect();
    items.sort_by_key(|item| item.id.to_ascii_lowercase());
    items
}

fn parse_item(body: &str) -> Result<SampleItem, String> {
    let mut item: SampleItem =
        serde_json::from_str(body).map_err(|_| "invalid item json".to_string())?;
    item.id = item.id.trim().to_string();
    item.name = item.name.trim().to_string();
    if item.name.is_empty() {
        return Err("item.name is required".to_string());
    }
    Ok(item)
}

fn spawn_job(state: AppState, id: String) {
    tokio::spawn(async move {
        for step in 1..=5 {
            tokio::time::sleep(Duration::from_millis(250)).await;
            if let Ok(mut rt) = state.runtime.lock() {
                if let Some(job) = rt.jobs.get_mut(&id) {
                    job.status = "running".to_string();
                    job.progress = step * 20;
                    job.updated_at = now_rfc3339();
                    if step == 5 {
                        job.status = "done".to_string();
                        job.message = "sample-r job completed".to_string();
                        prune_jobs(&mut rt);
                    }
                }
            }
        }
    });
}

fn list_scheduled_tasks(state: &AppState) -> Vec<ScheduledTask> {
    let mut tasks = state.runtime.lock().unwrap().config.scheduled_tasks.clone();
    tasks.sort_by_key(|task| task.id.to_ascii_lowercase());
    tasks
}

fn save_scheduled_tasks(state: &AppState, mut tasks: Vec<ScheduledTask>) -> Result<(), String> {
    sanitize_tasks(&mut tasks)?;
    let (path, mut cfg) = {
        let rt = state.runtime.lock().unwrap();
        (rt.config_path.clone(), rt.config.clone())
    };
    cfg.scheduled_tasks = tasks;
    write_config_path(&path, &cfg)?;
    load_config(state, true)
}

fn start_scheduler(state: &AppState) {
    let mut handle = state.scheduler.lock().unwrap();
    let should_run = state
        .runtime
        .lock()
        .map(|rt| rt.config.scheduled_tasks.iter().any(|task| task.enabled))
        .unwrap_or(false);
    if !should_run {
        if let Some(task) = handle.take() {
            task.abort();
        }
        return;
    }
    if handle.is_some() {
        return;
    }
    let cloned = state.clone();
    *handle = Some(tokio::spawn(async move {
        loop {
            check_due_tasks(cloned.clone());
            tokio::time::sleep(Duration::from_secs(30)).await;
        }
    }));
}

fn check_due_tasks(state: AppState) {
    if ensure_loaded(&state).is_err() {
        return;
    }
    for task in list_scheduled_tasks(&state) {
        if scheduled_task_due(&task) {
            let _ = start_scheduled_task_run(state.clone(), task.id, false);
        }
    }
}

fn start_scheduled_task_run(
    state: AppState,
    task_id: String,
    manual: bool,
) -> Result<ScheduledTask, String> {
    let task_id = task_id.trim().to_string();
    if task_id.is_empty() {
        return Err("scheduled task id is required".to_string());
    }
    {
        let mut rt = state.runtime.lock().unwrap();
        if rt.scheduled_running.get(&task_id).copied().unwrap_or(false) {
            return Err("scheduled task is already running".to_string());
        }
        rt.scheduled_running.insert(task_id.clone(), true);
    }
    match reserve_scheduled_task_run(&state, &task_id, manual) {
        Ok((task, token)) => {
            let reserved = task.clone();
            tokio::spawn(async move {
                execute_scheduled_task(state.clone(), reserved, token, manual).await;
                if let Ok(mut rt) = state.runtime.lock() {
                    rt.scheduled_running.remove(&task_id);
                }
            });
            Ok(task)
        }
        Err(err) => {
            if let Ok(mut rt) = state.runtime.lock() {
                rt.scheduled_running.remove(&task_id);
            }
            Err(err)
        }
    }
}

fn reserve_scheduled_task_run(
    state: &AppState,
    task_id: &str,
    manual: bool,
) -> Result<(ScheduledTask, String), String> {
    ensure_loaded(state)?;
    let now = Utc::now();
    let (path, mut cfg, found_index) = {
        let rt = state.runtime.lock().unwrap();
        let found = rt
            .config
            .scheduled_tasks
            .iter()
            .position(|task| task.id.eq_ignore_ascii_case(task_id));
        (rt.config_path.clone(), rt.config.clone(), found)
    };
    let index = found_index.ok_or_else(|| format!("scheduled task not found: {task_id}"))?;
    let mut task = cfg.scheduled_tasks[index].clone();
    normalize_task(&mut task, index)?;
    if !manual && !scheduled_task_due(&task) {
        return Err("scheduled task is not due".to_string());
    }
    if parse_time(&task.running_until)
        .map(|until| until > now)
        .unwrap_or(false)
    {
        return Err("scheduled task is already running".to_string());
    }
    let token = format!(
        "scheduled_{}_{}",
        task.id,
        now.timestamp_nanos_opt().unwrap_or_default()
    );
    task.run_token = token.clone();
    task.last_started_at = now.to_rfc3339_opts(SecondsFormat::Nanos, true);
    task.running_until = (now + chrono::Duration::minutes(15)).to_rfc3339();
    task.next_run_at = (now + chrono::Duration::minutes(normalize_interval(&task))).to_rfc3339();
    task.last_error.clear();
    task.updated_at = now.to_rfc3339_opts(SecondsFormat::Nanos, true);
    cfg.scheduled_tasks[index] = task.clone();
    write_config_path(&path, &cfg)?;
    if let Ok(mut rt) = state.runtime.lock() {
        rt.config = cfg;
    }
    Ok((task, token))
}

async fn execute_scheduled_task(state: AppState, task: ScheduledTask, token: String, manual: bool) {
    let result = match task.action.as_str() {
        "echo" => string_from_map(
            &task.payload,
            "message",
            &format!("scheduled echo: {}", task.name),
        ),
        "tool" => {
            let tool_name = string_from_map(&task.payload, "tool", "sample-r.scheduled.tool");
            format!("mock result from {tool_name}")
        }
        _ => {
            let now = now_rfc3339();
            let job = SampleJob {
                id: next_id("job"),
                status: "queued".to_string(),
                progress: 0,
                message: string_from_map(
                    &task.payload,
                    "message",
                    &format!("scheduled task: {}", task.name),
                ),
                created_at: now.clone(),
                updated_at: now,
            };
            state
                .runtime
                .lock()
                .map(|mut rt| {
                    rt.jobs.insert(job.id.clone(), job.clone());
                    prune_jobs(&mut rt);
                })
                .ok();
            spawn_job(state.clone(), job.id.clone());
            format!("started background job {}", job.id)
        }
    };
    if let Err(err) =
        finish_scheduled_task_run(&state, &task.id, &token, &result, "", manual, &task)
    {
        if let Ok(mut rt) = state.runtime.lock() {
            rt.last_error = err;
        }
    }
}

fn finish_scheduled_task_run(
    state: &AppState,
    task_id: &str,
    token: &str,
    result: &str,
    err_text: &str,
    manual: bool,
    reserved: &ScheduledTask,
) -> Result<(), String> {
    ensure_loaded(state)?;
    let finished_at = Utc::now();
    let (path, mut cfg, index) = {
        let rt = state.runtime.lock().unwrap();
        let index = rt
            .config
            .scheduled_tasks
            .iter()
            .position(|task| task.id.eq_ignore_ascii_case(task_id));
        (rt.config_path.clone(), rt.config.clone(), index)
    };
    let index = index.ok_or_else(|| format!("scheduled task not found: {task_id}"))?;
    let mut task = cfg.scheduled_tasks[index].clone();
    if !task.run_token.trim().is_empty() && !token.trim().is_empty() && task.run_token != token {
        return Ok(());
    }
    task.last_run_at = finished_at.to_rfc3339_opts(SecondsFormat::Nanos, true);
    task.running_until.clear();
    task.run_token.clear();
    task.last_started_at.clear();
    task.last_result = truncate_text(result, 500);
    task.last_error = truncate_text(err_text, 500);
    task.updated_at = finished_at.to_rfc3339_opts(SecondsFormat::Nanos, true);
    cfg.scheduled_tasks[index] = task.clone();
    write_config_path(&path, &cfg)?;
    if let Ok(mut rt) = state.runtime.lock() {
        rt.config = cfg;
    }
    append_scheduled_log(
        state,
        ScheduledLogEntry {
            id: String::new(),
            task_id: task.id,
            task_name: task.name,
            action: task.action,
            status: if err_text.trim().is_empty() {
                "success"
            } else {
                "failed"
            }
            .to_string(),
            manual,
            payload: reserved.payload.clone(),
            result: result.to_string(),
            error: err_text.to_string(),
            started_at: first_non_empty(&[&reserved.last_started_at, &task.last_run_at]),
            finished_at: task.last_run_at,
            created_at: String::new(),
        },
    )
}

fn sanitize_tasks(tasks: &mut Vec<ScheduledTask>) -> Result<(), String> {
    if tasks.len() > 100 {
        tasks.truncate(100);
    }
    let now = now_rfc3339();
    let mut seen = HashMap::new();
    for (index, task) in tasks.iter_mut().enumerate() {
        if task.id.trim().is_empty() {
            task.id = format!("task_{}", index + 1);
        }
        normalize_task(task, index)?;
        let key = task.id.to_ascii_lowercase();
        if seen.insert(key, true).is_some() {
            return Err(format!("duplicate scheduled task id: {}", task.id));
        }
        if task.created_at.is_empty() {
            task.created_at = now.clone();
        }
        if task.updated_at.is_empty() {
            task.updated_at = now.clone();
        }
        if task.enabled && task.next_run_at.is_empty() {
            task.next_run_at =
                (Utc::now() + chrono::Duration::minutes(normalize_interval(task))).to_rfc3339();
        }
        if !task.enabled {
            task.next_run_at.clear();
        }
    }
    Ok(())
}

fn normalize_task(task: &mut ScheduledTask, index: usize) -> Result<(), String> {
    task.id = safe_id(&task.id);
    if task.id.is_empty() {
        return Err(format!("scheduled_tasks[{index}].id is required"));
    }
    task.name = task.name.trim().to_string();
    if task.name.is_empty() {
        task.name = "未命名定期任務".to_string();
    }
    task.action = task.action.trim().to_ascii_lowercase();
    if task.action.is_empty() {
        task.action = "job".to_string();
    }
    if !["job", "echo", "tool"].contains(&task.action.as_str()) {
        return Err(format!(
            "scheduled_tasks[{index}].action is not supported: {}",
            task.action
        ));
    }
    if task.interval_minutes <= 0 {
        task.interval_minutes = 60;
    }
    if task.interval_minutes > 1440 {
        task.interval_minutes = 1440;
    }
    task.name = truncate_text(&task.name, 120);
    task.last_result = truncate_text(&task.last_result, 500);
    task.last_error = truncate_text(&task.last_error, 500);
    Ok(())
}

fn scheduled_task_due(task: &ScheduledTask) -> bool {
    if !task.enabled {
        return false;
    }
    let now = Utc::now();
    if parse_time(&task.running_until)
        .map(|until| until > now)
        .unwrap_or(false)
    {
        return false;
    }
    if let Some(next) = parse_time(&task.next_run_at) {
        return next <= now;
    }
    if let Some(last) = parse_time(&task.last_run_at) {
        return last + chrono::Duration::minutes(normalize_interval(task)) <= now;
    }
    true
}

fn normalize_interval(task: &ScheduledTask) -> i64 {
    task.interval_minutes.clamp(1, 1440)
}

fn scheduled_log_base_dir(state: &AppState) -> PathBuf {
    state
        .runtime
        .lock()
        .unwrap()
        .config_path
        .parent()
        .unwrap_or_else(|| Path::new("."))
        .join("runtime")
        .join("scheduled-logs")
}

fn normalize_log_date(value: &str) -> String {
    let value = value.trim();
    if value.len() >= 10 {
        let date = &value[..10];
        if chrono::NaiveDate::parse_from_str(date, "%Y-%m-%d").is_ok() {
            return date.to_string();
        }
    }
    Local::now().format("%Y-%m-%d").to_string()
}

fn scheduled_log_path(state: &AppState, date: &str) -> PathBuf {
    scheduled_log_base_dir(state).join(format!("{}.jsonl", normalize_log_date(date)))
}

fn append_scheduled_log(state: &AppState, mut entry: ScheduledLogEntry) -> Result<(), String> {
    entry.task_id = entry.task_id.trim().to_string();
    if entry.task_id.is_empty() {
        return Err("task_id is required".to_string());
    }
    entry.task_name = truncate_text(entry.task_name.trim(), 120);
    entry.action = entry.action.trim().to_ascii_lowercase();
    entry.status = entry.status.trim().to_ascii_lowercase();
    if !["success", "failed", "skipped"].contains(&entry.status.as_str()) {
        entry.status = "success".to_string();
    }
    entry.result = truncate_text(entry.result.trim(), 20000);
    entry.error = truncate_text(entry.error.trim(), 20000);
    let now = Utc::now().to_rfc3339_opts(SecondsFormat::Nanos, true);
    if entry.created_at.is_empty() {
        entry.created_at = now.clone();
    }
    if entry.finished_at.is_empty() {
        entry.finished_at = now.clone();
    }
    if entry.started_at.is_empty() {
        entry.started_at = entry.finished_at.clone();
    }
    if entry.id.is_empty() {
        entry.id = format!(
            "{}_{}",
            safe_log_name(&entry.task_id),
            Utc::now().timestamp_nanos_opt().unwrap_or_default()
        );
    }
    let path = scheduled_log_path(state, &entry.finished_at);
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|err| err.to_string())?;
    }
    let mut file = fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(path)
        .map_err(|err| err.to_string())?;
    let line = serde_json::to_string(&entry).map_err(|err| err.to_string())?;
    file.write_all(format!("{line}\n").as_bytes())
        .map_err(|err| err.to_string())
}

fn read_scheduled_logs(
    state: &AppState,
    date: &str,
    task_id: &str,
) -> Result<Vec<ScheduledLogEntry>, String> {
    let path = scheduled_log_path(state, date);
    if !path.exists() {
        return Ok(Vec::new());
    }
    let file = fs::File::open(path).map_err(|err| err.to_string())?;
    let mut logs = Vec::new();
    for line in BufReader::new(file).lines().map_while(Result::ok) {
        if line.trim().is_empty() {
            continue;
        }
        if let Ok(entry) = serde_json::from_str::<ScheduledLogEntry>(&line) {
            if task_id.trim().is_empty() || entry.task_id.eq_ignore_ascii_case(task_id.trim()) {
                logs.push(entry);
            }
        }
    }
    Ok(logs)
}

fn skill_root(state: &AppState) -> String {
    state
        .runtime
        .lock()
        .unwrap()
        .skill_root_path
        .display()
        .to_string()
}

fn skill_cards_path(state: &AppState) -> String {
    state
        .runtime
        .lock()
        .unwrap()
        .skill_root_path
        .join("skill-cards.json")
        .display()
        .to_string()
}

fn default_skill_cards_path(state: &AppState) -> PathBuf {
    state
        .runtime
        .lock()
        .unwrap()
        .skill_root_path
        .join("skill-cards.default.json")
}

fn list_skills(state: &AppState) -> Result<Vec<Value>, String> {
    let root = state.runtime.lock().unwrap().skill_root_path.clone();
    fs::create_dir_all(&root).map_err(|err| err.to_string())?;
    let mut skills = Vec::new();
    for entry in fs::read_dir(&root).map_err(|err| err.to_string())? {
        let entry = entry.map_err(|err| err.to_string())?;
        if !entry.file_type().map_err(|err| err.to_string())?.is_dir() {
            continue;
        }
        let entry_path = entry.path().join("SKILL.md");
        if !entry_path.exists() {
            continue;
        }
        let modified = fs::metadata(&entry_path)
            .and_then(|m| m.modified())
            .map(DateTime::<Utc>::from)
            .map(|t| t.to_rfc3339())
            .unwrap_or_default();
        let id = entry.file_name().to_string_lossy().to_string();
        skills.push(
            json!({"id": id, "dir": entry.path(), "entry": entry_path, "modified": modified}),
        );
    }
    Ok(skills)
}

fn read_skill_content(state: &AppState, id: &str) -> Result<(String, String), String> {
    let safe = sanitize_skill_id(id);
    if safe.is_empty() {
        return Err("skill id is required".to_string());
    }
    let root = state.runtime.lock().unwrap().skill_root_path.clone();
    let entry = root.join(&safe).join("SKILL.md");
    let content = fs::read_to_string(&entry)
        .map_err(|_| format!("skill not found: {}", sanitize_skill_id(id)))?;
    Ok((content, entry.display().to_string()))
}

fn read_skill_cards(state: &AppState) -> Result<Vec<SkillCard>, String> {
    ensure_skill_cards_file(state)?;
    let path = PathBuf::from(skill_cards_path(state));
    let text = fs::read_to_string(&path).map_err(|err| err.to_string())?;
    let value: Value = serde_json::from_str(&text)
        .map_err(|err| format!("invalid sample-r skill cards json: {err}"))?;
    let cards_value = value.get("cards").cloned().unwrap_or(value);
    let cards: Vec<SkillCard> = serde_json::from_value(cards_value)
        .map_err(|err| format!("invalid sample-r skill cards json: {err}"))?;
    Ok(normalize_skill_cards(cards))
}

fn ensure_skill_cards_file(state: &AppState) -> Result<(), String> {
    let path = PathBuf::from(skill_cards_path(state));
    if path.exists() {
        return Ok(());
    }
    let default_path = default_skill_cards_path(state);
    let data = fs::read(&default_path).map_err(|err| err.to_string())?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|err| err.to_string())?;
    }
    fs::write(path, data).map_err(|err| err.to_string())
}

fn write_skill_cards(state: &AppState, cards: &[SkillCard]) -> Result<(), String> {
    let path = PathBuf::from(skill_cards_path(state));
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).map_err(|err| err.to_string())?;
    }
    let text =
        serde_json::to_string_pretty(&json!({"cards": normalize_skill_cards(cards.to_vec())}))
            .map_err(|err| err.to_string())?;
    fs::write(path, format!("{text}\n")).map_err(|err| err.to_string())
}

fn parse_skill_card(body: &str) -> Result<SkillCard, String> {
    let mut card: SkillCard =
        serde_json::from_str(body).map_err(|_| "invalid skill card json".to_string())?;
    card.id = card.id.trim().to_string();
    card.title = card.title.trim().to_string();
    card.description = card.description.trim().to_string();
    card.icon = card.icon.trim().to_string();
    card.prompt = card.prompt.trim().to_string();
    if card.title.is_empty() {
        return Err("skill card title is required".to_string());
    }
    if card.prompt.is_empty() {
        return Err("skill card prompt is required".to_string());
    }
    if card.icon.is_empty() {
        card.icon = "fa-wand-magic-sparkles".to_string();
    }
    Ok(card)
}

fn normalize_skill_cards(cards: Vec<SkillCard>) -> Vec<SkillCard> {
    let mut out = Vec::new();
    let mut seen = HashMap::new();
    for mut card in cards {
        card.id = card.id.trim().to_string();
        card.title = card.title.trim().to_string();
        card.description = card.description.trim().to_string();
        card.icon = card.icon.trim().to_string();
        card.prompt = card.prompt.trim().to_string();
        if card.title.is_empty() || card.prompt.is_empty() {
            continue;
        }
        if card.id.is_empty() {
            card.id = next_id("skill");
        }
        if card.icon.is_empty() {
            card.icon = "fa-wand-magic-sparkles".to_string();
        }
        if seen.insert(card.id.clone(), true).is_none() {
            out.push(card);
        }
    }
    out
}

fn upsert_skill_card(cards: &mut Vec<SkillCard>, card: SkillCard) {
    if let Some(existing) = cards.iter_mut().find(|existing| existing.id == card.id) {
        *existing = card;
    } else {
        cards.push(card);
    }
}

async fn publish_message_to_host(
    state: &AppState,
    req: PublishMessageRequest,
) -> Result<Value, String> {
    let topic = req.topic.trim();
    if topic.is_empty() {
        return Err("topic is required".to_string());
    }
    let (host_url, headers) = host_base_url_and_headers(state);
    if host_url.is_empty() {
        return Err(
            "host_url is required; wait for runtime.auth injection or set AGENTIC_HOST_URL"
                .to_string(),
        );
    }
    if headers.is_empty() {
        return Err("host auth token is required before publishing message".to_string());
    }
    let mut payload = json!({
        "topic": topic,
        "event": first_non_empty(&[&req.event, "message"]),
        "source": PLUGIN_ID,
        "payload": req.payload
    });
    if !req.msg_id.trim().is_empty() {
        payload["msg_id"] = json!(req.msg_id.trim());
    }
    let result = tokio::task::spawn_blocking(move || {
        post_host_http_json(&host_url, "/api/msg/publish", headers, &payload)
    })
    .await
    .map_err(|err| format!("host publish worker failed: {err}"))??;
    let status = result
        .get("_http_status")
        .and_then(Value::as_u64)
        .unwrap_or(200);
    if !(200..300).contains(&status) {
        return Err(format!(
            "host publish failed: {}",
            result
                .get("error")
                .and_then(Value::as_str)
                .unwrap_or("HTTP error")
        ));
    }
    if result.get("success").and_then(Value::as_bool) == Some(false) {
        return Err(format!(
            "host publish failed: {}",
            result
                .get("error")
                .and_then(Value::as_str)
                .unwrap_or("unknown error")
        ));
    }
    Ok(result)
}

fn post_host_http_json(
    host_url: &str,
    path: &str,
    headers: HashMap<String, String>,
    payload: &Value,
) -> Result<Value, String> {
    let endpoint = parse_http_endpoint(host_url, path)?;
    let body = compact_json(payload);
    let mut request = format!(
        "POST {} HTTP/1.1\r\nHost: {}\r\nContent-Type: application/json\r\nAccept: application/json\r\nContent-Length: {}\r\nConnection: close\r\n",
        endpoint.path,
        endpoint.host_header,
        body.len()
    );
    for (key, value) in headers {
        if is_safe_header_name(&key) && !value.contains('\r') && !value.contains('\n') {
            request.push_str(&format!("{key}: {value}\r\n"));
        }
    }
    request.push_str("\r\n");
    request.push_str(&body);

    let mut stream = TcpStream::connect((endpoint.host.as_str(), endpoint.port))
        .map_err(|err| format!("host publish connect failed: {err}"))?;
    stream
        .set_read_timeout(Some(Duration::from_secs(8)))
        .map_err(|err| err.to_string())?;
    stream
        .set_write_timeout(Some(Duration::from_secs(8)))
        .map_err(|err| err.to_string())?;
    stream
        .write_all(request.as_bytes())
        .map_err(|err| format!("host publish write failed: {err}"))?;

    let mut response = String::new();
    BufReader::new(stream)
        .read_to_string(&mut response)
        .map_err(|err| format!("host publish read failed: {err}"))?;
    let mut parts = response.splitn(2, "\r\n\r\n");
    let head = parts.next().unwrap_or("");
    let raw_body = parts.next().unwrap_or("").trim();
    let status = head
        .lines()
        .next()
        .and_then(|line| line.split_whitespace().nth(1))
        .and_then(|code| code.parse::<u64>().ok())
        .unwrap_or(0);
    let mut result: Value = serde_json::from_str(raw_body).map_err(|_| {
        format!(
            "host publish returned non-json response: {}",
            truncate_text(raw_body, 300)
        )
    })?;
    if let Some(object) = result.as_object_mut() {
        object.insert("_http_status".to_string(), json!(status));
    }
    Ok(result)
}

struct HttpEndpoint {
    host: String,
    port: u16,
    host_header: String,
    path: String,
}

fn parse_http_endpoint(host_url: &str, request_path: &str) -> Result<HttpEndpoint, String> {
    let raw = host_url.trim().trim_end_matches('/');
    let rest = raw
        .strip_prefix("http://")
        .ok_or_else(|| "host publish currently supports http:// host_url only".to_string())?;
    let (authority, base_path) = rest.split_once('/').unwrap_or((rest, ""));
    let (host, port) = if let Some((host, port)) = authority.rsplit_once(':') {
        let port = port
            .parse::<u16>()
            .map_err(|_| format!("invalid host port: {port}"))?;
        (host.to_string(), port)
    } else {
        (authority.to_string(), 80)
    };
    if host.trim().is_empty() {
        return Err("host_url host is required".to_string());
    }
    let mut path = String::from("/");
    if !base_path.trim_matches('/').is_empty() {
        path.push_str(base_path.trim_matches('/'));
        path.push('/');
    }
    path.push_str(request_path.trim_start_matches('/'));
    let host_header = if authority.contains(':') {
        authority.to_string()
    } else {
        format!("{host}:{port}")
    };
    Ok(HttpEndpoint {
        host,
        port,
        host_header,
        path,
    })
}

fn is_safe_header_name(value: &str) -> bool {
    !value.is_empty()
        && value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_'))
}

fn host_base_url_and_headers(state: &AppState) -> (String, HashMap<String, String>) {
    let auth = state.runtime.lock().unwrap().host_auth.clone();
    let mut host_url = first_non_empty(&[&auth.host_url, &auth.base_url]);
    if host_url.is_empty() {
        host_url = env::var("AGENTIC_HOST_URL")
            .or_else(|_| env::var("AGENTIC_SERVICE_URL"))
            .unwrap_or_default();
    }
    let mut headers = HashMap::new();
    if !auth.token.trim().is_empty() {
        let value = format!(
            "{} {}",
            first_non_empty(&[&auth.token_type, "Bearer"]),
            auth.token
        );
        headers.insert(
            first_non_empty(&[&auth.header, "Authentication"]),
            value.clone(),
        );
        headers.insert("Authentication".to_string(), value.clone());
        headers.insert("Authorization".to_string(), value);
    }
    (host_url.trim_end_matches('/').to_string(), headers)
}

fn stream_response(body: &str) -> Response {
    let payload = parse_object(body);
    let message = string_from_map(&payload, "message", "sample-r stream");
    let mut text = String::new();
    for step in 1..=5 {
        text.push_str(&format!(
            "event: progress\ndata: {}\n\n",
            compact_json(&json!({"step": step, "total": 5, "message": message}))
        ));
    }
    text.push_str(&format!(
        "event: done\ndata: {}\n\n",
        compact_json(&json!({"message": message, "completed_at": now_rfc3339()}))
    ));
    let mut response = Response::new(Body::from(text));
    let headers = response.headers_mut();
    headers.insert(header::CONTENT_TYPE, "text/event-stream".parse().unwrap());
    headers.insert(
        header::CACHE_CONTROL,
        "no-cache, no-transform".parse().unwrap(),
    );
    headers.insert("X-Accel-Buffering", "no".parse().unwrap());
    with_cors(response)
}

fn json_response(payload: Value) -> Response {
    let mut response = Response::new(Body::from(compact_json(&payload)));
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        "application/json; charset=utf-8".parse().unwrap(),
    );
    with_cors(response)
}

fn with_cors(mut response: Response) -> Response {
    let headers = response.headers_mut();
    headers.insert(header::ACCESS_CONTROL_ALLOW_ORIGIN, "*".parse().unwrap());
    headers.insert(
        header::ACCESS_CONTROL_ALLOW_METHODS,
        "GET, POST, PUT, PATCH, DELETE, OPTIONS".parse().unwrap(),
    );
    headers.insert(
        header::ACCESS_CONTROL_ALLOW_HEADERS,
        "Content-Type, Authorization, Authentication, Accept, MCP-Protocol-Version, Mcp-Method, Mcp-Name"
            .parse()
            .unwrap(),
    );
    headers.insert(header::ACCESS_CONTROL_MAX_AGE, "600".parse().unwrap());
    response
}

fn method_not_allowed() -> Value {
    json!({"success": false, "error": "method not allowed"})
}

fn normalize_api_path(raw_path: &str) -> Vec<String> {
    raw_path
        .trim_matches('/')
        .split('/')
        .filter_map(|part| {
            let part = part.trim();
            if part.is_empty() || part == "api" || part == PLUGIN_ID {
                None
            } else {
                Some(part.to_string())
            }
        })
        .collect()
}

fn is_stream_path(raw_path: &str) -> bool {
    normalize_api_path(raw_path).first().map(String::as_str) == Some("stream")
}

fn parse_object(body: &str) -> Map<String, Value> {
    serde_json::from_str::<Value>(body)
        .ok()
        .and_then(|v| v.as_object().cloned())
        .unwrap_or_default()
}

fn query_to_map(query: &str) -> HashMap<String, Vec<String>> {
    let mut map = HashMap::new();
    for pair in query.split('&').filter(|part| !part.is_empty()) {
        let mut parts = pair.splitn(2, '=');
        let key = parts.next().unwrap_or("").to_string();
        let value = parts.next().unwrap_or("").to_string();
        map.entry(key).or_insert_with(Vec::new).push(value);
    }
    map
}

fn selected_headers(headers: &HeaderMap) -> HashMap<String, String> {
    let mut out = HashMap::new();
    for key in ["Content-Type", "Accept", "User-Agent", "X-Request-Id"] {
        if let Some(value) = headers.get(key).and_then(|v| v.to_str().ok()) {
            if !value.trim().is_empty() {
                out.insert(key.to_string(), value.trim().to_string());
            }
        }
    }
    out
}

fn bearer_token_from_header(headers: &HeaderMap, name: &str) -> Option<String> {
    let raw = headers.get(name)?.to_str().ok()?.trim();
    if raw.is_empty() {
        None
    } else if raw.to_ascii_lowercase().starts_with("bearer ") {
        Some(raw[7..].trim().to_string())
    } else {
        Some(raw.to_string())
    }
}

fn first_non_empty(values: &[&str]) -> String {
    values
        .iter()
        .map(|value| value.trim())
        .find(|value| !value.is_empty())
        .unwrap_or("")
        .to_string()
}

fn string_from_map(map: &Map<String, Value>, key: &str, fallback: &str) -> String {
    map.get(key)
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|text| !text.is_empty())
        .unwrap_or(fallback)
        .to_string()
}

fn compact_json(value: &Value) -> String {
    serde_json::to_string(value).unwrap_or_else(|_| "{}".to_string())
}

fn truncate_text(value: &str, limit: usize) -> String {
    if value.len() <= limit {
        value.to_string()
    } else {
        value.chars().take(limit).collect()
    }
}

fn now_rfc3339() -> String {
    Utc::now().to_rfc3339_opts(SecondsFormat::Secs, true)
}

fn next_id(prefix: &str) -> String {
    format!(
        "{prefix}_{}",
        Utc::now().timestamp_nanos_opt().unwrap_or_default()
    )
}

fn parse_time(value: &str) -> Option<DateTime<Utc>> {
    DateTime::parse_from_rfc3339(value.trim())
        .ok()
        .map(|time| time.with_timezone(&Utc))
}

fn safe_id(value: &str) -> String {
    value
        .trim()
        .chars()
        .map(|ch| {
            if ch.is_ascii_alphanumeric() || ch == '_' || ch == '-' {
                ch.to_ascii_lowercase()
            } else {
                '_'
            }
        })
        .collect::<String>()
        .trim_matches('_')
        .to_string()
}

fn safe_log_name(value: &str) -> String {
    let name = value
        .trim()
        .chars()
        .map(|ch| {
            if ch.is_ascii_alphanumeric() || ch == '_' || ch == '-' || ch == '.' {
                ch
            } else {
                '_'
            }
        })
        .collect::<String>()
        .trim_matches(['.', '_', '-'])
        .to_string();
    if name.is_empty() {
        "task".to_string()
    } else {
        name
    }
}

fn sanitize_skill_id(input: &str) -> String {
    let mut out = String::new();
    for ch in input.trim().to_ascii_lowercase().chars() {
        if ch.is_ascii_alphanumeric() {
            out.push(ch);
        } else if ch == '-' || ch == '_' {
            out.push('-');
        }
    }
    while out.contains("--") {
        out = out.replace("--", "-");
    }
    out.trim_matches('-').to_string()
}

fn parse_args() -> HashMap<String, String> {
    let mut out = HashMap::new();
    let mut args = env::args().skip(1);
    while let Some(arg) = args.next() {
        if let Some(value) = arg.strip_prefix("--addr=") {
            out.insert("addr".to_string(), value.to_string());
        } else if let Some(value) = arg.strip_prefix("-addr=") {
            out.insert("addr".to_string(), value.to_string());
        } else if arg == "--addr" || arg == "-addr" {
            if let Some(value) = args.next() {
                out.insert("addr".to_string(), value);
            }
        } else if let Some(value) = arg.strip_prefix("--config=") {
            out.insert("config".to_string(), value.to_string());
        } else if let Some(value) = arg.strip_prefix("-config=") {
            out.insert("config".to_string(), value.to_string());
        } else if arg == "--config" || arg == "-config" {
            if let Some(value) = args.next() {
                out.insert("config".to_string(), value);
            }
        }
    }
    out
}

fn ensure_plugin_workdir() -> Result<PathBuf, String> {
    for key in ["SAMPLE_R_PLUGIN_ROOT", "SAMPLER_PLUGIN_ROOT"] {
        if let Ok(root) = env::var(key) {
            if !root.trim().is_empty() {
                return chdir_to_plugin_root(PathBuf::from(root));
            }
        }
    }
    if let Ok(cwd) = env::current_dir() {
        if let Some(root) = find_plugin_root(&cwd) {
            return chdir_to_plugin_root(root);
        }
    }
    if let Ok(exe) = env::current_exe() {
        if let Some(parent) = exe.parent() {
            if let Some(root) = find_plugin_root(parent) {
                return chdir_to_plugin_root(root);
            }
        }
    }
    Err(format!(
        "找不到 SampleR plugin 根目錄；請從包含 {MANIFEST_PATH} 的目錄或其子目錄啟動，或設定 SAMPLE_R_PLUGIN_ROOT"
    ))
}

fn chdir_to_plugin_root(root: PathBuf) -> Result<PathBuf, String> {
    let abs = root
        .canonicalize()
        .map_err(|err| format!("SampleR plugin 根目錄無效: {}: {err}", root.display()))?;
    if !abs.join(MANIFEST_PATH).exists() {
        return Err(format!("SampleR plugin 根目錄無效: {}", abs.display()));
    }
    env::set_current_dir(&abs).map_err(|err| err.to_string())?;
    Ok(abs)
}

fn find_plugin_root(start: &Path) -> Option<PathBuf> {
    let mut dir = start.to_path_buf();
    loop {
        if dir.join(MANIFEST_PATH).exists() {
            return Some(dir);
        }
        if !dir.pop() {
            return None;
        }
    }
}
