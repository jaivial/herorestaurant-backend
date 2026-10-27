package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Coordination id: wa_bot_special_date_adelanto_v1
//
// prod 2026-10-27 17:07, privileged group "Bot Alqueria": "actualiza el
// adelanto requerido del menu especial del dia de reyes 6 enero de 2027 para
// que sea de 14 euros en lugar de 20". The adelanto of a special date lives in
// special_dates.adelanto_unified_amount (unified) or per menu / per section
// (special_date_menus / special_date_menu_sections), and the only panel write
// for it was the full-replace POST /config/special-dates. The model patched
// the catalogue section instead (special_menu_sections ignores
// adelanto_amount), got a 200 with nothing changed, re-read 20 €, retried and
// exhausted the turn. This panel_change direct op writes exactly that amount.

func init() {
	botPanelDirectOps[botSpecialDateAdelantoOp] = botSpecialDateAdelantoSet
	botPanelDirectOpDesc[botSpecialDateAdelantoOp] = "cambiar el adelanto por persona de una fecha especial"
}

const botSpecialDateAdelantoOp = "special_date_adelanto_set"

// botSpecialDateAdelantoSet sets the per-person adelanto of a special date:
// the unified amount when the date is unified, otherwise every menu and
// section of the date (optionally narrowed by special_date_menu_id/section_id).
// Existing bookings keep the adelanto snapshotted when they were made.
func botSpecialDateAdelantoSet(s *Server, ctx context.Context, rid int, p map[string]any) (any, error) {
	date, okDate := botNormaliseToolDate(botPanelString(p, "date"))
	amount, err := strconv.ParseFloat(strings.Replace(botPanelString(p, "amount"), ",", ".", 1), 64)
	if !okDate || err != nil || amount < 0 {
		return nil, errors.New("date (YYYY-MM-DD) y amount (euros por persona, >= 0) son obligatorios")
	}
	sdmID, sectionID := botPanelInt64(p, "special_date_menu_id"), botPanelInt64(p, "section_id")

	var (
		sdID              int64
		title             string
		isActive, unified int
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, title, is_active, adelanto_unified FROM special_dates WHERE restaurant_id = ? AND date = ? LIMIT 1
	`, rid, date).Scan(&sdID, &title, &isActive, &unified); err != nil {
		return nil, fmt.Errorf("no hay fecha especial configurada para %s", date)
	}

	// Target filter shared by the count, the menu update and the section upsert.
	filter, fargs := "", []any{}
	if sdmID > 0 {
		filter, fargs = " AND sdm.id = ?", []any{sdmID}
	}
	secFilter, secArgs := "", []any{}
	if sectionID > 0 {
		secFilter, secArgs = " AND sms.id = ?", []any{sectionID}
	}
	var menus, sections int
	countArgs := append(append(append([]any{}, secArgs...), rid, sdID), fargs...)
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT sdm.id), COUNT(sms.id)
		FROM special_date_menus sdm
		LEFT JOIN special_menu_sections sms
		  ON sms.menu_id = sdm.menu_id AND sms.restaurant_id = sdm.restaurant_id`+secFilter+`
		WHERE sdm.restaurant_id = ? AND sdm.special_date_id = ?`+filter, countArgs...).Scan(&menus, &sections); err != nil {
		return nil, errors.New("no se pudo leer los menús de la fecha especial")
	}
	if unified == 0 && (menus == 0 || sectionID > 0 && sections == 0) {
		return nil, errors.New("ese menú o sección no pertenece a la fecha especial (usa los ids de get_special_date_menu)")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, errors.New("no se pudo guardar el adelanto")
	}
	defer func() { _ = tx.Rollback() }()
	scope := "por menú/sección"
	if unified != 0 {
		scope = "unificado (todos los menús de la fecha)"
		_, err = tx.ExecContext(ctx, `UPDATE special_dates SET adelanto_unified_amount = ? WHERE id = ? AND restaurant_id = ?`, amount, sdID, rid)
	} else {
		if sectionID == 0 {
			_, err = tx.ExecContext(ctx, `UPDATE special_date_menus sdm SET sdm.adelanto_amount = ? WHERE sdm.restaurant_id = ? AND sdm.special_date_id = ?`+filter,
				append([]any{amount, rid, sdID}, fargs...)...)
		}
		if err == nil && sections > 0 {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO special_date_menu_sections (restaurant_id, special_date_menu_id, section_id, adelanto_amount)
				SELECT sdm.restaurant_id, sdm.id, sms.id, ?
				FROM special_date_menus sdm
				JOIN special_menu_sections sms
				  ON sms.menu_id = sdm.menu_id AND sms.restaurant_id = sdm.restaurant_id`+secFilter+`
				WHERE sdm.restaurant_id = ? AND sdm.special_date_id = ?`+filter+`
				ON DUPLICATE KEY UPDATE adelanto_amount = VALUES(adelanto_amount)
			`, append(append(append([]any{amount}, secArgs...), rid, sdID), fargs...)...)
		}
	}
	if err != nil || tx.Commit() != nil {
		return nil, errors.New("no se pudo guardar el adelanto")
	}
	botCheckpoint("wa_bot_special_date_adelanto_v1", "restaurant_id=%d date=%s amount=%.2f unified=%t menus=%d sections=%d", rid, date, amount, unified != 0, menus, sections)
	s.broadcastBOSpecialDateChange(rid, "special_date_changed", date, isActive != 0, title)
	return map[string]any{
		"date": date, "title": title, "adelanto_por_persona": amount, "ambito": scope,
		"menus": menus, "secciones": sections,
		"nota": "Solo cambia la configuración de la fecha: las reservas ya creadas conservan el adelanto con el que se hicieron. Para recalcular una reserva ya hecha: special_booking_propose_menus con sus MISMOS menús (los de special_booking_get).",
	}, nil
}
