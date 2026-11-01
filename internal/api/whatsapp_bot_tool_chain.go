package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Coordination id: wa_bot_tool_chain_v1. One privileged custom tool runs an
// ordered batch of other custom tools through the SAME executor, so grants,
// write guards, tenant scope and delivery policies cannot be bypassed. A step
// may feed any JSON value from a previous result into a later input with
// {"$ref":"step_id.path.to.value"}; independent steps simply omit refs.
const botToolChainMaxCalls = 20

var botToolChainIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

type botToolChainCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// The same orchestrator is a first-class Forky tool in the backoffice. Nested
// calls go through assistantExecuteTool, retaining the caller's ACL and audit.
func init() {
	def := botToolChainDef()
	assistantToolRegistry = append(assistantToolRegistry, assistantTool{
		Name: def.Name, Description: def.Description, Schema: def.InputSchema,
		Section: assistantSessionSection, BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			return s.botToolChain(ctx, func(ctx context.Context, name string, input json.RawMessage) (string, error) {
				// A chain runs within ONE model action: there is no human turn in
				// which to approve a token returned by an earlier step. Keep Forky
				// chains read-only; WhatsApp writes retain panel_change/direct policy.
				def, known := assistantToolLookup(name)
				if name == "admin_call" || known && (def.Write || def.Confirm) {
					return botJSON(map[string]any{"error": "en Forky, tool_chain solo ejecuta lecturas: las escrituras requieren una confirmación humana en una llamada separada", "terminal": true}), nil
				}
				return s.assistantExecuteTool(ctx, rid, name, input)
			}, input)
		},
	})
}

func botToolChainDef() botToolDef {
	return botToolDef{
		Name: "tool_chain",
		Description: "Ejecuta hasta 20 custom tools en UNA llamada, en orden, usando los mismos permisos y guardas que una llamada individual. Úsala para varias lecturas/escrituras (por ejemplo crear varios cafés) sin gastar una iteración por elemento. " +
			"calls: [{id, name, input}]. id es único y name es otra herramienta disponible (no tool_chain ni herramientas de envío de mensajes). Para un pipeline, dentro de input usa {\"$ref\":\"id_anterior.ruta.al.valor\"}; la ruta empieza en el resultado JSON de ese paso y también admite índices de arrays. El resultado de cada paso queda disponible para los siguientes y se devuelve completo. Una referencia inválida falla solo ese paso. Si algún paso falla, NO repitas los que tengan ok=true: corrige y ejecuta solo los fallidos para evitar escrituras duplicadas.",
		InputSchema: botSchema(`{"type":"object","additionalProperties":false,"properties":{"calls":{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","description":"Identificador único para referenciar el resultado"},"name":{"type":"string","description":"Nombre de la custom tool"},"input":{"type":"object","description":"Argumentos; cualquier valor puede ser {\"$ref\":\"paso.ruta\"}"}},"required":["id","name","input"]}}},"required":["calls"]}`),
	}
}

func (s *Server) botToolChain(ctx context.Context, exec botToolExecutor, input json.RawMessage) (string, error) {
	var in struct {
		Calls json.RawMessage `json:"calls"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return botJSON(map[string]any{"error": "argumentos de tool_chain ilegibles (JSON inválido o cortado): " + err.Error() + ". Envía menos llamadas por cadena y vuelve a intentarlo."}), nil
	}
	calls, err := botToolChainParseCalls(in.Calls)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	in2 := struct{ Calls []botToolChainCall }{calls}
	if len(in2.Calls) == 0 {
		return botJSON(map[string]any{"error": "calls debe contener al menos una llamada"}), nil
	}
	if len(in2.Calls) > botToolChainMaxCalls {
		return botJSON(map[string]any{"error": fmt.Sprintf("demasiadas llamadas: máximo %d", botToolChainMaxCalls)}), nil
	}

	type stepResult struct {
		Index  int             `json:"index"`
		ID     string          `json:"id"`
		Name   string          `json:"name"`
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result,omitempty"`
		Error  string          `json:"error,omitempty"`
	}
	results := make([]stepResult, 0, len(in2.Calls))
	outputs := make(map[string]any, len(in2.Calls))
	seen := make(map[string]bool, len(in2.Calls))
	succeeded := 0
	terminal := false
	for i, call := range in2.Calls {
		call.ID = strings.TrimSpace(call.ID)
		call.Name = strings.TrimSpace(call.Name)
		result := stepResult{Index: i, ID: call.ID, Name: call.Name}
		var resolved json.RawMessage
		var err error
		switch {
		case !botToolChainIDRe.MatchString(call.ID):
			err = errors.New("id inválido: usa 1-40 letras, números, _ o -")
		case seen[call.ID]:
			err = errors.New("id duplicado")
		case call.Name == "" || call.Name == "tool_chain" || botDeliveryTools[call.Name]:
			err = errors.New("name debe ser otra herramienta de datos o acciones; no se permiten tool_chain ni envíos")
		default:
			seen[call.ID] = true
			resolved, err = botToolChainResolve(call.Input, outputs)
		}
		started := time.Now()
		var out string
		if err == nil {
			out, err = exec(ctx, call.Name, resolved)
		}
		if out == "" && err != nil {
			out = botJSON(map[string]any{"error": err.Error()})
		}
		var decoded any
		if json.Unmarshal([]byte(out), &decoded) != nil {
			decoded = map[string]any{"text": out}
			out = botJSON(decoded)
		}
		result.Result = json.RawMessage(out)
		result.OK = err == nil && !botResultFailed(out)
		if err != nil {
			result.Error = err.Error()
		}
		if result.OK {
			succeeded++
		} else if botToolTerminal(out) {
			terminal = true
		}
		outputs[call.ID] = decoded
		results = append(results, result)
		botCheckpointCtx(ctx, "wa_bot_tool_chain_v1", "index=%d id=%s tool=%s ok=%t ms=%d", i, call.ID, call.Name, result.OK, time.Since(started).Milliseconds())
	}
	response := map[string]any{"tool": "tool_chain", "count": len(results), "succeeded": succeeded, "results": results}
	if terminal {
		response["terminal"] = true
	}
	return botJSON(response), nil
}

// botToolChainParseCalls accepts calls as a JSON array or as a string holding
// that array: models sometimes stringify nested arrays (wa_bot_flexible_types_v1).
// Each call's input may likewise arrive as a JSON string.
func botToolChainParseCalls(raw json.RawMessage) ([]botToolChainCall, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("calls debe contener al menos una llamada")
	}
	if raw[0] == '"' {
		var inner string
		if err := json.Unmarshal(raw, &inner); err != nil {
			return nil, errors.New("calls ilegible: " + err.Error())
		}
		raw = json.RawMessage(inner)
	}
	var calls []botToolChainCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, errors.New("calls debe ser un array de {id, name, input}: " + err.Error())
	}
	for i := range calls {
		in := strings.TrimSpace(string(calls[i].Input))
		if strings.HasPrefix(in, "\"") {
			var inner string
			if json.Unmarshal(calls[i].Input, &inner) == nil && json.Valid([]byte(inner)) {
				calls[i].Input = json.RawMessage(inner)
			}
		}
	}
	return calls, nil
}

// botToolChainResolve substitutes exact {$ref:...} objects recursively. All
// other objects and arrays retain their shape, so refs can fill one field of a
// normal tool input without stringifying structured data.
func botToolChainResolve(raw json.RawMessage, outputs map[string]any) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.New("input JSON inválido")
	}
	resolved, err := botToolChainResolveValue(value, outputs)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(resolved)
	return json.RawMessage(out), err
}

func botToolChainResolveValue(value any, outputs map[string]any) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 1 {
			if ref, ok := v["$ref"].(string); ok {
				return botToolChainLookup(ref, outputs)
			}
		}
		out := make(map[string]any, len(v))
		for key, item := range v {
			resolved, err := botToolChainResolveValue(item, outputs)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			resolved, err := botToolChainResolveValue(item, outputs)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	default:
		return value, nil
	}
}

func botToolChainLookup(ref string, outputs map[string]any) (any, error) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(ref), "."), ".")
	if len(parts) == 0 || parts[0] == "" {
		return nil, errors.New("$ref vacía")
	}
	value, ok := outputs[parts[0]]
	if !ok {
		return nil, fmt.Errorf("$ref apunta a un paso anterior desconocido: %s", parts[0])
	}
	for _, part := range parts[1:] {
		switch current := value.(type) {
		case map[string]any:
			value, ok = current[part]
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(current) {
				return nil, fmt.Errorf("$ref tiene un índice inválido: %s", part)
			}
			value, ok = current[index], true
		default:
			ok = false
		}
		if !ok {
			return nil, fmt.Errorf("$ref no existe: %s", ref)
		}
	}
	return value, nil
}
