package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

// Coordination id: wa_bot_tool_input_coercion_v1
//
// Models call custom tools with JSON they build by hand and they quote typed
// values: {"party_size":"8"}, {"menu_id":"52"}, {"confirmed":"true"}, even a
// whole array as one JSON string. The handlers decode into typed structs, so
// ONE quoted field failed the whole decode and the tool answered with an
// unrelated validation error (prod 2026-11-19: a privileged-group order lost
// four tool_chain rounds to quoted party_size/menu_id/booking_id until the
// consecutive-failures guard cut the turn).
//
// This is the reusable fix: ONE schema-driven coercion applied at the two
// dispatch boundaries (botExecuteTool for the WhatsApp bot tools and
// assistantExecuteTool for the Forky registry), so every custom tool —
// present and future — accepts the types its own schema declares without a
// per-tool lenient decoder. tool_chain steps inherit it because they run
// through the same executors.
//
// Rules: a value that already matches the declared type passes through
// untouched (unchanged input stays byte-identical); a string is parsed when
// the schema declares integer/number/boolean/array/object; anything else is
// a clear error naming the parameter and the expected type, so the model can
// correct the call instead of reading a failed struct decode.

// botToolInputSchemas maps every WhatsApp bot tool name to its declared input
// schema. It is built from the SAME catalog constructors the webhook merges
// for a turn, so a tool added there is covered here without touching this
// file.
var botToolInputSchemas = sync.OnceValue(func() map[string]json.RawMessage {
	defs := botMergeToolDefs(
		botToolDefsForChat(botTenantConfig{}, true),
		botSpecialAdminToolDefs(),
		botToolDefsAdminReads(),
		botToolDefsPrivilegedControl(),
	)
	schemas := make(map[string]json.RawMessage, len(defs))
	for _, d := range defs {
		if len(d.InputSchema) > 0 {
			schemas[d.Name] = d.InputSchema
		}
	}
	return schemas
})

// botCoerceToolInput rewrites input so each value matches the type the tool's
// JSON schema declares, and reports whether the bytes changed. A value that
// cannot be parsed as the declared type is an error naming the parameter: the
// caller returns it as the tool result so the model fixes its call.
func botCoerceToolInput(schema, input json.RawMessage) (json.RawMessage, bool, error) {
	if len(bytes.TrimSpace(schema)) == 0 {
		return input, false, nil
	}
	raw := bytes.TrimSpace(input)
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		// Not valid JSON at all: keep the handler's own decode error, which
		// already covers that case.
		return input, false, nil
	}
	coerced, changed, err := botCoerceSchemaValue(schema, value, "")
	if err != nil || !changed {
		return input, false, err
	}
	out, err := json.Marshal(coerced)
	if err != nil {
		return input, false, nil
	}
	return json.RawMessage(out), true, nil
}

// botSchemaType reads only the "type" of a schema node. Anything richer
// (unions, compositors, a missing type) is left to the handlers.
func botSchemaType(schema json.RawMessage) string {
	var node struct {
		Type json.RawMessage `json:"type"`
	}
	if json.Unmarshal(schema, &node) != nil {
		return ""
	}
	var typ string
	if json.Unmarshal(node.Type, &typ) != nil {
		return ""
	}
	return typ
}

func botSchemaObjectProps(schema json.RawMessage) map[string]json.RawMessage {
	var node struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(schema, &node) != nil {
		return nil
	}
	return node.Properties
}

func botSchemaItems(schema json.RawMessage) json.RawMessage {
	var node struct {
		Items json.RawMessage `json:"items"`
	}
	if json.Unmarshal(schema, &node) != nil {
		return nil
	}
	return node.Items
}

// botCoerceSchemaValue coerces one value against its schema node and reports
// whether anything changed: maps and slices are mutated in place, so change
// detection cannot compare before/after bytes and is carried as a flag.
func botCoerceSchemaValue(schema json.RawMessage, value any, path string) (any, bool, error) {
	typ := botSchemaType(schema)
	if typ == "" || value == nil {
		return value, false, nil
	}
	switch typ {
	case "integer", "number":
		switch v := value.(type) {
		case float64:
			if typ == "integer" && v != math.Trunc(v) {
				return nil, false, botCoerceTypeError(path, botCoerceTypeLabel(typ), value)
			}
			return value, false, nil
		case string:
			f, perr := botParseFlexibleNumber(v)
			if perr != nil {
				return nil, false, botCoerceTypeError(path, botCoerceTypeLabel(typ), value)
			}
			if typ == "integer" {
				if f != math.Trunc(f) {
					return nil, false, botCoerceTypeError(path, botCoerceTypeLabel(typ), value)
				}
				return int64(f), true, nil
			}
			return f, true, nil
		default:
			return nil, false, botCoerceTypeError(path, botCoerceTypeLabel(typ), value)
		}
	case "boolean":
		switch v := value.(type) {
		case bool:
			return value, false, nil
		case string:
			b, perr := botParseFlexibleBool(v)
			if perr != nil {
				return nil, false, botCoerceTypeError(path, "un booleano (true/false)", value)
			}
			return b, true, nil
		case float64:
			if v == 1 {
				return true, true, nil
			}
			if v == 0 {
				return false, true, nil
			}
			return nil, false, botCoerceTypeError(path, "un booleano (true/false)", value)
		default:
			return nil, false, botCoerceTypeError(path, "un booleano (true/false)", value)
		}
	case "array":
		changed := false
		if s, ok := value.(string); ok {
			var arr []any
			if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &arr); err != nil {
				return nil, false, botCoerceTypeError(path, "un array JSON", value)
			}
			value = arr
			changed = true
		}
		arr, ok := value.([]any)
		if !ok {
			return nil, false, botCoerceTypeError(path, "un array JSON", value)
		}
		items := botSchemaItems(schema)
		if len(items) == 0 {
			return value, changed, nil
		}
		for i, item := range arr {
			coerced, childChanged, err := botCoerceSchemaValue(items, item, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, false, err
			}
			if childChanged {
				arr[i] = coerced
				changed = true
			}
		}
		return arr, changed, nil
	case "object":
		changed := false
		if s, ok := value.(string); ok {
			var obj map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &obj); err != nil {
				return nil, false, botCoerceTypeError(path, "un objeto JSON", value)
			}
			value = obj
			changed = true
		}
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, false, botCoerceTypeError(path, "un objeto JSON", value)
		}
		for key, propSchema := range botSchemaObjectProps(schema) {
			item, present := obj[key]
			if !present {
				continue
			}
			fieldPath := key
			if path != "" {
				fieldPath = path + "." + key
			}
			coerced, childChanged, err := botCoerceSchemaValue(propSchema, item, fieldPath)
			if err != nil {
				return nil, false, err
			}
			if childChanged {
				obj[key] = coerced
				changed = true
			}
		}
		return obj, changed, nil
	default:
		// "string" and unknown types pass through.
		return value, false, nil
	}
}

func botCoerceTypeLabel(typ string) string {
	if typ == "integer" {
		return "un número entero"
	}
	return "un número"
}

// botCoerceTypeError names the offending parameter, the type the schema
// declares and the value received, so the model's next call gets it right.
func botCoerceTypeError(path, expected string, value any) error {
	got := "un valor ilegible"
	if raw, err := json.Marshal(value); err == nil {
		got = string(raw)
		if len(got) > 60 {
			got = got[:60] + "…"
		}
	}
	if path == "" {
		path = "de entrada"
	}
	return fmt.Errorf("el parámetro %q debe ser %s y llegó %s; corrige el tipo y vuelve a llamar", path, expected, got)
}

// botParseFlexibleNumber accepts "12.5", "12,5" and "12 €" besides plain
// JSON-number strings. An empty string is an error here, not a zero.
func botParseFlexibleNumber(s string) (float64, error) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "€"))
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		return 0, fmt.Errorf("número vacío")
	}
	return strconv.ParseFloat(s, 64)
}

// botParseFlexibleBool mirrors flexBool for the schema path; an empty string
// is an error, not a false.
func botParseFlexibleBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "si", "sí", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("booleano no válido: %q", s)
}
