package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Coordination id: chatgpt_plugin_v1
//
// The OpenAPI document is generated from assistantToolRegistry, the single
// source of truth for the Forky tools. Each tool becomes one GET operation that
// takes the tool's JSON schema as its query parameters, so the manifest served
// to ChatGPT can never describe a capability the executor would refuse.
//
// This follows the ChatGPT plugin contract: a single authenticated host, the
// mandatory /.well-known/ai-plugin.json manifest, an openapi.yaml served from
// the same host, and bearer-authenticated operations.

// chatgptPluginSpec builds the whole document for a given public base URL.
func chatgptPluginSpec(baseURL string) map[string]any {
	ops := chatgptPluginOperations()
	paths := map[string]any{
		// Tool discovery: ChatGPT reads this to learn which CRUD operations
		// the credential can reach, already filtered by the caller's ACL.
		"/tools": map[string]any{
			"get": map[string]any{
				"operationId": "listTools",
				"summary":     "List the operations this token is allowed to run",
				"description": "Returns every tool of the plugin whose backoffice section the token's role grants. Read and write operations are flagged separately.",
				"tags":        []any{"meta"},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Operations allowed for the active token",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"restaurant_id": map[string]any{"type": "integer"},
										"role":          map[string]any{"type": "string"},
										"sections":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
										"tools":         map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Tool"}},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	for path, op := range ops {
		paths[path] = map[string]any{"get": op}
	}

	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Villa Carmen — ChatGPT Plugin API",
			"description": "Full read/write access to the restaurant backoffice through natural language. Every operation is scoped to the restaurant the bearer token is pinned to and is additionally filtered by the owning user's role, so the plugin can never exceed the permissions of the account that created it. Write operations return a confirmation token that must be echoed back to execute the change.",
			"version":     "1.0.0",
		},
		"servers": []any{map[string]any{"url": baseURL}},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":        "http",
					"scheme":      "bearer",
					"description": "Plugin token generated for a backoffice user. Pin the token in the ChatGPT connector as an API key.",
				},
			},
			"schemas": map[string]any{
				"Tool": map[string]any{
					"type":        "object",
					"description": "One plugin operation with the parameters it accepts.",
					"properties": map[string]any{
						"name":        map[string]any{"type": "string", "description": "Operation name, unique and stable."},
						"description": map[string]any{"type": "string"},
						"section":     map[string]any{"type": "string", "description": "Backoffice ACL section gating this operation."},
						"write":       map[string]any{"type": "boolean", "description": "When true the call first returns a confirmation token."},
						"parameters":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Parameter"}},
					},
					"required": []any{"name", "description", "write", "parameters"},
				},
				"Parameter": map[string]any{
					"type":        "object",
					"description": "A single query parameter accepted by the operation.",
					"properties": map[string]any{
						"name":        map[string]any{"type": "string"},
						"in":          map[string]any{"type": "string", "enum": []any{"query"}},
						"required":    map[string]any{"type": "boolean"},
						"type":        map[string]any{"type": "string", "description": "string, integer, number or boolean."},
						"enum":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"description": map[string]any{"type": "string"},
					},
					"required": []any{"name", "in", "required", "type"},
				},
				"Result": map[string]any{
					"type":        "object",
					"description": "Tool result envelope. Write operations that were not yet confirmed return requires_confirmation plus a single-use confirmation_token valid for two minutes.",
					"properties": map[string]any{
						"success":               map[string]any{"type": "boolean"},
						"tool":                  map[string]any{"type": "string"},
						"requires_confirmation": map[string]any{"type": "boolean"},
						"confirmation_token":    map[string]any{"type": "string"},
						"expires_in_seconds":    map[string]any{"type": "integer"},
					},
				},
			},
		},
		"security": []any{map[string]any{"bearerAuth": []any{}}},
		"paths":    paths,
	}
}

// chatgptPluginOperations maps each registry tool to its OpenAPI operation and
// the URL path the runtime dispatches on.
func chatgptPluginOperations() map[string]map[string]any {
	ops := make(map[string]map[string]any, len(assistantToolRegistry))
	for _, t := range assistantToolRegistry {
		params, required := chatgptPluginParameters(t.Schema)
		summary := t.Description
		operationID := t.Name
		doc := "Read operation. The response is the tool result JSON."
		if t.Write {
			doc = "Write operation. Call it without confirmed to receive a confirmation_token, then call it again with confirmed=true and that token to apply the change."
			operationID = t.Name
			summary = strings.TrimSpace(t.Description + " (requiere confirmación)")
		}
		ops["/tools/"+t.Name] = map[string]any{
			"operationId": operationID,
			"summary":     firstNonEmpty(summary, t.Name),
			"description": doc,
			"tags":        []any{chatgptPluginTag(t.Section)},
			"parameters":  params,
			"responses": map[string]any{
				"200": map[string]any{
					"description": "Tool result",
					"content": map[string]any{
						"application/json": map[string]any{
							"schema": map[string]any{"$ref": "#/components/schemas/Result"},
						},
					},
				},
				"400": chatgptPluginErrorResponse("Invalid arguments for this operation"),
				"401": chatgptPluginErrorResponse("Missing, invalid or revoked plugin token"),
				"403": chatgptPluginErrorResponse("The token's role does not grant this section"),
			},
		}
		_ = required
	}
	return ops
}

// chatgptPluginParameters flattens a tool's JSON schema into OpenAPI query
// parameters. The required list is returned alongside so the runtime can tell
// a missing mandatory argument apart from an omitted optional one.
func chatgptPluginParameters(schema json.RawMessage) ([]any, []string) {
	params := []any{}
	requiredNames := []string{}

	var parsed struct {
		Properties map[string]struct {
			Type        string   `json:"type"`
			Enum        []string `json:"enum"`
			Description string   `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		return params, requiredNames
	}
	required := map[string]bool{}
	for _, name := range parsed.Required {
		required[name] = true
		requiredNames = append(requiredNames, name)
	}
	sort.Strings(requiredNames)

	names := make([]string, 0, len(parsed.Properties))
	for name := range parsed.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		prop := parsed.Properties[name]
		param := map[string]any{
			"name":        name,
			"in":          "query",
			"required":    required[name],
			"type":        chatgptPluginParamType(prop.Type),
			"description": chatgptPluginParamDescription(name, prop.Description),
		}
		if len(prop.Enum) > 0 {
			param["enum"] = prop.Enum
		}
		params = append(params, param)
	}
	return params, requiredNames
}

// chatgptPluginParamType maps a JSON-schema type to the OpenAPI scalar types
// the ChatGPT manifest validator accepts. Anything unusual degrades to string
// rather than emitting an invalid document.
func chatgptPluginParamType(t string) string {
	switch t {
	case "integer", "number", "boolean", "string":
		return t
	default:
		return "string"
	}
}

// chatgptPluginParamDescription documents the well-known parameters in the
// caller's language so the model knows how to fill them without guessing.
func chatgptPluginParamDescription(name, desc string) string {
	if known, ok := map[string]string{
		"confirmed":          "Set to true together with confirmation_token to actually apply a write. Omit it to only receive a confirmation token.",
		"confirmation_token": "Single-use token returned by the previous call of this same operation. Valid for 120 seconds.",
		"date":               "Date as YYYY-MM-DD.",
		"date_from":          "Range start as YYYY-MM-DD.",
		"date_to":            "Range end as YYYY-MM-DD.",
		"time":               "Time as HH:MM.",
		"id":                 "Identifier of the record inside the active restaurant.",
		"resource":           "Resource family to operate on.",
		"search":             "Free-text filter matched against the record name.",
		"limit":              "Maximum number of records to return.",
		"people":             "Number of people in the booking.",
		"name":               "Name of the person or the record.",
	}[name]; ok {
		return known
	}
	if strings.TrimSpace(desc) != "" {
		return desc
	}
	return "Value for " + name + "."
}

// chatgptPluginTag groups operations in the manifest UI by ACL section.
func chatgptPluginTag(section string) string {
	if section == "" {
		return "general"
	}
	return section
}

func chatgptPluginErrorResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"success": map[string]any{"type": "boolean"},
						"message": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

// chatgptPluginOpenAPIYAML renders the document as YAML for the manifest's
// openapi.yaml reference. Emitting it directly avoids taking a YAML dependency
// for a document whose shape is fully static.
func chatgptPluginOpenAPIYAML(baseURL string) string {
	spec := chatgptPluginSpec(baseURL)
	var b strings.Builder
	b.WriteString("openapi: 3.0.3\n")
	b.WriteString("info:\n")
	b.WriteString("  title: Villa Carmen — ChatGPT Plugin API\n")
	b.WriteString(fmt.Sprintf("  version: %s\n", spec["info"].(map[string]any)["version"]))
	b.WriteString("servers:\n")
	b.WriteString("  - url: " + yamlScalar(baseURL) + "\n")
	b.WriteString("security:\n")
	b.WriteString("  - bearerAuth: []\n")
	b.WriteString("components:\n")
	b.WriteString("  securitySchemes:\n")
	b.WriteString("    bearerAuth:\n")
	b.WriteString("      type: http\n")
	b.WriteString("      scheme: bearer\n")
	b.WriteString("paths:\n")
	// Paths are sorted so the manifest is byte-stable across restarts.
	paths := spec["paths"].(map[string]any)
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		op := paths[name].(map[string]any)["get"].(map[string]any)
		b.WriteString("  " + yamlScalar(name) + ":\n")
		b.WriteString("    get:\n")
		b.WriteString("      operationId: " + yamlScalar(op["operationId"].(string)) + "\n")
		b.WriteString("      summary: " + yamlScalar(op["summary"].(string)) + "\n")
		if params, ok := op["parameters"].([]any); ok && len(params) > 0 {
			b.WriteString("      parameters:\n")
			for _, p := range params {
				pm := p.(map[string]any)
				b.WriteString("        - name: " + yamlScalar(pm["name"].(string)) + "\n")
				b.WriteString("          in: query\n")
				b.WriteString("          required: " + fmt.Sprint(pm["required"]) + "\n")
				b.WriteString("          schema:\n")
				b.WriteString("            type: " + pm["type"].(string) + "\n")
			}
		}
		b.WriteString("      responses:\n")
		b.WriteString("        '200':\n")
		b.WriteString("          description: Tool result\n")
		b.WriteString("        '401':\n")
		b.WriteString("          description: Missing, invalid or revoked plugin token\n")
	}
	return b.String()
}

// yamlScalar quotes a value so paths, colons and non-ASCII titles always parse.
func yamlScalar(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// chatgptPluginManifest builds ai-plugin.json. api.url points at the same
// host that serves the document, which is what the plugin contract requires.
func chatgptPluginManifest(baseURL string) map[string]any {
	return map[string]any{
		"schema_version":        "v1",
		"name_for_human":        "Villa Carmen",
		"name_for_model":        "villacarmen",
		"description_for_human": "Consult and manage a Villa Carmen restaurant from ChatGPT: bookings, menus, catalog, stock, POS, members, invoices, schedules, time clock, settings and reports.",
		"description_for_model": "Read and write every area of the Villa Carmen restaurant backoffice in natural language. The credential is bound to one restaurant and to the role of the backoffice user that created it, so only the operations that user is allowed to perform are listed. Write operations are two-step: the first call returns a single-use confirmation_token that the second call must echo back.",
		"auth": map[string]any{
			"type": "bearer",
			// instructions is what the user pastes into the connector; the raw
			// token is only ever shown at generation time.
			"instructions": "Paste the plugin token generated with `go run ./cmd/chatgpt-plugin-token --user <id> --restaurant <id>`. It is bound to that user and restaurant and inherits their role permissions.",
		},
		"api": map[string]any{
			"type": "openapi",
			"url":  strings.TrimSuffix(baseURL, "/") + "/openapi.yaml",
		},
		"logo_url":       strings.TrimSuffix(baseURL, "/") + "/chatgpt-plugin-logo.png",
		"contact_email":  "soporte@villacarmen.com",
		"legal_info_url": strings.TrimSuffix(baseURL, "/") + "/legal",
	}
}

// writeYAMLContentType serves the manifest documents. YAML is served as plain
// text so the connector fetches it verbatim.
func writeChatGPTPluginYAML(w http.ResponseWriter, doc string) {
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(doc))
}
