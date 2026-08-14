package accountmanager

import (
	"bufio"
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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var AccountManagerConfigPath = "plugins/account-manager/config.json"
var AccountManagerVersion = "0.1.0"

const (
	accountManagerVerifyRateLimitMaxAttempts = 5
	accountManagerVerifyRateLimitWindow      = 5 * time.Minute
	accountManagerVerifyRateLimitCooldown    = 15 * time.Minute
	accountManagerInvalidCredentialsError    = "invalid credentials"
	accountManagerDefaultWorkspaceID         = "default"
)

func recoverAccountManagerPanic(context string) any {
	recovered := recover()
	if recovered == nil {
		return nil
	}
	context = strings.TrimSpace(context)
	if context == "" {
		context = "unknown"
	}
	_, _ = fmt.Fprintf(os.Stderr, "[account-manager] recovered panic in %s: %v\n%s\n", context, recovered, debug.Stack())
	return recovered
}

func accountManagerRecoveredJSON(context string) []byte {
	return mustAccountManagerJSON(map[string]any{
		"success": false,
		"error":   "internal account manager error",
		"code":    "PANIC_RECOVERED",
		"context": strings.TrimSpace(context),
	})
}

func accountManagerRecoveredError(context string) error {
	context = strings.TrimSpace(context)
	if context == "" {
		return errors.New("internal account manager error")
	}
	return fmt.Errorf("internal account manager error: %s", context)
}

var accountManagerStdioAuth = struct {
	sync.Mutex
	writeMu sync.Mutex
	enabled bool
	writer  io.Writer
	pending map[string]chan accountManagerStdioAuthResponse
	nextID  uint64
}{pending: map[string]chan accountManagerStdioAuthResponse{}}

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
	pendingStop  bool
	verifyLimits map[string]accountManagerVerifyRateLimit
	Shutdown     func()
}

type accountManagerConfig struct {
	Version        string           `json:"version"`
	Encryption     encryptionConfig `json:"encryption"`
	PasswordPolicy passwordPolicy   `json:"password_policy"`
	MCP            mcpConfig        `json:"mcp"`
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

type mcpConfig struct {
	Read   bool `json:"read"`
	Write  bool `json:"write"`
	Delete bool `json:"delete"`
}

type mcpConfigPatch struct {
	Read   *bool `json:"read"`
	Write  *bool `json:"write"`
	Delete *bool `json:"delete"`
}

type accountManagerSettingsRequest struct {
	MCP *mcpConfigPatch `json:"mcp"`
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
	APIKeys           []accountAPIKey    `json:"api_keys,omitempty"`
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
	APIKeys           []publicAPIKey     `json:"api_keys,omitempty"`
	Metadata          map[string]any     `json:"metadata,omitempty"`
}

type accountAPIKey struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	KeyHash    string `json:"key_hash"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"created_at,omitempty"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

type publicAPIKey struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"created_at,omitempty"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

type pluginPermission struct {
	PluginID   string         `json:"plugin_id"`
	PluginName string         `json:"plugin_name,omitempty"`
	Enabled    bool           `json:"enabled"`
	Scopes     []string       `json:"scopes"`
	Note       string         `json:"note,omitempty"`
	Settings   map[string]any `json:"settings,omitempty"`
	UpdatedAt  string         `json:"updated_at,omitempty"`
}

type accountGroup struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Enabled      bool               `json:"enabled"`
	Note         string             `json:"note,omitempty"`
	WorkspaceIDs []string           `json:"workspace_ids"`
	Permissions  []pluginPermission `json:"permissions"`
	CreatedAt    string             `json:"created_at,omitempty"`
	UpdatedAt    string             `json:"updated_at,omitempty"`
	Metadata     map[string]any     `json:"metadata,omitempty"`
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
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Enabled      *bool              `json:"enabled"`
	Note         *string            `json:"note"`
	WorkspaceIDs []string           `json:"workspace_ids"`
	Permissions  []pluginPermission `json:"permissions"`
	Metadata     map[string]any     `json:"metadata"`
}

type passwordUpdateRequest struct {
	Password string `json:"password"`
}

type passwordSelfUpdateRequest struct {
	CurrentPassword string `json:"current_password"`
	OldPassword     string `json:"old_password"`
	Password        string `json:"password"`
	NewPassword     string `json:"new_password"`
}

type apiKeyIssueRequest struct {
	Name string `json:"name"`
}

type verifyRequest struct {
	Account  string `json:"account"`
	Username string `json:"username"`
	Password string `json:"password"`
	APIKey   string `json:"api_key"`
	Key      string `json:"key"`
	Project  string `json:"project"`
	PluginID string `json:"plugin_id"`
	Scope    string `json:"scope"`
}

type accountManagerVerifyRateLimit struct {
	Failures     int
	ResetAt      time.Time
	BlockedUntil time.Time
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

type accountManagerStdioAuthResponse struct {
	ID     string                    `json:"id"`
	Result accountManagerStdioResult `json:"result"`
	Error  any                       `json:"error,omitempty"`
}

type accountManagerAuthIntrospection struct {
	Success       bool     `json:"success"`
	Authenticated bool     `json:"authenticated"`
	Account       string   `json:"account"`
	Project       string   `json:"project"`
	DisplayName   string   `json:"display_name"`
	Roles         []string `json:"roles"`
	Permissions   []string `json:"permissions"`
	Source        string   `json:"source"`
	ExpiresAt     string   `json:"expires_at"`
	TokenType     string   `json:"token_type"`
}

type accountManagerStdioResult struct {
	accountManagerAuthIntrospection
	Token        string `json:"token"`
	SegmentCount int    `json:"segment_count"`
}

func StartStdioAuthClient(input io.Reader, output io.Writer) {
	defer recoverAccountManagerPanic("StartStdioAuthClient")
	if input == nil || output == nil {
		return
	}
	accountManagerStdioAuth.Lock()
	if accountManagerStdioAuth.enabled {
		accountManagerStdioAuth.Unlock()
		return
	}
	accountManagerStdioAuth.enabled = true
	accountManagerStdioAuth.writer = output
	if accountManagerStdioAuth.pending == nil {
		accountManagerStdioAuth.pending = map[string]chan accountManagerStdioAuthResponse{}
	}
	accountManagerStdioAuth.Unlock()

	go readStdioAuthResponses(input)
}

func readStdioAuthResponses(input io.Reader) {
	defer recoverAccountManagerPanic("readStdioAuthResponses")
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var resp accountManagerStdioAuthResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil || strings.TrimSpace(resp.ID) == "" {
			continue
		}
		accountManagerStdioAuth.Lock()
		ch := accountManagerStdioAuth.pending[resp.ID]
		if ch != nil {
			delete(accountManagerStdioAuth.pending, resp.ID)
		}
		accountManagerStdioAuth.Unlock()
		if ch != nil {
			ch <- resp
			close(ch)
		}
	}
	accountManagerStdioAuth.Lock()
	accountManagerStdioAuth.enabled = false
	for id, ch := range accountManagerStdioAuth.pending {
		delete(accountManagerStdioAuth.pending, id)
		close(ch)
	}
	accountManagerStdioAuth.Unlock()
}

func accountManagerStdioAuthEnabled() bool {
	accountManagerStdioAuth.Lock()
	defer accountManagerStdioAuth.Unlock()
	return accountManagerStdioAuth.enabled && accountManagerStdioAuth.writer != nil
}

func (h *HttpAPI_Plugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recoverAccountManagerPanic("ServeHTTP") != nil {
			writeAccountManagerJSONBytes(w, accountManagerRecoveredJSON("ServeHTTP"))
		}
	}()
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
	if h.consumePendingServiceShutdown() {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		h.requestServiceShutdown()
	}
}

func applyAccountManagerCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Authentication, X-API-Key, Accept")
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

func accountManagerUnauthorizedResponse(w http.ResponseWriter) []byte {
	if w != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
	}
	return mustAccountManagerJSON(map[string]any{"success": false, "error": "unauthorized", "code": "AUTH_REQUIRED"})
}

func accountManagerRequiresAuth(path []string) bool {
	if len(path) == 0 {
		return true
	}
	switch strings.ToLower(path[0]) {
	case "auth":
		return true
	case "account":
		return true
	case "plugins":
		return true
	case "plugin":
		return !accountManagerIsPluginStatusPath(path) && !accountManagerIsPluginAuthPath(path)
	default:
		return true
	}
}

func accountManagerIsVerifyPath(path []string) bool {
	if len(path) == 0 {
		return false
	}
	if strings.EqualFold(path[0], "auth") {
		return len(path) == 1 || strings.EqualFold(path[1], "verify")
	}
	return len(path) > 1 && strings.EqualFold(path[0], "account") && strings.EqualFold(path[1], "verify")
}

func accountManagerIsPluginStatusPath(path []string) bool {
	return len(path) > 0 && strings.EqualFold(path[0], "plugin") && (len(path) == 1 || strings.EqualFold(path[len(path)-1], "status"))
}

func accountManagerIsPluginAuthPath(path []string) bool {
	return len(path) > 1 && strings.EqualFold(path[0], "plugin") && strings.EqualFold(path[1], "auth")
}

func (h *HttpAPI_Plugin) accountManagerHasRequestAuth(r *http.Request) bool {
	session, ok := h.accountManagerRequestIntrospection(r)
	return ok && session.Success && session.Authenticated
}

func (h *HttpAPI_Plugin) accountManagerRequestIntrospection(r *http.Request) (accountManagerAuthIntrospection, bool) {
	if r == nil {
		return accountManagerAuthIntrospection{}, false
	}
	token := accountManagerRequestAuthToken(r)
	if token == "" {
		return accountManagerAuthIntrospection{}, false
	}
	h.mu.Lock()
	hostURL := h.hostAuth.HostURL
	h.mu.Unlock()
	session, ok := accountManagerIntrospectTokenWithHost(hostURL, token)
	if !ok || !session.Success || !session.Authenticated {
		return accountManagerAuthIntrospection{}, false
	}
	return session, true
}

func (h *HttpAPI_Plugin) accountManagerVerifyAuthBypassAllowed(path []string, r *http.Request) bool {
	if !accountManagerIsVerifyPath(path) {
		return false
	}
	return h.accountManagerRequestFromTrustedHost(r)
}

func (h *HttpAPI_Plugin) accountManagerRequestFromTrustedHost(r *http.Request) bool {
	host := accountManagerRemoteHost(r)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() {
			return true
		}
		if accountManagerIPBelongsToLocalInterface(ip) {
			return true
		}
	}
	h.mu.Lock()
	hostAuthURL := h.hostAuth.HostURL
	h.mu.Unlock()
	trustedHost := accountManagerHostFromURL(hostAuthURL)
	if trustedHost == "" {
		return false
	}
	if strings.EqualFold(host, trustedHost) {
		return true
	}
	trustedIP := net.ParseIP(trustedHost)
	return ip != nil && trustedIP != nil && ip.Equal(trustedIP)
}

func accountManagerRemoteHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(strings.TrimSpace(r.RemoteAddr), "[]")
}

func accountManagerIPBelongsToLocalInterface(ip net.IP) bool {
	if ip == nil {
		return false
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		var candidate net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			candidate = value.IP
		case *net.IPAddr:
			candidate = value.IP
		}
		if candidate != nil && candidate.Equal(ip) {
			return true
		}
	}
	return false
}

func accountManagerHostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(parsed.Host)
	if host == "" {
		return ""
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	return strings.Trim(host, "[]")
}

func accountManagerRequestAuthToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if token := bearerTokenFromHeader(r.Header.Get("Authentication")); token != "" {
		return token
	}
	if token := bearerTokenFromHeader(r.Header.Get("Authorization")); token != "" {
		return token
	}
	for _, cookie := range r.Cookies() {
		name := strings.ToLower(strings.TrimSpace(cookie.Name))
		value := strings.TrimSpace(cookie.Value)
		if value == "" {
			continue
		}
		switch name {
		case "agentic_auth_token", "auth_token", "authtoken", "token", "authentication", "authorization":
			return value
		}
	}
	return ""
}

func (h *HttpAPI_Plugin) Process(w http.ResponseWriter, r *http.Request, path []string, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("Process") != nil {
			response = accountManagerRecoveredJSON("Process")
		}
	}()
	normalizedPath := normalizeAccountManagerAPIPath(path)
	if len(normalizedPath) == 0 {
		if !h.accountManagerHasRequestAuth(r) {
			return accountManagerUnauthorizedResponse(w)
		}
		return h.handleCatalog()
	}
	if accountManagerRequiresAuth(normalizedPath) &&
		!h.accountManagerVerifyAuthBypassAllowed(normalizedPath, r) &&
		!h.accountManagerHasRequestAuth(r) {
		return accountManagerUnauthorizedResponse(w)
	}

	switch normalizedPath[0] {
	case "plugin":
		return h.handlePluginAPI(w, r, normalizedPath[1:])
	case "account":
		return h.handleAccountAliasAPI(w, r, normalizedPath[1:], body)
	case "mcp":
		return h.handleMCPAPI(r)
	case "config":
		return h.handleConfigAPI(r, body)
	case "settings":
		return h.handleSettingsAPI(r, body)
	case "accounts":
		return h.handleAccountsAPI(r, normalizedPath[1:], body)
	case "groups":
		return h.handleGroupsAPI(r, normalizedPath[1:], body)
	case "auth":
		return h.handleAuthAPI(w, r, normalizedPath[1:], body)
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
			{"path": "/api/account-manager/settings", "method": "GET|PUT", "description": "讀寫 Account Manager 功能設定"},
			{"path": "/api/account-manager/accounts", "method": "GET|POST", "description": "列出或建立帳號"},
			{"path": "/api/account-manager/accounts/{id}", "method": "GET|PUT|DELETE", "description": "讀取、修改或刪除帳號"},
			{"path": "/api/account-manager/accounts/{id}/password", "method": "PUT", "description": "更新帳號密碼並以 AES 儲存"},
			{"path": "/api/account-manager/auth/password", "method": "PUT", "description": "目前登入的 AccountManager 帳號變更自己的密碼"},
			{"path": "/api/account-manager/accounts/{id}/api-keys", "method": "GET|POST", "description": "列出或核發帳號遠端 API 金鑰"},
			{"path": "/api/account-manager/accounts/{id}/api-keys/{key_id}", "method": "DELETE", "description": "刪除帳號遠端 API 金鑰"},
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

func (h *HttpAPI_Plugin) handlePluginAPI(w http.ResponseWriter, r *http.Request, path []string) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("handlePluginAPI") != nil {
			response = accountManagerRecoveredJSON("handlePluginAPI")
		}
	}()
	cmd := lastAccountManagerPathSegment(path, "status")
	switch cmd {
	case "status":
		if r.Method != http.MethodGet {
			return accountManagerMethodNotAllowedResponse()
		}
		if !h.accountManagerHasRequestAuth(r) {
			return mustAccountManagerJSON(map[string]any{"success": true})
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
		return h.authResponse(w, r)
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
		h.pendingStop = true
		h.mu.Unlock()
		return mustAccountManagerJSON(map[string]any{"success": true, "plugin": h.statusPayload()})
	case "registration":
		if r.Method != http.MethodGet {
			return accountManagerMethodNotAllowedResponse()
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "plugin": h.registrationPayload(r)})
	default:
		return h.handleCatalog()
	}
}

func (h *HttpAPI_Plugin) handleAccountAliasAPI(w http.ResponseWriter, r *http.Request, path []string, body []byte) []byte {
	if len(path) == 0 {
		return h.handleCatalog()
	}
	switch strings.ToLower(path[0]) {
	case "verify":
		return h.handleAuthAPI(w, r, []string{"verify"}, body)
	case "permissions":
		return h.handlePluginsAPI(r, []string{"permissions"}, body)
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

func (h *HttpAPI_Plugin) handleMCPAPI(r *http.Request) []byte {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return accountManagerMethodNotAllowedResponse()
	}
	if err := h.ensureLoaded(); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error()})
	}
	h.mu.Lock()
	settings := h.config.MCP
	h.mu.Unlock()
	return mustAccountManagerJSON(map[string]any{
		"success": true,
		"mcp": map[string]any{
			"name":            "account-manager",
			"description":     "帳號、AES 密碼與跨 Plugin 權限管理工具。",
			"version":         AccountManagerVersion,
			"plugin_api_base": "/api/account-manager",
			"enabled":         settings.Read || settings.Write || settings.Delete,
			"access":          accountManagerMCPSettingsPayload(settings),
			"tools":           accountManagerMCPTools(settings),
		},
	})
}

func accountManagerMCPTools(settings mcpConfig) []map[string]any {
	tools := make([]map[string]any, 0, 14)
	appendTool := func(access string, name string, method string, path string, description string) {
		tools = append(tools, map[string]any{
			"access": access, "name": name, "method": method, "path": path, "description": description,
		})
	}
	if settings.Read {
		appendTool("read", "account_manager.accounts.list", "GET", "/api/plugin/account-manager/api/account-manager/accounts", "列出帳號與權限摘要。")
		appendTool("read", "account_manager.accounts.get", "GET", "/api/plugin/account-manager/api/account-manager/accounts/{id}", "讀取指定帳號與權限摘要。")
		appendTool("read", "account_manager.groups.list", "GET", "/api/plugin/account-manager/api/account-manager/groups", "列出群組與卡片權限摘要。")
		appendTool("read", "account_manager.groups.get", "GET", "/api/plugin/account-manager/api/account-manager/groups/{id}", "讀取指定群組。")
		appendTool("read", "account_manager.permissions.get", "GET", "/api/plugin/account-manager/api/account-manager/groups/{id}/permissions", "讀取指定群組的卡片權限。")
		appendTool("read", "account_manager.auth.verify", "POST", "/api/plugin/account-manager/api/account-manager/auth/verify", "驗證帳號、密碼與 plugin scope，不修改資料。")
	}
	if settings.Write {
		appendTool("write", "account_manager.accounts.create", "POST", "/api/plugin/account-manager/api/account-manager/accounts", "建立帳號。")
		appendTool("write", "account_manager.accounts.update", "PUT", "/api/plugin/account-manager/api/account-manager/accounts/{id}", "更新指定帳號。")
		appendTool("write", "account_manager.password.update", "PUT", "/api/plugin/account-manager/api/account-manager/accounts/{id}/password", "以 AES-GCM 儲存新密碼。")
		appendTool("write", "account_manager.groups.create", "POST", "/api/plugin/account-manager/api/account-manager/groups", "建立群組。")
		appendTool("write", "account_manager.groups.update", "PUT", "/api/plugin/account-manager/api/account-manager/groups/{id}", "更新指定群組。")
		appendTool("write", "account_manager.permissions.save", "PUT", "/api/plugin/account-manager/api/account-manager/groups/{id}/permissions", "更新群組可存取的卡片權限。")
	}
	if settings.Delete {
		appendTool("delete", "account_manager.accounts.delete", "DELETE", "/api/plugin/account-manager/api/account-manager/accounts/{id}", "刪除指定帳號。")
		appendTool("delete", "account_manager.groups.delete", "DELETE", "/api/plugin/account-manager/api/account-manager/groups/{id}", "刪除指定群組。")
	}
	return tools
}

func accountManagerMCPSettingsPayload(settings mcpConfig) map[string]any {
	return map[string]any{
		"read": settings.Read, "write": settings.Write, "delete": settings.Delete,
	}
}

func (h *HttpAPI_Plugin) authResponse(w http.ResponseWriter, r *http.Request) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("authResponse") != nil {
			response = accountManagerRecoveredJSON("authResponse")
		}
	}()
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
	if !accountManagerTokenValidWithHost(hostURL, token) {
		return accountManagerUnauthorizedResponse(w)
	}
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
		"plugin_id":           "account-manager",
		"name":                "帳號管理",
		"enabled":             true,
		"method":              "POST",
		"verify_api":          "/api/account/verify",
		"permission_api":      "/api/account/permissions",
		"permission_settings": map[string]any{},
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

func accountManagerTokenValidWithHost(hostURL string, token string) bool {
	session, ok := accountManagerIntrospectTokenWithHost(hostURL, token)
	return ok && session.Success && session.Authenticated
}

func accountManagerIntrospectTokenWithHost(hostURL string, token string) (accountManagerAuthIntrospection, bool) {
	if accountManagerStdioAuthEnabled() {
		if session, ok := accountManagerIntrospectTokenWithStdio(token); ok {
			return session, true
		}
	}
	return accountManagerIntrospectTokenWithHostHTTP(hostURL, token)
}

func accountManagerIntrospectTokenWithStdio(token string) (accountManagerAuthIntrospection, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return accountManagerAuthIntrospection{}, false
	}
	accountManagerStdioAuth.Lock()
	if !accountManagerStdioAuth.enabled || accountManagerStdioAuth.writer == nil {
		accountManagerStdioAuth.Unlock()
		return accountManagerAuthIntrospection{}, false
	}
	id := fmt.Sprintf("auth-%d", atomic.AddUint64(&accountManagerStdioAuth.nextID, 1))
	ch := make(chan accountManagerStdioAuthResponse, 1)
	accountManagerStdioAuth.pending[id] = ch
	writer := accountManagerStdioAuth.writer
	accountManagerStdioAuth.Unlock()

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "auth.introspect",
		"params": map[string]any{
			"token": token,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		accountManagerStdioAuth.Lock()
		delete(accountManagerStdioAuth.pending, id)
		accountManagerStdioAuth.Unlock()
		return accountManagerAuthIntrospection{}, false
	}
	accountManagerStdioAuth.writeMu.Lock()
	_, err = fmt.Fprintln(writer, string(data))
	accountManagerStdioAuth.writeMu.Unlock()
	if err != nil {
		accountManagerStdioAuth.Lock()
		delete(accountManagerStdioAuth.pending, id)
		accountManagerStdioAuth.Unlock()
		return accountManagerAuthIntrospection{}, false
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp.Error != nil || !resp.Result.Success || !resp.Result.Authenticated {
			return accountManagerAuthIntrospection{}, false
		}
		return resp.Result.accountManagerAuthIntrospection, true
	case <-time.After(2 * time.Second):
		accountManagerStdioAuth.Lock()
		delete(accountManagerStdioAuth.pending, id)
		accountManagerStdioAuth.Unlock()
		return accountManagerAuthIntrospection{}, false
	}
}

// EncodeExtendedJWTWithStdio 要求主系統以目前 MARS SDK 金鑰編碼 JWT。
// Account Manager 只提供第二段 payload 與最多兩個 extension 明文，不接觸簽章或加密金鑰。
func EncodeExtendedJWTWithStdio(jwtPayload map[string]any, jwtExtensions []map[string]any) (string, bool) {
	if len(jwtPayload) == 0 || len(jwtExtensions) > 2 {
		return "", false
	}
	accountManagerStdioAuth.Lock()
	if !accountManagerStdioAuth.enabled || accountManagerStdioAuth.writer == nil {
		accountManagerStdioAuth.Unlock()
		return "", false
	}
	id := fmt.Sprintf("jwt-%d", atomic.AddUint64(&accountManagerStdioAuth.nextID, 1))
	ch := make(chan accountManagerStdioAuthResponse, 1)
	accountManagerStdioAuth.pending[id] = ch
	writer := accountManagerStdioAuth.writer
	accountManagerStdioAuth.Unlock()

	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "auth.token.encode",
		"params": map[string]any{
			"jwt_payload":    jwtPayload,
			"jwt_extensions": jwtExtensions,
		},
	}
	data, err := json.Marshal(request)
	if err != nil {
		accountManagerStdioAuth.Lock()
		delete(accountManagerStdioAuth.pending, id)
		accountManagerStdioAuth.Unlock()
		return "", false
	}
	accountManagerStdioAuth.writeMu.Lock()
	_, err = fmt.Fprintln(writer, string(data))
	accountManagerStdioAuth.writeMu.Unlock()
	if err != nil {
		accountManagerStdioAuth.Lock()
		delete(accountManagerStdioAuth.pending, id)
		accountManagerStdioAuth.Unlock()
		return "", false
	}

	select {
	case resp, ok := <-ch:
		if !ok || resp.Error != nil || !resp.Result.Success || strings.TrimSpace(resp.Result.Token) == "" {
			return "", false
		}
		return strings.TrimSpace(resp.Result.Token), true
	case <-time.After(2 * time.Second):
		accountManagerStdioAuth.Lock()
		delete(accountManagerStdioAuth.pending, id)
		accountManagerStdioAuth.Unlock()
		return "", false
	}
}

func accountManagerIntrospectTokenWithHostHTTP(hostURL string, token string) (accountManagerAuthIntrospection, bool) {
	hostURL = normalizeAccountManagerHostURL(hostURL)
	token = strings.TrimSpace(token)
	if hostURL == "" || token == "" {
		return accountManagerAuthIntrospection{}, false
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(hostURL, "/")+"/auth/introspect", nil)
	if err != nil {
		return accountManagerAuthIntrospection{}, false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Authentication", "Bearer "+token)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return accountManagerAuthIntrospection{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return accountManagerAuthIntrospection{}, false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return accountManagerAuthIntrospection{}, false
	}
	var payload accountManagerAuthIntrospection
	if err := json.Unmarshal(data, &payload); err != nil {
		return accountManagerAuthIntrospection{}, false
	}
	return payload, payload.Success && payload.Authenticated
}

func resolveAccountManagerHostURL(r *http.Request, req accountManagerHostAuthRequest) string {
	for _, raw := range []string{
		os.Getenv("AGENTIC_HOST_URL"),
		os.Getenv("ACCOUNT_MANAGER_HOST_URL"),
		os.Getenv("AGENTIC_BASE_URL"),
	} {
		if candidate := accountManagerTrustedHostURL(raw, r); candidate != "" {
			return candidate
		}
	}
	for _, raw := range []string{req.HostURL, req.BaseURL} {
		if candidate := accountManagerTrustedHostURL(raw, r); candidate != "" {
			return candidate
		}
	}
	return accountManagerRemoteAddrHostURL(r)
}

func accountManagerTrustedHostURL(raw string, r *http.Request) string {
	candidate := normalizeAccountManagerHostURL(raw)
	if candidate == "" || r == nil {
		return ""
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return ""
	}
	candidateHost := strings.TrimSpace(parsed.Hostname())
	remoteHost := accountManagerRemoteHost(r)
	if candidateHost == "" || remoteHost == "" {
		return ""
	}
	if strings.EqualFold(candidateHost, remoteHost) {
		return candidate
	}
	candidateIP := net.ParseIP(candidateHost)
	remoteIP := net.ParseIP(remoteHost)
	if candidateIP != nil && remoteIP != nil {
		if candidateIP.Equal(remoteIP) || candidateIP.IsLoopback() && remoteIP.IsLoopback() {
			return candidate
		}
	}
	if strings.EqualFold(candidateHost, "localhost") && remoteIP != nil && remoteIP.IsLoopback() {
		return candidate
	}
	if strings.EqualFold(remoteHost, "localhost") && candidateIP != nil && candidateIP.IsLoopback() {
		return candidate
	}
	return ""
}

func accountManagerRemoteAddrHostURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = strings.Trim(remoteAddr, "[]")
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return normalizeAccountManagerHostURL(scheme + "://" + host)
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

func (h *HttpAPI_Plugin) handleConfigAPI(r *http.Request, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("handleConfigAPI") != nil {
			response = accountManagerRecoveredJSON("handleConfigAPI")
		}
	}()
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

func (h *HttpAPI_Plugin) handleSettingsAPI(r *http.Request, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("handleSettingsAPI") != nil {
			response = accountManagerRecoveredJSON("handleSettingsAPI")
		}
	}()
	if err := h.ensureLoaded(); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error()})
	}
	if r.Method == http.MethodGet {
		h.mu.Lock()
		settings := h.config.MCP
		h.mu.Unlock()
		return mustAccountManagerJSON(map[string]any{
			"success":  true,
			"settings": map[string]any{"mcp": accountManagerMCPSettingsPayload(settings)},
		})
	}
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		return accountManagerMethodNotAllowedResponse()
	}
	var req accountManagerSettingsRequest
	if err := json.Unmarshal(body, &req); err != nil || req.MCP == nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid settings json"})
	}
	h.mu.Lock()
	cfg := h.config
	h.mu.Unlock()
	if req.MCP.Read != nil {
		cfg.MCP.Read = *req.MCP.Read
	}
	if req.MCP.Write != nil {
		cfg.MCP.Write = *req.MCP.Write
	}
	if req.MCP.Delete != nil {
		cfg.MCP.Delete = *req.MCP.Delete
	}
	if err := writeAccountManagerConfig(cfg); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error()})
	}
	if err := h.loadConfig(true); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error()})
	}
	return mustAccountManagerJSON(map[string]any{
		"success":  true,
		"settings": map[string]any{"mcp": accountManagerMCPSettingsPayload(cfg.MCP)},
	})
}

func (h *HttpAPI_Plugin) handleAccountsAPI(r *http.Request, path []string, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("handleAccountsAPI") != nil {
			response = accountManagerRecoveredJSON("handleAccountsAPI")
		}
	}()
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
		case "api-keys", "keys":
			return h.handleAccountAPIKeysAPI(r, id, path[2:], body)
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

func (h *HttpAPI_Plugin) handleSelfPasswordAPI(r *http.Request, body []byte) []byte {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodPost {
		return accountManagerMethodNotAllowedResponse()
	}
	if err := h.ensureLoaded(); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	session, ok := h.accountManagerRequestIntrospection(r)
	if !ok || !session.Success || !session.Authenticated {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "unauthorized", "code": "AUTH_REQUIRED"})
	}
	if !strings.EqualFold(strings.TrimSpace(session.Source), "account-manager") {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "only account-manager users can change password here"})
	}
	var req passwordSelfUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid password json"})
	}
	currentPassword := strings.TrimSpace(firstNonEmptyAccountManager(req.CurrentPassword, req.OldPassword))
	newPassword := strings.TrimSpace(firstNonEmptyAccountManager(req.NewPassword, req.Password))
	if currentPassword == "" || newPassword == "" {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "current_password and new_password are required"})
	}
	account, err := h.updateOwnAccountPassword(session.Account, currentPassword, newPassword)
	if err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error()})
	}
	return mustAccountManagerJSON(map[string]any{"success": true, "account": account})
}

func (h *HttpAPI_Plugin) handleAccountPermissionsAPI(r *http.Request, id string, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("handleAccountPermissionsAPI") != nil {
			response = accountManagerRecoveredJSON("handleAccountPermissionsAPI")
		}
	}()
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

func (h *HttpAPI_Plugin) handleAccountAPIKeysAPI(r *http.Request, id string, path []string, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("handleAccountAPIKeysAPI") != nil {
			response = accountManagerRecoveredJSON("handleAccountAPIKeysAPI")
		}
	}()
	if len(path) == 0 {
		switch r.Method {
		case http.MethodGet:
			keys, err := h.listAccountAPIKeys(id)
			if err != nil {
				return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "id": id})
			}
			return mustAccountManagerJSON(map[string]any{"success": true, "id": id, "api_keys": keys})
		case http.MethodPost:
			var req apiKeyIssueRequest
			if len(body) > 0 {
				if err := json.Unmarshal(body, &req); err != nil {
					return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid api key json", "id": id})
				}
			}
			issued, key, err := h.issueAccountAPIKey(id, req.Name)
			if err != nil {
				return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "id": id})
			}
			return mustAccountManagerJSON(map[string]any{"success": true, "id": id, "api_key": issued, "key": key})
		default:
			return accountManagerMethodNotAllowedResponse()
		}
	}
	keyID := strings.TrimSpace(path[0])
	if keyID == "" {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "api key id is required", "id": id})
	}
	if r.Method != http.MethodDelete {
		return accountManagerMethodNotAllowedResponse()
	}
	if err := h.deleteAccountAPIKey(id, keyID); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "id": id, "key_id": keyID})
	}
	return mustAccountManagerJSON(map[string]any{"success": true, "id": id, "key_id": keyID})
}

func (h *HttpAPI_Plugin) handleGroupsAPI(r *http.Request, path []string, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("handleGroupsAPI") != nil {
			response = accountManagerRecoveredJSON("handleGroupsAPI")
		}
	}()
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

func (h *HttpAPI_Plugin) handleGroupPermissionsAPI(r *http.Request, id string, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("handleGroupPermissionsAPI") != nil {
			response = accountManagerRecoveredJSON("handleGroupPermissionsAPI")
		}
	}()
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

func (h *HttpAPI_Plugin) handleAuthAPI(w http.ResponseWriter, r *http.Request, path []string, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("handleAuthAPI") != nil {
			response = accountManagerRecoveredJSON("handleAuthAPI")
		}
	}()
	if len(path) == 0 || strings.EqualFold(path[0], "verify") {
		if r.Method != http.MethodPost {
			return accountManagerMethodNotAllowedResponse()
		}
		return h.verifyCredentials(w, r, body)
	}
	if strings.EqualFold(path[0], "password") {
		return h.handleSelfPasswordAPI(r, body)
	}
	return mustAccountManagerJSON(map[string]any{"success": false, "error": "unknown auth endpoint"})
}

func (h *HttpAPI_Plugin) handlePluginsAPI(r *http.Request, path []string, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("handlePluginsAPI") != nil {
			response = accountManagerRecoveredJSON("handlePluginsAPI")
		}
	}()
	if len(path) == 0 || !strings.EqualFold(path[0], "permissions") {
		return h.handleCatalog()
	}
	if err := h.ensureLoaded(); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	if r.Method == http.MethodGet {
		accountID := firstNonEmptyAccountManager(
			r.URL.Query().Get("account_id"),
			r.URL.Query().Get("account"),
			r.URL.Query().Get("username"),
			r.URL.Query().Get("user"),
		)
		if accountID != "" {
			account, ok := h.getAccount(accountID)
			if !ok {
				return mustAccountManagerJSON(map[string]any{"success": false, "error": "account not found", "id": accountID})
			}
			permissions := h.effectivePermissions(account)
			return mustAccountManagerJSON(accountPermissionPayload(account, permissions, h.effectiveWorkspaceIDs(account), "", true, pluginPermission{}))
		}
		return mustAccountManagerJSON(map[string]any{"success": true, "accounts": h.listAccounts(r)})
	}
	if r.Method == http.MethodPost {
		var req verifyRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid permission verify json"})
		}
		username := firstNonEmptyAccountManager(req.Account, req.Username)
		var account managedAccount
		var ok bool
		apiKey := strings.TrimSpace(firstNonEmptyAccountManager(req.APIKey, req.Key))
		if apiKey != "" {
			account, ok = h.findAccountByAPIKey(apiKey)
			if ok && username != "" && !strings.EqualFold(account.Username, username) && !strings.EqualFold(account.ID, username) {
				return mustAccountManagerJSON(map[string]any{"success": true, "allowed": false, "reason": "api key account mismatch"})
			}
		} else {
			account, ok = h.findAccountByUsername(username)
		}
		if !ok {
			return mustAccountManagerJSON(map[string]any{"success": true, "allowed": false, "reason": "account not found"})
		}
		allowed, matched := h.accountAllowsPlugin(account, req.PluginID, req.Scope)
		permissions := h.effectivePermissions(account)
		return mustAccountManagerJSON(accountPermissionPayload(account, permissions, h.effectiveWorkspaceIDs(account), req.PluginID, allowed, matched))
	}
	return accountManagerMethodNotAllowedResponse()
}

func (h *HttpAPI_Plugin) loadResponse() (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("loadResponse") != nil {
			response = accountManagerRecoveredJSON("loadResponse")
		}
	}()
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

func (h *HttpAPI_Plugin) loadConfig(force bool) (err error) {
	defer func() {
		if recoverAccountManagerPanic("loadConfig") != nil {
			err = accountManagerRecoveredError("loadConfig")
		}
	}()
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
		"mcp":           accountManagerMCPSettingsPayload(h.config.MCP),
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

func (h *HttpAPI_Plugin) registrationPayload(_ *http.Request) map[string]any {
	return map[string]any{
		"id":                  "account-manager",
		"name":                "帳號管理",
		"version":             AccountManagerVersion,
		"type":                "service",
		"auto_start":          true,
		"service":             "account-manager-service",
		"service_url":         "http://127.0.0.1:18186",
		"api_base":            "/api/plugin/account-manager",
		"plugin_api_base":     "/api/account-manager",
		"routes":              []string{"/api/account-manager", "/api/account"},
		"mcp_url":             "http://127.0.0.1:18186/mcp",
		"website_path":        "./website/account-manager/index.html",
		"permission_settings": map[string]any{},
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
		"mcp":             accountManagerMCPSettingsPayload(h.config.MCP),
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
	snapshot := make([]managedAccount, 0, len(h.accounts))
	for _, account := range h.accounts {
		snapshot = append(snapshot, account)
	}
	h.mu.Unlock()

	accounts := make([]publicAccount, 0, len(snapshot))
	for _, account := range snapshot {
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

func (h *HttpAPI_Plugin) createAccount(body []byte) (result publicAccount, err error) {
	defer func() {
		if recoverAccountManagerPanic("createAccount") != nil {
			err = accountManagerRecoveredError("createAccount")
		}
	}()
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
	h.clearAccountManagerVerifyFailuresForAccounts(account.ID, account.Username)
	return toPublicAccount(account), nil
}

func (h *HttpAPI_Plugin) updateAccount(id string, body []byte) (result publicAccount, err error) {
	defer func() {
		if recoverAccountManagerPanic("updateAccount") != nil {
			err = accountManagerRecoveredError("updateAccount")
		}
	}()
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
	h.clearAccountManagerVerifyFailuresForAccounts(existing.ID, existing.Username, account.ID, account.Username)
	return toPublicAccount(account), nil
}

func (h *HttpAPI_Plugin) updateAccountPassword(id string, password string) (result publicAccount, err error) {
	defer func() {
		if recoverAccountManagerPanic("updateAccountPassword") != nil {
			err = accountManagerRecoveredError("updateAccountPassword")
		}
	}()
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
	h.clearAccountManagerVerifyFailuresForAccounts(account.ID, account.Username)
	return toPublicAccount(account), nil
}

func (h *HttpAPI_Plugin) updateOwnAccountPassword(accountID string, currentPassword string, newPassword string) (result publicAccount, err error) {
	defer func() {
		if recoverAccountManagerPanic("updateOwnAccountPassword") != nil {
			err = accountManagerRecoveredError("updateOwnAccountPassword")
		}
	}()
	accountID = strings.TrimSpace(accountID)
	currentPassword = strings.TrimSpace(currentPassword)
	newPassword = strings.TrimSpace(newPassword)
	if accountID == "" {
		return publicAccount{}, errors.New("account is required")
	}
	if currentPassword == "" {
		return publicAccount{}, errors.New("current password is required")
	}
	if err := validatePasswordPolicy(newPassword, h.passwordPolicy()); err != nil {
		return publicAccount{}, err
	}
	h.mu.Lock()
	account, ok := h.findAccountLocked(accountID)
	h.mu.Unlock()
	if !ok {
		return publicAccount{}, errors.New("account not found")
	}
	decrypted, err := decryptPassword(account.PasswordAES, h.encryptionKey())
	if err != nil {
		return publicAccount{}, errors.New("current password verification failed")
	}
	if subtle.ConstantTimeCompare([]byte(decrypted), []byte(currentPassword)) != 1 {
		return publicAccount{}, errors.New("current password verification failed")
	}
	return h.updateAccountPassword(account.ID, newPassword)
}

func (h *HttpAPI_Plugin) updateAccountPermissions(id string, permissions []pluginPermission) (result publicAccount, err error) {
	defer func() {
		if recoverAccountManagerPanic("updateAccountPermissions") != nil {
			err = accountManagerRecoveredError("updateAccountPermissions")
		}
	}()
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

func (h *HttpAPI_Plugin) listAccountAPIKeys(id string) ([]publicAPIKey, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	account, ok := h.findAccountLocked(id)
	if !ok {
		return nil, errors.New("account not found")
	}
	return publicAPIKeys(account.APIKeys), nil
}

func (h *HttpAPI_Plugin) issueAccountAPIKey(id string, name string) (result publicAPIKey, keyValue string, err error) {
	defer func() {
		if recoverAccountManagerPanic("issueAccountAPIKey") != nil {
			err = accountManagerRecoveredError("issueAccountAPIKey")
		}
	}()
	name = strings.TrimSpace(name)
	if name == "" {
		return publicAPIKey{}, "", errors.New("api key name is required")
	}
	key, prefix, hash, err := generateAccountAPIKey()
	if err != nil {
		return publicAPIKey{}, "", err
	}
	now := time.Now().Format(time.RFC3339)
	apiKey := accountAPIKey{
		ID:        sanitizeAccountManagerID(name),
		Name:      name,
		Prefix:    prefix,
		KeyHash:   hash,
		Enabled:   true,
		CreatedAt: now,
	}
	if apiKey.ID == "" {
		apiKey.ID = "key"
	}
	h.mu.Lock()
	account, ok := h.findAccountLocked(id)
	if !ok {
		h.mu.Unlock()
		return publicAPIKey{}, "", errors.New("account not found")
	}
	apiKey.ID = uniqueAccountAPIKeyID(account.APIKeys, apiKey.ID)
	account.APIKeys = append(normalizeAccountAPIKeys(account.APIKeys), apiKey)
	account.UpdatedAt = now
	h.accounts[account.ID] = account
	h.config.Accounts = accountsMapToSlice(h.accounts)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return publicAPIKey{}, "", err
	}
	_ = h.loadConfig(true)
	return toPublicAPIKey(apiKey), key, nil
}

func (h *HttpAPI_Plugin) deleteAccountAPIKey(id string, keyID string) (err error) {
	defer func() {
		if recoverAccountManagerPanic("deleteAccountAPIKey") != nil {
			err = accountManagerRecoveredError("deleteAccountAPIKey")
		}
	}()
	keyID = strings.TrimSpace(keyID)
	h.mu.Lock()
	account, ok := h.findAccountLocked(id)
	if !ok {
		h.mu.Unlock()
		return errors.New("account not found")
	}
	next := make([]accountAPIKey, 0, len(account.APIKeys))
	removed := false
	for _, key := range account.APIKeys {
		if strings.EqualFold(key.ID, keyID) {
			removed = true
			continue
		}
		next = append(next, key)
	}
	if !removed {
		h.mu.Unlock()
		return errors.New("api key not found")
	}
	account.APIKeys = next
	account.UpdatedAt = time.Now().Format(time.RFC3339)
	h.accounts[account.ID] = account
	h.config.Accounts = accountsMapToSlice(h.accounts)
	cfg := h.config
	h.mu.Unlock()
	if err := writeAccountManagerConfig(cfg); err != nil {
		return err
	}
	_ = h.loadConfig(true)
	return nil
}

func (h *HttpAPI_Plugin) deleteAccount(id string) (err error) {
	defer func() {
		if recoverAccountManagerPanic("deleteAccount") != nil {
			err = accountManagerRecoveredError("deleteAccount")
		}
	}()
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

func (h *HttpAPI_Plugin) createGroup(body []byte) (result accountGroup, err error) {
	defer func() {
		if recoverAccountManagerPanic("createGroup") != nil {
			err = accountManagerRecoveredError("createGroup")
		}
	}()
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

func (h *HttpAPI_Plugin) updateGroup(id string, body []byte) (result accountGroup, err error) {
	defer func() {
		if recoverAccountManagerPanic("updateGroup") != nil {
			err = accountManagerRecoveredError("updateGroup")
		}
	}()
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

func (h *HttpAPI_Plugin) updateGroupPermissions(id string, permissions []pluginPermission) (result accountGroup, err error) {
	defer func() {
		if recoverAccountManagerPanic("updateGroupPermissions") != nil {
			err = accountManagerRecoveredError("updateGroupPermissions")
		}
	}()
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

func (h *HttpAPI_Plugin) deleteGroup(id string) (err error) {
	defer func() {
		if recoverAccountManagerPanic("deleteGroup") != nil {
			err = accountManagerRecoveredError("deleteGroup")
		}
	}()
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
	if req.WorkspaceIDs != nil {
		group.WorkspaceIDs = normalizeAccountManagerWorkspaceIDs(req.WorkspaceIDs)
	} else if len(group.WorkspaceIDs) == 0 {
		group.WorkspaceIDs = defaultAccountManagerWorkspaceIDs()
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
		return account, errors.New("system-admin is reserved and cannot be used as account id")
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

func (h *HttpAPI_Plugin) verifyCredentials(w http.ResponseWriter, r *http.Request, body []byte) (response []byte) {
	defer func() {
		if recoverAccountManagerPanic("verifyCredentials") != nil {
			response = accountManagerRecoveredJSON("verifyCredentials")
		}
	}()
	if err := h.ensureLoaded(); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": err.Error(), "plugin": h.statusPayload()})
	}
	var req verifyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return mustAccountManagerJSON(map[string]any{"success": false, "error": "invalid verify json"})
	}
	username := firstNonEmptyAccountManager(req.Account, req.Username)
	password := strings.TrimSpace(req.Password)
	apiKey := strings.TrimSpace(firstNonEmptyAccountManager(req.APIKey, req.Key))
	project := firstNonEmptyAccountManager(req.Project, "default")
	rateLimitKey := accountManagerVerifyRateLimitKey(r, req)
	if retryAfter := h.accountManagerVerifyRateLimited(rateLimitKey, time.Now()); retryAfter > 0 {
		return mustAccountManagerJSON(accountManagerRateLimitedVerifyPayload(w, username, project, retryAfter))
	}
	if apiKey != "" {
		account, ok := h.findAccountByAPIKey(apiKey)
		if !ok {
			h.recordAccountManagerVerifyFailure(rateLimitKey, time.Now())
			return mustAccountManagerJSON(genericVerifyFailurePayload(username, project))
		}
		if username != "" && !strings.EqualFold(account.Username, username) && !strings.EqualFold(account.ID, username) {
			h.recordAccountManagerVerifyFailure(rateLimitKey, time.Now())
			return mustAccountManagerJSON(genericVerifyFailurePayload(username, project))
		}
		policy := h.passwordPolicy()
		if policy.RequireEnabledAccount && !account.Enabled {
			h.recordAccountManagerVerifyFailure(rateLimitKey, time.Now())
			return mustAccountManagerJSON(genericVerifyFailurePayload(username, project))
		}
		allowed, matched := h.accountAllowsPlugin(account, req.PluginID, req.Scope)
		if req.PluginID != "" && !allowed {
			return mustAccountManagerJSON(verifyFailurePayload(account.Username, project, "permission denied"))
		}
		h.clearAccountManagerVerifyFailures(rateLimitKey)
		return mustAccountManagerJSON(h.verifySuccessPayload(account, project, req.PluginID, allowed, matched))
	}
	if username == "" || password == "" {
		h.recordAccountManagerVerifyFailure(rateLimitKey, time.Now())
		return mustAccountManagerJSON(genericVerifyFailurePayload(username, project))
	}
	account, ok := h.findAccountByUsername(username)
	if !ok {
		h.recordAccountManagerVerifyFailure(rateLimitKey, time.Now())
		return mustAccountManagerJSON(genericVerifyFailurePayload(username, project))
	}
	decrypted, err := decryptPassword(account.PasswordAES, h.encryptionKey())
	if err != nil {
		h.recordAccountManagerVerifyFailure(rateLimitKey, time.Now())
		return mustAccountManagerJSON(genericVerifyFailurePayload(username, project))
	}
	valid := subtle.ConstantTimeCompare([]byte(decrypted), []byte(password)) == 1
	if !valid {
		h.recordAccountManagerVerifyFailure(rateLimitKey, time.Now())
		return mustAccountManagerJSON(genericVerifyFailurePayload(username, project))
	}
	policy := h.passwordPolicy()
	if policy.RequireEnabledAccount && !account.Enabled {
		h.recordAccountManagerVerifyFailure(rateLimitKey, time.Now())
		return mustAccountManagerJSON(genericVerifyFailurePayload(username, project))
	}
	allowed, matched := h.accountAllowsPlugin(account, req.PluginID, req.Scope)
	if req.PluginID != "" && !allowed {
		return mustAccountManagerJSON(verifyFailurePayload(account.Username, project, "permission denied"))
	}
	account.LastLoginAt = time.Now().Format(time.RFC3339)
	h.persistAccount(account)
	h.clearAccountManagerVerifyFailures(rateLimitKey)
	return mustAccountManagerJSON(h.verifySuccessPayload(account, project, req.PluginID, allowed, matched))
}

func (h *HttpAPI_Plugin) verifySuccessPayload(account managedAccount, project string, pluginID string, allowed bool, matched pluginPermission) map[string]any {
	permissions := h.effectivePermissions(account)
	roles := accountRoleNames(account)
	workspaceIDs := h.effectiveWorkspaceIDs(account)
	response := map[string]any{
		"success":            true,
		"account":            account.Username,
		"project":            project,
		"display_name":       account.DisplayName,
		"roles":              roles,
		"permissions":        accountPermissionNames(permissions),
		"permission_details": permissionDetailsForResponse(permissions),
		"plugin_permissions": permissionDetailsForResponse(permissions),
		"plugin_settings":    pluginSettingsMap(permissions),
		"settings":           pluginSettingsMap(permissions),
		"expires_in":         86400,
		"valid":              true,
		"allowed":            allowed,
		"permission":         matched,
		"workspace_ids":      workspaceIDs,
	}
	jwtPayload, jwtExtensions, requested, err := accountManagerJWTRequest(account, project, roles, permissions, workspaceIDs)
	if err != nil {
		return verifyFailurePayload(account.Username, project, "account JWT configuration is invalid")
	}
	if requested {
		response["jwt_payload"] = jwtPayload
		if len(jwtExtensions) > 0 {
			response["jwt_extensions"] = jwtExtensions
		}
	}
	if pluginID != "" {
		response["features"] = pluginSettingsForPlugin(permissions, pluginID)
		response["plugin_features"] = map[string]any{pluginID: pluginSettingsForPlugin(permissions, pluginID)}
	}
	return response
}

func accountManagerJWTRequest(account managedAccount, project string, roles []string, permissions []pluginPermission, workspaceIDs []string) (map[string]any, []map[string]any, bool, error) {
	if account.Metadata == nil {
		return nil, nil, false, nil
	}
	rawConfig, exists := account.Metadata["jwt"]
	if !exists {
		return nil, nil, false, nil
	}
	config, ok := rawConfig.(map[string]any)
	if !ok {
		return nil, nil, false, fmt.Errorf("metadata.jwt must be an object")
	}
	enabled, _ := config["enabled"].(bool)
	if !enabled {
		return nil, nil, false, nil
	}

	now := time.Now()
	subject := firstNonEmptyAccountManager(account.ID, account.Username)
	payload := map[string]any{
		"iss":                account.Username,
		"sub":                subject,
		"account":            account.Username,
		"project":            firstNonEmptyAccountManager(project, "default"),
		"display_name":       account.DisplayName,
		"roles":              roles,
		"permissions":        accountPermissionNames(permissions),
		"plugin_permissions": permissionDetailsForResponse(permissions),
		"workspace_ids":      workspaceIDs,
		"iat":                now.Unix(),
		"exp":                now.Add(24 * time.Hour).Unix(),
	}
	if len(roles) > 0 {
		payload["group"] = roles[0]
	}
	if rawClaims, exists := config["claims"]; exists {
		claims, ok := rawClaims.(map[string]any)
		if !ok {
			return nil, nil, false, fmt.Errorf("metadata.jwt.claims must be an object")
		}
		for key, value := range claims {
			key = strings.TrimSpace(key)
			if key == "" {
				return nil, nil, false, fmt.Errorf("metadata.jwt.claims contains an empty key")
			}
			payload[key] = value
		}
	}

	extensions, err := accountManagerJWTExtensions(config["extensions"])
	if err != nil {
		return nil, nil, false, err
	}
	if _, err := json.Marshal(payload); err != nil {
		return nil, nil, false, fmt.Errorf("metadata.jwt.claims is not JSON encodable: %w", err)
	}
	return payload, extensions, true, nil
}

func accountManagerJWTExtensions(raw any) ([]map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		if typed, typedOK := raw.([]map[string]any); typedOK {
			items = make([]any, len(typed))
			for index := range typed {
				items[index] = typed[index]
			}
		} else {
			return nil, fmt.Errorf("metadata.jwt.extensions must be an array")
		}
	}
	if len(items) > 2 {
		return nil, fmt.Errorf("metadata.jwt.extensions supports at most two items")
	}
	extensions := make([]map[string]any, 0, len(items))
	for index, item := range items {
		extension, ok := item.(map[string]any)
		if !ok || extension == nil {
			return nil, fmt.Errorf("metadata.jwt.extensions[%d] must be an object", index)
		}
		if _, err := json.Marshal(extension); err != nil {
			return nil, fmt.Errorf("metadata.jwt.extensions[%d] is not JSON encodable: %w", index, err)
		}
		extensions = append(extensions, extension)
	}
	return extensions, nil
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

func genericVerifyFailurePayload(account string, project string) map[string]any {
	return verifyFailurePayload(account, project, accountManagerInvalidCredentialsError)
}

func accountManagerRateLimitedVerifyPayload(w http.ResponseWriter, account string, project string, retryAfter time.Duration) map[string]any {
	retryAfterSeconds := int((retryAfter + time.Second - time.Nanosecond) / time.Second)
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	if w != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfterSeconds))
		w.WriteHeader(http.StatusTooManyRequests)
	}
	payload := verifyFailurePayload(account, project, "too many attempts")
	payload["code"] = "RATE_LIMITED"
	payload["retry_after"] = retryAfterSeconds
	return payload
}

func accountManagerVerifyRateLimitKey(r *http.Request, req verifyRequest) string {
	remoteHost := accountManagerRequestRemoteHost(r)
	account := strings.ToLower(strings.TrimSpace(firstNonEmptyAccountManager(req.Account, req.Username)))
	apiKey := strings.TrimSpace(firstNonEmptyAccountManager(req.APIKey, req.Key))
	subject := "account:" + account
	if apiKey != "" {
		hash := hashAccountAPIKey(apiKey)
		if len(hash) > 16 {
			hash = hash[:16]
		}
		subject = "api-key:" + hash
	} else if account == "" {
		subject = "account:unknown"
	}
	return remoteHost + "|" + subject
}

func accountManagerRequestRemoteHost(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	remoteAddr := strings.TrimSpace(r.RemoteAddr)
	if remoteAddr == "" {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = strings.Trim(remoteAddr, "[]")
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "unknown"
	}
	return strings.ToLower(host)
}

func (h *HttpAPI_Plugin) accountManagerVerifyRateLimited(key string, now time.Time) time.Duration {
	if key == "" {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanupAccountManagerVerifyLimitsLocked(now)
	state, ok := h.verifyLimits[key]
	if !ok {
		return 0
	}
	if state.BlockedUntil.After(now) {
		return state.BlockedUntil.Sub(now)
	}
	return 0
}

func (h *HttpAPI_Plugin) recordAccountManagerVerifyFailure(key string, now time.Time) {
	if key == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.verifyLimits == nil {
		h.verifyLimits = map[string]accountManagerVerifyRateLimit{}
	}
	state := h.verifyLimits[key]
	if state.ResetAt.IsZero() || now.After(state.ResetAt) {
		state = accountManagerVerifyRateLimit{ResetAt: now.Add(accountManagerVerifyRateLimitWindow)}
	}
	state.Failures++
	if state.Failures >= accountManagerVerifyRateLimitMaxAttempts {
		state.BlockedUntil = now.Add(accountManagerVerifyRateLimitCooldown)
		state.ResetAt = state.BlockedUntil
	}
	h.verifyLimits[key] = state
}

func (h *HttpAPI_Plugin) clearAccountManagerVerifyFailures(key string) {
	if key == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.verifyLimits, key)
}

func (h *HttpAPI_Plugin) clearAccountManagerVerifyFailuresForAccounts(accounts ...string) {
	subjects := map[string]bool{}
	for _, account := range accounts {
		account = strings.ToLower(strings.TrimSpace(account))
		if account != "" {
			subjects["account:"+account] = true
		}
	}
	if len(subjects) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for key := range h.verifyLimits {
		parts := strings.Split(key, "|")
		if len(parts) == 0 {
			continue
		}
		if subjects[parts[len(parts)-1]] {
			delete(h.verifyLimits, key)
		}
	}
}

func (h *HttpAPI_Plugin) cleanupAccountManagerVerifyLimitsLocked(now time.Time) {
	if len(h.verifyLimits) == 0 {
		return
	}
	for key, state := range h.verifyLimits {
		if state.BlockedUntil.After(now) || state.ResetAt.After(now) {
			continue
		}
		delete(h.verifyLimits, key)
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

func (h *HttpAPI_Plugin) findAccountByAPIKey(key string) (managedAccount, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return managedAccount{}, false
	}
	hash := hashAccountAPIKey(key)
	now := time.Now().Format(time.RFC3339)
	h.mu.Lock()
	for _, account := range h.accounts {
		keys := normalizeAccountAPIKeys(account.APIKeys)
		for index, apiKey := range keys {
			if !apiKey.Enabled || apiKey.KeyHash == "" {
				continue
			}
			if subtle.ConstantTimeCompare([]byte(apiKey.KeyHash), []byte(hash)) != 1 {
				continue
			}
			apiKey.LastUsedAt = now
			keys[index] = apiKey
			account.APIKeys = keys
			account.LastLoginAt = now
			h.accounts[account.ID] = account
			h.config.Accounts = accountsMapToSlice(h.accounts)
			cfg := h.config
			h.mu.Unlock()
			_ = writeAccountManagerConfig(cfg)
			_ = h.loadConfig(true)
			return account, true
		}
	}
	h.mu.Unlock()
	return managedAccount{}, false
}

func accountRoleNames(account managedAccount) []string {
	roles := normalizeStringIDs([]string{account.Role})
	if len(roles) == 0 {
		return []string{"user"}
	}
	return roles
}

func accountPermissionPayload(account managedAccount, permissions []pluginPermission, workspaceIDs []string, pluginID string, allowed bool, matched pluginPermission) map[string]any {
	pluginID = strings.TrimSpace(pluginID)
	payload := map[string]any{
		"success":            true,
		"allowed":            allowed,
		"account":            toPublicAccount(account),
		"permissions":        permissionDetailsForResponse(permissions),
		"permission_details": permissionDetailsForResponse(permissions),
		"plugin_permissions": permissionDetailsForResponse(permissions),
		"permission_names":   accountPermissionNames(permissions),
		"plugin_settings":    pluginSettingsMap(permissions),
		"settings":           pluginSettingsMap(permissions),
		"permission":         matched,
		"workspace_ids":      normalizeAccountManagerWorkspaceIDs(workspaceIDs),
	}
	if pluginID != "" {
		payload["features"] = pluginSettingsForPlugin(permissions, pluginID)
		payload["plugin_features"] = map[string]any{pluginID: pluginSettingsForPlugin(permissions, pluginID)}
	}
	return payload
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

func permissionDetailsForResponse(permissions []pluginPermission) []pluginPermission {
	out := make([]pluginPermission, 0, len(permissions))
	for _, permission := range permissions {
		permission.Settings = copyAccountManagerSettings(permission.Settings)
		out = append(out, permission)
	}
	return out
}

func pluginSettingsMap(permissions []pluginPermission) map[string]any {
	out := map[string]any{}
	for _, permission := range permissions {
		pluginID := strings.TrimSpace(permission.PluginID)
		if pluginID == "" || len(permission.Settings) == 0 {
			continue
		}
		out[pluginID] = copyAccountManagerSettings(permission.Settings)
	}
	return out
}

func pluginSettingsForPlugin(permissions []pluginPermission, pluginID string) map[string]any {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return map[string]any{}
	}
	for _, permission := range permissions {
		if strings.EqualFold(permission.PluginID, pluginID) {
			return copyAccountManagerSettings(permission.Settings)
		}
	}
	for _, permission := range permissions {
		if permission.PluginID == "*" {
			return copyAccountManagerSettings(permission.Settings)
		}
	}
	return map[string]any{}
}

func copyAccountManagerSettings(settings map[string]any) map[string]any {
	if len(settings) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(settings))
	for key, value := range settings {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
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

func readAccountManagerConfig(path string) (cfg accountManagerConfig, modified time.Time, err error) {
	defer func() {
		if recoverAccountManagerPanic("readAccountManagerConfig") != nil {
			err = accountManagerRecoveredError("readAccountManagerConfig")
		}
	}()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && path == AccountManagerConfigPath {
			return initializeAccountManagerConfigFromDefault()
		}
		return accountManagerConfig{}, time.Time{}, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return accountManagerConfig{}, time.Time{}, fmt.Errorf("invalid account manager config: %w", err)
	}
	if err := normalizeAccountManagerConfig(&cfg); err != nil {
		return accountManagerConfig{}, time.Time{}, err
	}
	modified = time.Now()
	if stat, err := os.Stat(path); err == nil {
		modified = stat.ModTime()
	}
	return cfg, modified, nil
}

func initializeAccountManagerConfigFromDefault() (cfg accountManagerConfig, modified time.Time, err error) {
	defer func() {
		if recoverAccountManagerPanic("initializeAccountManagerConfigFromDefault") != nil {
			err = accountManagerRecoveredError("initializeAccountManagerConfigFromDefault")
		}
	}()
	cfg = accountManagerConfig{Version: AccountManagerVersion}
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
	modified = time.Now()
	if stat, err := os.Stat(AccountManagerConfigPath); err == nil {
		modified = stat.ModTime()
	}
	return cfg, modified, nil
}

func normalizeAccountManagerConfig(cfg *accountManagerConfig) (err error) {
	defer func() {
		if recoverAccountManagerPanic("normalizeAccountManagerConfig") != nil {
			err = accountManagerRecoveredError("normalizeAccountManagerConfig")
		}
	}()
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
		group.WorkspaceIDs = normalizeAccountManagerWorkspaceIDs(group.WorkspaceIDs)
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
		account.APIKeys = normalizeAccountAPIKeys(account.APIKeys)
		accounts = append(accounts, account)
	}
	cfg.Accounts = accounts
	return nil
}

func writeAccountManagerConfig(cfg accountManagerConfig) (err error) {
	defer func() {
		if recoverAccountManagerPanic("writeAccountManagerConfig") != nil {
			err = accountManagerRecoveredError("writeAccountManagerConfig")
		}
	}()
	if err := normalizeAccountManagerConfig(&cfg); err != nil {
		return err
	}
	configDir := filepath.Dir(AccountManagerConfigPath)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(configDir, filepath.Base(AccountManagerConfigPath)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(append(data, '\n')); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := replaceAccountManagerConfigFile(tmpPath, AccountManagerConfigPath); err != nil {
		return err
	}
	committed = true
	return nil
}

func replaceAccountManagerConfigFile(tmpPath string, targetPath string) error {
	if err := os.Rename(tmpPath, targetPath); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	if err := os.Remove(targetPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpPath, targetPath)
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
		APIKeys:           publicAPIKeys(account.APIKeys),
		Metadata:          account.Metadata,
	}
}

func toPublicAPIKey(key accountAPIKey) publicAPIKey {
	return publicAPIKey{
		ID:         key.ID,
		Name:       key.Name,
		Prefix:     key.Prefix,
		Enabled:    key.Enabled,
		CreatedAt:  key.CreatedAt,
		LastUsedAt: key.LastUsedAt,
	}
}

func publicAPIKeys(keys []accountAPIKey) []publicAPIKey {
	keys = normalizeAccountAPIKeys(keys)
	out := make([]publicAPIKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, toPublicAPIKey(key))
	}
	return out
}

func normalizeAccountAPIKeys(keys []accountAPIKey) []accountAPIKey {
	out := make([]accountAPIKey, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		key.ID = sanitizeAccountManagerID(firstNonEmptyAccountManager(key.ID, key.Name, key.Prefix))
		key.Name = strings.TrimSpace(key.Name)
		key.Prefix = strings.TrimSpace(key.Prefix)
		key.KeyHash = strings.TrimSpace(key.KeyHash)
		if key.ID == "" || key.Name == "" || key.KeyHash == "" {
			continue
		}
		idKey := strings.ToLower(key.ID)
		if seen[idKey] {
			continue
		}
		seen[idKey] = true
		if key.Prefix == "" {
			key.Prefix = key.ID
		}
		out = append(out, key)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].CreatedAt+out[i].ID) < strings.ToLower(out[j].CreatedAt+out[j].ID)
	})
	return out
}

func uniqueAccountAPIKeyID(keys []accountAPIKey, base string) string {
	base = sanitizeAccountManagerID(base)
	if base == "" {
		base = "key"
	}
	seen := map[string]bool{}
	for _, key := range keys {
		seen[strings.ToLower(key.ID)] = true
	}
	if !seen[strings.ToLower(base)] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !seen[strings.ToLower(candidate)] {
			return candidate
		}
	}
}

func generateAccountAPIKey() (string, string, string, error) {
	random := make([]byte, 32)
	if _, err := crand.Read(random); err != nil {
		return "", "", "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(random)
	key := "acct_" + raw
	prefix := key
	if len(prefix) > 15 {
		prefix = prefix[:15]
	}
	return key, prefix, hashAccountAPIKey(key), nil
}

func hashAccountAPIKey(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
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

func (h *HttpAPI_Plugin) effectiveWorkspaceIDs(account managedAccount) []string {
	workspaceIDs := []string{}
	h.mu.Lock()
	for _, groupID := range account.GroupIDs {
		group, ok := h.findGroupLocked(groupID)
		if !ok || !group.Enabled {
			continue
		}
		workspaceIDs = append(workspaceIDs, group.WorkspaceIDs...)
	}
	h.mu.Unlock()
	return normalizeAccountManagerWorkspaceIDs(workspaceIDs)
}

func permissionsAllowPlugin(permissions []pluginPermission, pluginID string, scope string) (bool, pluginPermission) {
	pluginID = strings.TrimSpace(pluginID)
	scope = strings.TrimSpace(strings.ToLower(scope))
	if pluginID == "" {
		return true, pluginPermission{}
	}
	var wildcardPermission pluginPermission
	wildcardMatched := false
	for _, permission := range permissions {
		if permission.PluginID == "*" {
			if !wildcardMatched {
				wildcardPermission = permission
				wildcardMatched = true
			}
			continue
		}
		if !strings.EqualFold(permission.PluginID, pluginID) {
			continue
		}
		if !permission.Enabled {
			return false, permission
		}
		if scope == "" || scopeAllowed(permission.Scopes, scope) {
			return true, permission
		}
		return false, permission
	}
	if wildcardMatched {
		if !wildcardPermission.Enabled {
			return false, wildcardPermission
		}
		if scope == "" || scopeAllowed(wildcardPermission.Scopes, scope) {
			return true, wildcardPermission
		}
		return false, wildcardPermission
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

func defaultAccountManagerWorkspaceIDs() []string {
	return []string{accountManagerDefaultWorkspaceID}
}

func normalizeAccountManagerWorkspaceIDs(values []string) []string {
	seen := map[string]bool{accountManagerDefaultWorkspaceID: true}
	out := defaultAccountManagerWorkspaceIDs()
	for _, value := range values {
		id := sanitizeAccountManagerID(value)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out[1:])
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
	return strings.EqualFold(strings.TrimSpace(input), "system-admin")
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
