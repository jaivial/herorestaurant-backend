package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Coordination id: menu_weekday_availability_v1
// (backoffice WeekdayGrid -> group-menus-v2 WS -> menu_weekday_availability
//  -> public API + WhatsApp bot -> booking's weekday menu resolution).
//
// The canonical weekday keys are the SAME tokens used by the backoffice
// `WeekdayGrid` component and the public client SDK, so a value written from
// one side is always understood by the others.

const boMenuWeekdayStateFrame = "menu_weekdays"

var boMenuWeekdayKeys = []string{
	"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday",
}

// normalizeBOMenuWeekday accepts the english keys plus the Spanish names and
// ISO numbers (1=Monday .. 7=Sunday) used by legacy callers, and returns the
// canonical english key. Empty string means "not a weekday".
func normalizeBOMenuWeekday(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "monday", "lunes", "mon", "1":
		return "monday"
	case "tuesday", "martes", "tue", "2":
		return "tuesday"
	case "wednesday", "miercoles", "miércoles", "wed", "3":
		return "wednesday"
	case "thursday", "jueves", "thu", "4":
		return "thursday"
	case "friday", "viernes", "fri", "5":
		return "friday"
	case "saturday", "sabado", "sábado", "sat", "6":
		return "saturday"
	case "sunday", "domingo", "sun", "0", "7":
		return "sunday"
	default:
		return ""
	}
}

// boMenuWeekdayKeyForDate maps a calendar date to its canonical weekday key.
func boMenuWeekdayKeyForDate(t time.Time) string {
	switch t.Weekday() {
	case time.Monday:
		return "monday"
	case time.Tuesday:
		return "tuesday"
	case time.Wednesday:
		return "wednesday"
	case time.Thursday:
		return "thursday"
	case time.Friday:
		return "friday"
	case time.Saturday:
		return "saturday"
	case time.Sunday:
		return "sunday"
	default:
		return ""
	}
}

func emptyBOMenuWeekdays() map[string]bool {
	out := make(map[string]bool, len(boMenuWeekdayKeys))
	for _, key := range boMenuWeekdayKeys {
		out[key] = false
	}
	return out
}

// loadBOMenuWeekdays returns the availability flag for every canonical weekday
// (false when the menu has no row for that day).
func (s *Server) loadBOMenuWeekdays(ctx context.Context, restaurantID int, menuID int64) (map[string]bool, error) {
	out := emptyBOMenuWeekdays()
	rows, err := s.db.QueryContext(ctx, `
		SELECT weekday, available
		FROM menu_weekday_availability
		WHERE restaurant_id = ? AND menu_id = ?
	`, restaurantID, menuID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			weekday     string
			availableIn int
		)
		if err := rows.Scan(&weekday, &availableIn); err != nil {
			return out, err
		}
		key := normalizeBOMenuWeekday(weekday)
		if key == "" {
			continue
		}
		out[key] = availableIn != 0
	}
	return out, rows.Err()
}

// saveBOMenuWeekday upserts a single weekday flag for a menu.
func (s *Server) saveBOMenuWeekday(ctx context.Context, restaurantID int, menuID int64, weekday string, available bool) error {
	key := normalizeBOMenuWeekday(weekday)
	if key == "" {
		return errInvalidBOMenuWeekday
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO menu_weekday_availability (restaurant_id, menu_id, weekday, available)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE available = VALUES(available)
	`, restaurantID, menuID, key, boolToTinyint(available))
	return err
}

type boInvalidMenuWeekdayError struct{}

func (boInvalidMenuWeekdayError) Error() string { return "invalid weekday" }

var errInvalidBOMenuWeekday = boInvalidMenuWeekdayError{}

// handleBOMenuWeekdayWSMessage persists weekday availability changes sent from
// the backoffice menu editor over the existing group-menus-v2 socket. Reads
// (`weekday_refresh`) answer on the requesting client only; writes broadcast
// the new snapshot to every editor watching that menu so two operators never
// drift.
func (s *Server) handleBOMenuWeekdayWSMessage(_ *http.Request, restaurantID int, menuID int64, client *boGroupMenuV2AIClient, raw []byte) {
	var msg struct {
		Type          string `json:"type"`
		MenuID        int64  `json:"menu_id"`
		Weekday       string `json:"weekday"`
		Available     *bool  `json:"available"`
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	if msg.MenuID > 0 && msg.MenuID != menuID {
		return
	}
	typ := strings.ToLower(strings.TrimSpace(msg.Type))

	sendState := func(target *boGroupMenuV2AIClient, frameType string) {
		weekdays, err := s.loadBOMenuWeekdays(context.Background(), restaurantID, menuID)
		if err != nil {
			return
		}
		_ = target.writeJSON(map[string]any{
			"type":           frameType,
			"restaurant_id":  restaurantID,
			"menu_id":        menuID,
			"menu_weekdays":  weekdays,
			"weekdays":       weekdays,
			"correlation_id": msg.CorrelationID,
			"at":             time.Now().UTC().Format(time.RFC3339),
		})
	}

	switch typ {
	case "weekday_refresh":
		sendState(client, boMenuWeekdayStateFrame)
	case "weekday_set":
		weekday := normalizeBOMenuWeekday(msg.Weekday)
		if weekday == "" || msg.Available == nil {
			_ = client.writeJSON(map[string]any{
				"type":    "weekday_error",
				"menu_id": menuID,
				"code":    "validation",
				"message": "weekday y available son obligatorios",
			})
			return
		}
		if err := s.saveBOMenuWeekday(context.Background(), restaurantID, menuID, weekday, *msg.Available); err != nil {
			_ = client.writeJSON(map[string]any{
				"type":    "weekday_error",
				"menu_id": menuID,
				"code":    "server",
				"message": "No se pudo guardar el calendario semanal",
			})
			return
		}
		s.logBOGroupMenuV2AITrace(
			"ws weekday saved restaurant=%d menu=%d weekday=%s available=%t",
			restaurantID, menuID, weekday, *msg.Available,
		)
		s.groupMenusV2AIHub.broadcast(restaurantID, menuID, map[string]any{
			"type":           "weekday_saved",
			"restaurant_id":  restaurantID,
			"menu_id":        menuID,
			"weekday":        weekday,
			"available":      *msg.Available,
			"correlation_id": msg.CorrelationID,
			"at":             time.Now().UTC().Format(time.RFC3339),
		})
		sendState(client, boMenuWeekdayStateFrame)
	}
}

// botMenusAvailableOnWeekday returns the active, bookable menus that are marked
// as available on the given weekday key. When menuType is non-empty only that
// menu_type is returned (e.g. "closed_conventional" for the default menus).
//
// When the restaurant has no weekday configuration at all yet, it falls back
// to the active menus of the requested type so the bot keeps working while the
// operator fills in the calendar.
func (s *Server) botMenusAvailableOnWeekday(ctx context.Context, restaurantID int, weekday string, menuType string) ([]map[string]any, bool, error) {
	key := normalizeBOMenuWeekday(weekday)
	if key == "" {
		return []map[string]any{}, false, nil
	}

	args := []any{restaurantID, key}
	where := `m.restaurant_id = ? AND m.active = 1 AND m.is_draft = 0 AND a.weekday = ? AND a.available = 1`
	if strings.TrimSpace(menuType) != "" {
		where += ` AND COALESCE(NULLIF(TRIM(m.menu_type), ''), 'closed_conventional') = ?`
		args = append(args, strings.TrimSpace(menuType))
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.menu_title,
		       COALESCE(NULLIF(TRIM(m.menu_type), ''), 'closed_conventional'),
		       COALESCE(m.price, ''), COALESCE(m.menu_subtitle, '')
		FROM menus m
		JOIN menu_weekday_availability a
		  ON a.menu_id = m.id AND a.restaurant_id = m.restaurant_id
		WHERE `+where+`
		ORDER BY m.modified_at DESC, m.id DESC
	`, args...)
	if err != nil {
		return nil, false, err
	}
	out, serr := scanBotMenuRows(rows)
	if serr != nil {
		return nil, false, serr
	}
	if len(out) > 0 {
		return out, true, nil
	}

	// The restaurant may simply have configured other weekdays. In that case
	// "none available on this weekday" is the real answer.
	var configuredRows int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM menu_weekday_availability WHERE restaurant_id = ?
	`, restaurantID).Scan(&configuredRows); err == nil && configuredRows > 0 {
		return []map[string]any{}, true, nil
	}

	// No weekday configuration at all yet: fall back to the active menus of the
	// requested category, flagged as not-yet-configured.
	fallbackArgs := []any{restaurantID}
	fallbackWhere := `restaurant_id = ? AND active = 1 AND is_draft = 0`
	if strings.TrimSpace(menuType) != "" {
		fallbackWhere += ` AND COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional') = ?`
		fallbackArgs = append(fallbackArgs, strings.TrimSpace(menuType))
	}
	fallbackRows, ferr := s.db.QueryContext(ctx, `
		SELECT id, menu_title,
		       COALESCE(NULLIF(TRIM(menu_type), ''), 'closed_conventional'),
		       COALESCE(price, ''), COALESCE(menu_subtitle, '')
		FROM menus
		WHERE `+fallbackWhere+`
		ORDER BY modified_at DESC, id DESC
	`, fallbackArgs...)
	if ferr != nil {
		return nil, false, ferr
	}
	fallback, fserr := scanBotMenuRows(fallbackRows)
	return fallback, false, fserr
}

func scanBotMenuRows(rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var (
			id          int64
			title       string
			menuType    string
			price       string
			subtitleRaw string
		)
		if err := rows.Scan(&id, &title, &menuType, &price, &subtitleRaw); err != nil {
			return nil, err
		}
		normalized := normalizeV2MenuType(menuType)
		cleanPrice := strings.TrimSpace(price)
		if cleanPrice == "" {
			cleanPrice = "0"
		}
		out = append(out, map[string]any{
			"menu_id":        id,
			"title":          strings.TrimSpace(title),
			"category":       normalized,
			"category_label": botMenuCategoryLabel(normalized),
			"price":          cleanPrice,
			"subtitle":       anySliceToStringList(decodeJSONOrFallback(subtitleRaw, []any{})),
		})
	}
	return out, rows.Err()
}

// menuDeGrupoAssignedTinyint derives the bookings.menu_de_grupo_assigned flag
// from the assigned menu id, so every write path stays consistent without each
// caller having to reason about the boolean.
func menuDeGrupoAssignedTinyint(menuID any) int {
	switch v := menuID.(type) {
	case nil:
		return 0
	case int:
		if v > 0 {
			return 1
		}
	case int64:
		if v > 0 {
			return 1
		}
	case sql.NullInt64:
		if v.Valid && v.Int64 > 0 {
			return 1
		}
	}
	return 0
}
