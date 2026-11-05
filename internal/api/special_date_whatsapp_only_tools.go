package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Coordination id: special_date_whatsapp_only_v1 (custom tools)
//
// "Reservas solo por WhatsApp" of a special date (/app/reservas/especial) as
// custom tools for Forky (special_date_whatsapp_only_get / _set) and the
// privileged WhatsApp bot (manage_special_date_whatsapp_only). One core: reads
// query special_dates; the write replays PATCH /admin/config/special-dates
// (handleBOSpecialDatesPatch: GET + merge + the same save, validation and
// special_date_changed broadcast as the panel). Forky confirms with its
// one-shot token, the bot with its propose + "OK <código>" flow.

type specialDateWhatsAppOnlyArgs struct {
	Action       string `json:"action,omitempty"`
	Date         string `json:"date,omitempty"`
	WhatsAppOnly *bool  `json:"whatsapp_only,omitempty"`
}

type specialDateWhatsAppOnlyRow struct {
	Date         string `json:"date"`
	Title        string `json:"title"`
	IsActive     bool   `json:"is_active"`
	Prereserva   bool   `json:"prereserva_enabled"`
	WhatsAppOnly bool   `json:"whatsapp_only"`
}

// specialDateWhatsAppOnlyRows lists one date (any state) or, with an empty
// date, the special dates from today on.
func (s *Server) specialDateWhatsAppOnlyRows(ctx context.Context, rid int, date string) ([]specialDateWhatsAppOnlyRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DATE_FORMAT(date, '%Y-%m-%d'), title, is_active, prereserva_enabled, whatsapp_only
		FROM special_dates
		WHERE restaurant_id = ? AND ((? <> '' AND date = ?) OR (? = '' AND date >= CURDATE()))
		ORDER BY date LIMIT 24`, rid, date, date, date)
	if err != nil {
		return nil, errors.New("no se pudieron leer las fechas especiales")
	}
	defer rows.Close()
	out := []specialDateWhatsAppOnlyRow{}
	for rows.Next() {
		var r specialDateWhatsAppOnlyRow
		var active, pre, wa int
		if err := rows.Scan(&r.Date, &r.Title, &active, &pre, &wa); err != nil {
			return nil, errors.New("no se pudieron leer las fechas especiales")
		}
		r.IsActive, r.Prereserva, r.WhatsAppOnly = active != 0, pre != 0, wa != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// specialDateWhatsAppOnlyView is the read: the dates with their flag plus the
// WhatsApp number the website notice links to.
func (s *Server) specialDateWhatsAppOnlyView(ctx context.Context, rid int, date string) (map[string]any, error) {
	date = strings.TrimSpace(date)
	if date != "" && !isValidISODate(date) {
		return nil, errors.New("date debe ser YYYY-MM-DD")
	}
	rows, err := s.specialDateWhatsAppOnlyRows(ctx, rid, date)
	if err != nil {
		return nil, err
	}
	if date != "" && len(rows) == 0 {
		return nil, fmt.Errorf("no hay fecha especial el %s: créala primero en /app/reservas/especial", date)
	}
	specialDateWhatsAppOnlyCheckpoint("restaurant_id=%d tool_read date=%s rows=%d", rid, date, len(rows))
	return map[string]any{
		"special_dates":  rows,
		"whatsapp_phone": s.restaurantWhatsAppPhone(ctx, rid),
		"note":           "whatsapp_only=true: la web no deja reservar ni prereservar ese día y muestra un aviso con este WhatsApp (teléfono de gestión de Configuración > Contacto).",
	}, nil
}

// specialDateWhatsAppOnlyPreview validates a write and describes it.
func (s *Server) specialDateWhatsAppOnlyPreview(ctx context.Context, rid int, a specialDateWhatsAppOnlyArgs) (string, error) {
	if a.WhatsAppOnly == nil {
		return "", errors.New("falta whatsapp_only (true para activar, false para desactivar)")
	}
	date := strings.TrimSpace(a.Date)
	if !isValidISODate(date) {
		return "", errors.New("date debe ser YYYY-MM-DD")
	}
	rows, err := s.specialDateWhatsAppOnlyRows(ctx, rid, date)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("no hay fecha especial el %s: créala primero en /app/reservas/especial", date)
	}
	label := rows[0].Title + " (" + botFormatISODateES(date) + ")"
	if !*a.WhatsAppOnly {
		return "Desactivar «Reservas solo por WhatsApp» en " + label + ": la web vuelve a permitir reservar o prereservar ese día", nil
	}
	return fmt.Sprintf("Activar «Reservas solo por WhatsApp» en %s: la web no dejará reservar ni prereservar ese día y mostrará el aviso con el WhatsApp +%s", label, s.restaurantWhatsAppPhone(ctx, rid)), nil
}

// specialDateWhatsAppOnlyApply saves the flag through the panel's own PATCH.
func (s *Server) specialDateWhatsAppOnlyApply(ctx context.Context, rid int, a specialDateWhatsAppOnlyArgs) (string, error) {
	summary, err := s.specialDateWhatsAppOnlyPreview(ctx, rid, a)
	if err != nil {
		return "", err
	}
	body, code, err := s.assistantCallHandler(campaignToolCtx(ctx, rid), s.handleBOSpecialDatesPatch, assistantHandlerInput{
		Method: http.MethodPatch,
		Body:   map[string]any{"date": strings.TrimSpace(a.Date), "whatsapp_only": *a.WhatsAppOnly},
	})
	var res struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err != nil || code >= 400 || json.Unmarshal(body, &res) != nil || !res.Success {
		specialDateWhatsAppOnlyCheckpoint("restaurant_id=%d tool_apply_failed date=%s status=%d message=%s err=%v", rid, a.Date, code, res.Message, err)
		return "", errors.New(firstNonEmpty(res.Message, "no se pudo guardar la fecha especial"))
	}
	specialDateWhatsAppOnlyCheckpoint("restaurant_id=%d tool_applied date=%s whatsapp_only=%t", rid, a.Date, *a.WhatsAppOnly)
	return summary, nil
}
