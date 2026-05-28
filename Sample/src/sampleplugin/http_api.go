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
	mu           sync.Mutex
	loaded       bool
	config       sampleConfig
	items        map[string]sampleItem
	jobs         map[string]*sampleJob
	hostAuth     sampleHostAuth
	loadedAt     time.Time
	lastLoadErr  string
	lastModified time.Time
}

type sampleHostAuth struct {
	Token     string
	TokenType string
	Header    string
	Account   string
	Project   string
	Source    string
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
	ExpiresAt string `json:"expires_at"`
}

type sampleConfig struct {
	Version  string          `json:"version"`
	Message  string          `json:"message"`
	Features map[string]bool `json:"features"`
	Items    []sampleItem    `json:"items"`
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

func (h *HttpAPI_Plugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	applySampleCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
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
}

func applySampleCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Authentication, Accept")
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
	case "files":
		return h.handleFilesAPI(r, body)
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
		"apis": []map[string]any{
			{"path": "/api/sample/plugin/status", "method": "GET", "description": "show plugin runtime status"},
			{"path": "/api/sample/plugin/registration", "method": "GET", "description": "show plugin registration metadata"},
			{"path": "/api/sample/plugin/auth", "method": "POST", "description": "receive host auth token for plugin service program calls"},
			{"path": "/api/sample/plugin/load", "method": "POST", "description": "load config and initialize runtime state"},
			{"path": "/api/sample/plugin/unload", "method": "POST", "description": "clear runtime state"},
			{"path": "/api/sample/plugin/reload", "method": "POST", "description": "reload config"},
			{"path": "/api/sample/config", "method": "GET|PUT", "description": "read or update sample config"},
			{"path": "/api/sample/echo", "method": "GET|POST", "description": "return method, query, headers and JSON body"},
			{"path": "/api/sample/items", "method": "GET|POST", "description": "list or create demo item"},
			{"path": "/api/sample/items/{id}", "method": "GET|PUT|DELETE", "description": "read, update or delete demo item"},
			{"path": "/api/sample/skills", "method": "GET", "description": "list sample plugin skills"},
			{"path": "/api/sample/skills/{id}/content", "method": "GET", "description": "read sample plugin skill markdown content"},
			{"path": "/api/sample/skills/cards", "method": "GET|POST", "description": "list or create sample chat skill card"},
			{"path": "/api/sample/skills/cards/{id}", "method": "GET|PUT|DELETE", "description": "read, update or delete sample chat skill card"},
			{"path": "/api/sample/stream", "method": "GET|POST", "description": "server-sent events stream example"},
			{"path": "/api/sample/jobs", "method": "POST", "description": "start background job"},
			{"path": "/api/sample/jobs/{id}", "method": "GET", "description": "read background job status"},
			{"path": "/api/sample/files", "method": "POST", "description": "receive JSON base64 or multipart file payload"},
			{"path": "/api/sample/tools/run", "method": "POST", "description": "mock tool invocation endpoint"},
			{"path": "/mcp", "method": "GET", "description": "show MCP metadata for host service orchestration"},
		},
	})
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
		h.loadedAt = time.Time{}
		h.lastLoadErr = ""
		h.lastModified = time.Time{}
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

func (h *HttpAPI_Plugin) handleMCPAPI(r *http.Request, _ []string) []byte {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return sampleMethodNotAllowedResponse()
	}
	return mustSampleJSON(map[string]any{
		"success": true,
		"mcp": map[string]any{
			"name":        "sample",
			"description": "Sample plugin MCP metadata for host orchestration.",
			"version":     SamplePluginVersion,
			"tools": []map[string]any{
				{"name": "sample.echo", "method": "POST", "path": "/api/plugin/sample/api/sample/echo", "description": "Echo JSON payload and request metadata."},
				{"name": "sample.items.list", "method": "GET", "path": "/api/plugin/sample/api/sample/items", "description": "List in-memory sample items."},
				{"name": "sample.items.create", "method": "POST", "path": "/api/plugin/sample/api/sample/items", "description": "Create a sample item."},
				{"name": "sample.skill.guide", "method": "GET", "path": "/api/plugin/sample/api/sample/skills/guide/content", "description": "Read the Sample Plugin chat guide SKILL.md."},
				{"name": "sample.skill_cards.list", "method": "GET", "path": "/api/plugin/sample/api/sample/skills/cards", "description": "List Sample Plugin chat skill cards."},
				{"name": "sample.skill_cards.save", "method": "POST|PUT|DELETE", "path": "/api/plugin/sample/api/sample/skills/cards", "description": "Create, update or delete Sample Plugin chat skill cards."},
				{"name": "sample.stream", "method": "POST", "path": "/api/plugin/sample/api/sample/stream", "description": "Stream SSE progress events."},
				{"name": "sample.jobs.create", "method": "POST", "path": "/api/plugin/sample/api/sample/jobs", "description": "Start background job."},
				{"name": "sample.files.receive", "method": "POST", "path": "/api/plugin/sample/api/sample/files", "description": "Receive file payload."},
				{"name": "sample.tools.run", "method": "POST", "path": "/api/plugin/sample/api/sample/tools/run", "description": "Run mock tool endpoint."},
			},
		},
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
	return mustSampleJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
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
		"id":            "sample",
		"name":          "Sample Plugin",
		"version":       SamplePluginVersion,
		"loaded":        h.loaded,
		"loaded_at":     optionalSampleRFC3339(h.loadedAt),
		"last_error":    h.lastLoadErr,
		"config_path":   SamplePluginConfigPath,
		"last_modified": optionalSampleRFC3339(h.lastModified),
		"item_count":    len(h.items),
		"job_count":     len(h.jobs),
		"host_auth":     sampleHostAuthStatus(h.hostAuth),
	}
}

func sampleHostAuthStatus(auth sampleHostAuth) map[string]any {
	return map[string]any{
		"available":  strings.TrimSpace(auth.Token) != "",
		"account":    auth.Account,
		"project":    auth.Project,
		"source":     auth.Source,
		"updated_at": optionalSampleRFC3339(auth.UpdatedAt),
		"expires_at": optionalSampleRFC3339(auth.ExpiresAt),
	}
}

func (h *HttpAPI_Plugin) registrationPayload() map[string]any {
	return map[string]any{
		"id":              "sample",
		"name":            "Sample Plugin",
		"version":         SamplePluginVersion,
		"type":            "service",
		"auto_start":      true,
		"service":         "sample-service",
		"service_url":     "http://127.0.0.1:18182",
		"api_base":        "/api/plugin/sample",
		"plugin_api_base": "/api/sample",
		"routes":          []string{"/api/sample"},
		"mcp_url":         "http://127.0.0.1:18182/mcp",
		"website_path":    "./website/sample/index.html",
		"runtime": map[string]any{
			"auth":         "/api/sample/plugin/auth",
			"load":         "/api/sample/plugin/load",
			"unload":       "/api/sample/plugin/unload",
			"registration": "/api/sample/plugin/registration",
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
		"invoke":       "CallPlugin",
		"capabilities": []string{"lifecycle", "registration", "host-auth", "mcp", "config", "crud", "skill-guide", "chat-guide", "stream", "background-job", "file-payload", "tool-call"},
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
	if cfg.Features == nil {
		cfg.Features = map[string]bool{"echo": true, "items": true, "stream": true, "jobs": true, "files": true, "tools": true}
	}
	if cfg.Items == nil {
		cfg.Items = []sampleItem{}
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

func nextSampleID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func optionalSampleRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
