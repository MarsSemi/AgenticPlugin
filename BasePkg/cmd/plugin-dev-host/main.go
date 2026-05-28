package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type devHost struct {
	pluginRoot    string
	baseRoot      string
	pluginID      string
	serviceURL    *url.URL
	pluginAPIBase string
	indexPath     string
	authToken     string
	injectAuth    bool
	dataMu        sync.Mutex
	dataStore     map[string]any
}

func main() {
	pluginRoot := flag.String("plugin-root", "../Sample", "plugin package root")
	baseRoot := flag.String("base-root", ".", "BasePkg root")
	pluginID := flag.String("plugin-id", "sample", "plugin id")
	service := flag.String("service", "http://127.0.0.1:18182", "plugin service URL")
	listen := flag.String("listen", "127.0.0.1:19090", "dev host listen address")
	indexPath := flag.String("index", "website/sample/index.html", "plugin index path relative to plugin root")
	pluginAPIBase := flag.String("plugin-api-base", "", "plugin API base, default /api/{plugin-id}")
	authToken := flag.String("auth-token", "dev-auth-token", "dev host auth token passed to plugin auth hook and browser adapter")
	injectAuth := flag.Bool("inject-auth", true, "call /api/{plugin-id}/plugin/auth on startup when auth-token is not empty")
	flag.Parse()

	serviceURL, err := url.Parse(strings.TrimRight(*service, "/"))
	if err != nil {
		log.Fatalf("invalid service URL: %v", err)
	}
	rootAbs, err := filepath.Abs(*pluginRoot)
	if err != nil {
		log.Fatal(err)
	}
	baseAbs, err := filepath.Abs(*baseRoot)
	if err != nil {
		log.Fatal(err)
	}
	apiBase := strings.TrimSpace(*pluginAPIBase)
	if apiBase == "" {
		apiBase = "/api/" + strings.Trim(*pluginID, "/")
	}

	host := &devHost{
		pluginRoot:    rootAbs,
		baseRoot:      baseAbs,
		pluginID:      strings.TrimSpace(*pluginID),
		serviceURL:    serviceURL,
		pluginAPIBase: normalizePath(apiBase),
		indexPath:     filepath.Clean(*indexPath),
		authToken:     strings.TrimSpace(*authToken),
		injectAuth:    *injectAuth,
		dataStore:     map[string]any{},
	}

	if host.injectAuth && host.authToken != "" {
		if err := host.injectDevAuth(); err != nil {
			log.Printf("plugin auth injection skipped: %v", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", host.serve)

	log.Printf("plugin dev host listening on http://%s", *listen)
	log.Printf("plugin root=%s, base root=%s, service=%s", host.pluginRoot, host.baseRoot, host.serviceURL.String())
	if err := http.ListenAndServe(*listen, mux); err != nil {
		log.Fatal(err)
	}
}

func (h *devHost) serve(w http.ResponseWriter, r *http.Request) {
	addDevCORS(w)
	h.ensureAuthCookie(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	path := normalizePath(r.URL.Path)
	switch {
	case path == "/":
		http.Redirect(w, r, h.websiteIndexURL(), http.StatusFound)
	case path == "/talk/ask/direct_stream":
		h.mockChatStream(w, r)
	case path == "/talk/models":
		h.mockTalkModels(w, r)
	case path == "/talk/session/create":
		h.mockSessionCreate(w, r)
	case path == "/talk/rag/file/content":
		h.mockRAGFileContent(w, r)
	case strings.HasPrefix(path, "/talk/rag/"):
		h.mockRAGList(w, r)
	case path == "/api/calendar_query":
		h.mockCalendarQuery(w, r)
	case path == "/data/write" || path == "/data/read" || path == "/data/delete":
		h.mockDataAPI(w, r, path)
	case path == "/basepkg/host-adapter.js":
		h.serveBaseFile(w, r, "web/host-adapter.js")
	case strings.HasPrefix(path, "/assets/"):
		h.serveBaseFile(w, r, filepath.Join("web", filepath.FromSlash(strings.TrimPrefix(path, "/"))))
	case strings.HasPrefix(path, "/api/plugin/"):
		h.proxyGateway(w, r, path)
	case path == "/mcp" || strings.HasPrefix(path, h.pluginAPIBase+"/"):
		h.proxyToService(w, r, path)
	default:
		h.servePluginWebsite(w, r, path)
	}
}

func (h *devHost) mockTalkModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"default_provider": "dev-provider",
		"default_model":    "dev-model",
		"providers": []map[string]any{
			{
				"id":      "dev-provider",
				"name":    "BasePkg Dev Provider",
				"enabled": true,
				"models":  []map[string]any{{"id": "dev-model", "name": "BasePkg Dev Model"}},
			},
		},
	})
}

func (h *devHost) injectDevAuth() error {
	payload := map[string]any{
		"auth_token": h.authToken,
		"token_type": "Bearer",
		"header":     "Authentication",
		"account":    "dev",
		"project":    "dev",
		"source":     "basepkg-dev-host",
		"expires_at": time.Now().Add(24 * time.Hour).Format(time.RFC3339Nano),
	}
	body, _ := json.Marshal(payload)
	target := *h.serviceURL
	target.Path = singleJoiningSlash(h.serviceURL.Path, h.pluginAPIBase+"/plugin/auth")
	req, err := http.NewRequest(http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authentication", "Bearer "+h.authToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	log.Printf("plugin auth token injected into %s", h.pluginID)
	return nil
}

func (h *devHost) mockSessionCreate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"session": map[string]any{
			"id":         fmt.Sprintf("dev-session-%d", time.Now().UnixNano()),
			"title":      "BasePkg Dev Session",
			"created_at": time.Now().Format(time.RFC3339),
		},
	})
}

func (h *devHost) mockRAGList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"libraries": []map[string]any{
			{"id": "dev-rag-schedule", "name": "AI智慧排程"},
		},
		"files": []map[string]any{
			{"id": "dev-rag-schedule-file", "library_id": "dev-rag-schedule", "file_name": "AI智慧排程.md", "display_name": "AI智慧排程.md"},
		},
		"chunks": []map[string]any{
			{"id": "dev-rag-schedule-chunk", "library_id": "dev-rag-schedule", "file_id": "dev-rag-schedule-file", "file_name": "AI智慧排程.md"},
		},
	})
}

func (h *devHost) mockRAGFileContent(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"content": strings.Join([]string{
			"# AI智慧排程開發參考",
			"",
			"- 依訂單交期、工序順序、基準工時與資源可用日曆推算排程。",
			"- 若資源不足，優先回報瓶頸資源與可調整建議。",
			"- BasePkg dev host 只提供 mock 參考內容；匯入主服務後會讀取正式 RAG 資料。",
		}, "\n"),
	})
}

func (h *devHost) mockCalendarQuery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func (h *devHost) mockDataAPI(w http.ResponseWriter, r *http.Request, path string) {
	key := r.URL.Query().Get("account") + "::" + r.URL.Query().Get("name")
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	if value := strings.TrimSpace(fmt.Sprint(payload["account"])); value != "" {
		key = value + "::" + strings.TrimSpace(fmt.Sprint(payload["name"]))
	}
	if key == "::" || strings.HasSuffix(key, "::") {
		key = "dev::default"
	}
	h.dataMu.Lock()
	defer h.dataMu.Unlock()
	switch path {
	case "/data/write":
		h.dataStore[key] = payload["data"]
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "key": key})
	case "/data/read":
		data, ok := h.dataStore[key]
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "dev data not found", "key": key})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "key": key, "data": data})
	case "/data/delete":
		delete(h.dataStore, key)
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "key": key})
	}
}

func addDevCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Authentication, Accept")
}

func (h *devHost) ensureAuthCookie(w http.ResponseWriter) {
	if strings.TrimSpace(h.authToken) == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "agentic_auth_token",
		Value:    h.authToken,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *devHost) serveBaseFile(w http.ResponseWriter, r *http.Request, rel string) {
	path := filepath.Join(h.baseRoot, filepath.Clean(rel))
	if !strings.HasPrefix(path, h.baseRoot) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

func (h *devHost) servePluginWebsite(w http.ResponseWriter, r *http.Request, requestPath string) {
	rel := strings.TrimPrefix(requestPath, "/")
	if rel == "" {
		rel = strings.TrimPrefix(filepath.ToSlash(h.indexPath), "website/")
	}
	path := filepath.Join(h.pluginRoot, "website", filepath.Clean(rel))
	if !strings.HasPrefix(path, filepath.Join(h.pluginRoot, "website")) {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

func (h *devHost) websiteIndexURL() string {
	rel := strings.TrimPrefix(filepath.ToSlash(h.indexPath), "website/")
	if strings.TrimSpace(rel) == "" || rel == "." {
		return "/"
	}
	return "/" + strings.TrimPrefix(rel, "/")
}

func (h *devHost) proxyGateway(w http.ResponseWriter, r *http.Request, requestPath string) {
	parts := strings.Split(strings.Trim(requestPath, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "plugin" {
		http.NotFound(w, r)
		return
	}
	id, _ := url.PathUnescape(parts[2])
	if id != h.pluginID {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "unknown plugin id"})
		return
	}
	rest := "/" + strings.Join(parts[3:], "/")
	if strings.HasPrefix(rest, "/_plugin/") {
		command := strings.TrimPrefix(rest, "/_plugin/")
		if command == "mcp" {
			h.proxyToService(w, r, "/mcp")
			return
		}
		h.proxyToService(w, r, h.pluginAPIBase+"/plugin/"+command)
		return
	}
	if rest == "/" {
		rest = h.pluginAPIBase + "/plugin/status"
	}
	h.proxyToService(w, r, rest)
}

func (h *devHost) proxyToService(w http.ResponseWriter, r *http.Request, targetPath string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": err.Error()})
		return
	}
	target := *h.serviceURL
	target.Path = singleJoiningSlash(h.serviceURL.Path, targetPath)
	target.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	for key, values := range r.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "error": err.Error(), "target": target.String()})
		return
	}
	defer resp.Body.Close()
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (h *devHost) mockChatStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	message := strings.TrimSpace(fmt.Sprint(payload["message"]))
	if len(message) > 240 {
		message = message[:240] + "..."
	}
	reply := "這是 BasePkg dev host 的 mock CHAT 回應。\n\n" +
		"- Plugin API 會由 dev host 代理到目前的 plugin service。\n" +
		"- 匯入主服務後，這個端點會改由主服務 `/talk/ask/direct_stream` 接管。\n" +
		"- 本次收到的訊息摘要：`" + strings.ReplaceAll(message, "`", "'") + "`"

	flusher, _ := w.(http.Flusher)
	for _, chunk := range splitText(reply, 80) {
		writeSSE(w, "delta", map[string]string{"delta": chunk})
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(35 * time.Millisecond)
	}
	writeSSE(w, "done", map[string]any{"assistant_message": map[string]string{"content": reply}})
	if flusher != nil {
		flusher.Flush()
	}
}

func writeSSE(w http.ResponseWriter, event string, payload any) {
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func splitText(text string, size int) []string {
	runes := []rune(text)
	if size <= 0 || len(runes) <= size {
		return []string{text}
	}
	out := []string{}
	for len(runes) > size {
		out = append(out, string(runes[:size]))
		runes = runes[size:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func normalizePath(path string) string {
	text := strings.TrimSpace(path)
	if text == "" {
		return "/"
	}
	if !strings.HasPrefix(text, "/") {
		text = "/" + text
	}
	return text
}

func singleJoiningSlash(left, right string) string {
	leftSlash := strings.HasSuffix(left, "/")
	rightSlash := strings.HasPrefix(right, "/")
	switch {
	case leftSlash && rightSlash:
		return left + right[1:]
	case !leftSlash && !rightSlash:
		return left + "/" + right
	default:
		return left + right
	}
}
