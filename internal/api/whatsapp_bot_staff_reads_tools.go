package api

import (
	"context"
	"encoding/json"
	"log"
	"strings"
)

// Coordination id: wa_bot_staff_reads_v2
//
// The missing READ half: the forky reads that botForkyExcludedFromWA keeps
// panel/MCP-only because "their data goes through dedicated WA staff tools
// with the fresh member gate" — and those dedicated tools did not exist yet.
// ONE parameterised tool (staff_domain_read) is that dedicated surface for
// ALL of them (minimum common multiple): the model names the tool and passes
// its arguments, the real assistant path executes it with its exact route
// gates, and the call is only allowed from an allowed (privileged) contact or
// group whose actor re-proves membership (fresh provider fetch).

// botStaffReadTools are the panel/MCP-only reads reachable through
// staff_domain_read (every member of botForkyExcludedFromWA).
var botStaffReadTools = map[string]bool{
	"pos_read":                 true,
	"stock_read":               true,
	"members_read":             true,
	"fichaje_read":             true,
	"sheets_read":              true,
	"menus_read":               true,
	"reservas_read":            true,
	"website_read":             true,
	"campaigns_read":           true,
	"invoices_read":            true,
	"legal_pages_list":         true,
	"site_builder_drafts_list": true,
}

// botToolDefsStaffReads is the catalog of the dedicated staff read surface.
func botToolDefsStaffReads() []botToolDef {
	return []botToolDef{
		{
			Name: "staff_domain_read",
			Description: "Consulta de SOLO LECTURA sobre los datos del panel que el grupo necesita operar: tool elige el dominio ('pos_read', 'stock_read', 'members_read', 'fichaje_read', 'sheets_read', 'menus_read', 'reservas_read', 'website_read', 'campaigns_read', 'invoices_read', 'legal_pages_list', 'site_builder_drafts_list') e input lleva sus argumentos tal cual los describe admin_describe (por ejemplo {\"resource\":\"valuation\"} para stock_read). " +
				"Ejecuta la MISMA consulta del panel con sus permisos exactos; nunca escribe.",
			InputSchema: botSchema(`{"type":"object","properties":{
				"tool":{"type":"string","enum":["pos_read","stock_read","members_read","fichaje_read","sheets_read","menus_read","reservas_read","website_read","campaigns_read","invoices_read","legal_pages_list","site_builder_drafts_list"],"description":"Dominio a consultar"},
				"input":{"type":"object","description":"Argumentos de la herramienta (admin_describe)"}
			},"required":["tool"]}`),
		},
	}
}

// botToolStaffDomainRead executes staff_domain_read: allowed-chat gate with
// fresh membership proof, then the real assistant read path.
func (s *Server) botToolStaffDomainRead(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var in struct {
		Tool  string          `json:"tool"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	in.Tool = strings.TrimSpace(in.Tool)
	if !botStaffReadTools[in.Tool] {
		return botJSON(map[string]any{"error": "tool inválido: " + in.Tool}), nil
	}
	if _, _, ok := s.botPrivilegedControlAuthorize(ctx, restaurantID, msg); !ok {
		log.Printf("[bot] checkpoint wa_bot_staff_reads_v2 restaurant_id=%d denied=actor", restaurantID)
		return botJSON(map[string]any{"error": "no autorizado para esta consulta desde este chat"}), nil
	}
	if len(in.Input) == 0 {
		in.Input = json.RawMessage(`{}`)
	}
	log.Printf("[bot] checkpoint wa_bot_staff_reads_v2 restaurant_id=%d tool=%s", restaurantID, in.Tool)
	return s.assistantExecuteTool(s.botPrivilegedForkyContext(ctx, restaurantID), restaurantID, in.Tool, in.Input)
}
