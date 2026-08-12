package sampleplugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	sampleMCPProtocolLatest       = "2026-07-28"
	sampleMCPProtocolLegacyLatest = "2025-11-25"
	sampleMCPProtocolMetadataKey  = "io.modelcontextprotocol/protocolVersion"
	sampleMCPMaxRequestBytes      = 1 << 20
	sampleMCPMaxResponseBytes     = 2 << 20
)

var sampleMCPSupportedProtocolVersions = map[string]bool{
	"2026-07-28": true,
	"2025-11-25": true,
	"2025-06-18": true,
	"2025-03-26": true,
}

type sampleMCPRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type sampleMCPInitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
	ClientInfo      map[string]any `json:"clientInfo,omitempty"`
	Meta            map[string]any `json:"_meta,omitempty"`
}

type sampleMCPToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Meta      map[string]any `json:"_meta,omitempty"`
}

type sampleMCPProtocolError struct {
	HTTPStatus int
	Code       int
	Message    string
	Data       any
}

func (h *HttpAPI_Plugin) serveSampleMCPHTTP(w http.ResponseWriter, r *http.Request) bool {
	if r == nil || !isSampleMCPPath(r.URL.Path) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("MCP-Protocol-Version", sampleMCPProtocolLatest)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeSampleMCPHTTPJSON(w, http.StatusMethodNotAllowed, map[string]any{
			"error": "此 MCP Server 採無狀態 HTTP；請使用 POST。",
		})
		return true
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, sampleMCPMaxRequestBytes+1))
	if err != nil {
		h.writeSampleMCPRPCError(w, http.StatusBadRequest, nil, -32700, "Parse error", err.Error())
		return true
	}
	if len(body) > sampleMCPMaxRequestBytes {
		writeSampleMCPHTTPJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error": "MCP request exceeds 1 MiB",
		})
		return true
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] == '[' {
		h.writeSampleMCPRPCError(w, http.StatusOK, nil, -32600, "Invalid Request", "不接受空白或 batch request")
		return true
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	request := sampleMCPRPCRequest{}
	if err := decoder.Decode(&request); err != nil {
		h.writeSampleMCPRPCError(w, http.StatusOK, nil, -32700, "Parse error", err.Error())
		return true
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		h.writeSampleMCPRPCError(w, http.StatusOK, nil, -32700, "Parse error", "JSON-RPC request contains trailing content")
		return true
	}
	if request.JSONRPC != "2.0" || strings.TrimSpace(request.Method) == "" {
		h.writeSampleMCPRPCError(w, http.StatusOK, request.ID, -32600, "Invalid Request", nil)
		return true
	}

	protocol, modern, protocolErr := validateSampleMCPProtocol(r, request)
	if protocolErr != nil {
		h.writeSampleMCPRPCError(w, protocolErr.HTTPStatus, request.ID, protocolErr.Code, protocolErr.Message, protocolErr.Data)
		return true
	}
	w.Header().Set("MCP-Protocol-Version", protocol)
	h.dispatchSampleMCP(w, r, request, protocol, modern)
	return true
}

func isSampleMCPPath(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return len(parts) == 1 && strings.EqualFold(parts[0], "mcp")
}

func (h *HttpAPI_Plugin) dispatchSampleMCP(w http.ResponseWriter, r *http.Request, request sampleMCPRPCRequest, protocol string, modern bool) {
	method := strings.TrimSpace(request.Method)
	if strings.HasPrefix(method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch method {
	case "server/discover":
		if !modern {
			h.writeSampleMCPRPCError(w, http.StatusOK, request.ID, -32601, "Method not found", map[string]any{"method": method})
			return
		}
		h.writeSampleMCPRPCResult(w, request.ID, map[string]any{
			"resultType":        "complete",
			"supportedVersions": sampleMCPSortedProtocolVersions(),
			"capabilities":      sampleMCPServerCapabilities(),
			"instructions":      sampleMCPInstructions(),
			"ttlMs":             60000,
			"cacheScope":        "private",
			"_meta": map[string]any{
				"io.modelcontextprotocol/serverInfo": map[string]any{"name": "Sample Plugin MCP", "version": SamplePluginVersion},
			},
		})
	case "initialize":
		if modern {
			h.writeSampleMCPRPCError(w, http.StatusNotFound, request.ID, -32601, "Method not found", map[string]any{"method": method})
			return
		}
		params := sampleMCPInitializeParams{}
		_ = decodeSampleMCPParams(request.Params, &params)
		version := protocol
		if sampleMCPSupportedProtocolVersions[params.ProtocolVersion] && params.ProtocolVersion != sampleMCPProtocolLatest {
			version = params.ProtocolVersion
		}
		w.Header().Set("MCP-Protocol-Version", version)
		h.writeSampleMCPRPCResult(w, request.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    sampleMCPServerCapabilities(),
			"serverInfo":      map[string]any{"name": "Sample Plugin MCP", "version": SamplePluginVersion},
			"instructions":    sampleMCPInstructions(),
		})
	case "ping":
		h.writeSampleMCPRPCResult(w, request.ID, map[string]any{})
	case "tools/list":
		result := map[string]any{"tools": sampleMCPToolDefinitions()}
		if modern {
			result["resultType"] = "complete"
		}
		h.writeSampleMCPRPCResult(w, request.ID, result)
	case "tools/call":
		params := sampleMCPToolCallParams{}
		if err := decodeSampleMCPParams(request.Params, &params); err != nil || strings.TrimSpace(params.Name) == "" {
			if err == nil {
				err = fmt.Errorf("tool name is required")
			}
			h.writeSampleMCPRPCError(w, http.StatusOK, request.ID, -32602, "Invalid tools/call params", err.Error())
			return
		}
		payload, err := h.callSampleMCPTool(r.Context(), params.Name, params.Arguments)
		if err != nil {
			if payload == nil {
				payload = map[string]any{}
			}
			payload["success"] = false
			payload["error"] = err.Error()
			h.writeSampleMCPRPCResult(w, request.ID, sampleMCPCallEnvelope(payload, true))
			return
		}
		h.writeSampleMCPRPCResult(w, request.ID, sampleMCPCallEnvelope(payload, false))
	default:
		status := http.StatusOK
		if modern {
			status = http.StatusNotFound
		}
		h.writeSampleMCPRPCError(w, status, request.ID, -32601, "Method not found", map[string]any{"method": method})
	}
}

func validateSampleMCPProtocol(r *http.Request, request sampleMCPRPCRequest) (string, bool, *sampleMCPProtocolError) {
	headerVersion := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version"))
	params := sampleMCPParamsObject(request.Params)
	metadata, _ := params["_meta"].(map[string]any)
	bodyVersion := strings.TrimSpace(fmt.Sprint(metadata[sampleMCPProtocolMetadataKey]))
	if bodyVersion == "<nil>" {
		bodyVersion = ""
	}
	requestedVersion := firstNonEmptySample(bodyVersion, headerVersion)
	if requestedVersion != "" && !sampleMCPSupportedProtocolVersions[requestedVersion] {
		return "", false, &sampleMCPProtocolError{
			HTTPStatus: http.StatusBadRequest,
			Code:       -32022,
			Message:    "Unsupported protocol version",
			Data:       map[string]any{"supported": sampleMCPSortedProtocolVersions(), "requested": requestedVersion},
		}
	}

	modern := headerVersion == sampleMCPProtocolLatest || bodyVersion == sampleMCPProtocolLatest
	if modern {
		if headerVersion == "" || bodyVersion == "" || headerVersion != bodyVersion {
			return "", true, sampleMCPHeaderMismatch("MCP-Protocol-Version 與 request _meta 不一致")
		}
		if methodHeader := strings.TrimSpace(r.Header.Get("Mcp-Method")); methodHeader == "" || methodHeader != request.Method {
			return "", true, sampleMCPHeaderMismatch("Mcp-Method 與 JSON-RPC method 不一致")
		}
		if request.Method == "tools/call" {
			expectedName := strings.TrimSpace(fmt.Sprint(params["name"]))
			if expectedName == "<nil>" {
				expectedName = ""
			}
			if expectedName == "" || strings.TrimSpace(r.Header.Get("Mcp-Name")) != expectedName {
				return "", true, sampleMCPHeaderMismatch("Mcp-Name 與 JSON-RPC params 不一致")
			}
		}
		return bodyVersion, true, nil
	}
	if headerVersion == "" {
		return "2025-03-26", false, nil
	}
	return headerVersion, false, nil
}

func sampleMCPHeaderMismatch(message string) *sampleMCPProtocolError {
	return &sampleMCPProtocolError{
		HTTPStatus: http.StatusBadRequest,
		Code:       -32020,
		Message:    "Header mismatch",
		Data:       map[string]any{"reason": message},
	}
}

func sampleMCPSortedProtocolVersions() []string {
	return []string{sampleMCPProtocolLatest, sampleMCPProtocolLegacyLatest, "2025-06-18", "2025-03-26"}
}

func sampleMCPParamsObject(raw json.RawMessage) map[string]any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}
	}
	var params map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&params); err != nil || params == nil {
		return map[string]any{}
	}
	return params
}

func decodeSampleMCPParams(raw json.RawMessage, output any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	return decoder.Decode(output)
}

func sampleMCPServerCapabilities() map[string]any {
	return map[string]any{"tools": map[string]any{"listChanged": false}}
}

func sampleMCPInstructions() string {
	return "此 Server 是 Sample Plugin 的標準 MCP 開發範例。請先用 tools/list 取得封閉 Schema；讀取、寫入與刪除工具具獨立 annotations 與 permission key。Sample 資料只供示範，不應當成正式持久化資料庫。"
}

func sampleMCPCallEnvelope(structured map[string]any, isError bool) map[string]any {
	textBytes, err := json.Marshal(structured)
	if err != nil {
		textBytes = []byte(`{"success":false,"error":"MCP result encoding failed"}`)
		isError = true
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(textBytes)}},
		"structuredContent": structured,
		"isError":           isError,
	}
}

func (h *HttpAPI_Plugin) writeSampleMCPRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeSampleMCPHTTPJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": sampleMCPRPCID(id), "result": result})
}

func (h *HttpAPI_Plugin) writeSampleMCPRPCError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string, data any) {
	errorValue := map[string]any{"code": code, "message": message}
	if data != nil {
		errorValue["data"] = data
	}
	writeSampleMCPHTTPJSON(w, status, map[string]any{"jsonrpc": "2.0", "id": sampleMCPRPCID(id), "error": errorValue})
}

func sampleMCPRPCID(id json.RawMessage) any {
	if len(bytes.TrimSpace(id)) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(id, &value); err != nil {
		return nil
	}
	return value
}

func writeSampleMCPHTTPJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
}
