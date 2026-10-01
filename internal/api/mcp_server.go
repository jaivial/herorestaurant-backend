package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// MCP (Model Context Protocol) server exposing the same tool registry that the
// ChatGPT plugin uses. Tools are generated from assistantToolRegistry, so the
// MCP surface can never advertise an operation the executor would refuse, and
// every call still passes through assistantToolAllowed.
//
// Protocol: JSON-RPC 2.0 over HTTP POST at /mcp.
// Authentication: OAuth 2.1 authorization code with PKCE, at /mcp/oauth/*.
//
// Coordination id: mcp_server_v1

const mcpProtocolVersion = "2025-06-18"

// errMCPToolForbidden is returned when a token is valid but the caller's role
// does not grant the requested tool.
var errMCPToolForbidden = errors.New("Forbidden")

type mcpJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpJSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpJSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *mcpJSONRPCError `json:"error,omitempty"`
}

// JSON-RPC error codes used by this server.
const (
	mcpErrParse          = -32700
	mcpErrInvalidRequest = -32600
	mcpErrMethodNotFound = -32601
	mcpErrInvalidParams  = -32602
	mcpErrUnauthorized   = -32001
	mcpErrForbidden      = -32003
)

func mcpWrite(w http.ResponseWriter, status int, id json.RawMessage, result any, rerr *mcpJSONRPCError) {
	body := mcpJSONRPCResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rerr}
	httpx.WriteJSON(w, status, body)
}

// mcpWriteError maps a Go error onto a JSON-RPC error code. Authorization
// failures keep the same meaning as the plugin: a token the caller may not use
// is reported as forbidden, never as an internal error that would leak state.
func mcpWriteError(w http.ResponseWriter, id json.RawMessage, status int, err error) {
	code := mcpErrInvalidParams
	if status == http.StatusUnauthorized {
		code = mcpErrUnauthorized
	} else if status == http.StatusForbidden {
		code = mcpErrForbidden
	}
	mcpWrite(w, status, id, nil, &mcpJSONRPCError{Code: code, Message: err.Error()})
}

// HandleMCP is the single JSON-RPC entry point. It authenticates first, so an
// unauthenticated caller never reaches tool enumeration.
func (s *Server) HandleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		httpx.WriteError(w, http.StatusMethodNotAllowed, "Use POST")
		return
	}
	auth, err := s.mcpRequireAuth(r)
	if err != nil {
		// OAuth 2.1: a 401 carrying WWW-Authenticate is what tells an MCP client
		// to start the authorization flow rather than retrying blindly.
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", error_description="Run the OAuth authorization flow first"`)
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req mcpJSONRPCRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, chatgptPluginMaxBodyBytes)).Decode(&req); err != nil {
		mcpWrite(w, http.StatusBadRequest, nil, nil, &mcpJSONRPCError{Code: mcpErrParse, Message: "invalid JSON-RPC request"})
		return
	}
	if strings.TrimSpace(req.Method) == "" {
		mcpWrite(w, http.StatusBadRequest, req.ID, nil, &mcpJSONRPCError{Code: mcpErrInvalidRequest, Message: "method is required"})
		return
	}

	switch req.Method {
	case "initialize":
		mcpWrite(w, http.StatusOK, req.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "villacarmen", "version": "1.0.0"},
		}, nil)
	case "notifications/initialized", "ping":
		mcpWrite(w, http.StatusOK, req.ID, map[string]any{}, nil)
	case "tools/list":
		mcpWrite(w, http.StatusOK, req.ID, map[string]any{"tools": s.mcpToolDefinitions(auth)}, nil)
	case "tools/call":
		s.mcpToolsCall(w, r, req, auth)
	default:
		mcpWrite(w, http.StatusNotFound, req.ID, nil, &mcpJSONRPCError{Code: mcpErrMethodNotFound, Message: "unknown method: " + req.Method})
	}
}

// mcpToolDefinitions lists only the operations this caller is allowed to run,
// using the exact same ACL gate as the plugin and the backoffice.
func (s *Server) mcpToolDefinitions(auth boAuth) []any {
	out := []any{}
	for _, t := range assistantToolRegistry {
		if !assistantToolAllowed(auth, t.Name) {
			continue
		}
		var schema any = map[string]any{"type": "object", "properties": map[string]any{}}
		var parsed map[string]any
		if json.Unmarshal(t.Schema, &parsed) == nil && parsed != nil {
			schema = parsed
		}
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		})
	}
	return out
}

// mcpToolsCall runs one tool under the caller's own restaurant, keeping the
// plugin's two-step confirmation for every write.
func (s *Server) mcpToolsCall(w http.ResponseWriter, r *http.Request, req mcpJSONRPCRequest, auth boAuth) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			mcpWrite(w, http.StatusBadRequest, req.ID, nil, &mcpJSONRPCError{Code: mcpErrInvalidParams, Message: "invalid params"})
			return
		}
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		mcpWrite(w, http.StatusBadRequest, req.ID, nil, &mcpJSONRPCError{Code: mcpErrInvalidParams, Message: "tool name is required"})
		return
	}
	// Authorize before any work.
	if !assistantToolAllowed(auth, name) {
		if _, exists := assistantToolLookup(name); !exists {
			mcpWrite(w, http.StatusNotFound, req.ID, nil, &mcpJSONRPCError{Code: mcpErrInvalidParams, Message: "Unknown tool"})
			return
		}
		mcpWriteError(w, req.ID, http.StatusForbidden, errMCPToolForbidden)
		return
	}

	input := params.Arguments
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}

	ctx := r.Context()
	ctx = withBOAuth(ctx, auth)
	ctx = withRestaurantID(ctx, auth.ActiveRestaurantID)

	out, err := s.assistantExecuteTool(ctx, auth.ActiveRestaurantID, name, input)
	if err != nil {
		mcpWriteError(w, req.ID, chatgptPluginStatusForError(err), err)
		return
	}
	// MCP returns tool output as content blocks; the payload is the decoded
	// tool JSON so the model sees structured data, not a quoted string.
	var payload any
	if json.Unmarshal([]byte(out), &payload) != nil {
		payload = map[string]any{"result": out}
	}
	text, err := json.Marshal(payload)
	if err != nil {
		text = []byte(out)
	}
	mcpWrite(w, http.StatusOK, req.ID, map[string]any{
		"content": []any{map[string]any{"type": "text", "text": string(text)}},
		"isError": false,
	}, nil)
}
