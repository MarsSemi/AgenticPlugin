package accountmanager

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
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

var AccountManagerConfigPath = "plugins/account-manager/config.json"
var AccountManagerVersion = "0.1.0"

type HttpAPI_Plugin struct {
	mu           sync.Mutex
	loaded       bool
	config       accountManagerConfig
	accounts     map[string]managedAccount
	groups       map[string]accountGroup
	hostAuth     accountManagerHostAuth
	loadedAt     time.Time
	lastLoadErr  string
	lastModified time.Time
}

type accountManagerConfig struct {
	Version        string           `json:"version"`
	Encryption     encryptionConfig `json:"encryption"`
	PasswordPolicy passwordPolicy   `json:"password_policy"`
	Accounts       []managedAccount `json:"accounts"`
	Groups         []accountGroup   `json:"groups"`
}

type encryptionConfig struct {
	Key string `json:"key"`
}

type passwordPolicy struct {
	MinLength             int  `json:"min_length"`
	RequireEnabledAccount bool `json:"require_enabled_account"`
}

type managedAccount struct {
	ID                string             `json:"id"`
	Username          string             `json:"username"`
	DisplayName       string             `json:"display_name,omitempty"`
	Email             string             `json:"email,omitempty"`
	Role              string             `json:"role,omitempty"`
	Enabled           bool               `json:"enabled"`
	Note              string             `json:"note,omitempty"`
	GroupIDs          []string           `json:"group_ids,omitempty"`
	PasswordAES       string             `json:"password_aes,omitempty"`
	InitialPassword   string             `json:"initial_password,omitempty"`
	PasswordUpdatedAt string             `json:"password_updated_at,omitempty"`
	CreatedAt         string             `json:"created_at,omitempty"`
	UpdatedAt         string             `json:"updated_at,omitempty"`
	LastLoginAt       string             `json:"last_login_at,omitempty"`
	Permissions       []pluginPermission `json:"permissions"`
	Metadata          map[string]any     `json:"metadata,omitempty"`
}

type publicAccount struct {
	ID                string             `json:"id"`
	Username          string             `json:"username"`
	DisplayName       string             `json:"display_name,omitempty"`
	Email             string             `json:"email,omitempty"`
	Role              string             `json:"role,omitempty"`
	Enabled           bool               `json:"enabled"`
	Note              string             `json:"note,omitempty"`
	GroupIDs          []string           `json:"group_ids,omitempty"`
	PasswordSet       bool               `json:"password_set"`
	PasswordUpdatedAt string             `json:"password_updated_at,omitempty"`
	CreatedAt         string             `json:"created_at,omitempty"`
	UpdatedAt         string             `json:"updated_at,omitempty"`
	LastLoginAt       string             `json:"last_login_at,omitempty"`
	Permissions       []pluginPermission `json:"permissions"`
	Metadata          map[string]any     `json:"metadata,omitempty"`
}

type pluginPermission struct {
	PluginID   string   `json:"plugin_id"`
	PluginName string   `json:"plugin_name,omitempty"`
	Enabled    bool     `json:"enabled"`
	Scopes     []string `json:"scopes"`
	Note       string   `json:"note,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}

type accountGroup struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Enabled     bool               `json:"enabled"`
	Note        string             `json:"note,omitempty"`
	Permissions []pluginPermission `json:"permissions"`
	CreatedAt   string             `json:"created_at,omitempty"`
	UpdatedAt   string             `json:"updated_at,omitempty"`
	Metadata    map[string]any     `json:"metadata,omitempty"`
}

type accountRequest struct {
	ID          string             `json:"id"`
	Username    string             `json:"username"`
	DisplayName string             `json:"display_name"`
	Email       string             `json:"email"`
	Role        string             `json:"role"`
	Enabled     *bool              `json:"enabled"`
	Note        *string            `json:"note"`
	GroupIDs    []string           `json:"group_ids"`
	Password    string             `json:"password"`
	Permissions []pluginPermission `json:"permissions"`
	Metadata    map[string]any     `json:"metadata"`
}

type groupRequest struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Enabled     *bool              `json:"enabled"`
	Note        *string            `json:"note"`
	Permissions []pluginPermission `json:"permissions"`
	Metadata    map[string]any     `json:"metadata"`
}

type passwordUpdateRequest struct {
	Password string `json:"password"`
}

type verifyRequest struct {
	Account  string `json:"account"`
	Username string `json:"username"`
	Password string `json:"password"`
	Project  string `json:"project"`
	PluginID string `json:"plugin_id"`
	Scope    string `json:"scope"`
}

type accountManagerHostAuth struct {
	Token              string
	TokenType          string
	Header             string
	Account            string
	Project            string
	Source             string
	HostURL            string
	ExpiresAt          time.Time
	UpdatedAt          time.Time
	LastRegisterOK     bool
	LastRegisterError  string
	LastRegisterAt     time.Time
	LastRegisterTarget string
}

type accountManagerHostAuthRequest struct {
	AuthToken string `json:"auth_token"`
	TokenType string `json:"token_type"`
	Header    string `json:"header"`
	Account   string `json:"account"`
	Project   string `json:"project"`
	Source    string `json:"source"`
	HostURL   string `json:"host_url"`
	BaseURL   string `json:"base_url"`
	Origin    string `json:"origin"`
	ExpiresAt string `json:"expires_at"`
}

func (h *HttpAPI_Plugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	applyAccountManagerCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeAccountManagerJSONBytes(w, mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error()}))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	path := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	response := h.Process(w, r, path, bodyBytes)
	writeAccountManagerJSONBytes(w, response)
}

func applyAccountManagerCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Authentication, Accept")
	w.Header().Set("Access-Control-Max-Age", "600")
}

func writeAccountManagerJSONBytes(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}

func mustAccountManagerJSON(payload any) []byte {
	data, err := json.Marshal(payload)
	if err != nil {
		return []byte(`{"success":false,"error":"json marshal failed"}`)
	}
	return data
}

func accountManagerMethodNotAllowedResponse() []byte {
	return mustAccountManagerJSON(map[string]any{"success": false, "error": "method not allowed"})
}

func (h *HttpAPI_Plugin) Process(_ http.ResponseWriter, r *http.Request, path []string, body []byte) []byte {
	normalizedPath := normalizeAccountManagerAPIPath(path)
	if len(normalizedPath) == 0 {
		return h.handleCatalog()
	}

	switch normalizedPath[0] {
	case "plugin":
		return h.handlePluginAPI(r, normalizedPath[1:])
	case "account":
		return h.handleAccountAliasAPI(r, normalizedPath[1:], body)
	case "mcp":
		return h.handleMCPAPI(r)
	case "config":
		return h.handleConfigAPI(r, body)
	case "accounts":
		return h.handleAccountsAPI(r, normalizedPath[1:], body)
	case "groups":
		return h.handleGroupsAPI(r, normalizedPath[1:], body)
	case "auth":
		return h.handleAuthAPI(r, normalizedPath[1:], body)
	case "plugins":
		return h.handlePluginsAPI(r, normalizedPath[1:], body)
	default:
		return h.handleCatalog()
	}
}

func normalizeAccountManagerAPIPath(path []string) []string {
	normalized := normalizeAccountManagerEndpointPath(path, "api")
	return normalizeAccountManagerEndpointPath(normalized, "account-manager")
}

func normalizeAccountManagerEndpointPath(path []string, root string) []string {
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

func lastAccountManagerPathSegment(path []string, fallback string) string {
	if len(path) == 0 {
		return fallback
	}
	return path[len(path)-1]
}

func (h *HttpAPI_Plugin) handleCatalog() []byte {
	return mustAccountManagerJSON(map[string]any{
		"success": true,
		"service": "account-manager",
		"plugin":  h.statusPayload(),
		"apis": []map[string]any{
			{"path": "/api/account-manager/plugin/status", "method": "GET", "description": "取得外掛狀態"},
			{"path": "/api/account-manager/plugin/registration", "method": "GET", "description": "取得外掛註冊資訊"},
			{"path": "/api/account-manager/plugin/auth", "method": "POST", "description": "接收主系統 TOKEN 並註冊帳號管理服務"},
			{"path": "/api/account-manager/config", "method": "GET|PUT", "description": "讀寫帳號管理設定"},
			{"path": "/api/account-manager/accounts", "method": "GET|POST", "description": "列出或建立帳號"},
			{"path": "/api/account-manager/accounts/{id}", "method": "GET|PUT|DELETE", "description": "讀取、修改或刪除帳號"},
			{"path": "/api/account-manager/accounts/{id}/password", "method": "PUT", "description": "更新帳號密碼並以 AES 儲存"},
			{"path": "/api/account-manager/groups", "method": "GET|POST", "description": "列出或建立群組"},
			{"path": "/api/account-manager/groups/{id}", "method": "GET|PUT|DELETE", "description": "讀取、修改或刪除群組"},
			{"path": "/api/account-manager/groups/{id}/permissions", "method": "GET|PUT", "description": "讀寫群組可存取 plugin 權限"},
			{"path": "/api/account-manager/auth/verify", "method": "POST", "description": "驗證帳號密碼與 plugin 權限"},
			{"path": "/api/account-manager/plugins/permissions", "method": "GET|POST", "description": "查詢或驗證 plugin 權限"},
			{"path": "/api/account/verify", "method": "POST", "description": "主系統註冊用帳密驗證 API"},
			{"path": "/api/account/permissions", "method": "GET|POST", "description": "主系統註冊用權限查詢 API"},
			{"path": "/mcp", "method": "GET", "description": "取得 MCP metadata"},
		},
	})
}

func (h *HttpAPI_Plugin) handlePluginAPI(r *http.Request, path []string) []byte {
	cmd := lastAccountManagerPathSegment(path, "status")
	switch cmd {
	case "status":
		if r.Method != http.MethodGet {
			return accountManagerMethodNotAllowedResponse()
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
	case "load":
		if r.Method != http.MethodPost {
			return accountManagerMethodNotAllowedResponse()
		}
		return h.loadResponse()
	case "auth":
		if r.Method != http.MethodPost {
			return accountManagerMethodNotAllowedResponse()
		}
		return h.authResponse(r)
	case "reload":
		if r.Method != http.MethodPost {
			return accountManagerMethodNotAllowedResponse()
		}
		h.mu.Lock()
		h.loaded = false
		h.mu.Unlock()
		return h.loadResponse()
	case "unload":
		if r.Method != http.MethodPost {
			return accountManagerMethodNotAllowedResponse()
		}
		h.mu.Lock()
		h.loaded = false
		h.config = accountManagerConfig{}
		h.accounts = nil
		h.groups = nil
		h.loadedAt = time.Time{}
		h.lastLoadErr = ""
		h.lastModified = time.Time{}
		h.mu.Unlock()
		return mustAccountManagerJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
	case "registration":
		if r.Method != http.MethodGet {
			return accountManagerMethodNotAllowedResponse()
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "plugin": h.registrationPayload()})
	default:
		return h.handleCatalog()
	}
}

func (h *HttpAPI_Plugin) handleAccountAliasAPI(r *http.Request, path []string, body []byte) []byte {
	if len(path) == 0 {
		return h.handleCatalog()
	}
	switch strings.ToLower(path[0]) {
	case "verify":
		return h.handleAuthAPI(r, []string{"verify"}, body)
	case "permissions":
		return h.handlePluginsAPI(r, []string{"permissions"}, body)
	default:
		return h.handleCatalog()
	}
}

func (h *HttpAPI_Plugin) handleMCPAPI(r *http.Request) []byte {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return accountManagerMethodNotAllowedResponse()
	}
	return mustAccountManagerJSON(map[string]any{
		"success": true,
		"mcp": map[string]any{
			"name":            "account-manager",
			"description":     "帳號、AES 密碼與跨 Plugin 權限管理工具。",
			"version":         AccountManagerVersion,
			"plugin_api_base": "/api/account-manager",
			"tools": []map[string]any{
				{"name": "account_manager.accounts.list", "method": "GET", "path": "/api/plugin/account-manager/api/account-manager/accounts", "description": "列出帳號與權限摘要。"},
				{"name": "account_manager.accounts.save", "method": "POST|PUT|DELETE", "path": "/api/plugin/account-manager/api/account-manager/accounts", "description": "建立、更新或刪除帳號。"},
				{"name": "account_manager.password.update", "method": "PUT", "path": "/api/plugin/account-manager/api/account-manager/accounts/{id}/password", "description": "以 AES-GCM 儲存新密碼。"},
				{"name": "account_manager.auth.verify", "method": "POST", "path": "/api/plugin/account-manager/api/account-manager/auth/verify", "description": "驗證帳號、密碼與 plugin scope。"},
				{"name": "account_manager.groups.save", "method": "POST|PUT|DELETE", "path": "/api/plugin/account-manager/api/account-manager/groups", "description": "建立、更新或刪除群組。"},
				{"name": "account_manager.permissions.save", "method": "PUT", "path": "/api/plugin/account-manager/api/account-manager/groups/{id}/permissions", "description": "更新群組可存取 plugin 權限。"},
			},
		},
	})
}

func (h *HttpAPI_Plugin) authResponse(r *http.Request) []byte {
	var req accountManagerHostAuthRequest
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
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "auth_token is required", "plugin": h.statusPayload()})
	}
	expiresAt := time.Time{}
	if text := strings.TrimSpace(req.ExpiresAt); text != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, text); err == nil {
			expiresAt = parsed
		}
	}
	hostURL := resolveAccountManagerHostURL(r, req)
	auth := accountManagerHostAuth{
		Token:     token,
		TokenType: firstNonEmptyAccountManager(req.TokenType, "Bearer"),
		Header:    firstNonEmptyAccountManager(req.Header, "Authentication"),
		Account:   strings.TrimSpace(req.Account),
		Project:   strings.TrimSpace(req.Project),
		Source:    firstNonEmptyAccountManager(req.Source, "host"),
		HostURL:   hostURL,
		ExpiresAt: expiresAt,
		UpdatedAt: time.Now(),
	}
	registerResult := map[string]any{"success": false, "error": "host_url is required"}
	if hostURL != "" {
		registerResult = h.registerAccountManagerWithHost(auth)
	}
	h.mu.Lock()
	if ok, _ := registerResult["success"].(bool); ok {
		auth.LastRegisterOK = true
	} else {
		auth.LastRegisterError = strings.TrimSpace(fmt.Sprint(registerResult["error"]))
	}
	auth.LastRegisterAt = time.Now()
	auth.LastRegisterTarget = strings.TrimSpace(fmt.Sprint(registerResult["target"]))
	h.hostAuth = auth
	h.mu.Unlock()
	return mustAccountManagerJSON(map[string]any{
		"success":      true,
		"plugin":       h.statusPayload(),
		"registration": registerResult,
	})
}

func (h *HttpAPI_Plugin) registerAccountManagerWithHost(auth accountManagerHostAuth) map[string]any {
	target := strings.TrimRight(auth.HostURL, "/") + "/api/reg_account_manager"
	payload := accountManagerHostRegistrationPayload()
	data, err := json.Marshal(payload)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "target": target}
	}
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(data))
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "target": target}
	}
	req.Header.Set("Content-Type", "application/json")
	value := firstNonEmptyAccountManager(auth.TokenType, "Bearer") + " " + auth.Token
	header := firstNonEmptyAccountManager(auth.Header, "Authentication")
	req.Header.Set(header, value)
	req.Header.Set("Authentication", value)
	req.Header.Set("Authorization", value)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "target": target, "payload": payload}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	result := map[string]any{
		"success":     resp.StatusCode >= 200 && resp.StatusCode < 300,
		"status_code": resp.StatusCode,
		"target":      target,
		"payload":     payload,
	}
	if strings.TrimSpace(string(respBody)) != "" {
		var parsed any
		if err := json.Unmarshal(respBody, &parsed); err == nil {
			result["response"] = parsed
		} else {
			result["response"] = strings.TrimSpace(string(respBody))
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result["error"] = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return result
}

func accountManagerHostRegistrationPayload() map[string]any {
	return map[string]any{
		"plugin_id":      "account-manager",
		"name":           "帳號管理",
		"enabled":        true,
		"port":           18186,
		"method":         "POST",
		"verify_api":     "/api/account/verify",
		"permission_api": "/api/account/permissions",
	}
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

func resolveAccountManagerHostURL(r *http.Request, req accountManagerHostAuthRequest) string {
	for _, candidate := range []string{
		os.Getenv("ACCOUNT_MANAGER_HOST_URL"),
		req.HostURL,
		req.BaseURL,
		req.Origin,
		req.Source,
		headerURL(r, "X-Host-Url"),
		headerURL(r, "X-Forwarded-Host"),
		headerURL(r, "Origin"),
		refererOrigin(r),
	} {
		if normalized := normalizeAccountManagerHostURL(candidate); normalized != "" {
			return normalized
		}
	}
	return ""
}

func headerURL(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	value := strings.TrimSpace(r.Header.Get(name))
	if value == "" {
		return ""
	}
	if name == "X-Forwarded-Host" && !strings.Contains(value, "://") {
		proto := firstNonEmptyAccountManager(r.Header.Get("X-Forwarded-Proto"), "http")
		value = proto + "://" + strings.Split(value, ",")[0]
	}
	return value
}

func refererOrigin(r *http.Request) string {
	if r == nil {
		return ""
	}
	raw := strings.TrimSpace(r.Header.Get("Referer"))
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func normalizeAccountManagerHostURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "host") {
		return ""
	}
	if !strings.Contains(raw, "://") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/")
}

func (h *HttpAPI_Plugin) handleConfigAPI(r *http.Request, body []byte) []byte {
	if r.Method == http.MethodGet {
		if err := h.ensureLoaded(); err != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		return mustAccountManagerJSON(map[string]any{
			"success": true,
			"path":    AccountManagerConfigPath,
			"config":  h.publicConfigLocked(),
		})
	}
	if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodPatch {
		var cfg accountManagerConfig
		if err := json.Unmarshal(body, &cfg); err != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid config json"})
		}
		h.mergeSensitiveConfig(&cfg)
		if err := normalizeAccountManagerConfig(&cfg); err != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error()})
		}
		if err := writeAccountManagerConfig(cfg); err != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error()})
		}
		h.mu.Lock()
		h.loaded = false
		h.mu.Unlock()
		return h.loadResponse()
	}
	return accountManagerMethodNotAllowedResponse()
}

func (h *HttpAPI_Plugin) handleAccountsAPI(r *http.Request, path []string, body []byte) []byte {
	if err := h.ensureLoaded(); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	if len(path) == 0 {
		switch r.Method {
		case http.MethodGet:
			return mustAccountManagerJSON(map[string]any{"success": true, "accounts": h.listAccounts(r)})
		case http.MethodPost:
			account, err := h.createAccount(body)
			if err != nil {
				return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error()})
			}
			return mustAccountManagerJSON(map[string]any{"success": true, "account": account})
		default:
			return accountManagerMethodNotAllowedResponse()
		}
	}

	id := strings.TrimSpace(path[0])
	if id == "" {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "account id is required"})
	}
	if len(path) > 1 {
		switch strings.ToLower(path[1]) {
		case "password":
			return h.handleAccountPasswordAPI(r, id, body)
		case "permissions":
			return h.handleAccountPermissionsAPI(r, id, body)
		default:
			return mustAccountManagerJSON(map[string]any{"success": false, "error": "unknown account endpoint"})
		}
	}

	switch r.Method {
	case http.MethodGet:
		account, ok := h.getAccount(id)
		if !ok {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": "account not found", "id": id})
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "account": toPublicAccount(account)})
	case http.MethodPut, http.MethodPatch:
		account, err := h.updateAccount(id, body)
		if err != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "id": id})
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "account": account})
	case http.MethodDelete:
		if err := h.deleteAccount(id); err != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "id": id})
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "id": id})
	default:
		return accountManagerMethodNotAllowedResponse()
	}
}

func (h *HttpAPI_Plugin) handleAccountPasswordAPI(r *http.Request, id string, body []byte) []byte {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodPost {
		return accountManagerMethodNotAllowedResponse()
	}
	var req passwordUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid password json"})
	}
	account, err := h.updateAccountPassword(id, req.Password)
	if err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "id": id})
	}
	return mustAccountManagerJSON(map[string]any{"success": true, "account": account})
}

func (h *HttpAPI_Plugin) handleAccountPermissionsAPI(r *http.Request, id string, body []byte) []byte {
	if r.Method == http.MethodGet {
		account, ok := h.getAccount(id)
		if !ok {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": "account not found", "id": id})
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "id": id, "permissions": account.Permissions})
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodPost {
		return accountManagerMethodNotAllowedResponse()
	}
	var wrapper struct {
		Permissions []pluginPermission `json:"permissions"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		var permissions []pluginPermission
		if arrayErr := json.Unmarshal(body, &permissions); arrayErr != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid permissions json"})
		}
		wrapper.Permissions = permissions
	}
	account, err := h.updateAccountPermissions(id, wrapper.Permissions)
	if err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "id": id})
	}
	return mustAccountManagerJSON(map[string]any{"success": true, "account": account, "permissions": account.Permissions})
}

func (h *HttpAPI_Plugin) handleGroupsAPI(r *http.Request, path []string, body []byte) []byte {
	if err := h.ensureLoaded(); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	if len(path) == 0 {
		switch r.Method {
		case http.MethodGet:
			return mustAccountManagerJSON(map[string]any{"success": true, "groups": h.listGroups(r)})
		case http.MethodPost:
			group, err := h.createGroup(body)
			if err != nil {
				return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error()})
			}
			return mustAccountManagerJSON(map[string]any{"success": true, "group": group})
		default:
			return accountManagerMethodNotAllowedResponse()
		}
	}

	id := strings.TrimSpace(path[0])
	if id == "" {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "group id is required"})
	}
	if len(path) > 1 {
		if strings.EqualFold(path[1], "permissions") {
			return h.handleGroupPermissionsAPI(r, id, body)
		}
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "unknown group endpoint"})
	}

	switch r.Method {
	case http.MethodGet:
		group, ok := h.getGroup(id)
		if !ok {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": "group not found", "id": id})
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "group": group})
	case http.MethodPut, http.MethodPatch:
		group, err := h.updateGroup(id, body)
		if err != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "id": id})
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "group": group})
	case http.MethodDelete:
		if err := h.deleteGroup(id); err != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "id": id})
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "id": id})
	default:
		return accountManagerMethodNotAllowedResponse()
	}
}

func (h *HttpAPI_Plugin) handleGroupPermissionsAPI(r *http.Request, id string, body []byte) []byte {
	if r.Method == http.MethodGet {
		group, ok := h.getGroup(id)
		if !ok {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": "group not found", "id": id})
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "id": id, "permissions": group.Permissions})
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodPost {
		return accountManagerMethodNotAllowedResponse()
	}
	var wrapper struct {
		Permissions []pluginPermission `json:"permissions"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		var permissions []pluginPermission
		if arrayErr := json.Unmarshal(body, &permissions); arrayErr != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid permissions json"})
		}
		wrapper.Permissions = permissions
	}
	group, err := h.updateGroupPermissions(id, wrapper.Permissions)
	if err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "id": id})
	}
	return mustAccountManagerJSON(map[string]any{"success": true, "group": group, "permissions": group.Permissions})
}

func (h *HttpAPI_Plugin) handleAuthAPI(r *http.Request, path []string, body []byte) []byte {
	if len(path) == 0 || strings.EqualFold(path[0], "verify") {
		if r.Method != http.MethodPost {
			return accountManagerMethodNotAllowedResponse()
		}
		return h.verifyCredentials(body)
	}
	return mustAccountManagerJSON(map[string]any{"success": false, "error": "unknown auth endpoint"})
}

func (h *HttpAPI_Plugin) handlePluginsAPI(r *http.Request, path []string, body []byte) []byte {
	if len(path) == 0 || !strings.EqualFold(path[0], "permissions") {
		return h.handleCatalog()
	}
	if err := h.ensureLoaded(); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	if r.Method == http.MethodGet {
		accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
		if accountID != "" {
			account, ok := h.getAccount(accountID)
			if !ok {
				return mustAccountManagerJSON(map[string]any{"success": false, "error": "account not found", "id": accountID})
			}
			return mustAccountManagerJSON(map[string]any{"success": true, "account": toPublicAccount(account), "permissions": h.effectivePermissions(account)})
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "accounts": h.listAccounts(r)})
	}
	if r.Method == http.MethodPost {
		var req verifyRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid permission verify json"})
		}
		account, ok := h.findAccountByUsername(req.Username)
		if !ok {
			return mustAccountManagerJSON(map[string]any{"success": true, "allowed": false, "reason": "account not found"})
		}
		allowed, matched := h.accountAllowsPlugin(account, req.PluginID, req.Scope)
		return mustAccountManagerJSON(map[string]any{
			"success":    true,
			"allowed":    allowed,
			"account":    toPublicAccount(account),
			"permission": matched,
		})
	}
	return accountManagerMethodNotAllowedResponse()
}

func (h *HttpAPI_Plugin) loadResponse() []byte {
	if err := h.loadConfig(true); err != nil {
		h.mu.Lock()
		h.lastLoadErr = err.Error()
		h.mu.Unlock()
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	return mustAccountManagerJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
}

func (h *HttpAPI_Plugin) ensureLoaded() error {
	return h.loadConfig(false)
}

func (h *HttpAPI_Plugin) loadConfig(force bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	stat, statErr := os.Stat(AccountManagerConfigPath)
	if !force && h.loaded && h.lastLoadErr == "" && statErr == nil && !stat.ModTime().After(h.lastModified) {
		return nil
	}
	cfg, modified, err := readAccountManagerConfig(AccountManagerConfigPath)
	if err != nil {
		h.loaded = false
		h.lastLoadErr = err.Error()
		return err
	}
	h.config = cfg
	h.accounts = map[string]managedAccount{}
	for _, account := range cfg.Accounts {
		h.accounts[account.ID] = account
	}
	h.groups = map[string]accountGroup{}
	for _, group := range cfg.Groups {
		h.groups[group.ID] = group
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
		"id":            "account-manager",
		"name":          "帳號管理",
		"version":       AccountManagerVersion,
		"loaded":        h.loaded,
		"loaded_at":     optionalAccountManagerRFC3339(h.loadedAt),
		"last_error":    h.lastLoadErr,
		"config_path":   AccountManagerConfigPath,
		"last_modified": optionalAccountManagerRFC3339(h.lastModified),
		"account_count": len(h.accounts),
		"group_count":   len(h.groups),
		"host_auth":     accountManagerHostAuthStatus(h.hostAuth),
	}
}

func accountManagerHostAuthStatus(auth accountManagerHostAuth) map[string]any {
	return map[string]any{
		"available":            strings.TrimSpace(auth.Token) != "",
		"account":              auth.Account,
		"project":              auth.Project,
		"source":               auth.Source,
		"host_url":             auth.HostURL,
		"updated_at":           optionalAccountManagerRFC3339(auth.UpdatedAt),
		"expires_at":           optionalAccountManagerRFC3339(auth.ExpiresAt),
		"last_register_ok":     auth.LastRegisterOK,
		"last_register_error":  auth.LastRegisterError,
		"last_register_at":     optionalAccountManagerRFC3339(auth.LastRegisterAt),
		"last_register_target": auth.LastRegisterTarget,
	}
}

func (h *HttpAPI_Plugin) registrationPayload() map[string]any {
	return map[string]any{
		"id":              "account-manager",
		"name":            "帳號管理",
		"version":         AccountManagerVersion,
		"type":            "service",
		"auto_start":      true,
		"service":         "account-manager-service",
		"service_url":     "http://127.0.0.1:18186",
		"api_base":        "/api/plugin/account-manager",
		"plugin_api_base": "/api/account-manager",
		"routes":          []string{"/api/account-manager", "/api/account"},
		"mcp_url":         "http://127.0.0.1:18186/mcp",
		"website_path":    "./website/account-manager/index.html",
		"runtime": map[string]any{
			"auth":         "/api/account-manager/plugin/auth",
			"load":         "/api/account-manager/plugin/load",
			"unload":       "/api/account-manager/plugin/unload",
			"registration": "/api/account-manager/plugin/registration",
		},
		"ui": map[string]any{
			"enabled":      true,
			"order":        60,
			"website_path": "./website/account-manager/index.html",
			"href":         "/account-manager/index.html",
			"code":         "ACCT",
			"class":        "account-manager",
			"title":        "帳號管理",
			"description":  "帳號、密碼驗證與跨 Plugin 存取權限管理。",
			"action":       "進入帳號管理",
			"icon":         "fa-solid fa-user-shield",
		},
		"invoke":       "CallPlugin",
		"capabilities": []string{"lifecycle", "registration", "mcp", "config", "account-crud", "group-crud", "password-aes", "credential-verify", "plugin-permission"},
	}
}

func (h *HttpAPI_Plugin) publicConfigLocked() map[string]any {
	accounts := make([]publicAccount, 0, len(h.config.Accounts))
	for _, account := range h.config.Accounts {
		accounts = append(accounts, toPublicAccount(account))
	}
	return map[string]any{
		"version":         h.config.Version,
		"encryption":      map[string]any{"key_set": strings.TrimSpace(h.config.Encryption.Key) != ""},
		"password_policy": h.config.PasswordPolicy,
		"accounts":        accounts,
		"groups":          h.config.Groups,
	}
}

func (h *HttpAPI_Plugin) mergeSensitiveConfig(cfg *accountManagerConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.TrimSpace(cfg.Encryption.Key) == "" {
		cfg.Encryption.Key = h.config.Encryption.Key
	}
	if cfg.PasswordPolicy.MinLength <= 0 {
		cfg.PasswordPolicy.MinLength = h.config.PasswordPolicy.MinLength
	}
	existing := map[string]managedAccount{}
	for _, account := range h.config.Accounts {
		existing[strings.ToLower(account.ID)] = account
		existing[strings.ToLower(account.Username)] = account
	}
	for index := range cfg.Accounts {
		account := &cfg.Accounts[index]
		old, ok := existing[strings.ToLower(firstNonEmptyAccountManager(account.ID, account.Username))]
		if !ok {
			continue
		}
		if account.PasswordAES == "" {
			account.PasswordAES = old.PasswordAES
		}
		if account.PasswordUpdatedAt == "" {
			account.PasswordUpdatedAt = old.PasswordUpdatedAt
		}
		if len(account.GroupIDs) == 0 {
			account.GroupIDs = old.GroupIDs
		}
		if account.CreatedAt == "" {
			account.CreatedAt = old.CreatedAt
		}
		if account.LastLoginAt == "" {
			account.LastLoginAt = old.LastLoginAt
		}
	}
}

func (h *HttpAPI_Plugin) listAccounts(r *http.Request) []publicAccount {
	query := ""
	if r != nil {
		query = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	accounts := make([]publicAccount, 0, len(h.accounts))
	for _, account := range h.accounts {
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{account.ID, account.Username, account.DisplayName, account.Email, account.Role}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		accounts = append(accounts, toPublicAccount(account))
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		return strings.ToLower(accounts[i].Username) < strings.ToLower(accounts[j].Username)
	})
	return accounts
}

func (h *HttpAPI_Plugin) getAccount(id string) (managedAccount, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	account, ok := h.findAccountLocked(id)
	return account, ok
}

func (h *HttpAPI_Plugin) createAccount(body []byte) (publicAccount, error) {
	var req accountRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return publicAccount{}, errors.New("invalid account json")
	}
	account, err := h.accountFromRequest(req, managedAccount{})
	if err != nil {
		return publicAccount{}, err
	}
	now := time.Now().Format(time.RFC3339)
	account.CreatedAt = now
	account.UpdatedAt = now
	if account.PasswordAES == "" && strings.TrimSpace(req.Password) != "" {
		passwordAES, err := encryptPassword(req.Password, h.encryptionKey())
		if err != nil {
			return publicAccount{}, err
		}
		account.PasswordAES = passwordAES
		account.PasswordUpdatedAt = now
	}
	h.mu.Lock()
	if _, ok := h.accounts[account.ID]; ok {
		h.mu.Unlock()
		return publicAccount{}, fmt.Errorf("account already exists: %s", account.ID)
	}
	for _, existing := range h.accounts {
		if strings.EqualFold(existing.Username, account.Username) {
			h.mu.Unlock()
			return publicAccount{}, fmt.Errorf("username already exists: %s", account.Username)
		}
	}
	h.accounts[account.ID] = account
	h.config.Accounts = accountsMapToSlice(h.accounts)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return publicAccount{}, err
	}
	_ = h.loadConfig(true)
	return toPublicAccount(account), nil
}

func (h *HttpAPI_Plugin) updateAccount(id string, body []byte) (publicAccount, error) {
	var req accountRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return publicAccount{}, errors.New("invalid account json")
	}
	h.mu.Lock()
	existing, ok := h.findAccountLocked(id)
	if !ok {
		h.mu.Unlock()
		return publicAccount{}, errors.New("account not found")
	}
	h.mu.Unlock()
	account, err := h.accountFromRequest(req, existing)
	if err != nil {
		return publicAccount{}, err
	}
	account.ID = existing.ID
	account.Username = firstNonEmptyAccountManager(req.Username, existing.Username)
	account.CreatedAt = existing.CreatedAt
	account.PasswordAES = existing.PasswordAES
	account.PasswordUpdatedAt = existing.PasswordUpdatedAt
	account.LastLoginAt = existing.LastLoginAt
	account.UpdatedAt = time.Now().Format(time.RFC3339)
	if strings.TrimSpace(req.Password) != "" {
		passwordAES, err := encryptPassword(req.Password, h.encryptionKey())
		if err != nil {
			return publicAccount{}, err
		}
		account.PasswordAES = passwordAES
		account.PasswordUpdatedAt = account.UpdatedAt
	}

	h.mu.Lock()
	for _, candidate := range h.accounts {
		if candidate.ID != account.ID && strings.EqualFold(candidate.Username, account.Username) {
			h.mu.Unlock()
			return publicAccount{}, fmt.Errorf("username already exists: %s", account.Username)
		}
	}
	delete(h.accounts, existing.ID)
	h.accounts[account.ID] = account
	h.config.Accounts = accountsMapToSlice(h.accounts)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return publicAccount{}, err
	}
	_ = h.loadConfig(true)
	return toPublicAccount(account), nil
}

func (h *HttpAPI_Plugin) updateAccountPassword(id string, password string) (publicAccount, error) {
	password = strings.TrimSpace(password)
	if err := validatePasswordPolicy(password, h.passwordPolicy()); err != nil {
		return publicAccount{}, err
	}
	passwordAES, err := encryptPassword(password, h.encryptionKey())
	if err != nil {
		return publicAccount{}, err
	}
	now := time.Now().Format(time.RFC3339)
	h.mu.Lock()
	account, ok := h.findAccountLocked(id)
	if !ok {
		h.mu.Unlock()
		return publicAccount{}, errors.New("account not found")
	}
	account.PasswordAES = passwordAES
	account.PasswordUpdatedAt = now
	account.UpdatedAt = now
	h.accounts[account.ID] = account
	h.config.Accounts = accountsMapToSlice(h.accounts)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return publicAccount{}, err
	}
	_ = h.loadConfig(true)
	return toPublicAccount(account), nil
}

func (h *HttpAPI_Plugin) updateAccountPermissions(id string, permissions []pluginPermission) (publicAccount, error) {
	now := time.Now().Format(time.RFC3339)
	permissions = normalizePluginPermissions(permissions, now)
	h.mu.Lock()
	account, ok := h.findAccountLocked(id)
	if !ok {
		h.mu.Unlock()
		return publicAccount{}, errors.New("account not found")
	}
	account.Permissions = permissions
	account.UpdatedAt = now
	h.accounts[account.ID] = account
	h.config.Accounts = accountsMapToSlice(h.accounts)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return publicAccount{}, err
	}
	_ = h.loadConfig(true)
	return toPublicAccount(account), nil
}

func (h *HttpAPI_Plugin) deleteAccount(id string) error {
	h.mu.Lock()
	account, ok := h.findAccountLocked(id)
	if !ok {
		h.mu.Unlock()
		return errors.New("account not found")
	}
	delete(h.accounts, account.ID)
	h.config.Accounts = accountsMapToSlice(h.accounts)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return err
	}
	_ = h.loadConfig(true)
	return nil
}

func (h *HttpAPI_Plugin) listGroups(r *http.Request) []accountGroup {
	query := ""
	if r != nil {
		query = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	groups := make([]accountGroup, 0, len(h.groups))
	for _, group := range h.groups {
		if query != "" {
			haystack := strings.ToLower(strings.Join([]string{group.ID, group.Name, group.Note}, " "))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		groups = append(groups, group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name)
	})
	return groups
}

func (h *HttpAPI_Plugin) getGroup(id string) (accountGroup, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.findGroupLocked(id)
}

func (h *HttpAPI_Plugin) createGroup(body []byte) (accountGroup, error) {
	var req groupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return accountGroup{}, errors.New("invalid group json")
	}
	group, err := groupFromRequest(req, accountGroup{})
	if err != nil {
		return accountGroup{}, err
	}
	now := time.Now().Format(time.RFC3339)
	group.CreatedAt = now
	group.UpdatedAt = now
	h.mu.Lock()
	if _, ok := h.groups[group.ID]; ok {
		h.mu.Unlock()
		return accountGroup{}, fmt.Errorf("group already exists: %s", group.ID)
	}
	h.groups[group.ID] = group
	h.config.Groups = groupsMapToSlice(h.groups)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return accountGroup{}, err
	}
	_ = h.loadConfig(true)
	return group, nil
}

func (h *HttpAPI_Plugin) updateGroup(id string, body []byte) (accountGroup, error) {
	var req groupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return accountGroup{}, errors.New("invalid group json")
	}
	h.mu.Lock()
	existing, ok := h.findGroupLocked(id)
	if !ok {
		h.mu.Unlock()
		return accountGroup{}, errors.New("group not found")
	}
	h.mu.Unlock()
	group, err := groupFromRequest(req, existing)
	if err != nil {
		return accountGroup{}, err
	}
	group.ID = existing.ID
	group.CreatedAt = existing.CreatedAt
	group.UpdatedAt = time.Now().Format(time.RFC3339)
	h.mu.Lock()
	h.groups[group.ID] = group
	h.config.Groups = groupsMapToSlice(h.groups)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return accountGroup{}, err
	}
	_ = h.loadConfig(true)
	return group, nil
}

func (h *HttpAPI_Plugin) updateGroupPermissions(id string, permissions []pluginPermission) (accountGroup, error) {
	now := time.Now().Format(time.RFC3339)
	permissions = normalizePluginPermissions(permissions, now)
	h.mu.Lock()
	group, ok := h.findGroupLocked(id)
	if !ok {
		h.mu.Unlock()
		return accountGroup{}, errors.New("group not found")
	}
	group.Permissions = permissions
	group.UpdatedAt = now
	h.groups[group.ID] = group
	h.config.Groups = groupsMapToSlice(h.groups)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return accountGroup{}, err
	}
	_ = h.loadConfig(true)
	return group, nil
}

func (h *HttpAPI_Plugin) deleteGroup(id string) error {
	h.mu.Lock()
	group, ok := h.findGroupLocked(id)
	if !ok {
		h.mu.Unlock()
		return errors.New("group not found")
	}
	delete(h.groups, group.ID)
	for accountID, account := range h.accounts {
		account.GroupIDs = removeStringID(account.GroupIDs, group.ID)
		h.accounts[accountID] = account
	}
	h.config.Groups = groupsMapToSlice(h.groups)
	h.config.Accounts = accountsMapToSlice(h.accounts)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return err
	}
	_ = h.loadConfig(true)
	return nil
}

func (h *HttpAPI_Plugin) findGroupLocked(id string) (accountGroup, bool) {
	for _, group := range h.groups {
		if strings.EqualFold(group.ID, id) || strings.EqualFold(group.Name, id) {
			return group, true
		}
	}
	return accountGroup{}, false
}

func groupFromRequest(req groupRequest, fallback accountGroup) (accountGroup, error) {
	now := time.Now().Format(time.RFC3339)
	group := fallback
	group.ID = firstNonEmptyAccountManager(req.ID, fallback.ID)
	group.Name = strings.TrimSpace(firstNonEmptyAccountManager(req.Name, fallback.Name))
	if group.Name == "" {
		return group, errors.New("group name is required")
	}
	if group.ID == "" {
		group.ID = sanitizeAccountManagerID(group.Name)
	}
	if group.ID == "" {
		return group, errors.New("group id is required")
	}
	if req.Enabled != nil {
		group.Enabled = *req.Enabled
	} else if fallback.ID == "" {
		group.Enabled = true
	}
	if req.Note != nil {
		group.Note = strings.TrimSpace(*req.Note)
	}
	if req.Permissions != nil {
		group.Permissions = normalizePluginPermissions(req.Permissions, now)
	} else if group.Permissions == nil {
		group.Permissions = []pluginPermission{}
	}
	if req.Metadata != nil {
		group.Metadata = req.Metadata
	}
	return group, nil
}

func (h *HttpAPI_Plugin) accountFromRequest(req accountRequest, fallback managedAccount) (managedAccount, error) {
	now := time.Now().Format(time.RFC3339)
	account := fallback
	account.ID = firstNonEmptyAccountManager(req.ID, fallback.ID)
	account.Username = firstNonEmptyAccountManager(req.Username, fallback.Username)
	if account.Username == "" {
		return account, errors.New("username is required")
	}
	if account.ID == "" {
		account.ID = sanitizeAccountManagerID(account.Username)
	}
	if account.ID == "" {
		return account, errors.New("account id is required")
	}
	if isReservedAccountID(account.ID) || isReservedAccountID(account.Username) {
		return account, errors.New("root is reserved and cannot be used as account id")
	}
	account.DisplayName = strings.TrimSpace(firstNonEmptyAccountManager(req.DisplayName, fallback.DisplayName))
	account.Email = strings.TrimSpace(firstNonEmptyAccountManager(req.Email, fallback.Email))
	account.Role = strings.TrimSpace(firstNonEmptyAccountManager(req.Role, fallback.Role, "user"))
	if req.Note != nil {
		account.Note = strings.TrimSpace(*req.Note)
	}
	if req.GroupIDs != nil {
		account.GroupIDs = normalizeStringIDs(req.GroupIDs)
	} else {
		account.GroupIDs = normalizeStringIDs(account.GroupIDs)
	}
	if req.Enabled != nil {
		account.Enabled = *req.Enabled
	} else if fallback.ID == "" {
		account.Enabled = true
	}
	if req.Permissions != nil {
		account.Permissions = normalizePluginPermissions(req.Permissions, now)
	} else if account.Permissions == nil {
		account.Permissions = []pluginPermission{}
	}
	if req.Metadata != nil {
		account.Metadata = req.Metadata
	}
	if strings.TrimSpace(req.Password) != "" {
		if err := validatePasswordPolicy(req.Password, h.passwordPolicy()); err != nil {
			return account, err
		}
	}
	return account, nil
}

func (h *HttpAPI_Plugin) verifyCredentials(body []byte) []byte {
	if err := h.ensureLoaded(); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	var req verifyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid verify json"})
	}
	username := firstNonEmptyAccountManager(req.Account, req.Username)
	password := strings.TrimSpace(req.Password)
	project := firstNonEmptyAccountManager(req.Project, "default")
	if username == "" || password == "" {
		return mustAccountManagerJSON(verifyFailurePayload(username, project, "account and password are required"))
	}
	account, ok := h.findAccountByUsername(username)
	if !ok {
		return mustAccountManagerJSON(verifyFailurePayload(username, project, "account not found"))
	}
	policy := h.passwordPolicy()
	if policy.RequireEnabledAccount && !account.Enabled {
		return mustAccountManagerJSON(verifyFailurePayload(account.Username, project, "account disabled"))
	}
	decrypted, err := decryptPassword(account.PasswordAES, h.encryptionKey())
	if err != nil {
		return mustAccountManagerJSON(verifyFailurePayload(account.Username, project, "password decrypt failed"))
	}
	valid := subtle.ConstantTimeCompare([]byte(decrypted), []byte(password)) == 1
	if !valid {
		return mustAccountManagerJSON(verifyFailurePayload(account.Username, project, "password mismatch"))
	}
	allowed, matched := h.accountAllowsPlugin(account, req.PluginID, req.Scope)
	if req.PluginID != "" && !allowed {
		return mustAccountManagerJSON(verifyFailurePayload(account.Username, project, "permission denied"))
	}
	account.LastLoginAt = time.Now().Format(time.RFC3339)
	h.persistAccount(account)
	return mustAccountManagerJSON(map[string]any{
		"success":     true,
		"account":     account.Username,
		"project":     project,
		"roles":       accountRoleNames(account),
		"permissions": accountPermissionNames(h.effectivePermissions(account)),
		"expires_in":  86400,
		"valid":       true,
		"allowed":     allowed,
		"permission":  matched,
	})
}

func verifyFailurePayload(account string, project string, reason string) map[string]any {
	return map[string]any{
		"success":     false,
		"account":     strings.TrimSpace(account),
		"project":     firstNonEmptyAccountManager(project, "default"),
		"roles":       []string{},
		"permissions": []string{},
		"expires_in":  0,
		"error":       reason,
		"valid":       false,
	}
}

func (h *HttpAPI_Plugin) findAccountByUsername(username string) (managedAccount, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, account := range h.accounts {
		if strings.EqualFold(account.Username, username) || strings.EqualFold(account.ID, username) {
			return account, true
		}
	}
	return managedAccount{}, false
}

func accountRoleNames(account managedAccount) []string {
	roles := normalizeStringIDs([]string{account.Role})
	if len(roles) == 0 {
		return []string{"user"}
	}
	return roles
}

func accountPermissionNames(permissions []pluginPermission) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, permission := range permissions {
		if !permission.Enabled {
			continue
		}
		pluginID := strings.TrimSpace(permission.PluginID)
		if pluginID == "" {
			continue
		}
		for _, scope := range permission.Scopes {
			scope = strings.TrimSpace(strings.ToLower(scope))
			if scope == "" {
				continue
			}
			name := pluginID + "." + scope
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (h *HttpAPI_Plugin) findAccountLocked(id string) (managedAccount, bool) {
	for _, account := range h.accounts {
		if strings.EqualFold(account.ID, id) || strings.EqualFold(account.Username, id) {
			return account, true
		}
	}
	return managedAccount{}, false
}

func (h *HttpAPI_Plugin) encryptionKey() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.config.Encryption.Key
}

func (h *HttpAPI_Plugin) passwordPolicy() passwordPolicy {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.config.PasswordPolicy
}

func (h *HttpAPI_Plugin) persistAccount(account managedAccount) {
	h.mu.Lock()
	h.accounts[account.ID] = account
	h.config.Accounts = accountsMapToSlice(h.accounts)
	cfg := h.config
	h.mu.Unlock()
	_ = writeAccountManagerConfig(cfg)
	_ = h.loadConfig(true)
}

func readAccountManagerConfig(path string) (accountManagerConfig, time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && path == AccountManagerConfigPath {
			return initializeAccountManagerConfigFromDefault()
		}
		return accountManagerConfig{}, time.Time{}, err
	}
	var cfg accountManagerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return accountManagerConfig{}, time.Time{}, fmt.Errorf("invalid account manager config: %w", err)
	}
	if err := normalizeAccountManagerConfig(&cfg); err != nil {
		return accountManagerConfig{}, time.Time{}, err
	}
	modified := time.Now()
	if stat, err := os.Stat(path); err == nil {
		modified = stat.ModTime()
	}
	return cfg, modified, nil
}

func initializeAccountManagerConfigFromDefault() (accountManagerConfig, time.Time, error) {
	cfg := accountManagerConfig{Version: AccountManagerVersion}
	defaultPath := filepath.Join(filepath.Dir(AccountManagerConfigPath), "config.default.json")
	if data, err := os.ReadFile(defaultPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return accountManagerConfig{}, time.Time{}, fmt.Errorf("invalid account manager default config: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return accountManagerConfig{}, time.Time{}, err
	}
	if err := normalizeAccountManagerConfig(&cfg); err != nil {
		return accountManagerConfig{}, time.Time{}, err
	}
	if err := writeAccountManagerConfig(cfg); err != nil {
		return accountManagerConfig{}, time.Time{}, err
	}
	modified := time.Now()
	if stat, err := os.Stat(AccountManagerConfigPath); err == nil {
		modified = stat.ModTime()
	}
	return cfg, modified, nil
}

func normalizeAccountManagerConfig(cfg *accountManagerConfig) error {
	if cfg.Version == "" {
		cfg.Version = AccountManagerVersion
	}
	if strings.TrimSpace(cfg.Encryption.Key) == "" {
		cfg.Encryption.Key = "account-manager-default-aes-key-change-me"
	}
	if cfg.PasswordPolicy.MinLength <= 0 {
		cfg.PasswordPolicy.MinLength = 6
	}
	cfg.PasswordPolicy.RequireEnabledAccount = true
	now := time.Now().Format(time.RFC3339)
	seenGroupIDs := map[string]bool{}
	groups := make([]accountGroup, 0, len(cfg.Groups))
	for _, group := range cfg.Groups {
		group.ID = sanitizeAccountManagerID(firstNonEmptyAccountManager(group.ID, group.Name))
		group.Name = strings.TrimSpace(group.Name)
		if group.ID == "" || group.Name == "" {
			continue
		}
		groupKey := strings.ToLower(group.ID)
		if seenGroupIDs[groupKey] {
			continue
		}
		seenGroupIDs[groupKey] = true
		if group.CreatedAt == "" {
			group.CreatedAt = now
		}
		if group.UpdatedAt == "" {
			group.UpdatedAt = group.CreatedAt
		}
		group.Permissions = normalizePluginPermissions(group.Permissions, group.UpdatedAt)
		groups = append(groups, group)
	}
	cfg.Groups = groups

	seenIDs := map[string]bool{}
	seenUsers := map[string]bool{}
	accounts := make([]managedAccount, 0, len(cfg.Accounts))
	for _, account := range cfg.Accounts {
		account.ID = sanitizeAccountManagerID(firstNonEmptyAccountManager(account.ID, account.Username))
		account.Username = strings.TrimSpace(account.Username)
		if account.ID == "" || account.Username == "" {
			continue
		}
		idKey := strings.ToLower(account.ID)
		userKey := strings.ToLower(account.Username)
		if seenIDs[idKey] || seenUsers[userKey] {
			continue
		}
		seenIDs[idKey] = true
		seenUsers[userKey] = true
		if account.Role == "" {
			account.Role = "user"
		}
		if account.Note == "" {
			account.Note = noteFromAccountMetadata(account.Metadata)
		}
		if account.CreatedAt == "" {
			account.CreatedAt = now
		}
		if account.UpdatedAt == "" {
			account.UpdatedAt = account.CreatedAt
		}
		if account.InitialPassword != "" && account.PasswordAES == "" {
			passwordAES, err := encryptPassword(account.InitialPassword, cfg.Encryption.Key)
			if err != nil {
				return err
			}
			account.PasswordAES = passwordAES
			account.PasswordUpdatedAt = now
		}
		account.InitialPassword = ""
		account.GroupIDs = normalizeStringIDs(account.GroupIDs)
		account.Permissions = normalizePluginPermissions(account.Permissions, account.UpdatedAt)
		accounts = append(accounts, account)
	}
	cfg.Accounts = accounts
	return nil
}

func writeAccountManagerConfig(cfg accountManagerConfig) error {
	if err := normalizeAccountManagerConfig(&cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(AccountManagerConfigPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := AccountManagerConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, AccountManagerConfigPath)
}

func toPublicAccount(account managedAccount) publicAccount {
	return publicAccount{
		ID:                account.ID,
		Username:          account.Username,
		DisplayName:       account.DisplayName,
		Email:             account.Email,
		Role:              account.Role,
		Enabled:           account.Enabled,
		Note:              account.Note,
		GroupIDs:          normalizeStringIDs(account.GroupIDs),
		PasswordSet:       strings.TrimSpace(account.PasswordAES) != "",
		PasswordUpdatedAt: account.PasswordUpdatedAt,
		CreatedAt:         account.CreatedAt,
		UpdatedAt:         account.UpdatedAt,
		LastLoginAt:       account.LastLoginAt,
		Permissions:       normalizePluginPermissions(account.Permissions, account.UpdatedAt),
		Metadata:          account.Metadata,
	}
}

func normalizePluginPermissions(permissions []pluginPermission, fallbackUpdatedAt string) []pluginPermission {
	out := make([]pluginPermission, 0, len(permissions))
	seen := map[string]bool{}
	for _, permission := range permissions {
		permission.PluginID = strings.TrimSpace(permission.PluginID)
		if permission.PluginID == "" {
			continue
		}
		key := strings.ToLower(permission.PluginID)
		if seen[key] {
			continue
		}
		seen[key] = true
		permission.PluginName = strings.TrimSpace(permission.PluginName)
		permission.Scopes = normalizeScopes(permission.Scopes)
		if permission.Scopes == nil {
			permission.Scopes = []string{}
		}
		if permission.UpdatedAt == "" {
			permission.UpdatedAt = fallbackUpdatedAt
		}
		out = append(out, permission)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].PluginID) < strings.ToLower(out[j].PluginID)
	})
	return out
}

func normalizeScopes(scopes []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(strings.ToLower(scope))
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
	}
	return out
}

func (h *HttpAPI_Plugin) accountAllowsPlugin(account managedAccount, pluginID string, scope string) (bool, pluginPermission) {
	return permissionsAllowPlugin(h.effectivePermissions(account), pluginID, scope)
}

func (h *HttpAPI_Plugin) effectivePermissions(account managedAccount) []pluginPermission {
	permissions := append([]pluginPermission{}, account.Permissions...)
	h.mu.Lock()
	for _, groupID := range account.GroupIDs {
		group, ok := h.findGroupLocked(groupID)
		if !ok || !group.Enabled {
			continue
		}
		permissions = append(permissions, group.Permissions...)
	}
	h.mu.Unlock()
	return normalizePluginPermissions(permissions, time.Now().Format(time.RFC3339))
}

func permissionsAllowPlugin(permissions []pluginPermission, pluginID string, scope string) (bool, pluginPermission) {
	pluginID = strings.TrimSpace(pluginID)
	scope = strings.TrimSpace(strings.ToLower(scope))
	if pluginID == "" {
		return true, pluginPermission{}
	}
	for _, permission := range permissions {
		if !permission.Enabled {
			continue
		}
		if permission.PluginID != "*" && !strings.EqualFold(permission.PluginID, pluginID) {
			continue
		}
		if scope == "" || scopeAllowed(permission.Scopes, scope) {
			return true, permission
		}
		return false, permission
	}
	return false, pluginPermission{}
}

func scopeAllowed(scopes []string, required string) bool {
	required = strings.TrimSpace(strings.ToLower(required))
	if required == "" {
		return true
	}
	for _, scope := range scopes {
		scope = strings.TrimSpace(strings.ToLower(scope))
		if scope == "*" || scope == required || scope == "admin" {
			return true
		}
		if required == "read" && (scope == "write" || scope == "manage" || scope == "delete") {
			return true
		}
		if required == "write" && (scope == "manage" || scope == "delete") {
			return true
		}
	}
	return false
}

func accountsMapToSlice(accounts map[string]managedAccount) []managedAccount {
	out := make([]managedAccount, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, account)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Username) < strings.ToLower(out[j].Username)
	})
	return out
}

func groupsMapToSlice(groups map[string]accountGroup) []accountGroup {
	out := make([]accountGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, group)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func normalizeStringIDs(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		id := sanitizeAccountManagerID(value)
		if id == "" || seen[strings.ToLower(id)] {
			continue
		}
		seen[strings.ToLower(id)] = true
		out = append(out, id)
	}
	return out
}

func removeStringID(values []string, target string) []string {
	target = strings.ToLower(sanitizeAccountManagerID(target))
	out := make([]string, 0, len(values))
	for _, value := range normalizeStringIDs(values) {
		if strings.ToLower(value) != target {
			out = append(out, value)
		}
	}
	return out
}

func isReservedAccountID(input string) bool {
	return strings.EqualFold(strings.TrimSpace(input), "root")
}

func validatePasswordPolicy(password string, policy passwordPolicy) error {
	if len([]rune(password)) < policy.MinLength {
		return fmt.Errorf("password must be at least %d characters", policy.MinLength)
	}
	return nil
}

func encryptPassword(password string, keyText string) (string, error) {
	block, err := aes.NewCipher(deriveAESKey(keyText))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := crand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(password), nil)
	payload := append(nonce, sealed...)
	return "aes-gcm:v1:" + base64.StdEncoding.EncodeToString(payload), nil
}

func decryptPassword(encoded string, keyText string) (string, error) {
	encoded = strings.TrimSpace(encoded)
	if !strings.HasPrefix(encoded, "aes-gcm:v1:") {
		return "", errors.New("unsupported password format")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, "aes-gcm:v1:"))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(deriveAESKey(keyText))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid password payload")
	}
	nonce := raw[:gcm.NonceSize()]
	ciphertext := raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func deriveAESKey(keyText string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(keyText)))
	return sum[:]
}

func noteFromAccountMetadata(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	for _, key := range []string{"note", "source", "description"} {
		if value, ok := metadata[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func sanitizeAccountManagerID(input string) string {
	value := strings.TrimSpace(strings.ToLower(input))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-_.")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

func firstNonEmptyAccountManager(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func optionalAccountManagerRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
