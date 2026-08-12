package sampleplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func sampleMCPToolDefinitions() []map[string]any {
	return []map[string]any{
		sampleMCPTool("sample.status", "讀取 Sample 狀態", "讀取 Plugin 載入、設定、項目、工作及 Host Auth 可用狀態；不回傳 token。", sampleMCPObjectSchema(map[string]any{}, nil), sampleMCPOutputSchema("status"), true, false, true, false, "mcp_read"),
		sampleMCPTool("sample.hello", "Sample 握手", "取得 Sample Plugin ID、名稱、版本及目前時間。", sampleMCPObjectSchema(map[string]any{}, nil), sampleMCPOutputSchema("hello"), true, false, true, false, "mcp_read"),
		sampleMCPTool("sample.items.list", "列出 Sample 項目", "列出目前 runtime 記憶體中的 Sample 項目。", sampleMCPObjectSchema(map[string]any{}, nil), sampleMCPOutputSchema("items"), true, false, true, false, "mcp_read"),
		sampleMCPTool("sample.items.get", "讀取 Sample 項目", "依穩定 ID 讀取一筆 Sample 項目。", sampleMCPObjectSchema(map[string]any{
			"id": sampleMCPIDSchema("項目 ID。"),
		}, []string{"id"}), sampleMCPOutputSchema("item"), true, false, true, false, "mcp_read"),
		sampleMCPTool("sample.items.create", "建立 Sample 項目", "建立一筆記憶體內 Sample 項目；省略 ID 時由服務產生。", sampleMCPObjectSchema(sampleMCPItemProperties(true), []string{"name"}), sampleMCPOutputSchema("item"), false, false, false, false, "mcp_write"),
		sampleMCPTool("sample.items.update", "更新 Sample 項目", "依 ID 完整更新一筆 Sample 項目的名稱、值與 metadata。", sampleMCPObjectSchema(sampleMCPItemProperties(false), []string{"id", "name"}), sampleMCPOutputSchema("item"), false, false, true, false, "mcp_write"),
		sampleMCPTool("sample.items.delete", "刪除 Sample 項目", "依 ID 刪除一筆 Sample 項目。", sampleMCPObjectSchema(map[string]any{
			"id": sampleMCPIDSchema("要刪除的項目 ID。"),
		}, []string{"id"}), sampleMCPOutputSchema("delete"), false, true, true, false, "mcp_delete"),
		sampleMCPTool("sample.jobs.start", "啟動 Sample 背景工作", "啟動非同步示範工作並立即回傳 job ID；使用 sample.jobs.get 輪詢進度。", sampleMCPObjectSchema(map[string]any{
			"message": map[string]any{"type": "string", "maxLength": 500, "default": "sample background job"},
		}, nil), sampleMCPOutputSchema("job"), false, false, false, false, "mcp_write"),
		sampleMCPTool("sample.jobs.get", "讀取 Sample 背景工作", "依 job ID 讀取背景工作的狀態與進度。", sampleMCPObjectSchema(map[string]any{
			"id": sampleMCPIDSchema("背景工作 ID。"),
		}, []string{"id"}), sampleMCPOutputSchema("job"), true, false, true, false, "mcp_read"),
	}
}

func sampleMCPPermissionSettings() map[string]any {
	return map[string]any{
		"title":       "Sample MCP 權限",
		"description": "示範 Account Manager 如何保存 MCP 讀取、寫入與刪除能力偏好；實際呼叫仍由主系統帳號權限、MCP key scopes、allow_write、allow_delete 與工具白名單共同限制。",
		"items": []map[string]any{
			{"key": "mcp_read", "label": "MCP 讀取", "description": "允許讀取 Sample 狀態、項目與背景工作結果。", "type": "boolean", "default": false},
			{"key": "mcp_write", "label": "MCP 寫入", "description": "允許建立或更新 Sample 項目，以及啟動背景工作。", "type": "boolean", "default": false},
			{"key": "mcp_delete", "label": "MCP 刪除", "description": "允許刪除 Sample 項目。", "type": "boolean", "default": false},
		},
	}
}

func sampleConfigSettings() map[string]any {
	return map[string]any{
		"title":             "Sample Plugin 設定",
		"description":       "示範新版主系統以參數表單修改 Plugin 基本設定，並保留未顯示的完整 JSON。",
		"mode":              "json",
		"reload_after_save": true,
		"basic_parameters": []map[string]any{
			{"key": "title", "label": "顯示名稱", "type": "string", "default": "Sample Plugin", "help": "主頁主要卡片顯示名稱。"},
			{"key": "default_group", "label": "預設群組", "type": "string", "default": "開發工具", "help": "使用者尚未自行編排主頁時採用的建議群組。"},
			{"key": "message", "label": "範例訊息", "type": "string", "default": "Hello from Sample Plugin", "help": "Sample 頁面與 API 使用的示範訊息。"},
		},
	}
}

func sampleMCPMetadata() map[string]any {
	return map[string]any{
		"name":                "Sample Plugin MCP",
		"description":         "標準無狀態 MCP JSON-RPC 開發範例。",
		"version":             SamplePluginVersion,
		"endpoint":            "/mcp",
		"transport":           "streamable-http-stateless-json",
		"supported_protocols": sampleMCPSortedProtocolVersions(),
		"methods":             []string{"server/discover", "initialize", "ping", "tools/list", "tools/call"},
		"permission_settings": sampleMCPPermissionSettings(),
		"tools":               sampleMCPToolDefinitions(),
	}
}

func sampleMCPTool(name, title, description string, inputSchema, outputSchema map[string]any, readOnly, destructive, idempotent, openWorld bool, permission string) map[string]any {
	return map[string]any{
		"name":         name,
		"title":        title,
		"description":  description,
		"inputSchema":  inputSchema,
		"outputSchema": outputSchema,
		"annotations": map[string]any{
			"title":           title,
			"readOnlyHint":    readOnly,
			"destructiveHint": destructive,
			"idempotentHint":  idempotent,
			"openWorldHint":   openWorld,
		},
		"_meta": map[string]any{
			"io.agenticservice/permission": permission,
			"io.agenticservice.sample/executionLimits": map[string]any{
				"request_max_bytes":  sampleMCPMaxRequestBytes,
				"response_max_bytes": sampleMCPMaxResponseBytes,
				"storage":            "runtime_memory",
			},
		},
	}
}

func sampleMCPObjectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func sampleMCPIDSchema(description string) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": `^[A-Za-z0-9._-]+$`, "description": description}
}

func sampleMCPItemProperties(idOptional bool) map[string]any {
	idDescription := "項目 ID。"
	if idOptional {
		idDescription += "省略時由服務產生。"
	}
	return map[string]any{
		"id":       sampleMCPIDSchema(idDescription),
		"name":     map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": "項目名稱。"},
		"value":    map[string]any{"description": "任意 JSON 相容示範值。"},
		"metadata": map[string]any{"type": "object", "additionalProperties": true, "description": "選填的 JSON metadata。"},
	}
}

func sampleMCPOutputSchema(kind string) map[string]any {
	properties := map[string]any{"success": map[string]any{"type": "boolean"}}
	switch kind {
	case "status":
		properties["plugin"] = map[string]any{"type": "object", "additionalProperties": true}
	case "hello":
		properties["hello"] = map[string]any{"type": "object", "additionalProperties": true}
	case "items":
		properties["items"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": true}}
	case "item":
		properties["item"] = map[string]any{"type": "object", "additionalProperties": true}
	case "delete":
		properties["id"] = map[string]any{"type": "string"}
		properties["deleted"] = map[string]any{"type": "boolean"}
	case "job":
		properties["job"] = map[string]any{"type": "object", "additionalProperties": true}
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             []string{"success"},
		"additionalProperties": false,
	}
}

type sampleMCPIDArguments struct {
	ID string `json:"id"`
}

type sampleMCPItemArguments struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Value    any            `json:"value,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type sampleMCPJobArguments struct {
	Message string `json:"message,omitempty"`
}

func (h *HttpAPI_Plugin) callSampleMCPTool(ctx context.Context, name string, arguments map[string]any) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	payload, err := h.executeSampleMCPTool(ctx, strings.TrimSpace(name), arguments)
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, encodeErr := json.Marshal(payload)
	if encodeErr != nil {
		return nil, fmt.Errorf("MCP result encoding failed: %w", encodeErr)
	}
	if len(encoded) > sampleMCPMaxResponseBytes {
		return nil, fmt.Errorf("MCP result exceeds 2 MiB")
	}
	return payload, err
}

func (h *HttpAPI_Plugin) executeSampleMCPTool(ctx context.Context, name string, arguments map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch name {
	case "sample.status":
		if err := requireEmptySampleMCPArguments(arguments); err != nil {
			return nil, err
		}
		return map[string]any{"success": true, "plugin": h.statusPayload()}, nil
	case "sample.hello":
		if err := requireEmptySampleMCPArguments(arguments); err != nil {
			return nil, err
		}
		return map[string]any{"success": true, "hello": map[string]any{
			"id": "sample", "name": "Sample Plugin", "version": SamplePluginVersion, "time": time.Now().Format(time.RFC3339),
		}}, nil
	case "sample.items.list":
		if err := requireEmptySampleMCPArguments(arguments); err != nil {
			return nil, err
		}
		if err := h.ensureLoaded(); err != nil {
			return nil, err
		}
		return map[string]any{"success": true, "items": h.listItems()}, nil
	case "sample.items.get":
		args := sampleMCPIDArguments{}
		if err := decodeSampleMCPArguments(arguments, &args); err != nil {
			return nil, err
		}
		id, err := validateSampleMCPID(args.ID)
		if err != nil {
			return nil, err
		}
		if err := h.ensureLoaded(); err != nil {
			return nil, err
		}
		item, ok := h.getItem(id)
		if !ok {
			return map[string]any{"id": id}, errors.New("item not found")
		}
		return map[string]any{"success": true, "item": item}, nil
	case "sample.items.create":
		args := sampleMCPItemArguments{}
		if err := decodeSampleMCPArguments(arguments, &args); err != nil {
			return nil, err
		}
		return h.createSampleMCPItem(args)
	case "sample.items.update":
		args := sampleMCPItemArguments{}
		if err := decodeSampleMCPArguments(arguments, &args); err != nil {
			return nil, err
		}
		return h.updateSampleMCPItem(args)
	case "sample.items.delete":
		args := sampleMCPIDArguments{}
		if err := decodeSampleMCPArguments(arguments, &args); err != nil {
			return nil, err
		}
		return h.deleteSampleMCPItem(args.ID)
	case "sample.jobs.start":
		args := sampleMCPJobArguments{}
		if err := decodeSampleMCPArguments(arguments, &args); err != nil {
			return nil, err
		}
		message := strings.TrimSpace(args.Message)
		if len(message) > 500 {
			return nil, errors.New("message exceeds 500 characters")
		}
		if message == "" {
			message = "sample background job"
		}
		job := &sampleJob{
			ID: nextSampleID("job"), Status: "queued", Message: message,
			CreatedAt: time.Now().Format(time.RFC3339), UpdatedAt: time.Now().Format(time.RFC3339),
		}
		h.mu.Lock()
		if h.jobs == nil {
			h.jobs = map[string]*sampleJob{}
		}
		h.jobs[job.ID] = job
		h.mu.Unlock()
		jobSnapshot := *job
		go h.runSampleJob(job.ID)
		return map[string]any{"success": true, "job": jobSnapshot}, nil
	case "sample.jobs.get":
		args := sampleMCPIDArguments{}
		if err := decodeSampleMCPArguments(arguments, &args); err != nil {
			return nil, err
		}
		id, err := validateSampleMCPID(args.ID)
		if err != nil {
			return nil, err
		}
		job, ok := h.getJob(id)
		if !ok {
			return map[string]any{"id": id}, errors.New("job not found")
		}
		return map[string]any{"success": true, "job": job}, nil
	default:
		return nil, fmt.Errorf("unknown MCP tool: %s", name)
	}
}

func (h *HttpAPI_Plugin) createSampleMCPItem(args sampleMCPItemArguments) (map[string]any, error) {
	if err := h.ensureLoaded(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	if len(name) > 200 {
		return nil, errors.New("name exceeds 200 characters")
	}
	id := strings.TrimSpace(args.ID)
	if id != "" {
		var err error
		id, err = validateSampleMCPID(id)
		if err != nil {
			return nil, err
		}
	} else {
		id = nextSampleID("item")
	}
	now := time.Now().Format(time.RFC3339)
	item := sampleItem{ID: id, Name: name, Value: args.Value, Metadata: args.Metadata, CreatedAt: now, UpdatedAt: now}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.items == nil {
		h.items = map[string]sampleItem{}
	}
	if _, exists := h.items[id]; exists {
		return map[string]any{"id": id}, errors.New("item already exists")
	}
	h.items[id] = item
	return map[string]any{"success": true, "item": item}, nil
}

func (h *HttpAPI_Plugin) updateSampleMCPItem(args sampleMCPItemArguments) (map[string]any, error) {
	if err := h.ensureLoaded(); err != nil {
		return nil, err
	}
	id, err := validateSampleMCPID(args.ID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	if len(name) > 200 {
		return nil, errors.New("name exceeds 200 characters")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	old, exists := h.items[id]
	if !exists {
		return map[string]any{"id": id}, errors.New("item not found")
	}
	item := sampleItem{ID: id, Name: name, Value: args.Value, Metadata: args.Metadata, CreatedAt: old.CreatedAt, UpdatedAt: time.Now().Format(time.RFC3339)}
	h.items[id] = item
	return map[string]any{"success": true, "item": item}, nil
}

func (h *HttpAPI_Plugin) deleteSampleMCPItem(rawID string) (map[string]any, error) {
	if err := h.ensureLoaded(); err != nil {
		return nil, err
	}
	id, err := validateSampleMCPID(rawID)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.items[id]; !exists {
		return map[string]any{"id": id, "deleted": false}, errors.New("item not found")
	}
	delete(h.items, id)
	return map[string]any{"success": true, "id": id, "deleted": true}, nil
}

func validateSampleMCPID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", errors.New("id is required")
	}
	if len(id) > 128 {
		return "", errors.New("id exceeds 128 characters")
	}
	for _, char := range id {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return "", errors.New("id contains unsupported characters")
	}
	return id, nil
}

func requireEmptySampleMCPArguments(arguments map[string]any) error {
	if len(arguments) > 0 {
		return errors.New("this tool does not accept arguments")
	}
	return nil
}

func decodeSampleMCPArguments(arguments map[string]any, output any) error {
	if arguments == nil {
		arguments = map[string]any{}
	}
	data, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}
