package api

import (
	"context"
	"encoding/json"
	"errors"
)

// Coordination id: special_date_whatsapp_only_v1 (Forky half)
//
// Forky tools of «Reservas solo por WhatsApp» over the shared core
// (special_date_whatsapp_only_tools.go): the read answers at once, the write
// waits for the one-shot confirmation token and replays the panel PATCH.

func init() {
	assistantToolRegistry = append(assistantToolRegistry,
		assistantTool{
			Name: "special_date_whatsapp_only_get",
			Description: "Lee si las fechas especiales (festivas) se reservan SOLO POR WHATSAPP (whatsapp_only) y el número de WhatsApp al que la web manda a los clientes. " +
				"Sin date: las fechas especiales desde hoy; con date=YYYY-MM-DD: esa fecha.",
			BackofficeOnly: true, Section: "reservas",
			Schema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string","description":"YYYY-MM-DD (opcional)"}}}`),
			Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
				var a specialDateWhatsAppOnlyArgs
				_ = json.Unmarshal(input, &a)
				out, err := s.specialDateWhatsAppOnlyView(ctx, rid, a.Date)
				if err != nil {
					return "", err
				}
				return botJSON(out), nil
			},
		},
		assistantTool{
			Name: "special_date_whatsapp_only_set",
			Description: "Activa (whatsapp_only=true) o desactiva (false) «Reservas solo por WhatsApp» en una fecha especial existente: activado, la web no deja reservar ni prereservar ese día, " +
				"enseña un aviso con el WhatsApp del restaurante y el bot de WhatsApp no manda el enlace de la web. Primero confirmed=false (devuelve la vista previa) y, cuando el usuario diga que sí, confirmed=true con los mismos datos y el confirmation_token.",
			Write: true, Confirm: true, BackofficeOnly: true, Section: "reservas",
			Schema: json.RawMessage(`{"type":"object","properties":{"date":{"type":"string","description":"YYYY-MM-DD de la fecha especial"},"whatsapp_only":{"type":"boolean"},"confirmed":{"type":"boolean"},"confirmation_token":{"type":"string"}},"required":["date","whatsapp_only","confirmed"]}`),
			Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
				return s.assistantSpecialDateWhatsAppOnlySet(ctx, rid, input)
			},
		},
	)
}

func (s *Server) assistantSpecialDateWhatsAppOnlySet(ctx context.Context, rid int, input json.RawMessage) (string, error) {
	var in struct {
		specialDateWhatsAppOnlyArgs
		Confirmed         bool   `json:"confirmed"`
		ConfirmationToken string `json:"confirmation_token"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", errors.New("argumentos inválidos")
	}
	if !in.Confirmed {
		preview, err := s.specialDateWhatsAppOnlyPreview(ctx, rid, in.specialDateWhatsAppOnlyArgs)
		if err != nil {
			return "", err
		}
		tok, err := s.assistantRequireConfirmation(rid, "special_date_whatsapp_only_set", input)
		if err != nil {
			return "", err
		}
		var out map[string]any
		_ = json.Unmarshal([]byte(tok), &out)
		out["preview"] = preview
		return botJSON(out), nil
	}
	if err := s.assistantConsumeConfirmation(in.ConfirmationToken, rid, "special_date_whatsapp_only_set", input); err != nil {
		return "", err
	}
	summary, err := s.specialDateWhatsAppOnlyApply(ctx, rid, in.specialDateWhatsAppOnlyArgs)
	if err != nil {
		return "", err
	}
	return botJSON(map[string]any{"ok": true, "summary": "Aplicado: " + summary}), nil
}
