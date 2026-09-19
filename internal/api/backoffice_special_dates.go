package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

// Backoffice CRUD for per-date "special dates" (reservas especiales).
//
// Coordination id: special_dates_v1
// Style follows handleBOMandatoryMenusGet/Save (backoffice_config.go:2313/2382)
// so the backoffice reservas-config tab can mount this without surprises.
//
// Image upload reuses handleBOCampaignImageUpload's pipeline verbatim
// (backoffice_campaigns.go:405-437): readBOAdMultipartImage +
// specialmenuimage.NormalizeToWebPWithLimit + s.bunnyPut / s.bunnyPullURL.
// Object path: <restaurantId>/pictures/special-dates/<unixmillis>.webp.

// validAdelantoPaymentMethods is the canonical enum from SPEC §2.
var validAdelantoPaymentMethods = map[string]bool{
	"card":          true,
	"bizum":         true,
	"transferencia": true,
	"efectivo":      true,
}

// boSpecialDateMenu is one row of the special_dates.menus array.
type boSpecialDateMenu struct {
	ID             *int64   `json:"id,omitempty"`
	MenuID         *int64   `json:"menu_id,omitempty"`
	CustomTitle    *string  `json:"custom_title,omitempty"`
	CustomImageURL *string  `json:"custom_image_url,omitempty"`
	AdelantoAmount *float64 `json:"adelanto_amount,omitempty"`
	Position       int      `json:"position"`
}

// boSpecialDateSaveRequest is the POST body for /admin/config/special-dates.
// `Date` is the only required field; everything else is optional and falls
// back to the safe defaults the form starts with.
type boSpecialDateSaveRequest struct {
	Date                   string              `json:"date"`
	IsActive               *bool               `json:"is_active"`
	Title                  *string             `json:"title"`
	Description            *string             `json:"description"`
	PrereservaEnabled      *bool               `json:"prereserva_enabled"`
	MaxPerTableEnabled     *bool               `json:"max_per_table_enabled"`
	MaxPerTable            *int                `json:"max_per_table"`
	RequiresAdelanto       *bool               `json:"requires_adelanto"`
	AdelantoPaymentMethods []string            `json:"adelanto_payment_methods"`
	AdelantoUnified        *bool               `json:"adelanto_unified"`
	AdelantoUnifiedAmount  *float64            `json:"adelanto_unified_amount"`
	PrereservaStartsOn     *string             `json:"prereserva_starts_on"`
	PrereservaEndsOn       *string             `json:"prereserva_ends_on"`
	Menus                  []boSpecialDateMenu `json:"menus"`
}

func (s *Server) handleBOSpecialDatesGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	var id int64
	var isActive, prereservaEnabled, maxPerTableEnabled, requiresAdelanto, adelantoUnified int
	var title string
	var description sql.NullString
	var maxPerTable sql.NullInt64
	var adelantoMethodsRaw sql.NullString
	var adelantoUnifiedAmount sql.NullFloat64
	var prereservaStartsOn, prereservaEndsOn sql.NullTime

	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, is_active, title, description, prereserva_enabled,
		       max_per_table_enabled, max_per_table, requires_adelanto,
		       adelanto_payment_methods, adelanto_unified, adelanto_unified_amount,
		       prereserva_starts_on, prereserva_ends_on
		FROM special_dates
		WHERE restaurant_id = ? AND date = ?
		LIMIT 1
	`, a.ActiveRestaurantID, date).Scan(
		&id, &isActive, &title, &description, &prereservaEnabled,
		&maxPerTableEnabled, &maxPerTable, &requiresAdelanto,
		&adelantoMethodsRaw, &adelantoUnified, &adelantoUnifiedAmount,
		&prereservaStartsOn, &prereservaEndsOn,
	)

	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":      true,
			"date":         date,
			"special_date": nil,
		})
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando special_dates")
		return
	}

	var adelantoMethods []string
	if adelantoMethodsRaw.Valid && strings.TrimSpace(adelantoMethodsRaw.String) != "" {
		_ = json.Unmarshal([]byte(adelantoMethodsRaw.String), &adelantoMethods)
	}
	if adelantoMethods == nil {
		adelantoMethods = []string{}
	}

	menus, err := s.loadBOSpecialDateMenus(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando special_date_menus")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"special_date": map[string]any{
			"date":                     date,
			"is_active":                isActive != 0,
			"title":                    title,
			"description":              description.String,
			"prereserva_enabled":       prereservaEnabled != 0,
			"max_per_table_enabled":    maxPerTableEnabled != 0,
			"max_per_table":            nullIfZero(maxPerTable.Valid, maxPerTable.Int64),
			"requires_adelanto":        requiresAdelanto != 0,
			"adelanto_payment_methods": adelantoMethods,
			"adelanto_unified":         adelantoUnified != 0,
			"adelanto_unified_amount":  nullIfZeroFloat(adelantoUnifiedAmount.Valid, adelantoUnifiedAmount.Float64),
			"prereserva_starts_on":     nullIfEmptyTime(prereservaStartsOn),
			"prereserva_ends_on":       nullIfEmptyTime(prereservaEndsOn),
			"menus":                    menus,
		},
	})
}

// loadBOSpecialDateMenus reads the child rows for one special date.
func (s *Server) loadBOSpecialDateMenus(ctx context.Context, restaurantID int, specialDateID int64) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, menu_id, custom_title, custom_image_url, adelanto_amount, position
		FROM special_date_menus
		WHERE restaurant_id = ? AND special_date_id = ?
		ORDER BY position ASC, id ASC
	`, restaurantID, specialDateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]map[string]any, 0, 4)
	for rows.Next() {
		var id int64
		var menuID sql.NullInt64
		var customTitle, customImageURL sql.NullString
		var adelantoAmount sql.NullFloat64
		var position int
		if err := rows.Scan(&id, &menuID, &customTitle, &customImageURL, &adelantoAmount, &position); err != nil {
			return nil, err
		}
		row := map[string]any{
			"id":       id,
			"position": position,
		}
		if menuID.Valid {
			row["menu_id"] = menuID.Int64
		}
		if customTitle.Valid {
			row["custom_title"] = customTitle.String
		}
		if customImageURL.Valid {
			row["custom_image_url"] = customImageURL.String
		}
		if adelantoAmount.Valid {
			row["adelanto_amount"] = adelantoAmount.Float64
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Server) handleBOSpecialDatesSave(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boSpecialDateSaveRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	date := strings.TrimSpace(req.Date)
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	maxPerTable := 0
	maxPerTableEnabled := false
	if req.MaxPerTableEnabled != nil && *req.MaxPerTableEnabled {
		maxPerTableEnabled = true
		if req.MaxPerTable != nil {
			if *req.MaxPerTable < 1 {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": "max_per_table debe ser al menos 1",
				})
				return
			}
			maxPerTable = *req.MaxPerTable
		} else {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "max_per_table es obligatorio cuando max_per_table_enabled es true",
			})
			return
		}
	}

	// Whitelist payment methods against the SPEC §2 enum.
	cleanMethods := make([]string, 0, len(req.AdelantoPaymentMethods))
	for _, m := range req.AdelantoPaymentMethods {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if !validAdelantoPaymentMethods[m] {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Método de pago inválido: " + m,
			})
			return
		}
		cleanMethods = append(cleanMethods, m)
	}
	adelantoMethodsJSON, _ := json.Marshal(cleanMethods)

	if req.AdelantoUnifiedAmount != nil && *req.AdelantoUnifiedAmount < 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "adelanto_unified_amount debe ser >= 0",
		})
		return
	}

	var prereservaStartsOn, prereservaEndsOn any
	prereservaStartsOn = nil
	prereservaEndsOn = nil
	if req.PrereservaStartsOn != nil && strings.TrimSpace(*req.PrereservaStartsOn) != "" {
		if !isValidISODate(strings.TrimSpace(*req.PrereservaStartsOn)) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "prereserva_starts_on inválido",
			})
			return
		}
		prereservaStartsOn = strings.TrimSpace(*req.PrereservaStartsOn)
	}
	if req.PrereservaEndsOn != nil && strings.TrimSpace(*req.PrereservaEndsOn) != "" {
		if !isValidISODate(strings.TrimSpace(*req.PrereservaEndsOn)) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "prereserva_ends_on inválido",
			})
			return
		}
		prereservaEndsOn = strings.TrimSpace(*req.PrereservaEndsOn)
	}
	if prereservaStartsOn != nil && prereservaEndsOn != nil {
		ss, _ := prereservaStartsOn.(string)
		ee, _ := prereservaEndsOn.(string)
		if ss > ee {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "prereserva_starts_on debe ser anterior o igual a prereserva_ends_on",
			})
			return
		}
	}

	// Validate and resolve menu rows. menu_id XOR (custom_title + custom_image_url).
	cleanMenus := make([]boSpecialDateMenu, 0, len(req.Menus))
	seenMenuIDs := map[int64]bool{}
	for i, m := range req.Menus {
		hasMenu := m.MenuID != nil && *m.MenuID > 0
		hasCustomTitle := m.CustomTitle != nil && strings.TrimSpace(*m.CustomTitle) != ""
		hasCustomImage := m.CustomImageURL != nil && strings.TrimSpace(*m.CustomImageURL) != ""
		if hasMenu && (hasCustomTitle || hasCustomImage) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Cada menú debe tener menu_id o título/imagen personalizados, no ambos",
			})
			return
		}
		if !hasMenu && !(hasCustomTitle && hasCustomImage) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Cada menú personalizado requiere título e imagen",
			})
			return
		}
		if m.AdelantoAmount != nil && *m.AdelantoAmount < 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "adelanto_amount debe ser >= 0",
			})
			return
		}
		if hasMenu {
			mid := *m.MenuID
			if seenMenuIDs[mid] {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success": false,
					"message": "menu_id repetido en la lista",
				})
				return
			}
			seenMenuIDs[mid] = true
		}
		if m.Position == 0 {
			m.Position = i + 1
		}
		cleanMenus = append(cleanMenus, m)
	}

	// Pre-validate every menu_id belongs to this tenant and is active + non-draft.
	for _, m := range cleanMenus {
		if m.MenuID == nil {
			continue
		}
		var ok int
		err := s.db.QueryRowContext(r.Context(), `
			SELECT 1 FROM menus
			WHERE restaurant_id = ? AND id = ? AND active = 1 AND is_draft = 0
			LIMIT 1
		`, a.ActiveRestaurantID, *m.MenuID).Scan(&ok)
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "menu_id no válido para este restaurante",
			})
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error validando menu_id")
			return
		}
	}

	// Atomic upsert: special_dates first, then delete+reinsert menus inside the
	// same transaction so a partial save never leaks stale child rows.
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error iniciando transacción")
		return
	}
	defer func() {
		_ = tx.Rollback()
	}()

	isActive := false
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	title := ""
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
	}
	description := sql.NullString{}
	if req.Description != nil {
		description = sql.NullString{String: *req.Description, Valid: strings.TrimSpace(*req.Description) != ""}
	}
	prereservaEnabled := false
	if req.PrereservaEnabled != nil {
		prereservaEnabled = *req.PrereservaEnabled
	}
	requiresAdelanto := false
	if req.RequiresAdelanto != nil {
		requiresAdelanto = *req.RequiresAdelanto
	}
	adelantoUnified := false
	if req.AdelantoUnified != nil {
		adelantoUnified = *req.AdelantoUnified
	}
	var adelantoUnifiedAmount any
	adelantoUnifiedAmount = nil
	if req.AdelantoUnifiedAmount != nil {
		adelantoUnifiedAmount = *req.AdelantoUnifiedAmount
	}
	var maxPerTableVal any
	maxPerTableVal = nil
	if maxPerTableEnabled && maxPerTable > 0 {
		maxPerTableVal = maxPerTable
	}

	_, err = tx.ExecContext(r.Context(), `
		INSERT INTO special_dates
			(restaurant_id, date, is_active, title, description, prereserva_enabled,
			 max_per_table_enabled, max_per_table, requires_adelanto, adelanto_payment_methods,
			 adelanto_unified, adelanto_unified_amount, prereserva_starts_on, prereserva_ends_on)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			is_active = VALUES(is_active),
			title = VALUES(title),
			description = VALUES(description),
			prereserva_enabled = VALUES(prereserva_enabled),
			max_per_table_enabled = VALUES(max_per_table_enabled),
			max_per_table = VALUES(max_per_table),
			requires_adelanto = VALUES(requires_adelanto),
			adelanto_payment_methods = VALUES(adelanto_payment_methods),
			adelanto_unified = VALUES(adelanto_unified),
			adelanto_unified_amount = VALUES(adelanto_unified_amount),
			prereserva_starts_on = VALUES(prereserva_starts_on),
			prereserva_ends_on = VALUES(prereserva_ends_on)
	`, a.ActiveRestaurantID, date, boolToInt(isActive), title, description, boolToInt(prereservaEnabled),
		boolToInt(maxPerTableEnabled), maxPerTableVal, boolToInt(requiresAdelanto), string(adelantoMethodsJSON),
		boolToInt(adelantoUnified), normalizeAdelantoAmount(adelantoUnifiedAmount), prereservaStartsOn, prereservaEndsOn)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando special_dates")
		return
	}

	var specialDateID int64
	if err := tx.QueryRowContext(r.Context(), `
		SELECT id FROM special_dates WHERE restaurant_id = ? AND date = ? LIMIT 1
	`, a.ActiveRestaurantID, date).Scan(&specialDateID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo special_date_id")
		return
	}

	if _, err := tx.ExecContext(r.Context(), `
		DELETE FROM special_date_menus WHERE restaurant_id = ? AND special_date_id = ?
	`, a.ActiveRestaurantID, specialDateID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error limpiando special_date_menus")
		return
	}

	for _, m := range cleanMenus {
		var menuID, adelantoAmount any
		menuID = nil
		adelantoAmount = nil
		if m.MenuID != nil {
			menuID = *m.MenuID
		}
		if m.AdelantoAmount != nil {
			adelantoAmount = *m.AdelantoAmount
		}
		customTitle := sql.NullString{}
		if m.CustomTitle != nil && strings.TrimSpace(*m.CustomTitle) != "" {
			customTitle = sql.NullString{String: strings.TrimSpace(*m.CustomTitle), Valid: true}
		}
		customImage := sql.NullString{}
		if m.CustomImageURL != nil && strings.TrimSpace(*m.CustomImageURL) != "" {
			customImage = sql.NullString{String: strings.TrimSpace(*m.CustomImageURL), Valid: true}
		}
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO special_date_menus
				(restaurant_id, special_date_id, menu_id, custom_title, custom_image_url, adelanto_amount, position)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, a.ActiveRestaurantID, specialDateID, menuID, customTitle, customImage, adelantoAmount, m.Position); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error insertando special_date_menus")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error confirmando transacción")
		return
	}

	slog.Default().Info("special_date.settings_saved",
		"coord_id", "special_dates_v1",
		"restaurant_id", a.ActiveRestaurantID,
		"date", date,
		"menus", len(cleanMenus),
	)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"date":    date,
	})
}

// handleBOSpecialDateMenuImageUpload stores a custom-menu image in BunnyCDN
// and returns the resulting pull URL. Mirrors handleBOCampaignImageUpload
// (backoffice_campaigns.go:405-437) so the editor behaves identically to the
// campaign markdown image upload.
func (s *Server) handleBOSpecialDateMenuImageUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	if !s.bunnyConfigured(r.Context(), a.ActiveRestaurantID) {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Almacenamiento de imagenes no configurado"})
		return
	}
	raw, filename, ct, err := readBOAdMultipartImage(r, specialmenuimage.MaxInputBytes)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Imagen invalida"})
		return
	}
	normalized, err := specialmenuimage.NormalizeToWebPWithLimit(r.Context(), raw, filename, ct, specialDateMenuMaxImageBytes)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "No se pudo procesar la imagen"})
		return
	}
	objectPath := path.Join(strconv.Itoa(a.ActiveRestaurantID), "pictures", "special-dates", strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)+".webp")
	if err := s.bunnyPut(r.Context(), a.ActiveRestaurantID, objectPath, normalized, "image/webp"); err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "No se pudo subir la imagen"})
		return
	}
	url := s.bunnyPullURL(r.Context(), a.ActiveRestaurantID, objectPath)
	slog.Default().Info("special_date.menu_image.uploaded",
		"coord_id", "special_dates_v1",
		"restaurant_id", a.ActiveRestaurantID,
		"url", url,
	)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "url": url})
}

// specialDateMenuMaxImageBytes matches the campaign budget so editors behave
// the same across the two upload paths.
const specialDateMenuMaxImageBytes = 400 * 1024

// --- helpers ---

func nullIfZero(valid bool, v int64) any {
	if !valid {
		return nil
	}
	return v
}

func nullIfZeroFloat(valid bool, v float64) any {
	if !valid {
		return nil
	}
	return v
}

func nullIfEmptyTime(t sql.NullTime) any {
	if !t.Valid || t.Time.IsZero() {
		return nil
	}
	return t.Time.Format("2006-01-02")
}

// normalizeAdelantoAmount passes through a float64 or nil unchanged. The driver
// expects a concrete numeric value or sql NULL when the unified toggle is off;
// any-typed nil from the caller stays nil so the column receives NULL.
func normalizeAdelantoAmount(v any) any {
	if v == nil {
		return nil
	}
	if f, ok := v.(float64); ok {
		return f
	}
	return v
}
