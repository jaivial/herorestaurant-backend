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
	// Coordination id: stripe_prereserva_adelanto_v1 - online payment,
	// exclusive: when chosen it is the only accepted method.
	"stripe": true,
}

const adelantoMethodStripe = "stripe"

// boSpecialDateMenu is one row of the special_dates.menus array.
type boSpecialDateMenu struct {
	ID             *int64   `json:"id,omitempty"`
	MenuID         *int64   `json:"menu_id,omitempty"`
	CustomTitle    *string  `json:"custom_title,omitempty"`
	CustomImageURL *string  `json:"custom_image_url,omitempty"`
	AdelantoAmount *float64 `json:"adelanto_amount,omitempty"`
	Price          *float64 `json:"price,omitempty"`
	Position       int      `json:"position"`
	// Coordination id: special_date_section_menus_v1 - per-section adelanto
	// when menu_id is a special-type menu.
	Sections []specialDateSectionAdelanto `json:"sections,omitempty"`
}

// boSpecialDateSaveRequest is the POST body for /admin/config/special-dates.
// `Date` is the only required field; everything else is optional and falls
// back to the safe defaults the form starts with.
type boSpecialDateSaveRequest struct {
	Date               string  `json:"date"`
	IsActive           *bool   `json:"is_active"`
	Title              *string `json:"title"`
	Description        *string `json:"description"`
	PrereservaEnabled  *bool   `json:"prereserva_enabled"`
	MaxPerTableEnabled *bool   `json:"max_per_table_enabled"`
	MaxPerTable        *int    `json:"max_per_table"`
	MobilityEnabled    *bool   `json:"mobility_enabled"`
	// Coordination id: reservation_self_modification_v1 - opt this concrete
	// special date into the public "modify instead of rebook" self-service.
	AllowCustomerModification *bool               `json:"allow_customer_modification"`
	RequiresAdelanto          *bool               `json:"requires_adelanto"`
	AdelantoPaymentMethods    []string            `json:"adelanto_payment_methods"`
	AdelantoUnified           *bool               `json:"adelanto_unified"`
	AdelantoUnifiedAmount     *float64            `json:"adelanto_unified_amount"`
	PrereservaStartsOn        *string             `json:"prereserva_starts_on"`
	PrereservaEndsOn          *string             `json:"prereserva_ends_on"`
	Menus                     []boSpecialDateMenu `json:"menus"`
}

func (s *Server) handleBOSpecialDatesGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		s.handleBOSpecialDatesList(w, r, a.ActiveRestaurantID)
		return
	}
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date",
		})
		return
	}

	var id int64
	var isActive, prereservaEnabled, maxPerTableEnabled, requiresAdelanto, adelantoUnified int
	var allowCustomerModification int
	var title string
	var description sql.NullString
	var maxPerTable sql.NullInt64
	var adelantoMethodsRaw sql.NullString
	var adelantoUnifiedAmount sql.NullFloat64
	var prereservaStartsOn, prereservaEndsOn sql.NullTime

	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, is_active, title, description, prereserva_enabled,
		       max_per_table_enabled, max_per_table, requires_adelanto,
		       allow_customer_modification,
		       adelanto_payment_methods, adelanto_unified, adelanto_unified_amount,
		       prereserva_starts_on, prereserva_ends_on
		FROM special_dates
		WHERE restaurant_id = ? AND date = ?
		LIMIT 1
	`, a.ActiveRestaurantID, date).Scan(
		&id, &isActive, &title, &description, &prereservaEnabled,
		&maxPerTableEnabled, &maxPerTable, &requiresAdelanto,
		&allowCustomerModification,
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

	// Coordination id: mobility_day_override_v1 - resolved value for this date.
	mobilityEnabled, err := s.resolveMobilityEnabled(a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuración de movilidad")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"special_date": map[string]any{
			"date":                  date,
			"is_active":             isActive != 0,
			"title":                 title,
			"description":           description.String,
			"prereserva_enabled":    prereservaEnabled != 0,
			"max_per_table_enabled": maxPerTableEnabled != 0,
			"max_per_table":         nullIfZero(maxPerTable.Valid, maxPerTable.Int64),
			"mobility_enabled":      mobilityEnabled,
			"requires_adelanto":     requiresAdelanto != 0,
			// Coordination id: reservation_self_modification_v1
			"allow_customer_modification": allowCustomerModification != 0,
			"adelanto_payment_methods":    adelantoMethods,
			"adelanto_unified":            adelantoUnified != 0,
			"adelanto_unified_amount":     nullIfZeroFloat(adelantoUnifiedAmount.Valid, adelantoUnifiedAmount.Float64),
			"prereserva_starts_on":        nullIfEmptyTime(prereservaStartsOn),
			"prereserva_ends_on":          nullIfEmptyTime(prereservaEndsOn),
			"menus":                       menus,
		},
	})
}

// handleBOSpecialDatesList returns every active special date for the tenant
// with its menu labels plus current occupancy (people vs daily limit), used by
// the backoffice Especial tab card list. Coordination id: special_dates_v1.
func (s *Server) handleBOSpecialDatesList(w http.ResponseWriter, r *http.Request, restaurantID int) {
	ctx := r.Context()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, date, is_active, title, prereserva_enabled
		FROM special_dates
		WHERE restaurant_id = ?
		ORDER BY date ASC
	`, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando special_dates")
		return
	}
	defer rows.Close()

	type listEntry struct {
		ID                int64
		Date              string
		IsActive          bool
		Title             string
		PrereservaEnabled bool
	}
	var entries []listEntry
	for rows.Next() {
		var e listEntry
		var isActive, prereserva int
		var d time.Time
		if err := rows.Scan(&e.ID, &d, &isActive, &e.Title, &prereserva); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo special_dates")
			return
		}
		e.Date = d.Format("2006-01-02")
		e.IsActive = isActive != 0
		e.PrereservaEnabled = prereserva != 0
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo special_dates")
		return
	}

	out := make([]map[string]any, 0, len(entries))
	if len(entries) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "special_dates": out})
		return
	}

	ids := make([]any, 0, len(entries)*2)
	placeholders := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
		placeholders = append(placeholders, "?")
	}

	// Menu labels per special date (custom title wins over catalogue title).
	labelsByDate := map[int64][]string{}
	labelRows, err := s.db.QueryContext(ctx, `
		SELECT sdm.special_date_id,
		       COALESCE(NULLIF(sdm.custom_title, ''), m.menu_title, CONCAT('Menú #', sdm.menu_id))
		FROM special_date_menus sdm
		LEFT JOIN menus m ON m.id = sdm.menu_id
		WHERE sdm.restaurant_id = ? AND sdm.special_date_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY sdm.position ASC, sdm.id ASC
	`, append([]any{restaurantID}, ids...)...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando special_date_menus")
		return
	}
	for labelRows.Next() {
		var sdID int64
		var label string
		if err := labelRows.Scan(&sdID, &label); err != nil {
			labelRows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo special_date_menus")
			return
		}
		labelsByDate[sdID] = append(labelsByDate[sdID], label)
	}
	labelRows.Close()

	// Occupancy: people booked per date.
	dates := make([]any, 0, len(entries))
	datePlaceholders := make([]string, 0, len(entries))
	for _, e := range entries {
		dates = append(dates, e.Date)
		datePlaceholders = append(datePlaceholders, "?")
	}
	peopleByDate := map[string]int{}
	peopleRows, err := s.db.QueryContext(ctx, `
		SELECT reservation_date, COALESCE(SUM(party_size), 0)
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date IN (`+strings.Join(datePlaceholders, ",")+`)
		GROUP BY reservation_date
	`, append([]any{restaurantID}, dates...)...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando ocupación")
		return
	}
	for peopleRows.Next() {
		var d time.Time
		var people int
		if err := peopleRows.Scan(&d, &people); err != nil {
			peopleRows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo ocupación")
			return
		}
		peopleByDate[d.Format("2006-01-02")] = people
	}
	peopleRows.Close()

	// Daily limit per date (latest reservation_manager row, default 45 like availability).
	limitByDate := map[string]int{}
	limitRows, err := s.db.QueryContext(ctx, `
		SELECT rm.reservationDate, rm.dailyLimit
		FROM reservation_manager rm
		INNER JOIN (
			SELECT reservationDate, MAX(id) AS max_id
			FROM reservation_manager
			WHERE restaurant_id = ?
			GROUP BY reservationDate
		) latest ON latest.reservationDate = rm.reservationDate AND latest.max_id = rm.id
		WHERE rm.restaurant_id = ?
	`, restaurantID, restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando límites")
		return
	}
	for limitRows.Next() {
		var d time.Time
		var lim int
		if err := limitRows.Scan(&d, &lim); err != nil {
			limitRows.Close()
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo límites")
			return
		}
		limitByDate[d.Format("2006-01-02")] = lim
	}
	limitRows.Close()

	for _, e := range entries {
		limit := limitByDate[e.Date]
		if limit <= 0 {
			limit = 45
		}
		labels := labelsByDate[e.ID]
		if labels == nil {
			labels = []string{}
		}
		// Coordination id: mobility_day_override_v1 - resolved value per date.
		mobilityEnabled, err := s.resolveMobilityEnabled(restaurantID, e.Date)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error consultando configuración de movilidad")
			return
		}
		out = append(out, map[string]any{
			"id":                 e.ID, // special_menu_price_date_v1: menu link target
			"date":               e.Date,
			"title":              e.Title,
			"is_active":          e.IsActive,
			"prereserva_enabled": e.PrereservaEnabled,
			"mobility_enabled":   mobilityEnabled,
			"menus":              labels,
			"people":             peopleByDate[e.Date],
			"limit":              limit,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "special_dates": out})
}

// loadBOSpecialDateMenus reads the child rows for one special date.
func (s *Server) loadBOSpecialDateMenus(ctx context.Context, restaurantID int, specialDateID int64) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, menu_id, custom_title, custom_image_url, adelanto_amount, price, position
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
		var adelantoAmount, price sql.NullFloat64
		var position int
		if err := rows.Scan(&id, &menuID, &customTitle, &customImageURL, &adelantoAmount, &price, &position); err != nil {
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
		if price.Valid {
			row["price"] = price.Float64
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// Coordination id: special_date_section_menus_v1 - special-type menus
	// carry their sections (title, price, per-section adelanto).
	for _, row := range out {
		menuID, ok := row["menu_id"].(int64)
		if !ok || !s.specialDateMenuIsSpecialType(ctx, restaurantID, menuID) {
			continue
		}
		row["is_special_menu"] = true
		row["sections"] = s.loadSpecialDateMenuSections(ctx, restaurantID, row["id"].(int64), menuID, false)
	}
	return out, nil
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
	// Coordination id: stripe_prereserva_adelanto_v1 - stripe is exclusive and
	// needs the restaurant's Stripe config (demo or live).
	for _, m := range cleanMethods {
		if m != adelantoMethodStripe {
			continue
		}
		if len(cleanMethods) > 1 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Stripe no se puede combinar con otros métodos de pago",
			})
			return
		}
		if _, ready := s.connectReady(r.Context(), a.ActiveRestaurantID); !ready {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Activa los cobros online en Configuración → Cobros online antes de usar Stripe",
			})
			return
		}
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
	// Coordination id: reservation_self_modification_v1
	allowCustomerModificationReq := false
	if req.AllowCustomerModification != nil {
		allowCustomerModificationReq = *req.AllowCustomerModification
	}
	// Coordination id: mobility_issues_v1
	mobilityEnabledReq := false
	if req.MobilityEnabled != nil {
		mobilityEnabledReq = *req.MobilityEnabled
	}
	var maxPerTableVal any
	maxPerTableVal = nil
	if maxPerTableEnabled && maxPerTable > 0 {
		maxPerTableVal = maxPerTable
	}

	_, err = tx.ExecContext(r.Context(), `
		INSERT INTO special_dates
			(restaurant_id, date, is_active, title, description, prereserva_enabled,
			 max_per_table_enabled, max_per_table, mobility_enabled, requires_adelanto, allow_customer_modification, adelanto_payment_methods,
			 adelanto_unified, adelanto_unified_amount, prereserva_starts_on, prereserva_ends_on)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			is_active = VALUES(is_active),
			title = VALUES(title),
			description = VALUES(description),
			prereserva_enabled = VALUES(prereserva_enabled),
			max_per_table_enabled = VALUES(max_per_table_enabled),
			max_per_table = VALUES(max_per_table),
			mobility_enabled = VALUES(mobility_enabled),
			requires_adelanto = VALUES(requires_adelanto),
			allow_customer_modification = VALUES(allow_customer_modification),
			adelanto_payment_methods = VALUES(adelanto_payment_methods),
			adelanto_unified = VALUES(adelanto_unified),
			adelanto_unified_amount = VALUES(adelanto_unified_amount),
			prereserva_starts_on = VALUES(prereserva_starts_on),
			prereserva_ends_on = VALUES(prereserva_ends_on)
	`, a.ActiveRestaurantID, date, boolToInt(isActive), title, description, boolToInt(prereservaEnabled),
		boolToInt(maxPerTableEnabled), maxPerTableVal, boolToInt(mobilityEnabledReq), boolToInt(requiresAdelanto), boolToInt(allowCustomerModificationReq), string(adelantoMethodsJSON),
		boolToInt(adelantoUnified), normalizeAdelantoAmount(adelantoUnifiedAmount), prereservaStartsOn, prereservaEndsOn)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando special_dates")
		return
	}

	// Coordination id: mobility_day_override_v1 - an explicit mobility_enabled
	// in the request also upserts the per-day override (same transaction) so
	// the resolved flag matches this choice.
	if req.MobilityEnabled != nil {
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO mobility_day_override (restaurant_id, reservationDate, mobility_enabled)
			VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE mobility_enabled = VALUES(mobility_enabled)
		`, a.ActiveRestaurantID, date, boolToInt(*req.MobilityEnabled)); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error guardando mobility_day_override")
			return
		}
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
		var menuID, adelantoAmount, price any
		menuID = nil
		adelantoAmount = nil
		price = nil
		if m.MenuID != nil {
			menuID = *m.MenuID
		}
		if m.AdelantoAmount != nil {
			adelantoAmount = *m.AdelantoAmount
		}
		if m.Price != nil && *m.Price >= 0 {
			price = *m.Price
		}
		customTitle := sql.NullString{}
		if m.CustomTitle != nil && strings.TrimSpace(*m.CustomTitle) != "" {
			customTitle = sql.NullString{String: strings.TrimSpace(*m.CustomTitle), Valid: true}
		}
		customImage := sql.NullString{}
		if m.CustomImageURL != nil && strings.TrimSpace(*m.CustomImageURL) != "" {
			customImage = sql.NullString{String: strings.TrimSpace(*m.CustomImageURL), Valid: true}
		}
		res, err := tx.ExecContext(r.Context(), `
			INSERT INTO special_date_menus
				(restaurant_id, special_date_id, menu_id, custom_title, custom_image_url, adelanto_amount, price, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, a.ActiveRestaurantID, specialDateID, menuID, customTitle, customImage, adelantoAmount, price, m.Position)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error insertando special_date_menus")
			return
		}
		// Coordination id: special_date_section_menus_v1
		if m.MenuID != nil && len(m.Sections) > 0 {
			sdmID, _ := res.LastInsertId()
			if err := saveSpecialDateMenuSections(r.Context(), tx, a.ActiveRestaurantID, sdmID, *m.MenuID, m.Sections); err != nil {
				httpx.WriteError(w, http.StatusInternalServerError, "Error guardando adelantos por seccion")
				return
			}
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
