package sampleplugin

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const responseHandledMarker = "__sample_plugin_response_handled__"

var SamplePluginConfigPath = "plugins/sample/config.json"
var SamplePluginSkillRootPath = "plugins/sample/skill"
var SamplePluginSkillCardsPath = filepath.Join(SamplePluginSkillRootPath, "skill-cards.json")
var SamplePluginDefaultSkillCardsPath = filepath.Join(SamplePluginSkillRootPath, "skill-cards.default.json")
var SamplePluginVersion = "0.1.0"

type HttpAPI_Plugin struct {
	mu               sync.Mutex
	Shutdown         func()
	loaded           bool
	config           sampleConfig
	items            map[string]sampleItem
	jobs             map[string]*sampleJob
	scheduledStarted bool
	scheduledStop    chan struct{}
	scheduledRunning map[string]bool
	hostAuth         sampleHostAuth
	messageLog       []sampleHubMessage
	loadedAt         time.Time
	lastLoadErr      string
	lastModified     time.Time
	pendingStop      bool
}

type sampleHostAuth struct {
	Token     string
	TokenType string
	Header    string
	Account   string
	Project   string
	Source    string
	HostURL   string
	BaseURL   string
	ExpiresAt time.Time
	UpdatedAt time.Time
}

type sampleHostAuthRequest struct {
	AuthToken string `json:"auth_token"`
	TokenType string `json:"token_type"`
	Header    string `json:"header"`
	Account   string `json:"account"`
	Project   string `json:"project"`
	Source    string `json:"source"`
	HostURL   string `json:"host_url"`
	BaseURL   string `json:"base_url"`
	ExpiresAt string `json:"expires_at"`
}

type sampleConfig struct {
	Version        string                `json:"version"`
	Title          string                `json:"title,omitempty"`
	DefaultGroup   string                `json:"default_group,omitempty"`
	Message        string                `json:"message"`
	Features       map[string]bool       `json:"features"`
	Items          []sampleItem          `json:"items"`
	ScheduledTasks []sampleScheduledTask `json:"scheduled_tasks,omitempty"`
}

type sampleItem struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Value     any            `json:"value,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt string         `json:"created_at,omitempty"`
	UpdatedAt string         `json:"updated_at,omitempty"`
}

type sampleSkillCard struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Prompt      string `json:"prompt"`
}

type sampleJob struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Progress  int    `json:"progress"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type sampleScheduledTask struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	IntervalMinutes int            `json:"interval_minutes"`
	Action          string         `json:"action"`
	Payload         map[string]any `json:"payload,omitempty"`
	LastRunAt       string         `json:"last_run_at,omitempty"`
	NextRunAt       string         `json:"next_run_at,omitempty"`
	RunningUntil    string         `json:"running_until,omitempty"`
	RunToken        string         `json:"run_token,omitempty"`
	LastStartedAt   string         `json:"last_started_at,omitempty"`
	LastResult      string         `json:"last_result,omitempty"`
	LastError       string         `json:"last_error,omitempty"`
	CreatedAt       string         `json:"created_at,omitempty"`
	UpdatedAt       string         `json:"updated_at,omitempty"`
}

type sampleScheduledLogEntry struct {
	ID         string         `json:"id,omitempty"`
	TaskID     string         `json:"task_id"`
	TaskName   string         `json:"task_name,omitempty"`
	Action     string         `json:"action,omitempty"`
	Status     string         `json:"status"`
	Manual     bool           `json:"manual,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	Result     string         `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	StartedAt  string         `json:"started_at,omitempty"`
	FinishedAt string         `json:"finished_at,omitempty"`
	CreatedAt  string         `json:"created_at,omitempty"`
}

type sampleHubMessage struct {
	Seq        uint64         `json:"seq,omitempty"`
	MsgID      string         `json:"msg_id,omitempty"`
	Topic      string         `json:"topic"`
	Event      string         `json:"event"`
	Source     string         `json:"source,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	TS         string         `json:"ts,omitempty"`
	ReceivedAt string         `json:"received_at,omitempty"`
}

type sampleMessagePublishRequest struct {
	Topic   string         `json:"topic"`
	Event   string         `json:"event"`
	MsgID   string         `json:"msg_id"`
	Payload map[string]any `json:"payload"`
}

func (h *HttpAPI_Plugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	applySampleCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if h.serveSampleMCPHTTP(w, r) {
		return
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeSampleJSONBytes(w, mustSampleJSON(map[string]any{"success": false, "error": err.Error()}))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	path := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	response := h.Process(w, r, path, string(bodyBytes))
	if string(response) == responseHandledMarker {
		return
	}
	writeSampleJSONBytes(w, response)
	if h.consumePendingServiceShutdown() {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		h.requestServiceShutdown()
	}
}

func applySampleCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Authentication, Accept, MCP-Protocol-Version, Mcp-Method, Mcp-Name")
	w.Header().Set("Access-Control-Max-Age", "600")
}

func writeSampleJSONBytes(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}

func mustSampleJSON(payload any) []byte {
	data, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"success":false,"error":"json marshal failed"}`)
	}
	return data
}

func sampleMethodNotAllowedResponse() []byte {
	return mustSampleJSON(map[string]any{"success": false, "error": "method not allowed"})
}

func normalizeSampleEndpointPath(path []string, root string) []string {
	normalized := make([]string, 0, len(path))
	root = strings.TrimSpace(root)
	rootSkipped := false
	for _, part := range path {
		part = strings.TrimSpace(strings.Trim(part, "/"))
		if idx := strings.Index(part, "?"); idx >= 0 {
			part = part[:idx]
		}
		if part == "" {
			continue
		}
		if root != "" && !rootSkipped && part == root {
			rootSkipped = true
			continue
		}
		normalized = append(normalized, part)
	}
	return normalized
}

func lastSamplePathSegment(path []string, fallback string) string {
	if len(path) == 0 {
		return fallback
	}
	return path[len(path)-1]
}

func (h *HttpAPI_Plugin) Process(w http.ResponseWriter, r *http.Request, path []string, body string) []byte {
	normalizedPath := normalizeSampleAPIPath(path)
	if len(normalizedPath) == 0 {
		return h.handleCatalog()
	}

	switch normalizedPath[0] {
	case "plugin":
		return h.handlePluginAPI(r, normalizedPath[1:])
	case "hello":
		return h.handleHelloAPI(r)
	case "health", "heatlth":
		return h.handleHealthAPI(r)
	case "apis":
		return h.handleCatalog()
	case "mcp":
		return h.handleMCPAPI(r, normalizedPath[1:])
	case "config":
		return h.handleConfigAPI(r, body)
	case "echo":
		return h.handleEchoAPI(r, body)
	case "items":
		return h.handleItemsAPI(r, normalizedPath[1:], body)
	case "skills":
		return h.handleSkillsAPI(r, normalizedPath[1:], body)
	case "stream":
		return h.handleStreamAPI(w, r, body)
	case "jobs":
		return h.handleJobsAPI(r, normalizedPath[1:], body)
	case "scheduled-tasks":
		return h.handleScheduledTasksAPI(r, normalizedPath[1:], body)
	case "scheduled-logs":
		return h.handleScheduledLogsAPI(r, normalizedPath[1:], body)
	case "files":
		return h.handleFilesAPI(r, body)
	case "msg":
		return h.handleMessageAPI(r, normalizedPath[1:], body)
	case "tools":
		return h.handleToolsAPI(r, normalizedPath[1:], body)
	default:
		return h.handleCatalog()
	}
}

func normalizeSampleAPIPath(path []string) []string {
	normalized := normalizeSampleEndpointPath(path, "api")
	return normalizeSampleEndpointPath(normalized, "sample")
}

func (h *HttpAPI_Plugin) handleCatalog() []byte {
	return mustSampleJSON(map[string]any{
		"success": true,
		"service": "sample",
		"plugin":  h.statusPayload(),
		"apis":    sampleAPICatalog(),
	})
}

func sampleAPICatalog() []map[string]any {
	return []map[string]any{
		sampleAPIEntry("/api/sample/apis", "GET", "list every Sample Plugin API with descriptions and examples", nil),
		sampleAPIEntry("/api/hello", "GET", "minimal plugin hello handshake", nil),
		sampleAPIEntry("/api/health", "GET", "minimal plugin health check", nil),
		sampleAPIEntry("/api/heatlth", "GET", "compatibility alias for /api/health", nil),
		sampleAPIEntry("/api/sample/plugin/status", "GET", "show plugin runtime status", nil),
		sampleAPIEntry("/api/sample/plugin/registration", "GET", "show plugin registration metadata", nil),
		sampleAPIEntry("/api/sample/plugin/auth", "POST", "receive host auth token for plugin service program calls", map[string]any{
			"auth_token": "TOKEN",
			"token_type": "Bearer",
			"header":     "Authentication",
			"source":     "host",
		}),
		sampleAPIEntry("/api/sample/plugin/load", "POST", "load config and initialize runtime state", map[string]any{}),
		sampleAPIEntry("/api/sample/plugin/unload", "POST", "clear runtime state and stop service process after response", map[string]any{}),
		sampleAPIEntry("/api/sample/plugin/reload", "POST", "reload config", map[string]any{}),
		sampleAPIEntry("/api/sample/config", "GET|PUT", "read or update sample config", map[string]any{
			"version":       SamplePluginVersion,
			"title":         "Sample Plugin",
			"default_group": "開發工具",
			"message":       "Hello from Sample Plugin",
			"features":      map[string]bool{"echo": true, "items": true, "stream": true, "jobs": true, "scheduled_tasks": true, "files": true, "messaging": true, "tools": true, "mcp": true},
			"items":         []map[string]any{{"id": "demo-1", "name": "Demo Item", "value": "demo value"}},
		}),
		sampleAPIEntry("/api/sample/echo", "GET|POST", "return method, query, headers and JSON body", map[string]any{"message": "hello sample plugin"}),
		sampleAPIEntry("/api/sample/items", "GET|POST", "list or create demo item", map[string]any{"name": "demo item", "value": map[string]any{"enabled": true}}),
		sampleAPIEntry("/api/sample/items/{id}", "GET|PUT|DELETE", "read, update or delete demo item", map[string]any{"name": "updated item", "value": "updated value"}),
		sampleAPIEntry("/api/sample/skills", "GET", "list sample plugin skills", nil),
		sampleAPIEntry("/api/sample/skills/{id}/content", "GET", "read sample plugin skill markdown content", nil),
		sampleAPIEntry("/api/sample/skills/cards", "GET|POST", "list or create sample chat skill card", map[string]any{
			"title":       "外掛狀態診斷",
			"description": "檢查 service、config、items 與載入狀態。",
			"icon":        "fa-heart-pulse",
			"prompt":      "請根據目前 Sample Plugin 的即時狀態進行診斷。",
		}),
		sampleAPIEntry("/api/sample/skills/cards/{id}", "GET|PUT|DELETE", "read, update or delete sample chat skill card", map[string]any{
			"title":       "API 串接建議",
			"description": "產生 fetchPlugin 呼叫範例。",
			"icon":        "fa-route",
			"prompt":      "請整理 Sample Plugin API 呼叫範例。",
		}),
		sampleAPIEntry("/api/sample/stream", "GET|POST", "server-sent events stream example", map[string]any{"message": "stream progress"}),
		sampleAPIEntry("/api/sample/jobs", "POST", "start background job", map[string]any{"message": "sample background job"}),
		sampleAPIEntry("/api/sample/jobs/{id}", "GET", "read background job status", nil),
		sampleAPIEntry("/api/sample/scheduled-tasks", "GET|PUT", "list or save scheduled tasks", map[string]any{
			"scheduled_tasks": []map[string]any{{
				"id":               "sample_hourly_job",
				"name":             "Sample 每小時背景任務",
				"enabled":          false,
				"interval_minutes": 60,
				"action":           "job",
				"payload":          map[string]any{"message": "sample scheduled background job"},
			}},
		}),
		sampleAPIEntry("/api/sample/scheduled-tasks/{id}", "GET|DELETE", "read or delete scheduled task", nil),
		sampleAPIEntry("/api/sample/scheduled-tasks/{id}/run", "POST", "run scheduled task immediately", map[string]any{}),
		sampleAPIEntry("/api/sample/scheduled-tasks/{id}/terminate", "POST", "terminate a running scheduled task state", map[string]any{}),
		sampleAPIEntry("/api/sample/scheduled-logs", "GET", "read scheduled task logs by date and optional task_id", nil),
		sampleAPIEntry("/api/sample/files", "POST", "receive JSON base64 or multipart file payload", map[string]any{"file_name": "hello.txt", "text": "hello sample plugin"}),
		sampleAPIEntry("/api/sample/msg", "POST", "receive MessageHub webhook deliveries from the main service", map[string]any{
			"seq":    42,
			"topic":  "sample.notice",
			"event":  "created",
			"source": "other-plugin",
			"payload": map[string]any{
				"id": "demo",
			},
		}),
		sampleAPIEntry("/api/sample/msg/events", "GET|DELETE", "list or clear recent MessageHub webhook deliveries stored by Sample Plugin", nil),
		sampleAPIEntry("/api/sample/msg/publish", "POST", "publish a MessageHub event through the main service using host auth", map[string]any{
			"topic": "sample.notice",
			"event": "created",
			"payload": map[string]any{
				"message": "hello from sample plugin",
			},
		}),
		sampleAPIEntry("/api/sample/tools/run", "POST", "mock tool invocation endpoint", map[string]any{"tool": "sample.tool", "input": map[string]any{"message": "hello"}}),
		sampleAPIEntry("/api/sample/mcp", "GET", "show Sample MCP protocol metadata and tool definitions", nil),
		sampleAPIEntry("/mcp", "POST", "standard stateless MCP JSON-RPC endpoint", map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/list",
			"params":  map[string]any{},
		}),
	}
}

func sampleAPIEntry(path string, method string, description string, body any) map[string]any {
	entry := map[string]any{
		"path":        path,
		"method":      method,
		"description": description,
		"examples": map[string]any{
			"fetch_plugin": sampleFetchPluginExample(path, method, body),
			"curl":         sampleCurlExample(path, method, body),
		},
	}
	if body != nil {
		entry["request_body"] = body
	}
	return entry
}

func sampleFetchPluginExample(path string, method string, body any) string {
	methodName := samplePreferredMethod(method)
	if path == "/mcp" {
		return fmt.Sprintf(`window.AgenticTalkAPI.fetchPlugin("sample", "/mcp", { method: "%s", headers: { "Content-Type": "application/json", "MCP-Protocol-Version": "2025-11-25" }, body: JSON.stringify(%s) })`, methodName, sampleCompactJSON(body))
	}
	if body == nil || methodName == http.MethodGet || methodName == http.MethodDelete {
		return fmt.Sprintf(`window.AgenticTalkAPI.fetchPlugin("sample", "%s", { method: "%s" })`, path, methodName)
	}
	return fmt.Sprintf(`window.AgenticTalkAPI.fetchPlugin("sample", "%s", { method: "%s", body: JSON.stringify(%s) })`, path, methodName, sampleCompactJSON(body))
}

func sampleCurlExample(path string, method string, body any) string {
	methodName := samplePreferredMethod(method)
	url := "http://127.0.0.1:18182" + path
	if path == "/mcp" {
		url = "http://127.0.0.1:18182/mcp"
	}
	if body == nil || methodName == http.MethodGet || methodName == http.MethodDelete {
		return fmt.Sprintf(`curl -X %s "%s"`, methodName, url)
	}
	return fmt.Sprintf(`curl -X %s "%s" -H "Content-Type: application/json" -d '%s'`, methodName, url, sampleCompactJSON(body))
}

func samplePreferredMethod(method string) string {
	for _, candidate := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodGet} {
		if strings.Contains(method, candidate) {
			return candidate
		}
	}
	return http.MethodGet
}

func sampleCompactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (h *HttpAPI_Plugin) handlePluginAPI(r *http.Request, path []string) []byte {
	cmd := lastSamplePathSegment(path, "status")
	switch cmd {
	case "status":
		if r.Method != http.MethodGet {
			return sampleMethodNotAllowedResponse()
		}
		return mustSampleJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
	case "load":
		if r.Method != http.MethodPost {
			return sampleMethodNotAllowedResponse()
		}
		return h.loadResponse()
	case "auth":
		if r.Method != http.MethodPost {
			return sampleMethodNotAllowedResponse()
		}
		return h.authResponse(r)
	case "reload":
		if r.Method != http.MethodPost {
			return sampleMethodNotAllowedResponse()
		}
		h.mu.Lock()
		h.loaded = false
		h.mu.Unlock()
		return h.loadResponse()
	case "unload":
		if r.Method != http.MethodPost {
			return sampleMethodNotAllowedResponse()
		}
		h.mu.Lock()
		h.loaded = false
		h.config = sampleConfig{}
		h.items = nil
		h.stopScheduledTaskLoopLocked()
		h.loadedAt = time.Time{}
		h.lastLoadErr = ""
		h.lastModified = time.Time{}
		h.pendingStop = true
		h.mu.Unlock()
		return mustSampleJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
	case "registration":
		if r.Method != http.MethodGet {
			return sampleMethodNotAllowedResponse()
		}
		return mustSampleJSON(map[string]any{"success": true, "plugin": h.registrationPayload()})
	default:
		return h.handleCatalog()
	}
}

func (h *HttpAPI_Plugin) consumePendingServiceShutdown() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	pending := h.pendingStop
	h.pendingStop = false
	return pending
}

func (h *HttpAPI_Plugin) requestServiceShutdown() {
	if h == nil || h.Shutdown == nil {
		return
	}
	shutdown := h.Shutdown
	go shutdown()
}

func (h *HttpAPI_Plugin) handleHelloAPI(r *http.Request) []byte {
	if r.Method != http.MethodGet {
		return sampleMethodNotAllowedResponse()
	}
	return mustSampleJSON(map[string]any{
		"success": true,
		"hello": map[string]any{
			"id":      "sample",
			"name":    "Sample Plugin",
			"version": SamplePluginVersion,
			"time":    time.Now().Format(time.RFC3339),
		},
	})
}

func (h *HttpAPI_Plugin) handleHealthAPI(r *http.Request) []byte {
	if r.Method != http.MethodGet {
		return sampleMethodNotAllowedResponse()
	}
	plugin := h.statusPayload()
	return mustSampleJSON(map[string]any{
		"success": true,
		"healthy": true,
		"status":  "ok",
		"plugin":  plugin,
	})
}

func (h *HttpAPI_Plugin) handleMCPAPI(r *http.Request, _ []string) []byte {
	if r.Method != http.MethodGet {
		return sampleMethodNotAllowedResponse()
	}
	return mustSampleJSON(map[string]any{
		"success": true,
		"mcp":     sampleMCPMetadata(),
	})
}

func (h *HttpAPI_Plugin) handleConfigAPI(r *http.Request, body string) []byte {
	if r.Method == http.MethodGet {
		if err := h.ensureLoaded(); err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		return mustSampleJSON(map[string]any{"success": true, "path": SamplePluginConfigPath, "config": h.config})
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodPatch {
		var cfg sampleConfig
		if err := json.Unmarshal([]byte(body), &cfg); err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": "invalid config json"})
		}
		if err := writeSampleConfig(cfg); err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
		}
		h.mu.Lock()
		h.loaded = false
		h.mu.Unlock()
		return h.loadResponse()
	}
	return sampleMethodNotAllowedResponse()
}

func (h *HttpAPI_Plugin) handleEchoAPI(r *http.Request, body string) []byte {
	payload := map[string]any{}
	if strings.TrimSpace(body) != "" {
		_ = json.Unmarshal([]byte(body), &payload)
	}
	return mustSampleJSON(map[string]any{
		"success": true,
		"echo": map[string]any{
			"method":  r.Method,
			"path":    r.URL.Path,
			"query":   queryToMap(r),
			"headers": selectedHeaders(r),
			"body":    payload,
			"raw":     body,
			"time":    time.Now().Format(time.RFC3339),
		},
	})
}

func (h *HttpAPI_Plugin) handleItemsAPI(r *http.Request, path []string, body string) []byte {
	if err := h.ensureLoaded(); err != nil {
		return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	if len(path) == 0 {
		switch r.Method {
		case http.MethodGet:
			return mustSampleJSON(map[string]any{"success": true, "items": h.listItems()})
		case http.MethodPost:
			item, err := parseSampleItem(body)
			if err != nil {
				return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
			}
			if item.ID == "" {
				item.ID = nextSampleID("item")
			}
			now := time.Now().Format(time.RFC3339)
			item.CreatedAt = now
			item.UpdatedAt = now
			h.mu.Lock()
			if h.items == nil {
				h.items = map[string]sampleItem{}
			}
			h.items[item.ID] = item
			h.mu.Unlock()
			return mustSampleJSON(map[string]any{"success": true, "item": item})
		default:
			return sampleMethodNotAllowedResponse()
		}
	}

	id := strings.TrimSpace(path[0])
	if id == "" {
		return mustSampleJSON(map[string]any{"success": false, "error": "item id is required"})
	}
	switch r.Method {
	case http.MethodGet:
		item, ok := h.getItem(id)
		if !ok {
			return mustSampleJSON(map[string]any{"success": false, "error": "item not found", "id": id})
		}
		return mustSampleJSON(map[string]any{"success": true, "item": item})
	case http.MethodPut, http.MethodPatch:
		item, err := parseSampleItem(body)
		if err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
		}
		item.ID = id
		item.UpdatedAt = time.Now().Format(time.RFC3339)
		h.mu.Lock()
		if h.items == nil {
			h.items = map[string]sampleItem{}
		}
		if old, ok := h.items[id]; ok && item.CreatedAt == "" {
			item.CreatedAt = old.CreatedAt
		}
		h.items[id] = item
		h.mu.Unlock()
		return mustSampleJSON(map[string]any{"success": true, "item": item})
	case http.MethodDelete:
		h.mu.Lock()
		delete(h.items, id)
		h.mu.Unlock()
		return mustSampleJSON(map[string]any{"success": true, "id": id})
	default:
		return sampleMethodNotAllowedResponse()
	}
}

func (h *HttpAPI_Plugin) handleSkillsAPI(r *http.Request, path []string, body string) []byte {
	if len(path) == 0 {
		if r.Method != http.MethodGet {
			return sampleMethodNotAllowedResponse()
		}
		skills, err := listSamplePluginSkills()
		if err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "root": SamplePluginSkillRootPath})
		}
		return mustSampleJSON(map[string]any{"success": true, "root": SamplePluginSkillRootPath, "skills": skills})
	}
	if strings.EqualFold(path[0], "cards") {
		return h.handleSkillCardsAPI(r, path[1:], body)
	}
	if len(path) == 2 && strings.EqualFold(path[1], "content") {
		if r.Method != http.MethodGet {
			return sampleMethodNotAllowedResponse()
		}
		content, entryPath, err := readSamplePluginSkillContent(path[0])
		if err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "id": path[0]})
		}
		return mustSampleJSON(map[string]any{
			"success": true,
			"id":      sanitizeSamplePluginSkillID(path[0]),
			"entry":   entryPath,
			"content": content,
		})
	}
	return mustSampleJSON(map[string]any{"success": false, "error": "unknown sample skill endpoint"})
}

func (h *HttpAPI_Plugin) handleSkillCardsAPI(r *http.Request, path []string, body string) []byte {
	if len(path) == 0 {
		switch r.Method {
		case http.MethodGet:
			cards, err := readSampleSkillCards()
			if err != nil {
				return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "path": SamplePluginSkillCardsPath})
			}
			return mustSampleJSON(map[string]any{"success": true, "path": SamplePluginSkillCardsPath, "cards": cards})
		case http.MethodPost:
			card, err := parseSampleSkillCard(body)
			if err != nil {
				return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
			}
			cards, err := readSampleSkillCards()
			if err != nil {
				return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
			}
			if card.ID == "" {
				card.ID = nextSampleID("skill")
			}
			cards = upsertSampleSkillCard(cards, card)
			if err := writeSampleSkillCards(cards); err != nil {
				return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
			}
			return mustSampleJSON(map[string]any{"success": true, "card": card, "cards": cards})
		default:
			return sampleMethodNotAllowedResponse()
		}
	}
	id := strings.TrimSpace(path[0])
	if id == "" {
		return mustSampleJSON(map[string]any{"success": false, "error": "skill card id is required"})
	}
	cards, err := readSampleSkillCards()
	if err != nil {
		return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
	}
	switch r.Method {
	case http.MethodGet:
		for _, card := range cards {
			if card.ID == id {
				return mustSampleJSON(map[string]any{"success": true, "card": card})
			}
		}
		return mustSampleJSON(map[string]any{"success": false, "error": "skill card not found", "id": id})
	case http.MethodPut, http.MethodPatch:
		card, err := parseSampleSkillCard(body)
		if err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
		}
		card.ID = id
		cards = upsertSampleSkillCard(cards, card)
		if err := writeSampleSkillCards(cards); err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
		}
		return mustSampleJSON(map[string]any{"success": true, "card": card, "cards": cards})
	case http.MethodDelete:
		next := make([]sampleSkillCard, 0, len(cards))
		for _, card := range cards {
			if card.ID != id {
				next = append(next, card)
			}
		}
		if err := writeSampleSkillCards(next); err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
		}
		return mustSampleJSON(map[string]any{"success": true, "id": id, "cards": next})
	default:
		return sampleMethodNotAllowedResponse()
	}
}

func (h *HttpAPI_Plugin) handleStreamAPI(w http.ResponseWriter, r *http.Request, body string) []byte {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return sampleMethodNotAllowedResponse()
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	payload := map[string]any{}
	if strings.TrimSpace(body) != "" {
		_ = json.Unmarshal([]byte(body), &payload)
	}
	message := strings.TrimSpace(stringFromSampleMap(payload, "message", "sample stream"))
	for i := 1; i <= 5; i++ {
		writeSampleSSE(w, "progress", map[string]any{"step": i, "total": 5, "message": message})
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(180 * time.Millisecond)
	}
	writeSampleSSE(w, "done", map[string]any{"message": message, "completed_at": time.Now().Format(time.RFC3339)})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return []byte(responseHandledMarker)
}

func (h *HttpAPI_Plugin) handleJobsAPI(r *http.Request, path []string, body string) []byte {
	if len(path) == 0 {
		if r.Method != http.MethodPost {
			return sampleMethodNotAllowedResponse()
		}
		payload := map[string]any{}
		_ = json.Unmarshal([]byte(body), &payload)
		job := &sampleJob{
			ID:        nextSampleID("job"),
			Status:    "queued",
			Message:   stringFromSampleMap(payload, "message", "sample background job"),
			CreatedAt: time.Now().Format(time.RFC3339),
			UpdatedAt: time.Now().Format(time.RFC3339),
		}
		h.mu.Lock()
		if h.jobs == nil {
			h.jobs = map[string]*sampleJob{}
		}
		h.jobs[job.ID] = job
		h.mu.Unlock()
		go h.runSampleJob(job.ID)
		return mustSampleJSON(map[string]any{"success": true, "job": job})
	}
	if r.Method != http.MethodGet {
		return sampleMethodNotAllowedResponse()
	}
	job, ok := h.getJob(path[0])
	if !ok {
		return mustSampleJSON(map[string]any{"success": false, "error": "job not found", "id": path[0]})
	}
	return mustSampleJSON(map[string]any{"success": true, "job": job})
}

func (h *HttpAPI_Plugin) handleScheduledTasksAPI(r *http.Request, path []string, body string) []byte {
	if err := h.ensureLoaded(); err != nil {
		return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	if len(path) == 0 {
		switch r.Method {
		case http.MethodGet:
			return mustSampleJSON(map[string]any{"success": true, "scheduled_tasks": h.listScheduledTasks()})
		case http.MethodPost, http.MethodPut, http.MethodPatch:
			var req struct {
				ScheduledTasks []sampleScheduledTask `json:"scheduled_tasks"`
			}
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				return mustSampleJSON(map[string]any{"success": false, "error": "invalid scheduled tasks json"})
			}
			tasks, err := sanitizeSampleScheduledTasks(req.ScheduledTasks)
			if err != nil {
				return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
			}
			if err := h.saveScheduledTasks(tasks); err != nil {
				return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
			}
			return mustSampleJSON(map[string]any{"success": true, "scheduled_tasks": h.listScheduledTasks()})
		default:
			return sampleMethodNotAllowedResponse()
		}
	}
	if len(path) == 2 && strings.EqualFold(path[1], "run") {
		if r.Method != http.MethodPost {
			return sampleMethodNotAllowedResponse()
		}
		task, err := h.startScheduledTaskRunAsync(strings.TrimSpace(path[0]), true)
		if err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
		}
		return mustSampleJSON(map[string]any{"success": true, "accepted": true, "scheduled_task": task, "started_at": task.LastStartedAt, "scheduled_tasks": h.listScheduledTasks()})
	}
	if len(path) == 2 && strings.EqualFold(path[1], "terminate") {
		if r.Method != http.MethodPost {
			return sampleMethodNotAllowedResponse()
		}
		task, err := h.terminateScheduledTaskRun(strings.TrimSpace(path[0]), true, "由使用者手動終止。")
		if err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
		}
		return mustSampleJSON(map[string]any{"success": true, "terminated": true, "scheduled_task": task, "scheduled_tasks": h.listScheduledTasks()})
	}
	if len(path) != 1 {
		return mustSampleJSON(map[string]any{"success": false, "error": "unknown scheduled task endpoint"})
	}
	taskID := strings.TrimSpace(path[0])
	switch r.Method {
	case http.MethodGet:
		for _, task := range h.listScheduledTasks() {
			if strings.EqualFold(task.ID, taskID) {
				return mustSampleJSON(map[string]any{"success": true, "scheduled_task": task})
			}
		}
		return mustSampleJSON(map[string]any{"success": false, "error": "scheduled task not found", "id": taskID})
	case http.MethodDelete:
		tasks := h.listScheduledTasks()
		next := make([]sampleScheduledTask, 0, len(tasks))
		found := false
		for _, task := range tasks {
			if strings.EqualFold(task.ID, taskID) {
				found = true
				continue
			}
			next = append(next, task)
		}
		if !found {
			return mustSampleJSON(map[string]any{"success": false, "error": "scheduled task not found", "id": taskID})
		}
		if err := h.saveScheduledTasks(next); err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
		}
		return mustSampleJSON(map[string]any{"success": true, "scheduled_tasks": h.listScheduledTasks()})
	default:
		return sampleMethodNotAllowedResponse()
	}
}

func (h *HttpAPI_Plugin) handleScheduledLogsAPI(r *http.Request, path []string, _ string) []byte {
	if len(path) != 0 {
		return mustSampleJSON(map[string]any{"success": false, "error": "unknown scheduled log endpoint"})
	}
	if r.Method != http.MethodGet {
		return sampleMethodNotAllowedResponse()
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
	logs, err := readSampleScheduledLogs(date, taskID)
	if err != nil {
		return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "date": normalizeSampleLogDate(date), "task_id": taskID})
	}
	return mustSampleJSON(map[string]any{
		"success": true,
		"date":    normalizeSampleLogDate(date),
		"task_id": taskID,
		"count":   len(logs),
		"logs":    logs,
	})
}

func (h *HttpAPI_Plugin) handleFilesAPI(r *http.Request, body string) []byte {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		return sampleMethodNotAllowedResponse()
	}
	files := []map[string]any{}
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
		}
		files = append(files, multipartFileSummaries(r.MultipartForm)...)
	} else {
		var req struct {
			FileName string `json:"file_name"`
			Data     string `json:"data_base64"`
			Text     string `json:"text"`
		}
		_ = json.Unmarshal([]byte(body), &req)
		size := len([]byte(req.Text))
		if req.Data != "" {
			if decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(req.Data, "base64,")); err == nil {
				size = len(decoded)
			}
		}
		files = append(files, map[string]any{"file_name": req.FileName, "size": size, "source": "json"})
	}
	return mustSampleJSON(map[string]any{"success": true, "files": files})
}

func (h *HttpAPI_Plugin) handleMessageAPI(r *http.Request, path []string, body string) []byte {
	if len(path) == 0 {
		if r.Method != http.MethodPost {
			return sampleMethodNotAllowedResponse()
		}
		msg, err := parseSampleHubMessage(body)
		if err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error()})
		}
		h.appendSampleMessage(msg)
		return mustSampleJSON(map[string]any{"success": true, "received": msg, "message_count": h.messageCount()})
	}
	switch strings.ToLower(strings.TrimSpace(path[0])) {
	case "events":
		switch r.Method {
		case http.MethodGet:
			return mustSampleJSON(map[string]any{"success": true, "events": h.listSampleMessages(), "count": h.messageCount()})
		case http.MethodDelete:
			h.clearSampleMessages()
			return mustSampleJSON(map[string]any{"success": true, "events": []sampleHubMessage{}, "count": 0})
		default:
			return sampleMethodNotAllowedResponse()
		}
	case "publish":
		if r.Method != http.MethodPost {
			return sampleMethodNotAllowedResponse()
		}
		req, err := parseSampleMessagePublishRequest(body)
		if err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
		}
		result, err := h.publishSampleMessageToHost(req)
		if err != nil {
			return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
		}
		return mustSampleJSON(map[string]any{"success": true, "published": result})
	default:
		return mustSampleJSON(map[string]any{"success": false, "error": "unknown sample msg endpoint"})
	}
}

func parseSampleHubMessage(body string) (sampleHubMessage, error) {
	var msg sampleHubMessage
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		return msg, errors.New("invalid message json")
	}
	msg.Topic = strings.TrimSpace(msg.Topic)
	if msg.Topic == "" {
		return msg, errors.New("topic is required")
	}
	msg.Event = firstNonEmptySample(strings.TrimSpace(msg.Event), "message")
	msg.MsgID = strings.TrimSpace(msg.MsgID)
	msg.Source = strings.TrimSpace(msg.Source)
	msg.TS = strings.TrimSpace(msg.TS)
	if msg.Payload == nil {
		msg.Payload = map[string]any{}
	}
	msg.ReceivedAt = time.Now().Format(time.RFC3339Nano)
	return msg, nil
}

func parseSampleMessagePublishRequest(body string) (sampleMessagePublishRequest, error) {
	var req sampleMessagePublishRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return req, errors.New("invalid publish json")
	}
	req.Topic = strings.TrimSpace(req.Topic)
	if req.Topic == "" {
		return req, errors.New("topic is required")
	}
	req.Event = firstNonEmptySample(strings.TrimSpace(req.Event), "message")
	req.MsgID = strings.TrimSpace(req.MsgID)
	if req.Payload == nil {
		req.Payload = map[string]any{}
	}
	return req, nil
}

func (h *HttpAPI_Plugin) appendSampleMessage(msg sampleHubMessage) {
	const maxMessages = 50
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messageLog = append(h.messageLog, msg)
	if len(h.messageLog) > maxMessages {
		h.messageLog = append([]sampleHubMessage(nil), h.messageLog[len(h.messageLog)-maxMessages:]...)
	}
}

func (h *HttpAPI_Plugin) listSampleMessages() []sampleHubMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]sampleHubMessage(nil), h.messageLog...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (h *HttpAPI_Plugin) clearSampleMessages() {
	h.mu.Lock()
	h.messageLog = nil
	h.mu.Unlock()
}

func (h *HttpAPI_Plugin) messageCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.messageLog)
}

func (h *HttpAPI_Plugin) publishSampleMessageToHost(req sampleMessagePublishRequest) (map[string]any, error) {
	hostURL := h.hostBaseURL()
	if hostURL == "" {
		return nil, errors.New("host_url is required; wait for runtime.auth injection or set AGENTIC_HOST_URL")
	}
	authHeaders := h.HostAuthHeaders()
	if len(authHeaders) == 0 {
		return nil, errors.New("host auth token is required before publishing message")
	}
	payload := map[string]any{
		"topic":   req.Topic,
		"event":   req.Event,
		"source":  "sample",
		"payload": req.Payload,
	}
	if req.MsgID != "" {
		payload["msg_id"] = req.MsgID
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(hostURL, "/")+"/api/msg/publish", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	for key, value := range authHeaders {
		request.Header.Set(key, value)
	}
	client := http.Client{Timeout: 8 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("host publish returned non-json response: %s", truncateSampleText(string(body), 300))
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if errText := strings.TrimSpace(fmt.Sprint(result["error"])); errText != "" && errText != "<nil>" {
			return result, fmt.Errorf("host publish failed: %s", errText)
		}
		return result, fmt.Errorf("host publish failed: HTTP %d", response.StatusCode)
	}
	if success, ok := result["success"].(bool); ok && !success {
		if errText := strings.TrimSpace(fmt.Sprint(result["error"])); errText != "" && errText != "<nil>" {
			return result, fmt.Errorf("host publish failed: %s", errText)
		}
		return result, errors.New("host publish failed")
	}
	return result, nil
}

func (h *HttpAPI_Plugin) hostBaseURL() string {
	h.mu.Lock()
	hostURL := firstNonEmptySample(h.hostAuth.HostURL, h.hostAuth.BaseURL)
	h.mu.Unlock()
	hostURL = firstNonEmptySample(hostURL, os.Getenv("AGENTIC_HOST_URL"), os.Getenv("AGENTIC_SERVICE_URL"))
	return strings.TrimRight(strings.TrimSpace(hostURL), "/")
}

func (h *HttpAPI_Plugin) handleToolsAPI(r *http.Request, path []string, body string) []byte {
	if len(path) == 0 || path[0] != "run" {
		return h.handleCatalog()
	}
	if r.Method != http.MethodPost {
		return sampleMethodNotAllowedResponse()
	}
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(body), &payload)
	toolName := stringFromSampleMap(payload, "tool", "sample.tool")
	return mustSampleJSON(map[string]any{
		"success": true,
		"tool": map[string]any{
			"name":       toolName,
			"input":      payload["input"],
			"output":     fmt.Sprintf("mock result from %s", toolName),
			"started_at": time.Now().Format(time.RFC3339),
		},
	})
}

func (h *HttpAPI_Plugin) authResponse(r *http.Request) []byte {
	var req sampleHostAuthRequest
	if r != nil && r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	token := strings.TrimSpace(req.AuthToken)
	if token == "" && r != nil {
		token = bearerTokenFromHeader(r.Header.Get("Authentication"))
		if token == "" {
			token = bearerTokenFromHeader(r.Header.Get("Authorization"))
		}
	}
	if token == "" {
		return mustSampleJSON(map[string]any{"success": false, "error": "auth_token is required", "plugin": h.statusPayload()})
	}
	expiresAt := time.Time{}
	if text := strings.TrimSpace(req.ExpiresAt); text != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			expiresAt = parsed
		}
	}
	h.mu.Lock()
	h.hostAuth = sampleHostAuth{
		Token:     token,
		TokenType: firstNonEmptySample(req.TokenType, "Bearer"),
		Header:    firstNonEmptySample(req.Header, "Authentication"),
		Account:   strings.TrimSpace(req.Account),
		Project:   strings.TrimSpace(req.Project),
		Source:    firstNonEmptySample(req.Source, "host"),
		HostURL:   strings.TrimRight(strings.TrimSpace(firstNonEmptySample(req.HostURL, req.BaseURL)), "/"),
		BaseURL:   strings.TrimRight(strings.TrimSpace(firstNonEmptySample(req.BaseURL, req.HostURL)), "/"),
		ExpiresAt: expiresAt,
		UpdatedAt: time.Now(),
	}
	h.mu.Unlock()
	return mustSampleJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
}

func bearerTokenFromHeader(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return strings.TrimSpace(raw[7:])
	}
	return raw
}

func (h *HttpAPI_Plugin) HostAuthHeaders() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.TrimSpace(h.hostAuth.Token) == "" {
		return map[string]string{}
	}
	value := firstNonEmptySample(h.hostAuth.TokenType, "Bearer") + " " + h.hostAuth.Token
	header := firstNonEmptySample(h.hostAuth.Header, "Authentication")
	return map[string]string{
		header:           value,
		"Authentication": value,
		"Authorization":  value,
	}
}

func (h *HttpAPI_Plugin) loadResponse() []byte {
	if err := h.loadConfig(true); err != nil {
		h.mu.Lock()
		h.lastLoadErr = err.Error()
		h.mu.Unlock()
		return mustSampleJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	h.StartScheduledTasks()
	return mustSampleJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
}

func (h *HttpAPI_Plugin) Load() error {
	if err := h.loadConfig(true); err != nil {
		return err
	}
	h.StartScheduledTasks()
	return nil
}

func (h *HttpAPI_Plugin) ensureLoaded() error {
	return h.loadConfig(false)
}

func (h *HttpAPI_Plugin) loadConfig(force bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	configPath := SamplePluginConfigPath
	stat, statErr := os.Stat(configPath)
	if !force && h.loaded && h.lastLoadErr == "" && statErr == nil && !stat.ModTime().After(h.lastModified) {
		return nil
	}
	cfg, modified, err := readSampleConfig(configPath)
	if err != nil {
		h.loaded = false
		h.lastLoadErr = err.Error()
		return err
	}
	h.config = cfg
	h.items = map[string]sampleItem{}
	now := time.Now().Format(time.RFC3339)
	for _, item := range cfg.Items {
		if item.ID == "" {
			item.ID = nextSampleID("item")
		}
		if item.CreatedAt == "" {
			item.CreatedAt = now
		}
		if item.UpdatedAt == "" {
			item.UpdatedAt = item.CreatedAt
		}
		h.items[item.ID] = item
	}
	if h.jobs == nil {
		h.jobs = map[string]*sampleJob{}
	}
	if h.scheduledRunning == nil {
		h.scheduledRunning = map[string]bool{}
	}
	h.loaded = true
	h.loadedAt = time.Now()
	h.lastLoadErr = ""
	h.lastModified = modified
	return nil
}

func (h *HttpAPI_Plugin) statusPayload() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return map[string]any{
		"id":                   "sample",
		"name":                 "Sample Plugin",
		"version":              SamplePluginVersion,
		"loaded":               h.loaded,
		"loaded_at":            optionalSampleRFC3339(h.loadedAt),
		"last_error":           h.lastLoadErr,
		"config_path":          SamplePluginConfigPath,
		"last_modified":        optionalSampleRFC3339(h.lastModified),
		"item_count":           len(h.items),
		"job_count":            len(h.jobs),
		"scheduled_task_count": len(h.config.ScheduledTasks),
		"message_count":        len(h.messageLog),
		"host_auth":            sampleHostAuthStatus(h.hostAuth),
	}
}

func sampleHostAuthStatus(auth sampleHostAuth) map[string]any {
	return map[string]any{
		"available":  strings.TrimSpace(auth.Token) != "",
		"account":    auth.Account,
		"project":    auth.Project,
		"source":     auth.Source,
		"host_url":   firstNonEmptySample(auth.HostURL, auth.BaseURL),
		"updated_at": optionalSampleRFC3339(auth.UpdatedAt),
		"expires_at": optionalSampleRFC3339(auth.ExpiresAt),
	}
}

func (h *HttpAPI_Plugin) registrationPayload() map[string]any {
	return map[string]any{
		"id":              "sample",
		"plugin_code":     "sample",
		"name":            "Sample Plugin",
		"version":         SamplePluginVersion,
		"type":            "service",
		"auto_start":      true,
		"default_group":   "開發工具",
		"service":         "sample-service",
		"service_url":     "http://127.0.0.1:18182",
		"api_base":        "/api/plugin/sample",
		"plugin_api_base": "/api/sample",
		"api_catalog":     "/api/sample/apis",
		"routes":          []string{"/mcp", "/api/sample", "/api/hello", "/api/health", "/api/heatlth"},
		"mcp_url":         "http://127.0.0.1:18182/mcp",
		"website_path":    "./website/sample/index.html",
		"runtime": map[string]any{
			"service":        "sample-service",
			"addr":           "127.0.0.1:18182",
			"listen_addr":    "127.0.0.1:18182",
			"health":         "/api/health",
			"auth":           "/api/sample/plugin/auth",
			"load":           "/api/sample/plugin/load",
			"reload":         "/api/sample/plugin/reload",
			"unload":         "/api/sample/plugin/unload",
			"registration":   "/api/sample/plugin/registration",
			"start_args":     []string{"-config", "plugins/sample/config.json"},
			"preserve_paths": []string{"config.json", "runtime/", "skill/skill-cards.json"},
		},
		"messaging": map[string]any{
			"webhook": "/api/sample/msg",
			"topics":  []string{"sample.notice", "system.notice"},
		},
		"ui": map[string]any{
			"enabled":      true,
			"order":        90,
			"website_path": "./website/sample/index.html",
			"href":         "/sample/index.html",
			"code":         "SAMPLE",
			"class":        "sample",
			"title":        "Sample Plugin",
			"description":  "外掛開發參考頁，示範生命週期、設定、CRUD、串流與背景工作呼叫。",
			"action":       "進入 Sample Plugin",
			"icon":         "fa-solid fa-puzzle-piece",
		},
		"invoke": map[string]any{
			"type":            "CallPlugin",
			"api_base":        "/api/plugin/sample",
			"plugin_api_base": "/api/sample",
		},
		"permission_settings": sampleMCPPermissionSettings(),
		"config_settings":     sampleConfigSettings(),
		"business_capabilities": []string{
			"plugin-development-reference",
		},
		"technical_capabilities": []string{
			"standard-mcp", "host-auth", "message-hub", "runtime-config", "background-job",
		},
		"capabilities": []string{"lifecycle", "registration", "host-auth", "mcp", "standard-mcp", "config", "crud", "skill-guide", "chat-guide", "stream", "background-job", "scheduled-task", "file-payload", "messaging", "tool-call"},
	}
}

func (h *HttpAPI_Plugin) listItems() []sampleItem {
	h.mu.Lock()
	defer h.mu.Unlock()
	items := make([]sampleItem, 0, len(h.items))
	for _, item := range h.items {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].ID) < strings.ToLower(items[j].ID)
	})
	return items
}

func (h *HttpAPI_Plugin) getItem(id string) (sampleItem, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item, ok := h.items[id]
	return item, ok
}

func (h *HttpAPI_Plugin) runSampleJob(id string) {
	for step := 1; step <= 5; step++ {
		time.Sleep(250 * time.Millisecond)
		h.mu.Lock()
		if job, ok := h.jobs[id]; ok {
			job.Status = "running"
			job.Progress = step * 20
			job.UpdatedAt = time.Now().Format(time.RFC3339)
			if step == 5 {
				job.Status = "done"
				job.Message = "sample job completed"
			}
		}
		h.mu.Unlock()
	}
}

func (h *HttpAPI_Plugin) getJob(id string) (sampleJob, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	job, ok := h.jobs[id]
	if !ok || job == nil {
		return sampleJob{}, false
	}
	return *job, true
}

func (h *HttpAPI_Plugin) listScheduledTasks() []sampleScheduledTask {
	h.mu.Lock()
	defer h.mu.Unlock()
	tasks := append([]sampleScheduledTask(nil), h.config.ScheduledTasks...)
	sort.SliceStable(tasks, func(i, j int) bool {
		return strings.ToLower(tasks[i].ID) < strings.ToLower(tasks[j].ID)
	})
	return tasks
}

func (h *HttpAPI_Plugin) saveScheduledTasks(tasks []sampleScheduledTask) error {
	tasks, err := sanitizeSampleScheduledTasks(tasks)
	if err != nil {
		return err
	}
	h.mu.Lock()
	cfg := h.config
	cfg.ScheduledTasks = tasks
	h.mu.Unlock()
	if err := writeSampleConfig(cfg); err != nil {
		return err
	}
	return h.loadConfig(true)
}

func (h *HttpAPI_Plugin) StartScheduledTasks() {
	h.mu.Lock()
	if h.scheduledStarted {
		h.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	h.scheduledStop = stop
	h.scheduledStarted = true
	if h.scheduledRunning == nil {
		h.scheduledRunning = map[string]bool{}
	}
	h.mu.Unlock()
	go h.scheduledTaskLoop(stop)
}

func (h *HttpAPI_Plugin) scheduledTaskLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	h.checkDueScheduledTasks()
	for {
		select {
		case <-ticker.C:
			h.checkDueScheduledTasks()
		case <-stop:
			return
		}
	}
}

func (h *HttpAPI_Plugin) stopScheduledTaskLoopLocked() {
	if h.scheduledStop != nil {
		close(h.scheduledStop)
	}
	h.scheduledStop = nil
	h.scheduledStarted = false
	h.scheduledRunning = map[string]bool{}
}

func (h *HttpAPI_Plugin) checkDueScheduledTasks() {
	if err := h.ensureLoaded(); err != nil {
		return
	}
	now := time.Now()
	for _, task := range h.listScheduledTasks() {
		if !sampleScheduledTaskDue(task, now) {
			continue
		}
		_, _ = h.startScheduledTaskRunAsync(task.ID, false)
	}
}

func (h *HttpAPI_Plugin) markScheduledTaskRunning(taskID string, running bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.scheduledRunning == nil {
		h.scheduledRunning = map[string]bool{}
	}
	if running {
		if h.scheduledRunning[taskID] {
			return false
		}
		h.scheduledRunning[taskID] = true
		return true
	}
	delete(h.scheduledRunning, taskID)
	return true
}

func (h *HttpAPI_Plugin) startScheduledTaskRunAsync(taskID string, manual bool) (sampleScheduledTask, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return sampleScheduledTask{}, errors.New("scheduled task id is required")
	}
	if !h.markScheduledTaskRunning(taskID, true) {
		return sampleScheduledTask{}, errors.New("scheduled task is already running")
	}
	task, token, err := h.reserveScheduledTaskRun(taskID, manual)
	if err != nil {
		h.markScheduledTaskRunning(taskID, false)
		return sampleScheduledTask{}, err
	}
	go func() {
		defer h.markScheduledTaskRunning(taskID, false)
		h.executeScheduledTask(task, token, manual)
	}()
	return task, nil
}

func (h *HttpAPI_Plugin) reserveScheduledTaskRun(taskID string, manual bool) (sampleScheduledTask, string, error) {
	if err := h.ensureLoaded(); err != nil {
		return sampleScheduledTask{}, "", err
	}
	now := time.Now()
	h.mu.Lock()
	cfg := h.config
	found := -1
	for index, task := range cfg.ScheduledTasks {
		if strings.EqualFold(task.ID, taskID) {
			found = index
			break
		}
	}
	if found < 0 {
		h.mu.Unlock()
		return sampleScheduledTask{}, "", fmt.Errorf("scheduled task not found: %s", taskID)
	}
	task := cfg.ScheduledTasks[found]
	if err := normalizeSampleScheduledTask(&task, found); err != nil {
		h.mu.Unlock()
		return sampleScheduledTask{}, "", err
	}
	if !manual && !sampleScheduledTaskDue(task, now) {
		h.mu.Unlock()
		return sampleScheduledTask{}, "", errors.New("scheduled task is not due")
	}
	if until, ok := parseSampleRFC3339(task.RunningUntil); ok && until.After(now) {
		h.mu.Unlock()
		return sampleScheduledTask{}, "", errors.New("scheduled task is already running")
	}
	token := fmt.Sprintf("scheduled_%s_%d", task.ID, now.UnixNano())
	task.RunToken = token
	task.LastStartedAt = now.Format(time.RFC3339Nano)
	task.RunningUntil = now.Add(15 * time.Minute).Format(time.RFC3339Nano)
	task.NextRunAt = now.Add(time.Duration(normalizeSampleScheduledInterval(task)) * time.Minute).Format(time.RFC3339Nano)
	task.LastError = ""
	task.UpdatedAt = now.Format(time.RFC3339Nano)
	cfg.ScheduledTasks[found] = task
	h.config = cfg
	h.mu.Unlock()
	if err := writeSampleConfig(cfg); err != nil {
		return sampleScheduledTask{}, "", err
	}
	return task, token, nil
}

func (h *HttpAPI_Plugin) executeScheduledTask(task sampleScheduledTask, token string, manual bool) {
	result := ""
	errText := ""
	switch strings.ToLower(strings.TrimSpace(task.Action)) {
	case "echo":
		result = stringFromSampleMap(task.Payload, "message", fmt.Sprintf("scheduled echo: %s", task.Name))
	case "tool":
		toolName := stringFromSampleMap(task.Payload, "tool", "sample.scheduled.tool")
		result = fmt.Sprintf("mock result from %s", toolName)
	default:
		job := &sampleJob{
			ID:        nextSampleID("job"),
			Status:    "queued",
			Message:   stringFromSampleMap(task.Payload, "message", fmt.Sprintf("scheduled task: %s", task.Name)),
			CreatedAt: time.Now().Format(time.RFC3339),
			UpdatedAt: time.Now().Format(time.RFC3339),
		}
		h.mu.Lock()
		if h.jobs == nil {
			h.jobs = map[string]*sampleJob{}
		}
		h.jobs[job.ID] = job
		h.mu.Unlock()
		go h.runSampleJob(job.ID)
		result = fmt.Sprintf("started background job %s", job.ID)
	}
	if err := h.finishScheduledTaskRun(task.ID, token, time.Now(), result, errText, manual, task); err != nil {
		h.mu.Lock()
		h.lastLoadErr = err.Error()
		h.mu.Unlock()
	}
}

func (h *HttpAPI_Plugin) terminateScheduledTaskRun(taskID string, manual bool, reason string) (sampleScheduledTask, error) {
	if err := h.ensureLoaded(); err != nil {
		return sampleScheduledTask{}, err
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return sampleScheduledTask{}, errors.New("scheduled task id is required")
	}
	now := time.Now()
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "scheduled task terminated"
	}
	h.mu.Lock()
	cfg := h.config
	for index, task := range cfg.ScheduledTasks {
		if !strings.EqualFold(task.ID, taskID) {
			continue
		}
		if strings.TrimSpace(task.RunToken) == "" && strings.TrimSpace(task.RunningUntil) == "" {
			h.mu.Unlock()
			return sampleScheduledTask{}, errors.New("scheduled task is not running")
		}
		startedAt := task.LastStartedAt
		task.RunningUntil = ""
		task.RunToken = ""
		task.LastStartedAt = ""
		task.LastRunAt = now.Format(time.RFC3339Nano)
		task.LastResult = ""
		task.LastError = truncateSampleText(reason, 500)
		task.UpdatedAt = now.Format(time.RFC3339Nano)
		cfg.ScheduledTasks[index] = task
		h.config = cfg
		if h.scheduledRunning != nil {
			delete(h.scheduledRunning, taskID)
		}
		h.mu.Unlock()
		if err := writeSampleConfig(cfg); err != nil {
			return sampleScheduledTask{}, err
		}
		if err := appendSampleScheduledLog(&sampleScheduledLogEntry{
			TaskID:     task.ID,
			TaskName:   task.Name,
			Action:     task.Action,
			Status:     "terminated",
			Manual:     manual,
			Payload:    task.Payload,
			Result:     "",
			Error:      reason,
			StartedAt:  firstNonEmptySample(startedAt, task.LastRunAt),
			FinishedAt: task.LastRunAt,
		}); err != nil {
			return sampleScheduledTask{}, err
		}
		return task, nil
	}
	h.mu.Unlock()
	return sampleScheduledTask{}, fmt.Errorf("scheduled task not found: %s", taskID)
}

func (h *HttpAPI_Plugin) finishScheduledTaskRun(taskID string, token string, finishedAt time.Time, result string, errText string, manual bool, reservedTask sampleScheduledTask) error {
	if err := h.ensureLoaded(); err != nil {
		return err
	}
	h.mu.Lock()
	cfg := h.config
	for index, task := range cfg.ScheduledTasks {
		if !strings.EqualFold(task.ID, taskID) {
			continue
		}
		if strings.TrimSpace(token) != "" && strings.TrimSpace(task.RunToken) != token {
			h.mu.Unlock()
			return nil
		}
		task.LastRunAt = finishedAt.Format(time.RFC3339Nano)
		task.RunningUntil = ""
		task.RunToken = ""
		task.LastStartedAt = ""
		task.LastResult = truncateSampleText(result, 500)
		task.LastError = truncateSampleText(errText, 500)
		task.UpdatedAt = finishedAt.Format(time.RFC3339Nano)
		cfg.ScheduledTasks[index] = task
		h.config = cfg
		h.mu.Unlock()
		if err := writeSampleConfig(cfg); err != nil {
			return err
		}
		status := "success"
		if strings.TrimSpace(errText) != "" {
			status = "failed"
		}
		return appendSampleScheduledLog(&sampleScheduledLogEntry{
			TaskID:     task.ID,
			TaskName:   task.Name,
			Action:     task.Action,
			Status:     status,
			Manual:     manual,
			Payload:    reservedTask.Payload,
			Result:     result,
			Error:      errText,
			StartedAt:  firstNonEmptySample(reservedTask.LastStartedAt, task.LastRunAt),
			FinishedAt: task.LastRunAt,
		})
	}
	h.mu.Unlock()
	return fmt.Errorf("scheduled task not found: %s", taskID)
}

func parseSampleItem(body string) (sampleItem, error) {
	var item sampleItem
	if err := json.Unmarshal([]byte(body), &item); err != nil {
		return item, errors.New("invalid item json")
	}
	if strings.TrimSpace(item.Name) == "" {
		return item, errors.New("item.name is required")
	}
	item.ID = strings.TrimSpace(item.ID)
	item.Name = strings.TrimSpace(item.Name)
	return item, nil
}

func readSampleConfig(path string) (sampleConfig, time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && path == SamplePluginConfigPath {
			return initializeSampleConfigFromDefault()
		}
		return sampleConfig{}, time.Time{}, err
	}
	var cfg sampleConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return sampleConfig{}, time.Time{}, fmt.Errorf("invalid sample config: %w", err)
	}
	normalizeSampleConfig(&cfg)
	modified := time.Now()
	if stat, err := os.Stat(path); err == nil {
		modified = stat.ModTime()
	}
	return cfg, modified, nil
}

func initializeSampleConfigFromDefault() (sampleConfig, time.Time, error) {
	cfg := sampleConfig{Version: SamplePluginVersion}
	defaultPath := filepath.Join(filepath.Dir(SamplePluginConfigPath), "config.default.json")
	if data, err := os.ReadFile(defaultPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return sampleConfig{}, time.Time{}, fmt.Errorf("invalid sample default config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return sampleConfig{}, time.Time{}, err
	}
	normalizeSampleConfig(&cfg)
	if err := writeSampleConfig(cfg); err != nil {
		return sampleConfig{}, time.Time{}, err
	}
	modified := time.Now()
	if stat, err := os.Stat(SamplePluginConfigPath); err == nil {
		modified = stat.ModTime()
	}
	return cfg, modified, nil
}

func normalizeSampleConfig(cfg *sampleConfig) {
	if cfg.Version == "" {
		cfg.Version = SamplePluginVersion
	}
	if strings.TrimSpace(cfg.Title) == "" {
		cfg.Title = "Sample Plugin"
	}
	if strings.TrimSpace(cfg.DefaultGroup) == "" {
		cfg.DefaultGroup = "開發工具"
	}
	if strings.TrimSpace(cfg.Message) == "" {
		cfg.Message = "Hello from Sample Plugin"
	}
	if cfg.Features == nil {
		cfg.Features = map[string]bool{"echo": true, "items": true, "stream": true, "jobs": true, "scheduled_tasks": true, "files": true, "messaging": true, "tools": true, "mcp": true}
	} else if _, exists := cfg.Features["mcp"]; !exists {
		cfg.Features["mcp"] = true
	}
	if cfg.Items == nil {
		cfg.Items = []sampleItem{}
	}
	if cfg.ScheduledTasks == nil {
		cfg.ScheduledTasks = []sampleScheduledTask{}
	}
	tasks, err := sanitizeSampleScheduledTasks(cfg.ScheduledTasks)
	if err == nil {
		cfg.ScheduledTasks = tasks
	}
}

func writeSampleConfig(cfg sampleConfig) error {
	normalizeSampleConfig(&cfg)
	if err := os.MkdirAll(filepath.Dir(SamplePluginConfigPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := SamplePluginConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, SamplePluginConfigPath)
}

func sanitizeSampleScheduledTasks(tasks []sampleScheduledTask) ([]sampleScheduledTask, error) {
	const maxTasks = 100
	if len(tasks) > maxTasks {
		tasks = tasks[:maxTasks]
	}
	now := time.Now().Format(time.RFC3339)
	seen := map[string]bool{}
	out := make([]sampleScheduledTask, 0, len(tasks))
	for index, task := range tasks {
		if strings.TrimSpace(task.ID) == "" {
			task.ID = fmt.Sprintf("task_%d", index+1)
		}
		if err := normalizeSampleScheduledTask(&task, index); err != nil {
			return nil, err
		}
		key := strings.ToLower(task.ID)
		if seen[key] {
			return nil, fmt.Errorf("duplicate scheduled task id: %s", task.ID)
		}
		seen[key] = true
		if task.CreatedAt == "" {
			task.CreatedAt = now
		}
		if task.UpdatedAt == "" {
			task.UpdatedAt = now
		}
		if task.Enabled && task.NextRunAt == "" {
			task.NextRunAt = time.Now().Add(time.Duration(normalizeSampleScheduledInterval(task)) * time.Minute).Format(time.RFC3339Nano)
		}
		if !task.Enabled {
			task.NextRunAt = ""
		}
		out = append(out, task)
	}
	return out, nil
}

func normalizeSampleScheduledTask(task *sampleScheduledTask, index int) error {
	task.ID = strings.TrimSpace(task.ID)
	task.Name = strings.TrimSpace(task.Name)
	task.Action = strings.ToLower(strings.TrimSpace(task.Action))
	task.LastRunAt = strings.TrimSpace(task.LastRunAt)
	task.NextRunAt = strings.TrimSpace(task.NextRunAt)
	task.RunningUntil = strings.TrimSpace(task.RunningUntil)
	task.RunToken = strings.TrimSpace(task.RunToken)
	task.LastStartedAt = strings.TrimSpace(task.LastStartedAt)
	task.LastResult = strings.TrimSpace(task.LastResult)
	task.LastError = strings.TrimSpace(task.LastError)
	task.CreatedAt = strings.TrimSpace(task.CreatedAt)
	task.UpdatedAt = strings.TrimSpace(task.UpdatedAt)
	prefix := "scheduled_task"
	if index >= 0 {
		prefix = fmt.Sprintf("scheduled_tasks[%d]", index)
	}
	if task.ID == "" {
		return fmt.Errorf("%s.id is required", prefix)
	}
	task.ID = strings.ToLower(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, task.ID))
	if task.Name == "" {
		task.Name = "未命名定期任務"
	}
	switch task.Action {
	case "", "job":
		task.Action = "job"
	case "echo", "tool":
	default:
		return fmt.Errorf("%s.action is not supported: %s", prefix, task.Action)
	}
	if task.IntervalMinutes <= 0 {
		task.IntervalMinutes = 60
	}
	if task.IntervalMinutes > 1440 {
		task.IntervalMinutes = 1440
	}
	task.Name = truncateSampleText(task.Name, 120)
	task.LastResult = truncateSampleText(task.LastResult, 500)
	task.LastError = truncateSampleText(task.LastError, 500)
	if task.Payload == nil {
		task.Payload = map[string]any{}
	}
	return nil
}

func sampleScheduledTaskDue(task sampleScheduledTask, now time.Time) bool {
	if !task.Enabled {
		return false
	}
	if until, ok := parseSampleRFC3339(task.RunningUntil); ok && until.After(now) {
		return false
	}
	if next, ok := parseSampleRFC3339(task.NextRunAt); ok {
		return !next.After(now)
	}
	if last, ok := parseSampleRFC3339(task.LastRunAt); ok {
		return !last.Add(time.Duration(normalizeSampleScheduledInterval(task)) * time.Minute).After(now)
	}
	return true
}

func normalizeSampleScheduledInterval(task sampleScheduledTask) int {
	if task.IntervalMinutes <= 0 {
		return 60
	}
	if task.IntervalMinutes > 1440 {
		return 1440
	}
	return task.IntervalMinutes
}

func parseSampleRFC3339(value string) (time.Time, bool) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func sampleScheduledLogBaseDir() string {
	return filepath.Join(filepath.Dir(SamplePluginConfigPath), "runtime", "scheduled-logs")
}

func normalizeSampleLogDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= len("2006-01-02") {
		date := value[:len("2006-01-02")]
		if _, err := time.Parse("2006-01-02", date); err == nil {
			return date
		}
	}
	return time.Now().Format("2006-01-02")
}

func sampleScheduledLogPath(date string) string {
	return filepath.Join(sampleScheduledLogBaseDir(), normalizeSampleLogDate(date)+".jsonl")
}

func appendSampleScheduledLog(entry *sampleScheduledLogEntry) error {
	entry.TaskID = strings.TrimSpace(entry.TaskID)
	entry.TaskName = truncateSampleText(strings.TrimSpace(entry.TaskName), 120)
	entry.Action = strings.ToLower(strings.TrimSpace(entry.Action))
	entry.Status = strings.ToLower(strings.TrimSpace(entry.Status))
	entry.Result = truncateSampleText(strings.TrimSpace(entry.Result), 20000)
	entry.Error = truncateSampleText(strings.TrimSpace(entry.Error), 20000)
	entry.StartedAt = strings.TrimSpace(entry.StartedAt)
	entry.FinishedAt = strings.TrimSpace(entry.FinishedAt)
	if entry.TaskID == "" {
		return errors.New("task_id is required")
	}
	if entry.Status == "" {
		entry.Status = "success"
	}
	if entry.Status != "success" && entry.Status != "failed" && entry.Status != "skipped" && entry.Status != "terminated" {
		entry.Status = "success"
	}
	now := time.Now().Format(time.RFC3339Nano)
	if entry.CreatedAt == "" {
		entry.CreatedAt = now
	}
	if entry.FinishedAt == "" {
		entry.FinishedAt = now
	}
	if entry.StartedAt == "" {
		entry.StartedAt = entry.FinishedAt
	}
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("%s_%d", safeSampleLogName(entry.TaskID), time.Now().UnixNano())
	}
	logPath := sampleScheduledLogPath(entry.FinishedAt)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal scheduled log failed: %w", err)
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}

func readSampleScheduledLogs(date string, taskID string) ([]sampleScheduledLogEntry, error) {
	data, err := os.ReadFile(sampleScheduledLogPath(date))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []sampleScheduledLogEntry{}, nil
		}
		return nil, err
	}
	taskID = strings.TrimSpace(taskID)
	lines := strings.Split(string(data), "\n")
	logs := make([]sampleScheduledLogEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry sampleScheduledLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if taskID != "" && !strings.EqualFold(entry.TaskID, taskID) {
			continue
		}
		logs = append(logs, entry)
	}
	return logs, nil
}

func safeSampleLogName(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, r := range value {
		allowed := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.'
		if allowed {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	name := strings.Trim(builder.String(), "._-")
	if name == "" {
		return "task"
	}
	return name
}

func parseSampleSkillCard(body string) (sampleSkillCard, error) {
	var card sampleSkillCard
	if err := json.Unmarshal([]byte(body), &card); err != nil {
		return card, errors.New("invalid skill card json")
	}
	card.ID = strings.TrimSpace(card.ID)
	card.Title = strings.TrimSpace(card.Title)
	card.Description = strings.TrimSpace(card.Description)
	card.Icon = strings.TrimSpace(card.Icon)
	card.Prompt = strings.TrimSpace(card.Prompt)
	if card.Title == "" {
		return card, errors.New("skill card title is required")
	}
	if card.Prompt == "" {
		return card, errors.New("skill card prompt is required")
	}
	if card.Icon == "" {
		card.Icon = "fa-wand-magic-sparkles"
	}
	return card, nil
}

func readSampleSkillCards() ([]sampleSkillCard, error) {
	if err := ensureSampleSkillCardsFile(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(SamplePluginSkillCardsPath)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Cards []sampleSkillCard `json:"cards"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Cards != nil {
		return normalizeSampleSkillCards(wrapper.Cards), nil
	}
	var cards []sampleSkillCard
	if err := json.Unmarshal(data, &cards); err != nil {
		return nil, fmt.Errorf("invalid sample skill cards json: %w", err)
	}
	return normalizeSampleSkillCards(cards), nil
}

func ensureSampleSkillCardsFile() error {
	if _, err := os.Stat(SamplePluginSkillCardsPath); err == nil {
		return nil
	}
	data, err := os.ReadFile(SamplePluginDefaultSkillCardsPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(SamplePluginSkillCardsPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(SamplePluginSkillCardsPath, data, 0o644)
}

func writeSampleSkillCards(cards []sampleSkillCard) error {
	if err := os.MkdirAll(filepath.Dir(SamplePluginSkillCardsPath), 0o755); err != nil {
		return err
	}
	payload := map[string]any{"cards": normalizeSampleSkillCards(cards)}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(SamplePluginSkillCardsPath, append(data, '\n'), 0o644)
}

func normalizeSampleSkillCards(cards []sampleSkillCard) []sampleSkillCard {
	out := make([]sampleSkillCard, 0, len(cards))
	seen := map[string]bool{}
	for _, card := range cards {
		card.ID = strings.TrimSpace(card.ID)
		card.Title = strings.TrimSpace(card.Title)
		card.Description = strings.TrimSpace(card.Description)
		card.Icon = strings.TrimSpace(card.Icon)
		card.Prompt = strings.TrimSpace(card.Prompt)
		if card.Title == "" || card.Prompt == "" {
			continue
		}
		if card.ID == "" {
			card.ID = nextSampleID("skill")
		}
		if card.Icon == "" {
			card.Icon = "fa-wand-magic-sparkles"
		}
		if seen[card.ID] {
			continue
		}
		seen[card.ID] = true
		out = append(out, card)
	}
	return out
}

func upsertSampleSkillCard(cards []sampleSkillCard, card sampleSkillCard) []sampleSkillCard {
	next := normalizeSampleSkillCards(cards)
	for index, existing := range next {
		if existing.ID == card.ID {
			next[index] = card
			return next
		}
	}
	return append(next, card)
}

func listSamplePluginSkills() ([]map[string]any, error) {
	if err := os.MkdirAll(SamplePluginSkillRootPath, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(SamplePluginSkillRootPath)
	if err != nil {
		return nil, err
	}
	skills := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entryPath := filepath.Join(SamplePluginSkillRootPath, entry.Name(), "SKILL.md")
		info, err := os.Stat(entryPath)
		if err != nil {
			continue
		}
		skills = append(skills, map[string]any{
			"id":       entry.Name(),
			"dir":      filepath.Join(SamplePluginSkillRootPath, entry.Name()),
			"entry":    entryPath,
			"modified": optionalSampleRFC3339(info.ModTime()),
		})
	}
	return skills, nil
}

func readSamplePluginSkillContent(id string) (string, string, error) {
	_, entryPath, err := resolveSamplePluginSkillPath(id)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(entryPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", entryPath, fmt.Errorf("skill not found: %s", sanitizeSamplePluginSkillID(id))
		}
		return "", entryPath, err
	}
	return string(data), entryPath, nil
}

func resolveSamplePluginSkillPath(id string) (string, string, error) {
	safeID := sanitizeSamplePluginSkillID(id)
	if safeID == "" {
		return "", "", fmt.Errorf("skill id is required")
	}
	rootAbs, err := filepath.Abs(SamplePluginSkillRootPath)
	if err != nil {
		return "", "", err
	}
	dirAbs := filepath.Join(rootAbs, safeID)
	rel, err := filepath.Rel(rootAbs, dirAbs)
	if err != nil {
		return "", "", err
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("invalid skill path")
	}
	return filepath.Join(SamplePluginSkillRootPath, safeID), filepath.Join(SamplePluginSkillRootPath, safeID, "SKILL.md"), nil
}

func sanitizeSamplePluginSkillID(input string) string {
	value := strings.TrimSpace(strings.ToLower(input))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if r == '-' || r == '_' {
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

func queryToMap(r *http.Request) map[string][]string {
	if r == nil || r.URL == nil {
		return map[string][]string{}
	}
	return map[string][]string(r.URL.Query())
}

func selectedHeaders(r *http.Request) map[string]string {
	headers := map[string]string{}
	for _, key := range []string{"Content-Type", "Accept", "User-Agent", "X-Request-Id"} {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			headers[key] = value
		}
	}
	return headers
}

func writeSampleSSE(w http.ResponseWriter, event string, payload any) {
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}

func multipartFileSummaries(form *multipart.Form) []map[string]any {
	if form == nil {
		return nil
	}
	out := []map[string]any{}
	for field, headers := range form.File {
		for _, header := range headers {
			out = append(out, map[string]any{
				"field":     field,
				"file_name": header.Filename,
				"size":      header.Size,
				"source":    "multipart",
			})
		}
	}
	return out
}

func stringFromSampleMap(input map[string]any, key string, fallback string) string {
	if input == nil {
		return fallback
	}
	value, ok := input[key]
	if !ok {
		return fallback
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return fallback
	}
	return text
}

func firstNonEmptySample(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func truncateSampleText(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func nextSampleID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func optionalSampleRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
