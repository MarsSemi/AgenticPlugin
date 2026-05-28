package api

import (
	"agentic-plugin/emailcheck/src/emailcheck/mail"
	"agentic-plugin/emailcheck/src/emailcheck/model"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const responseHandledMarker = "__email_check_response_handled__"

var EmailCheckConfigPath = "plugins/email-check/config.json"
var EmailCheckSkillRootPath = "plugins/email-check/skill"
var EmailCheckPluginVersion = "0.1.0"
var EmailCheckSkillCardsPath = filepath.Join(EmailCheckSkillRootPath, "skill-cards.json")
var EmailCheckDefaultSkillCardsPath = filepath.Join(EmailCheckSkillRootPath, "skill-cards.default.json")

type emailConfig struct {
	Version    string                     `json:"version"`
	Defaults   emailDefaults              `json:"defaults"`
	Features   map[string]bool            `json:"features"`
	Accounts   []model.EmailAccount       `json:"accounts"`
	AutoChecks []model.EmailAutoCheckTask `json:"auto_checks"`
}

type emailDefaults struct {
	Folder     string `json:"folder"`
	Limit      int    `json:"limit"`
	UnreadOnly bool   `json:"unread_only"`
	SinceDays  int    `json:"since_days"`
}

type accountStatus struct {
	ID            string                `json:"id"`
	Name          string                `json:"name"`
	Email         string                `json:"email"`
	FromName      string                `json:"from_name"`
	Username      string                `json:"username"`
	PasswordSet   bool                  `json:"password_set"`
	DefaultFolder string                `json:"default_folder"`
	Enabled       bool                  `json:"enabled"`
	Protocol      string                `json:"incoming_protocol"`
	IMAP          model.EmailServer     `json:"imap"`
	POP3          model.EmailServer     `json:"pop3"`
	SMTP          model.EmailServer     `json:"smtp"`
	LastCheck     model.EmailCheckState `json:"last_check,omitempty"`
	Metadata      map[string]any        `json:"metadata,omitempty"`
}

type hostAuth struct {
	Token     string
	TokenType string
	Header    string
	Account   string
	Project   string
	Source    string
	ExpiresAt time.Time
	UpdatedAt time.Time
}

type hostAuthRequest struct {
	AuthToken string `json:"auth_token"`
	TokenType string `json:"token_type"`
	Header    string `json:"header"`
	Account   string `json:"account"`
	Project   string `json:"project"`
	Source    string `json:"source"`
	ExpiresAt string `json:"expires_at"`
}

type emailLoginRequest struct {
	AccountID        string          `json:"account_id"`
	Account          json.RawMessage `json:"account"`
	PersistOnSuccess bool            `json:"persist_on_success"`
	PasswordProvided bool            `json:"password_provided"`
}

type HttpAPI_Plugin struct {
	mu               sync.Mutex
	loaded           bool
	config           emailConfig
	accounts         map[string]model.EmailAccount
	hostAuth         hostAuth
	loadedAt         time.Time
	lastLoadErr      string
	lastModified     time.Time
	schedulerOnce    sync.Once
	autoCheckRunning map[string]bool
}

func (h *HttpAPI_Plugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recovered := recover(); recovered != nil {
			writeJSONBytes(w, mustJSON(map[string]any{"success": false, "error": fmt.Sprintf("email-check api panic: %v", recovered)}))
		}
	}()
	applyCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONBytes(w, mustJSON(map[string]any{"success": false, "error": err.Error()}))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	path := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	response := h.Process(w, r, path, string(bodyBytes))
	if string(response) == responseHandledMarker {
		return
	}
	writeJSONBytes(w, response)
}

func applyCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Authentication, Accept")
	w.Header().Set("Access-Control-Max-Age", "600")
}

func writeJSONBytes(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}

func mustJSON(payload any) []byte {
	data, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"success":false,"error":"json marshal failed"}`)
	}
	return data
}

func methodNotAllowedResponse() []byte {
	return mustJSON(map[string]any{"success": false, "error": "method not allowed"})
}

func (h *HttpAPI_Plugin) Process(w http.ResponseWriter, r *http.Request, path []string, body string) []byte {
	normalizedPath := normalizeAPIPath(path)
	if len(normalizedPath) == 0 {
		return h.handleCatalog()
	}

	switch normalizedPath[0] {
	case "plugin":
		return h.handlePluginAPI(r, normalizedPath[1:])
	case "mcp":
		return h.handleMCPAPI(r)
	case "config":
		return h.handleConfigAPI(r, body)
	case "accounts":
		return h.handleAccountsAPI(r, normalizedPath[1:], body)
	case "auto-checks":
		return h.handleAutoChecksAPI(r, normalizedPath[1:], body)
	case "login":
		return h.handleLoginAPI(r, body)
	case "messages":
		return h.handleMessagesAPI(r, normalizedPath[1:], body)
	case "check":
		return h.handleCheckAPI(r, body)
	case "reply":
		return h.handleReplyAPI(r, body)
	case "skills":
		return h.handleSkillsAPI(r, normalizedPath[1:], body)
	default:
		return h.handleCatalog()
	}
}

func normalizeAPIPath(path []string) []string {
	normalized := normalizeEndpointPath(path, "api")
	return normalizeEndpointPath(normalized, "email-check")
}

func normalizeEndpointPath(path []string, root string) []string {
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

func lastPathSegment(path []string, fallback string) string {
	if len(path) == 0 {
		return fallback
	}
	return path[len(path)-1]
}

func (h *HttpAPI_Plugin) handleCatalog() []byte {
	return mustJSON(map[string]any{
		"success": true,
		"service": "email-check",
		"plugin":  h.statusPayload(),
		"apis": []map[string]any{
			{"path": "/api/email-check/plugin/status", "method": "GET", "description": "show plugin runtime status"},
			{"path": "/api/email-check/plugin/registration", "method": "GET", "description": "show plugin registration metadata"},
			{"path": "/api/email-check/plugin/auth", "method": "POST", "description": "receive host auth token"},
			{"path": "/api/email-check/plugin/load", "method": "POST", "description": "load config and initialize runtime state"},
			{"path": "/api/email-check/plugin/unload", "method": "POST", "description": "clear runtime state"},
			{"path": "/api/email-check/plugin/reload", "method": "POST", "description": "reload config"},
			{"path": "/api/email-check/config", "method": "GET|PUT", "description": "read or update EMAIL config"},
			{"path": "/api/email-check/accounts", "method": "GET|POST", "description": "list or create EMAIL account"},
			{"path": "/api/email-check/accounts/{id}", "method": "GET|PUT|DELETE", "description": "read, update or delete EMAIL account"},
			{"path": "/api/email-check/auto-checks", "method": "GET|POST", "description": "list or create EMAIL auto-check task"},
			{"path": "/api/email-check/auto-checks/{id}", "method": "GET|PUT|DELETE", "description": "read, update, delete or run EMAIL auto-check task"},
			{"path": "/api/email-check/login", "method": "POST", "description": "test IMAP or POP3 and SMTP login"},
			{"path": "/api/email-check/messages", "method": "GET|POST", "description": "read messages from IMAP or POP3"},
			{"path": "/api/email-check/messages/{uid}", "method": "GET", "description": "read one message from IMAP or POP3"},
			{"path": "/api/email-check/check", "method": "POST", "description": "check unread messages"},
			{"path": "/api/email-check/reply", "method": "POST", "description": "reply through SMTP"},
			{"path": "/api/email-check/skills", "method": "GET", "description": "list EMAIL plugin skills"},
			{"path": "/api/email-check/skills/{id}/content", "method": "GET", "description": "read EMAIL plugin skill markdown content"},
			{"path": "/api/email-check/skills/cards", "method": "GET|POST", "description": "list or create EMAIL chat skill card"},
			{"path": "/mcp", "method": "GET|POST", "description": "show MCP metadata for host service orchestration"},
		},
	})
}

func (h *HttpAPI_Plugin) handlePluginAPI(r *http.Request, path []string) []byte {
	cmd := lastPathSegment(path, "status")
	switch cmd {
	case "status":
		if r.Method != http.MethodGet {
			return methodNotAllowedResponse()
		}
		return mustJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
	case "load":
		if r.Method != http.MethodPost {
			return methodNotAllowedResponse()
		}
		return h.loadResponse()
	case "auth":
		if r.Method != http.MethodPost {
			return methodNotAllowedResponse()
		}
		return h.authResponse(r)
	case "reload":
		if r.Method != http.MethodPost {
			return methodNotAllowedResponse()
		}
		h.mu.Lock()
		h.loaded = false
		h.mu.Unlock()
		return h.loadResponse()
	case "unload":
		if r.Method != http.MethodPost {
			return methodNotAllowedResponse()
		}
		h.mu.Lock()
		h.loaded = false
		h.config = emailConfig{}
		h.accounts = nil
		h.loadedAt = time.Time{}
		h.lastLoadErr = ""
		h.lastModified = time.Time{}
		h.mu.Unlock()
		return mustJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
	case "registration":
		if r.Method != http.MethodGet {
			return methodNotAllowedResponse()
		}
		return mustJSON(map[string]any{"success": true, "plugin": h.registrationPayload()})
	default:
		return h.handleCatalog()
	}
}

func (h *HttpAPI_Plugin) handleMCPAPI(r *http.Request) []byte {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return methodNotAllowedResponse()
	}
	return mustJSON(map[string]any{
		"success": true,
		"mcp": map[string]any{
			"name":        "email-check",
			"description": "EMAIL plugin MCP metadata for IMAP/POP3/SMTP orchestration.",
			"version":     EmailCheckPluginVersion,
			"tools": []map[string]any{
				{"name": "email.accounts.list", "method": "GET", "path": "/api/plugin/email-check/api/email-check/accounts", "description": "List configured EMAIL accounts without passwords."},
				{"name": "email.accounts.save", "method": "POST|PUT|DELETE", "path": "/api/plugin/email-check/api/email-check/accounts", "description": "Create, update or delete EMAIL accounts."},
				{"name": "email.auto_checks.manage", "method": "GET|POST|PUT|DELETE", "path": "/api/plugin/email-check/api/email-check/auto-checks", "description": "Manage scheduled EMAIL auto-check tasks."},
				{"name": "email.auto_checks.run", "method": "POST", "path": "/api/plugin/email-check/api/email-check/auto-checks/{id}/run", "description": "Run one EMAIL auto-check task immediately."},
				{"name": "email.login", "method": "POST", "path": "/api/plugin/email-check/api/email-check/login", "description": "Test IMAP or POP3 and SMTP login."},
				{"name": "email.messages.list", "method": "POST", "path": "/api/plugin/email-check/api/email-check/messages", "description": "Read messages from IMAP or POP3."},
				{"name": "email.messages.get", "method": "GET", "path": "/api/plugin/email-check/api/email-check/messages/{uid}", "description": "Read one message by UID."},
				{"name": "email.check", "method": "POST", "path": "/api/plugin/email-check/api/email-check/check", "description": "Check unread mail."},
				{"name": "email.reply", "method": "POST", "path": "/api/plugin/email-check/api/email-check/reply", "description": "Reply to a message through SMTP."},
				{"name": "email.skill.guide", "method": "GET", "path": "/api/plugin/email-check/api/email-check/skills/guide/content", "description": "Read the EMAIL Plugin chat guide SKILL.md."},
			},
		},
	})
}

func (h *HttpAPI_Plugin) handleConfigAPI(r *http.Request, body string) []byte {
	if r.Method == http.MethodGet {
		if err := h.ensureLoaded(); err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
		}
		h.mu.Lock()
		cfg := h.config
		h.mu.Unlock()
		return mustJSON(map[string]any{"success": true, "path": EmailCheckConfigPath, "config": sanitizeConfig(cfg)})
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodPatch {
		var cfg emailConfig
		if err := json.Unmarshal([]byte(body), &cfg); err != nil {
			return mustJSON(map[string]any{"success": false, "error": "invalid config json"})
		}
		if err := writeEmailConfig(cfg); err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		h.mu.Lock()
		h.loaded = false
		h.mu.Unlock()
		return h.loadResponse()
	}
	return methodNotAllowedResponse()
}

func (h *HttpAPI_Plugin) handleAccountsAPI(r *http.Request, path []string, body string) []byte {
	if err := h.ensureLoaded(); err != nil {
		return mustJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	if len(path) == 0 {
		switch r.Method {
		case http.MethodGet:
			return mustJSON(map[string]any{"success": true, "accounts": sanitizeAccounts(h.listAccounts())})
		case http.MethodPost:
			account, err := parseAccount(body, model.EmailAccount{})
			if err != nil {
				return mustJSON(map[string]any{"success": false, "error": err.Error()})
			}
			if err := h.saveAccount(account); err != nil {
				return mustJSON(map[string]any{"success": false, "error": err.Error()})
			}
			return mustJSON(map[string]any{"success": true, "persisted": true, "config_path": EmailCheckConfigPath, "account": sanitizeAccount(account)})
		default:
			return methodNotAllowedResponse()
		}
	}

	id := strings.TrimSpace(path[0])
	account, ok := h.accountByID(id)
	if !ok && r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		return mustJSON(map[string]any{"success": false, "error": "account not found", "id": id})
	}
	switch r.Method {
	case http.MethodGet:
		return mustJSON(map[string]any{"success": true, "account": sanitizeAccount(account)})
	case http.MethodPut, http.MethodPatch:
		next, err := parseAccount(body, account)
		if err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		next.ID = id
		if err := h.saveAccount(next); err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		return mustJSON(map[string]any{"success": true, "persisted": true, "config_path": EmailCheckConfigPath, "account": sanitizeAccount(next)})
	case http.MethodDelete:
		if err := h.deleteAccount(id); err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		return mustJSON(map[string]any{"success": true, "persisted": true, "config_path": EmailCheckConfigPath, "id": id})
	default:
		return methodNotAllowedResponse()
	}
}

func (h *HttpAPI_Plugin) handleAutoChecksAPI(r *http.Request, path []string, body string) []byte {
	if err := h.ensureLoaded(); err != nil {
		return mustJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	if len(path) == 0 {
		switch r.Method {
		case http.MethodGet:
			return mustJSON(map[string]any{"success": true, "tasks": h.listAutoCheckTasks()})
		case http.MethodPost:
			task, err := parseAutoCheckTask(body, model.EmailAutoCheckTask{})
			if err != nil {
				return mustJSON(map[string]any{"success": false, "error": err.Error()})
			}
			saved, err := h.saveAutoCheckTask(task)
			if err != nil {
				return mustJSON(map[string]any{"success": false, "error": err.Error()})
			}
			return mustJSON(map[string]any{"success": true, "persisted": true, "config_path": EmailCheckConfigPath, "task": saved})
		default:
			return methodNotAllowedResponse()
		}
	}

	id := strings.TrimSpace(path[0])
	if len(path) > 1 && strings.EqualFold(path[1], "run") {
		if r.Method != http.MethodPost {
			return methodNotAllowedResponse()
		}
		result, task, err := h.runAutoCheckTaskByID(r.Context(), id, true)
		if err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error(), "result": result, "task": task})
		}
		return mustJSON(map[string]any{"success": true, "result": result, "task": task, "persisted": true, "config_path": EmailCheckConfigPath})
	}

	task, ok := h.autoCheckTaskByID(id)
	if !ok && r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		return mustJSON(map[string]any{"success": false, "error": "auto-check task not found", "id": id})
	}
	switch r.Method {
	case http.MethodGet:
		return mustJSON(map[string]any{"success": true, "task": task})
	case http.MethodPut, http.MethodPatch:
		next, err := parseAutoCheckTask(body, task)
		if err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		next.ID = id
		saved, err := h.saveAutoCheckTask(next)
		if err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		return mustJSON(map[string]any{"success": true, "persisted": true, "config_path": EmailCheckConfigPath, "task": saved})
	case http.MethodDelete:
		if err := h.deleteAutoCheckTask(id); err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		return mustJSON(map[string]any{"success": true, "persisted": true, "config_path": EmailCheckConfigPath, "id": id})
	default:
		return methodNotAllowedResponse()
	}
}

func (h *HttpAPI_Plugin) handleLoginAPI(r *http.Request, body string) []byte {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return methodNotAllowedResponse()
	}
	req := emailLoginRequest{AccountID: r.URL.Query().Get("account_id")}
	if strings.TrimSpace(body) != "" {
		_ = json.Unmarshal([]byte(body), &req)
	}
	account, err := h.resolveAccount(req.AccountID)
	if err != nil {
		return mustJSON(map[string]any{"success": false, "error": err.Error()})
	}
	savedAccount := account
	passwordSource := "saved"
	if len(req.Account) > 0 && string(req.Account) != "null" {
		account, err = parseAccount(string(req.Account), account)
		if err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		if account.Password == "" {
			account.Password = savedAccount.Password
			passwordSource = "saved"
		} else {
			passwordSource = "form"
		}
	}
	if strings.TrimSpace(account.Password) == "" {
		lastCheck := newAccountCheckState("error", "account.password is required", mail.IncomingProtocol(account))
		_ = h.updateAccountLastCheck(account.ID, lastCheck)
		return mustJSON(map[string]any{"success": false, "error": "account.password is required", "account": sanitizeAccount(withAccountLastCheck(account, lastCheck))})
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	result := map[string]any{
		"account": sanitizeAccount(account),
		"diagnostics": map[string]any{
			"password_source":  passwordSource,
			"password_entered": req.PasswordProvided,
		},
	}
	authenticatedAccount, incomingAttempts, err := mail.TestIncomingLogin(ctx, account)
	if err != nil {
		lastCheck := newAccountCheckState("error", err.Error(), mail.IncomingProtocol(account))
		account.LastCheck = lastCheck
		_ = h.updateAccountLastCheck(account.ID, lastCheck)
		result["account"] = sanitizeAccount(account)
		result["incoming"] = map[string]any{"success": false, "protocol": mail.IncomingProtocol(account), "error": err.Error(), "attempts": incomingAttempts}
		return mustJSON(map[string]any{"success": false, "result": result})
	}
	account = authenticatedAccount
	result["account"] = sanitizeAccount(account)
	result["incoming"] = map[string]any{"success": true, "protocol": mail.IncomingProtocol(account), "attempts": incomingAttempts, "username": mail.RedactLoginUsername(account.Username)}
	if strings.TrimSpace(account.SMTP.Host) != "" {
		if err := mail.TestSMTPLogin(ctx, account); err != nil {
			lastCheck := newAccountCheckState("error", err.Error(), mail.IncomingProtocol(account))
			account.LastCheck = lastCheck
			_ = h.updateAccountLastCheck(account.ID, lastCheck)
			result["account"] = sanitizeAccount(account)
			result["smtp"] = map[string]any{"success": false, "error": err.Error()}
			return mustJSON(map[string]any{"success": false, "result": result})
		}
		result["smtp"] = map[string]any{"success": true}
	}
	account.LastCheck = newAccountCheckState("ok", "登入檢查成功", mail.IncomingProtocol(account))
	result["account"] = sanitizeAccount(account)
	response := map[string]any{"success": true, "result": result}
	if req.PersistOnSuccess {
		if err := h.saveAccount(account); err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error(), "result": result})
		}
		saved := sanitizeAccount(account)
		result["account"] = saved
		response["account"] = saved
		response["persisted"] = true
		response["config_path"] = EmailCheckConfigPath
	} else {
		_ = h.updateAccountLastCheck(account.ID, account.LastCheck)
	}
	return mustJSON(response)
}

func (h *HttpAPI_Plugin) handleMessagesAPI(r *http.Request, path []string, body string) []byte {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return methodNotAllowedResponse()
	}
	req := h.messageRequestFromHTTP(r, body)
	if len(path) > 0 {
		uid, err := url.PathUnescape(path[0])
		if err != nil {
			uid = path[0]
		}
		req.UIDs = []string{uid}
		req.IncludeBody = true
	}
	account, err := h.resolveAccount(req.AccountID)
	if err != nil {
		return mustJSON(map[string]any{"success": false, "error": err.Error()})
	}
	applyListDefaults(&req, h.config.Defaults, account)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if req.MarkSeen && !req.IncludeBody && len(req.UIDs) > 0 {
		if err := mail.MarkIncomingSeen(ctx, account, req); err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		return mustJSON(map[string]any{"success": true, "account": sanitizeAccount(account), "folder": req.Folder, "marked_seen": true, "uids": req.UIDs})
	}
	messages, err := mail.ListIncomingMessages(ctx, account, req)
	if err != nil {
		return mustJSON(map[string]any{"success": false, "error": err.Error()})
	}
	if len(path) > 0 {
		if len(messages) == 0 {
			return mustJSON(map[string]any{"success": false, "error": "message not found", "uid": path[0]})
		}
		return mustJSON(map[string]any{"success": true, "message": messages[0]})
	}
	return mustJSON(map[string]any{"success": true, "account": sanitizeAccount(account), "folder": req.Folder, "count": len(messages), "messages": messages})
}

func (h *HttpAPI_Plugin) handleCheckAPI(r *http.Request, body string) []byte {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return methodNotAllowedResponse()
	}
	req := h.messageRequestFromHTTP(r, body)
	req.UnreadOnly = true
	req.IncludeBody = false
	account, err := h.resolveAccount(req.AccountID)
	if err != nil {
		return mustJSON(map[string]any{"success": false, "error": err.Error()})
	}
	applyListDefaults(&req, h.config.Defaults, account)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	messages, err := mail.ListIncomingMessages(ctx, account, req)
	if err != nil {
		return mustJSON(map[string]any{"success": false, "error": err.Error()})
	}
	return mustJSON(map[string]any{
		"success":      true,
		"account":      sanitizeAccount(account),
		"folder":       req.Folder,
		"unread_count": len(messages),
		"messages":     messages,
		"checked_at":   time.Now().Format(time.RFC3339),
	})
}

func (h *HttpAPI_Plugin) handleReplyAPI(r *http.Request, body string) []byte {
	if r.Method != http.MethodPost {
		return methodNotAllowedResponse()
	}
	var req model.EmailReplyRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return mustJSON(map[string]any{"success": false, "error": "invalid reply json"})
	}
	account, err := h.resolveAccount(req.AccountID)
	if err != nil {
		return mustJSON(map[string]any{"success": false, "error": err.Error()})
	}
	if strings.TrimSpace(req.Text) == "" && strings.TrimSpace(req.HTML) == "" {
		return mustJSON(map[string]any{"success": false, "error": "reply text or html is required"})
	}
	if strings.TrimSpace(req.Folder) == "" {
		req.Folder = firstNonEmpty(account.DefaultFolder, h.config.Defaults.Folder, "INBOX")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var original *model.EmailMessage
	if strings.TrimSpace(req.UID) != "" {
		listReq := model.EmailListRequest{Folder: req.Folder, UIDs: []string{req.UID}, IncludeBody: false, Limit: 1}
		messages, err := mail.ListIncomingMessages(ctx, account, listReq)
		if err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		if len(messages) > 0 {
			original = &messages[0]
		}
	}
	sent, err := mail.SendSMTPReply(ctx, account, req, original)
	if err != nil {
		return mustJSON(map[string]any{"success": false, "error": err.Error()})
	}
	return mustJSON(map[string]any{"success": true, "sent": sent})
}

func (h *HttpAPI_Plugin) handleSkillsAPI(r *http.Request, path []string, body string) []byte {
	if len(path) == 0 {
		if r.Method != http.MethodGet {
			return methodNotAllowedResponse()
		}
		skills, err := listPluginSkills()
		if err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error(), "root": EmailCheckSkillRootPath})
		}
		return mustJSON(map[string]any{"success": true, "root": EmailCheckSkillRootPath, "skills": skills})
	}
	if strings.EqualFold(path[0], "cards") {
		return handleSkillCardsAPI(r, path[1:], body)
	}
	if len(path) == 2 && strings.EqualFold(path[1], "content") {
		if r.Method != http.MethodGet {
			return methodNotAllowedResponse()
		}
		content, entryPath, err := readPluginSkillContent(path[0])
		if err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error(), "id": path[0]})
		}
		return mustJSON(map[string]any{"success": true, "id": sanitizeSkillID(path[0]), "entry": entryPath, "content": content})
	}
	return mustJSON(map[string]any{"success": false, "error": "unknown email skill endpoint"})
}

func (h *HttpAPI_Plugin) messageRequestFromHTTP(r *http.Request, body string) model.EmailListRequest {
	query := r.URL.Query()
	req := model.EmailListRequest{
		AccountID:  query.Get("account_id"),
		Folder:     query.Get("folder"),
		UnreadOnly: query.Get("unread_only") == "1" || strings.EqualFold(query.Get("unread_only"), "true"),
		Since:      query.Get("since"),
	}
	if limit := intFromString(query.Get("limit")); limit > 0 {
		req.Limit = limit
	}
	if days := intFromString(query.Get("since_days")); days > 0 {
		req.SinceDays = days
	}
	if strings.TrimSpace(body) != "" {
		_ = json.Unmarshal([]byte(body), &req)
	}
	return req
}

func (h *HttpAPI_Plugin) authResponse(r *http.Request) []byte {
	var req hostAuthRequest
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
		return mustJSON(map[string]any{"success": false, "error": "auth_token is required", "plugin": h.statusPayload()})
	}
	expiresAt := time.Time{}
	if text := strings.TrimSpace(req.ExpiresAt); text != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			expiresAt = parsed
		}
	}
	h.mu.Lock()
	h.hostAuth = hostAuth{
		Token:     token,
		TokenType: firstNonEmpty(req.TokenType, "Bearer"),
		Header:    firstNonEmpty(req.Header, "Authentication"),
		Account:   strings.TrimSpace(req.Account),
		Project:   strings.TrimSpace(req.Project),
		Source:    firstNonEmpty(req.Source, "host"),
		ExpiresAt: expiresAt,
		UpdatedAt: time.Now(),
	}
	h.mu.Unlock()
	return mustJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
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

func (h *HttpAPI_Plugin) loadResponse() []byte {
	if err := h.loadConfig(true); err != nil {
		h.mu.Lock()
		h.lastLoadErr = err.Error()
		h.mu.Unlock()
		return mustJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	return mustJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
}

func (h *HttpAPI_Plugin) ensureLoaded() error {
	return h.loadConfig(false)
}

func (h *HttpAPI_Plugin) loadConfig(force bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	configPath := EmailCheckConfigPath
	stat, statErr := os.Stat(configPath)
	if !force && h.loaded && h.lastLoadErr == "" && statErr == nil && !stat.ModTime().After(h.lastModified) {
		return nil
	}
	cfg, modified, err := readEmailConfig(configPath)
	if err != nil {
		h.loaded = false
		h.lastLoadErr = err.Error()
		return err
	}
	h.config = cfg
	h.accounts = map[string]model.EmailAccount{}
	for _, account := range cfg.Accounts {
		if account.ID == "" {
			account.ID = nextID("account")
		}
		h.accounts[account.ID] = account
	}
	h.loaded = true
	h.loadedAt = time.Now()
	h.lastLoadErr = ""
	h.lastModified = modified
	if h.autoCheckRunning == nil {
		h.autoCheckRunning = map[string]bool{}
	}
	h.startAutoCheckSchedulerLocked()
	return nil
}

func (h *HttpAPI_Plugin) statusPayload() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return map[string]any{
		"id":               "email-check",
		"name":             "EMAIL 檢查",
		"version":          EmailCheckPluginVersion,
		"loaded":           h.loaded,
		"loaded_at":        optionalRFC3339(h.loadedAt),
		"last_error":       h.lastLoadErr,
		"config_path":      EmailCheckConfigPath,
		"last_modified":    optionalRFC3339(h.lastModified),
		"account_count":    len(h.accounts),
		"auto_check_count": len(h.config.AutoChecks),
		"host_auth":        hostAuthStatus(h.hostAuth),
	}
}

func hostAuthStatus(auth hostAuth) map[string]any {
	return map[string]any{
		"available":  strings.TrimSpace(auth.Token) != "",
		"account":    auth.Account,
		"project":    auth.Project,
		"source":     auth.Source,
		"updated_at": optionalRFC3339(auth.UpdatedAt),
		"expires_at": optionalRFC3339(auth.ExpiresAt),
	}
}

func (h *HttpAPI_Plugin) registrationPayload() map[string]any {
	return map[string]any{
		"id":              "email-check",
		"name":            "EMAIL 檢查",
		"version":         EmailCheckPluginVersion,
		"type":            "service",
		"auto_start":      true,
		"service":         "email-check-service",
		"service_url":     "http://127.0.0.1:18185",
		"api_base":        "/api/plugin/email-check",
		"plugin_api_base": "/api/email-check",
		"routes":          []string{"/api/email-check"},
		"mcp_url":         "http://127.0.0.1:18185/mcp",
		"website_path":    "./website/email-check/index.html",
		"runtime": map[string]any{
			"auth":         "/api/email-check/plugin/auth",
			"load":         "/api/email-check/plugin/load",
			"unload":       "/api/email-check/plugin/unload",
			"registration": "/api/email-check/plugin/registration",
		},
		"ui": map[string]any{
			"enabled":      true,
			"order":        40,
			"website_path": "./website/email-check/index.html",
			"href":         "/email-check/index.html",
			"code":         "EMAIL",
			"class":        "email-check",
			"title":        "EMAIL 檢查",
			"description":  "透過 IMAP/POP3/SMTP 登入信箱，讀取信件、回覆信件並檢查郵件。",
			"action":       "進入 EMAIL 檢查",
			"icon":         "fa-solid fa-envelope-open-text",
		},
		"invoke":       "CallPlugin",
		"capabilities": []string{"lifecycle", "registration", "host-auth", "mcp", "config", "imap-login", "imap-read", "pop3-login", "pop3-read", "smtp-reply", "mail-check", "mail-auto-check", "skill-guide", "chat-guide"},
	}
}

func (h *HttpAPI_Plugin) listAccounts() []model.EmailAccount {
	h.mu.Lock()
	defer h.mu.Unlock()
	accounts := make([]model.EmailAccount, 0, len(h.accounts))
	for _, account := range h.accounts {
		accounts = append(accounts, account)
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		return strings.ToLower(accounts[i].ID) < strings.ToLower(accounts[j].ID)
	})
	return accounts
}

func (h *HttpAPI_Plugin) accountByID(id string) (model.EmailAccount, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	account, ok := h.accounts[strings.TrimSpace(id)]
	return account, ok
}

func (h *HttpAPI_Plugin) resolveAccount(id string) (model.EmailAccount, error) {
	if err := h.ensureLoaded(); err != nil {
		return model.EmailAccount{}, err
	}
	id = strings.TrimSpace(id)
	h.mu.Lock()
	defer h.mu.Unlock()
	if id != "" {
		account, ok := h.accounts[id]
		if !ok {
			return model.EmailAccount{}, fmt.Errorf("account not found: %s", id)
		}
		return account, nil
	}
	for _, account := range h.accounts {
		if account.Enabled {
			return account, nil
		}
	}
	for _, account := range h.accounts {
		return account, nil
	}
	return model.EmailAccount{}, errors.New("no email account configured")
}

func (h *HttpAPI_Plugin) saveAccount(account model.EmailAccount) error {
	if strings.TrimSpace(account.ID) == "" {
		account.ID = sanitizeID(firstNonEmpty(account.Email, account.Username, account.Name))
	}
	if strings.TrimSpace(account.ID) == "" {
		account.ID = nextID("account")
	}
	normalizeAccount(&account)
	h.mu.Lock()
	cfg := h.config
	h.mu.Unlock()
	found := false
	for index, existing := range cfg.Accounts {
		if existing.ID == account.ID {
			if account.Password == "" {
				account.Password = existing.Password
			}
			cfg.Accounts[index] = account
			found = true
			break
		}
	}
	if !found {
		cfg.Accounts = append(cfg.Accounts, account)
	}
	if err := writeEmailConfig(cfg); err != nil {
		return err
	}
	h.mu.Lock()
	h.loaded = false
	h.mu.Unlock()
	return h.loadConfig(true)
}

func (h *HttpAPI_Plugin) updateAccountLastCheck(id string, lastCheck model.EmailCheckState) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("account id is required")
	}
	normalizeAccountCheckState(&lastCheck)
	h.mu.Lock()
	cfg := h.config
	h.mu.Unlock()
	found := false
	for index := range cfg.Accounts {
		if cfg.Accounts[index].ID == id {
			cfg.Accounts[index].LastCheck = lastCheck
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("account not found: %s", id)
	}
	if err := writeEmailConfig(cfg); err != nil {
		return err
	}
	h.mu.Lock()
	h.loaded = false
	h.mu.Unlock()
	return h.loadConfig(true)
}

func (h *HttpAPI_Plugin) deleteAccount(id string) error {
	id = strings.TrimSpace(id)
	h.mu.Lock()
	cfg := h.config
	h.mu.Unlock()
	next := make([]model.EmailAccount, 0, len(cfg.Accounts))
	found := false
	for _, account := range cfg.Accounts {
		if account.ID == id {
			found = true
			continue
		}
		next = append(next, account)
	}
	if !found {
		return fmt.Errorf("account not found: %s", id)
	}
	cfg.Accounts = next
	if err := writeEmailConfig(cfg); err != nil {
		return err
	}
	h.mu.Lock()
	h.loaded = false
	h.mu.Unlock()
	return h.loadConfig(true)
}

func (h *HttpAPI_Plugin) listAutoCheckTasks() []model.EmailAutoCheckTask {
	h.mu.Lock()
	defer h.mu.Unlock()
	tasks := append([]model.EmailAutoCheckTask(nil), h.config.AutoChecks...)
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Enabled != tasks[j].Enabled {
			return tasks[i].Enabled
		}
		return strings.ToLower(tasks[i].Name) < strings.ToLower(tasks[j].Name)
	})
	return tasks
}

func (h *HttpAPI_Plugin) autoCheckTaskByID(id string) (model.EmailAutoCheckTask, bool) {
	id = strings.TrimSpace(id)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, task := range h.config.AutoChecks {
		if task.ID == id {
			return task, true
		}
	}
	return model.EmailAutoCheckTask{}, false
}

func (h *HttpAPI_Plugin) saveAutoCheckTask(task model.EmailAutoCheckTask) (model.EmailAutoCheckTask, error) {
	normalizeAutoCheckTask(&task)
	if task.ID == "" {
		task.ID = nextID("auto-check")
	}
	if task.AccountID == "" {
		return task, errors.New("auto-check account_id is required")
	}
	if _, ok := h.accountByID(task.AccountID); !ok {
		return task, fmt.Errorf("account not found: %s", task.AccountID)
	}
	h.mu.Lock()
	cfg := h.config
	h.mu.Unlock()
	found := false
	for index, existing := range cfg.AutoChecks {
		if existing.ID == task.ID {
			if task.LastRun == "" {
				task.LastRun = existing.LastRun
			}
			if task.LastResult.Status == "" {
				task.LastResult = existing.LastResult
			}
			cfg.AutoChecks[index] = task
			found = true
			break
		}
	}
	if !found {
		cfg.AutoChecks = append(cfg.AutoChecks, task)
	}
	if err := h.replaceConfig(cfg); err != nil {
		return task, err
	}
	if saved, ok := h.autoCheckTaskByID(task.ID); ok {
		return saved, nil
	}
	return task, nil
}

func (h *HttpAPI_Plugin) updateAutoCheckTaskRun(task model.EmailAutoCheckTask, result model.EmailAutoCheckResult) (model.EmailAutoCheckTask, error) {
	normalizeAutoCheckResult(&result)
	task.LastResult = result
	task.LastRun = result.CheckedAt
	task.NextRun = nextAutoCheckTime(task, time.Now()).Format(time.RFC3339)
	normalizeAutoCheckTask(&task)
	h.mu.Lock()
	cfg := h.config
	h.mu.Unlock()
	found := false
	for index := range cfg.AutoChecks {
		if cfg.AutoChecks[index].ID == task.ID {
			cfg.AutoChecks[index] = task
			found = true
			break
		}
	}
	if !found {
		return task, fmt.Errorf("auto-check task not found: %s", task.ID)
	}
	if err := h.replaceConfig(cfg); err != nil {
		return task, err
	}
	saved, ok := h.autoCheckTaskByID(task.ID)
	if ok {
		return saved, nil
	}
	return task, nil
}

func (h *HttpAPI_Plugin) deleteAutoCheckTask(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("auto-check task id is required")
	}
	h.mu.Lock()
	cfg := h.config
	h.mu.Unlock()
	next := make([]model.EmailAutoCheckTask, 0, len(cfg.AutoChecks))
	found := false
	for _, task := range cfg.AutoChecks {
		if task.ID == id {
			found = true
			continue
		}
		next = append(next, task)
	}
	if !found {
		return fmt.Errorf("auto-check task not found: %s", id)
	}
	cfg.AutoChecks = next
	return h.replaceConfig(cfg)
}

func (h *HttpAPI_Plugin) replaceConfig(cfg emailConfig) error {
	if err := writeEmailConfig(cfg); err != nil {
		return err
	}
	h.mu.Lock()
	h.loaded = false
	h.mu.Unlock()
	return h.loadConfig(true)
}

func (h *HttpAPI_Plugin) startAutoCheckSchedulerLocked() {
	h.schedulerOnce.Do(func() {
		go h.autoCheckLoop()
	})
}

func (h *HttpAPI_Plugin) autoCheckLoop() {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		<-timer.C
		h.runDueAutoChecks(context.Background())
		timer.Reset(time.Minute)
	}
}

func (h *HttpAPI_Plugin) runDueAutoChecks(ctx context.Context) {
	if err := h.ensureLoaded(); err != nil {
		return
	}
	now := time.Now()
	tasks := []model.EmailAutoCheckTask{}
	h.mu.Lock()
	for _, task := range h.config.AutoChecks {
		if !task.Enabled {
			continue
		}
		nextRun, err := time.Parse(time.RFC3339Nano, task.NextRun)
		if err != nil || nextRun.After(now) {
			continue
		}
		tasks = append(tasks, task)
	}
	h.mu.Unlock()
	for _, task := range tasks {
		runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		_, _, _ = h.runAutoCheckTask(runCtx, task, true)
		cancel()
	}
}

func (h *HttpAPI_Plugin) runAutoCheckTaskByID(ctx context.Context, id string, manual bool) (model.EmailAutoCheckResult, model.EmailAutoCheckTask, error) {
	task, ok := h.autoCheckTaskByID(id)
	if !ok {
		return model.EmailAutoCheckResult{}, model.EmailAutoCheckTask{}, fmt.Errorf("auto-check task not found: %s", id)
	}
	return h.runAutoCheckTask(ctx, task, manual)
}

func (h *HttpAPI_Plugin) runAutoCheckTask(ctx context.Context, task model.EmailAutoCheckTask, manual bool) (model.EmailAutoCheckResult, model.EmailAutoCheckTask, error) {
	normalizeAutoCheckTask(&task)
	if !manual && !task.Enabled {
		return task.LastResult, task, nil
	}
	if !h.beginAutoCheckRun(task.ID) {
		return task.LastResult, task, fmt.Errorf("auto-check task is already running: %s", task.ID)
	}
	defer h.endAutoCheckRun(task.ID)

	result, err := h.executeAutoCheckTask(ctx, task)
	if err != nil {
		result = model.EmailAutoCheckResult{
			Status:    "error",
			Message:   err.Error(),
			CheckedAt: time.Now().Format(time.RFC3339),
		}
	}
	saved, saveErr := h.updateAutoCheckTaskRun(task, result)
	if err == nil && saveErr != nil {
		err = saveErr
	}
	return result, saved, err
}

func (h *HttpAPI_Plugin) beginAutoCheckRun(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.autoCheckRunning == nil {
		h.autoCheckRunning = map[string]bool{}
	}
	if h.autoCheckRunning[id] {
		return false
	}
	h.autoCheckRunning[id] = true
	return true
}

func (h *HttpAPI_Plugin) endAutoCheckRun(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.autoCheckRunning, id)
}

func (h *HttpAPI_Plugin) executeAutoCheckTask(ctx context.Context, task model.EmailAutoCheckTask) (model.EmailAutoCheckResult, error) {
	account, err := h.resolveAccount(task.AccountID)
	if err != nil {
		return model.EmailAutoCheckResult{}, err
	}
	req := model.EmailListRequest{
		AccountID:  task.AccountID,
		Folder:     task.Folder,
		Limit:      task.Limit,
		UnreadOnly: task.UnreadOnly,
		SinceDays:  task.SinceDays,
	}
	applyListDefaults(&req, h.config.Defaults, account)
	messages, err := mail.ListIncomingMessages(ctx, account, req)
	if err != nil {
		return model.EmailAutoCheckResult{}, err
	}
	matchedUIDs := make([]string, 0)
	for _, summary := range messages {
		if !matchesAutoCheckHeader(task, summary) {
			continue
		}
		message := summary
		if len(task.BodyKeywords) > 0 {
			bodyReq := req
			bodyReq.UIDs = []string{summary.UID}
			bodyReq.IncludeBody = true
			bodyReq.Limit = 1
			bodyMessages, err := mail.ListIncomingMessages(ctx, account, bodyReq)
			if err != nil {
				return model.EmailAutoCheckResult{}, err
			}
			if len(bodyMessages) > 0 {
				message = bodyMessages[0]
			}
		}
		if !matchesAutoCheckBody(task, message) {
			continue
		}
		matchedUIDs = append(matchedUIDs, summary.UID)
	}
	lineStatus := "disabled"
	if task.Line.Enabled {
		lineStatus = "reserved"
	}
	message := fmt.Sprintf("已檢查 %d 封信，符合條件 %d 封", len(messages), len(matchedUIDs))
	if task.Line.Enabled {
		message += "；LINE 通知尚未實作，已保留聊天室設定"
	}
	result := model.EmailAutoCheckResult{
		Status:       "ok",
		Message:      message,
		CheckedAt:    time.Now().Format(time.RFC3339),
		ScannedCount: len(messages),
		MatchedCount: len(matchedUIDs),
		MatchedUIDs:  matchedUIDs,
		LineStatus:   lineStatus,
	}
	normalizeAutoCheckResult(&result)
	return result, nil
}

func matchesAutoCheckHeader(task model.EmailAutoCheckTask, message model.EmailMessage) bool {
	if len(task.SubjectKeywords) == 0 && len(task.FromKeywords) == 0 && len(task.BodyKeywords) == 0 {
		return true
	}
	if len(task.SubjectKeywords) > 0 && !containsAnyKeyword(message.Subject, task.SubjectKeywords) {
		return false
	}
	if len(task.FromKeywords) > 0 && !containsAnyKeyword(message.From, task.FromKeywords) {
		return false
	}
	return true
}

func matchesAutoCheckBody(task model.EmailAutoCheckTask, message model.EmailMessage) bool {
	if len(task.BodyKeywords) == 0 {
		return true
	}
	body := strings.Join([]string{message.Subject, message.From, message.TextPreview, message.TextBody, message.HTMLBody}, "\n")
	return containsAnyKeyword(body, task.BodyKeywords)
}

func containsAnyKeyword(text string, keywords []string) bool {
	text = strings.ToLower(text)
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func readEmailConfig(path string) (emailConfig, time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && path == EmailCheckConfigPath {
			return initializeEmailConfigFromDefault()
		}
		return emailConfig{}, time.Time{}, err
	}
	var cfg emailConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return emailConfig{}, time.Time{}, fmt.Errorf("invalid EMAIL config: %w", err)
	}
	normalizeEmailConfig(&cfg)
	modified := time.Now()
	if stat, err := os.Stat(path); err == nil {
		modified = stat.ModTime()
	}
	return cfg, modified, nil
}

func initializeEmailConfigFromDefault() (emailConfig, time.Time, error) {
	cfg := emailConfig{Version: EmailCheckPluginVersion}
	defaultPath := filepath.Join(filepath.Dir(EmailCheckConfigPath), "config.default.json")
	if data, err := os.ReadFile(defaultPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return emailConfig{}, time.Time{}, fmt.Errorf("invalid EMAIL default config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return emailConfig{}, time.Time{}, err
	}
	normalizeEmailConfig(&cfg)
	if err := writeEmailConfig(cfg); err != nil {
		return emailConfig{}, time.Time{}, err
	}
	modified := time.Now()
	if stat, err := os.Stat(EmailCheckConfigPath); err == nil {
		modified = stat.ModTime()
	}
	return cfg, modified, nil
}

func writeEmailConfig(cfg emailConfig) error {
	normalizeEmailConfig(&cfg)
	if err := os.MkdirAll(filepath.Dir(EmailCheckConfigPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := EmailCheckConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, EmailCheckConfigPath)
}

func normalizeEmailConfig(cfg *emailConfig) {
	if cfg.Version == "" {
		cfg.Version = EmailCheckPluginVersion
	}
	if cfg.Defaults.Folder == "" {
		cfg.Defaults.Folder = "INBOX"
	}
	if cfg.Defaults.Limit <= 0 {
		cfg.Defaults.Limit = 20
	}
	if cfg.Defaults.SinceDays <= 0 {
		cfg.Defaults.SinceDays = 7
	}
	if cfg.Features == nil {
		cfg.Features = map[string]bool{"imap_login": true, "imap_read": true, "pop3_login": true, "pop3_read": true, "smtp_reply": true, "mail_check": true, "auto_check": true}
	}
	if cfg.Accounts == nil {
		cfg.Accounts = []model.EmailAccount{}
	}
	if cfg.AutoChecks == nil {
		cfg.AutoChecks = []model.EmailAutoCheckTask{}
	}
	for index := range cfg.Accounts {
		normalizeAccount(&cfg.Accounts[index])
	}
	for index := range cfg.AutoChecks {
		normalizeAutoCheckTask(&cfg.AutoChecks[index])
	}
}

func normalizeAutoCheckTask(task *model.EmailAutoCheckTask) {
	task.ID = sanitizeID(task.ID)
	task.Name = strings.TrimSpace(task.Name)
	task.AccountID = strings.TrimSpace(task.AccountID)
	task.Folder = firstNonEmpty(task.Folder, "INBOX")
	if task.SinceDays <= 0 {
		task.SinceDays = 7
	}
	if task.Limit <= 0 || task.Limit > 100 {
		task.Limit = 100
	}
	if task.IntervalMinutes <= 0 {
		task.IntervalMinutes = 60
	}
	task.SubjectKeywords = normalizeKeywordList(task.SubjectKeywords)
	task.FromKeywords = normalizeKeywordList(task.FromKeywords)
	task.BodyKeywords = normalizeKeywordList(task.BodyKeywords)
	task.Prompt = strings.TrimSpace(task.Prompt)
	task.Line.RoomID = strings.TrimSpace(task.Line.RoomID)
	task.Line.Note = strings.TrimSpace(task.Line.Note)
	task.LastRun = normalizeOptionalTime(task.LastRun)
	task.NextRun = normalizeOptionalTime(task.NextRun)
	normalizeAutoCheckResult(&task.LastResult)
	if task.Name == "" {
		task.Name = firstNonEmpty(task.AccountID, "信件自動檢測")
	}
	if task.Enabled && task.NextRun == "" {
		task.NextRun = nextAutoCheckTime(*task, time.Now()).Format(time.RFC3339)
	}
}

func normalizeAutoCheckResult(result *model.EmailAutoCheckResult) {
	result.Status = strings.ToLower(strings.TrimSpace(result.Status))
	switch result.Status {
	case "ok", "success", "ready":
		result.Status = "ok"
	case "error", "failed", "fail":
		result.Status = "error"
	case "":
		result.Status = ""
	default:
		result.Status = "unknown"
	}
	result.Message = strings.TrimSpace(result.Message)
	result.CheckedAt = normalizeOptionalTime(result.CheckedAt)
	result.LineStatus = strings.TrimSpace(result.LineStatus)
	result.MatchedUIDs = normalizeKeywordList(result.MatchedUIDs)
	if result.ScannedCount < 0 {
		result.ScannedCount = 0
	}
	if result.MatchedCount < 0 {
		result.MatchedCount = 0
	}
}

func normalizeKeywordList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == '，' || r == '\n' || r == '\r' || r == '\t'
		}) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			key := strings.ToLower(part)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, part)
		}
	}
	return out
}

func normalizeOptionalTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.Format(time.RFC3339)
	}
	return value
}

func nextAutoCheckTime(task model.EmailAutoCheckTask, base time.Time) time.Time {
	interval := time.Duration(task.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = time.Hour
	}
	return base.Add(interval)
}

func normalizeAccount(account *model.EmailAccount) {
	account.ID = strings.TrimSpace(account.ID)
	account.Name = strings.TrimSpace(account.Name)
	account.Email = strings.TrimSpace(account.Email)
	account.FromName = strings.TrimSpace(account.FromName)
	account.Username = strings.TrimSpace(account.Username)
	account.Protocol = mail.IncomingProtocol(*account)
	account.DefaultFolder = firstNonEmpty(account.DefaultFolder, "INBOX")
	if account.ID == "" {
		account.ID = sanitizeID(firstNonEmpty(account.Email, account.Username, account.Name))
	}
	if account.Name == "" {
		account.Name = firstNonEmpty(account.Email, account.Username, account.ID)
	}
	if account.Username == "" {
		account.Username = account.Email
	}
	if account.IMAP.Port == 0 {
		account.IMAP.Port = 993
	}
	if account.POP3.Port == 0 {
		account.POP3.Port = 995
	}
	if account.Protocol == "pop3" && strings.TrimSpace(account.POP3.Host) == "" {
		account.POP3.Host = account.IMAP.Host
	}
	if account.Protocol == "imap" && strings.TrimSpace(account.IMAP.Host) == "" {
		account.IMAP.Host = account.POP3.Host
	}
	if account.SMTP.Port == 0 {
		account.SMTP.Port = 465
	}
	normalizeAccountCheckState(&account.LastCheck)
}

func newAccountCheckState(status string, message string, protocol string) model.EmailCheckState {
	state := model.EmailCheckState{
		Status:    status,
		Message:   strings.TrimSpace(message),
		CheckedAt: time.Now().Format(time.RFC3339),
		Protocol:  protocol,
	}
	normalizeAccountCheckState(&state)
	return state
}

func withAccountLastCheck(account model.EmailAccount, lastCheck model.EmailCheckState) model.EmailAccount {
	account.LastCheck = lastCheck
	return account
}

func normalizeAccountCheckState(state *model.EmailCheckState) {
	state.Status = strings.ToLower(strings.TrimSpace(state.Status))
	switch state.Status {
	case "ok", "success", "ready":
		state.Status = "ok"
	case "error", "failed", "fail":
		state.Status = "error"
	case "unknown", "":
		state.Status = ""
	default:
		state.Status = "unknown"
	}
	state.Message = strings.TrimSpace(state.Message)
	state.CheckedAt = strings.TrimSpace(state.CheckedAt)
	if state.CheckedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, state.CheckedAt); err == nil {
			state.CheckedAt = parsed.Format(time.RFC3339)
		}
	}
	state.Protocol = strings.ToLower(strings.TrimSpace(state.Protocol))
	if state.Protocol != "imap" && state.Protocol != "pop3" {
		state.Protocol = ""
	}
	if state.Status == "" {
		state.Message = ""
		state.CheckedAt = ""
		state.Protocol = ""
	}
}

func parseAccount(body string, fallback model.EmailAccount) (model.EmailAccount, error) {
	account := fallback
	if err := json.Unmarshal([]byte(body), &account); err != nil {
		return account, errors.New("invalid account json")
	}
	normalizeAccount(&account)
	if account.Email == "" {
		return account, errors.New("account.email is required")
	}
	if mail.IncomingProtocol(account) == "pop3" {
		if account.POP3.Host == "" {
			return account, errors.New("account.pop3.host is required")
		}
	} else if account.IMAP.Host == "" {
		return account, errors.New("account.imap.host is required")
	}
	if account.Username == "" {
		return account, errors.New("account.username is required")
	}
	return account, nil
}

func parseAutoCheckTask(body string, fallback model.EmailAutoCheckTask) (model.EmailAutoCheckTask, error) {
	task := fallback
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		return task, errors.New("invalid auto-check task json")
	}
	normalizeAutoCheckTask(&task)
	if task.AccountID == "" {
		return task, errors.New("auto-check account_id is required")
	}
	if task.Name == "" {
		return task, errors.New("auto-check name is required")
	}
	return task, nil
}

func sanitizeConfig(cfg emailConfig) emailConfig {
	cfg.Accounts = sanitizeAccountsAsEmailAccounts(cfg.Accounts)
	return cfg
}

func sanitizeAccounts(accounts []model.EmailAccount) []accountStatus {
	out := make([]accountStatus, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, sanitizeAccount(account))
	}
	return out
}

func sanitizeAccountsAsEmailAccounts(accounts []model.EmailAccount) []model.EmailAccount {
	out := make([]model.EmailAccount, 0, len(accounts))
	for _, account := range accounts {
		account.Password = ""
		out = append(out, account)
	}
	return out
}

func sanitizeAccount(account model.EmailAccount) accountStatus {
	return accountStatus{
		ID:            account.ID,
		Name:          account.Name,
		Email:         account.Email,
		FromName:      account.FromName,
		Username:      account.Username,
		PasswordSet:   strings.TrimSpace(account.Password) != "",
		DefaultFolder: account.DefaultFolder,
		Enabled:       account.Enabled,
		Protocol:      mail.IncomingProtocol(account),
		IMAP:          account.IMAP,
		POP3:          account.POP3,
		SMTP:          account.SMTP,
		LastCheck:     account.LastCheck,
		Metadata:      account.Metadata,
	}
}

func applyListDefaults(req *model.EmailListRequest, defaults emailDefaults, account model.EmailAccount) {
	if strings.TrimSpace(req.Folder) == "" {
		req.Folder = firstNonEmpty(account.DefaultFolder, defaults.Folder, "INBOX")
	}
	if req.Limit > 100 {
		req.Limit = 100
	}
	if req.SinceDays <= 0 && strings.TrimSpace(req.Since) == "" {
		req.SinceDays = defaults.SinceDays
	}
}

type skillCard struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Prompt      string `json:"prompt"`
}

func handleSkillCardsAPI(r *http.Request, path []string, body string) []byte {
	if len(path) == 0 {
		switch r.Method {
		case http.MethodGet:
			cards, err := readSkillCards()
			if err != nil {
				return mustJSON(map[string]any{"success": false, "error": err.Error(), "path": EmailCheckSkillCardsPath})
			}
			return mustJSON(map[string]any{"success": true, "path": EmailCheckSkillCardsPath, "cards": cards})
		case http.MethodPost:
			card, err := parseSkillCard(body)
			if err != nil {
				return mustJSON(map[string]any{"success": false, "error": err.Error()})
			}
			cards, err := readSkillCards()
			if err != nil {
				return mustJSON(map[string]any{"success": false, "error": err.Error()})
			}
			if card.ID == "" {
				card.ID = nextID("skill")
			}
			cards = upsertSkillCard(cards, card)
			if err := writeSkillCards(cards); err != nil {
				return mustJSON(map[string]any{"success": false, "error": err.Error()})
			}
			return mustJSON(map[string]any{"success": true, "card": card, "cards": cards})
		default:
			return methodNotAllowedResponse()
		}
	}
	id := strings.TrimSpace(path[0])
	cards, err := readSkillCards()
	if err != nil {
		return mustJSON(map[string]any{"success": false, "error": err.Error()})
	}
	switch r.Method {
	case http.MethodGet:
		for _, card := range cards {
			if card.ID == id {
				return mustJSON(map[string]any{"success": true, "card": card})
			}
		}
		return mustJSON(map[string]any{"success": false, "error": "skill card not found", "id": id})
	case http.MethodPut, http.MethodPatch:
		card, err := parseSkillCard(body)
		if err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		card.ID = id
		cards = upsertSkillCard(cards, card)
		if err := writeSkillCards(cards); err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		return mustJSON(map[string]any{"success": true, "card": card, "cards": cards})
	case http.MethodDelete:
		next := make([]skillCard, 0, len(cards))
		for _, card := range cards {
			if card.ID != id {
				next = append(next, card)
			}
		}
		if err := writeSkillCards(next); err != nil {
			return mustJSON(map[string]any{"success": false, "error": err.Error()})
		}
		return mustJSON(map[string]any{"success": true, "id": id, "cards": next})
	default:
		return methodNotAllowedResponse()
	}
}

func parseSkillCard(body string) (skillCard, error) {
	var card skillCard
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

func readSkillCards() ([]skillCard, error) {
	if err := ensureSkillCardsFile(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(EmailCheckSkillCardsPath)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Cards []skillCard `json:"cards"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Cards != nil {
		return normalizeSkillCards(wrapper.Cards), nil
	}
	var cards []skillCard
	if err := json.Unmarshal(data, &cards); err != nil {
		return nil, fmt.Errorf("invalid EMAIL skill cards json: %w", err)
	}
	return normalizeSkillCards(cards), nil
}

func ensureSkillCardsFile() error {
	if _, err := os.Stat(EmailCheckSkillCardsPath); err == nil {
		return nil
	}
	data, err := os.ReadFile(EmailCheckDefaultSkillCardsPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(EmailCheckSkillCardsPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(EmailCheckSkillCardsPath, data, 0o644)
}

func writeSkillCards(cards []skillCard) error {
	if err := os.MkdirAll(filepath.Dir(EmailCheckSkillCardsPath), 0o755); err != nil {
		return err
	}
	payload := map[string]any{"cards": normalizeSkillCards(cards)}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(EmailCheckSkillCardsPath, append(data, '\n'), 0o644)
}

func normalizeSkillCards(cards []skillCard) []skillCard {
	out := make([]skillCard, 0, len(cards))
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
			card.ID = nextID("skill")
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

func upsertSkillCard(cards []skillCard, card skillCard) []skillCard {
	next := normalizeSkillCards(cards)
	for index, existing := range next {
		if existing.ID == card.ID {
			next[index] = card
			return next
		}
	}
	return append(next, card)
}

func listPluginSkills() ([]map[string]any, error) {
	if err := os.MkdirAll(EmailCheckSkillRootPath, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(EmailCheckSkillRootPath)
	if err != nil {
		return nil, err
	}
	skills := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entryPath := filepath.Join(EmailCheckSkillRootPath, entry.Name(), "SKILL.md")
		info, err := os.Stat(entryPath)
		if err != nil {
			continue
		}
		skills = append(skills, map[string]any{
			"id":       entry.Name(),
			"dir":      filepath.Join(EmailCheckSkillRootPath, entry.Name()),
			"entry":    entryPath,
			"modified": optionalRFC3339(info.ModTime()),
		})
	}
	return skills, nil
}

func readPluginSkillContent(id string) (string, string, error) {
	_, entryPath, err := resolvePluginSkillPath(id)
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(entryPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", entryPath, fmt.Errorf("skill not found: %s", sanitizeSkillID(id))
		}
		return "", entryPath, err
	}
	return string(data), entryPath, nil
}

func resolvePluginSkillPath(id string) (string, string, error) {
	safeID := sanitizeSkillID(id)
	if safeID == "" {
		return "", "", fmt.Errorf("skill id is required")
	}
	rootAbs, err := filepath.Abs(EmailCheckSkillRootPath)
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
	return filepath.Join(EmailCheckSkillRootPath, safeID), filepath.Join(EmailCheckSkillRootPath, safeID, "SKILL.md"), nil
}
