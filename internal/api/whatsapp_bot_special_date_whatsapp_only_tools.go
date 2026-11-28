package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Coordination id: special_date_whatsapp_only_v1 (WhatsApp half)
//
// manage_special_date_whatsapp_only: «Reservas solo por WhatsApp» of a special
// date for the allowed (privileged) contacts and groups, over the shared core
// (special_date_whatsapp_only_tools.go). 'get' answers at once; 'set' becomes a
// propose+OK proposal (op "special_date_whatsapp_only") that only the requester
// applies with "OK <código>" in the same chat (or at once with
// privileged_direct_writes).

const botSpecialDateWhatsAppOnlyOp = "special_date_whatsapp_only"

func botToolDefsSpecialDateWhatsAppOnly() []botToolDef {
	return []botToolDef{{
		Name: "manage_special_date_whatsapp_only",
		Description: "«Reservas solo por WhatsApp» de las fechas especiales (festivas), como el interruptor de /app/reservas/especial. Activado, la web no deja reservar ni prereservar ese día, " +
			"enseña un aviso con el WhatsApp del restaurante y el bot no manda el enlace de la web. action 'get' (inmediata): sin date, las fechas especiales desde hoy con su whatsapp_only y el número; con date, esa fecha. " +
			"action 'set' (date + whatsapp_only true/false) NO aplica nada: envía al chat una propuesta con código y quien la pidió confirma con 'OK <código>'. Para este interruptor usa SIEMPRE esta herramienta, nunca panel_change.",
		InputSchema: botSchema(`{"type":"object","properties":{
			"action":{"type":"string","enum":["get","set"]},
			"date":{"type":"string","description":"YYYY-MM-DD de la fecha especial (obligatoria en set)"},
			"whatsapp_only":{"type":"boolean","description":"Solo set: true activa, false desactiva"}
		},"required":["action"]}`),
	}}
}

// botToolManageSpecialDateWhatsAppOnly executes manage_special_date_whatsapp_only.
func (s *Server) botToolManageSpecialDateWhatsAppOnly(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var a specialDateWhatsAppOnlyArgs
	if err := json.Unmarshal(input, &a); err != nil {
		return botJSON(map[string]any{"error": "argumentos ilegibles"}), nil
	}
	a.Action = strings.ToLower(strings.TrimSpace(a.Action))
	if d, ok := botNormaliseToolDate(a.Date); ok {
		a.Date = d
	}
	actor, origin, ok := s.botPrivilegedControlAuthorize(ctx, rid, msg)
	if !ok {
		specialDateWhatsAppOnlyCheckpoint("restaurant_id=%d bot_denied action=%s", rid, a.Action)
		return botJSON(map[string]any{"error": "no autorizado para cambiar fechas especiales desde este chat"}), nil
	}
	switch a.Action {
	case "get":
		out, err := s.specialDateWhatsAppOnlyView(ctx, rid, a.Date)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		out["tool"] = "manage_special_date_whatsapp_only"
		return botJSON(out), nil
	case "set":
		text, err := s.specialDateWhatsAppOnlyPreview(ctx, rid, a)
		if err != nil {
			return botJSON(map[string]any{"error": err.Error()}), nil
		}
		code, err := botSpecialNewCode()
		if err != nil {
			return botJSON(map[string]any{"error": "no se pudo generar el código de confirmación"}), nil
		}
		specialDateWhatsAppOnlyCheckpoint("restaurant_id=%d bot_proposed date=%s whatsapp_only=%t", rid, a.Date, *a.WhatsAppOnly)
		return s.botProposalPersist(ctx, rid, msg, botSpecialDateWhatsAppOnlyOp, 0, a, botSpecialDateWhatsAppOnlyOp+":"+a.Date, actor, origin, "", "", "Propuesta: "+text+". Nada está aplicado todavía.", code)
	}
	return botJSON(map[string]any{"error": "action debe ser get o set"}), nil
}

// botSpecialDateWhatsAppOnlyExecute applies an approved proposal. Dispatched by
// botSpecialAdminExecute: the claim commits ONLY with a successful change.
func (s *Server) botSpecialDateWhatsAppOnlyExecute(ctx context.Context, tx *sql.Tx, rid int, p botSpecialProposalRow) (string, error) {
	var a specialDateWhatsAppOnlyArgs
	if err := json.Unmarshal([]byte(p.Args), &a); err != nil {
		return "", errors.New("propuesta ilegible")
	}
	summary, err := s.specialDateWhatsAppOnlyApply(ctx, rid, a)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", errors.New("error interno")
	}
	return "Aplicado: " + summary + ".", nil
}
