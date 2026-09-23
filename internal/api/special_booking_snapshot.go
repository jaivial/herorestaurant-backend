package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Special booking snapshot + validation + computed helpers.
//
// Coordination id: special_booking_v1
//
// Reused by:
//   - admin booking create / patch (backoffice_booking_mutations.go)
//   - public booking create (booking_insert.go handleInsertBookingFront)
//   - bookingRow + scanBookingRow response shape (backoffice_bookings.go,
//     backoffice_booking_mutations.go list / byID / export handlers)
//   - WhatsApp bot tools (whatsapp_bot_special_tools.go)
//
// The pattern follows the existing extras_json / principales_json snapshot
// columns: at booking time we copy the date's settings into a stable JSON
// payload, store is_special_booking + is_prereserva as cheap DB-only filters,
// and recompute the totals every time the booking is read (never stored).
// All queries are tenant-scoped via restaurantID.

// specialBookingSnapshotMenu is one menu row stored inside special_json. The
// field set mirrors SPEC §3 verbatim. Server-only fields like items[] carry
// the principals selected for non-custom menus.
type specialBookingSnapshotMenu struct {
	SpecialDateMenuID     int64                        `json:"special_date_menu_id"`
	MenuID                *int64                       `json:"menu_id,omitempty"`
	Label                 string                       `json:"label"`
	UnitPrice             float64                      `json:"unit_price"`
	Count                 int                          `json:"count"`
	AdelantoPerUnit       float64                      `json:"adelanto_per_unit"`
	AdelantoPaymentMethod *string                      `json:"adelanto_payment_method,omitempty"`
	Items                 []specialBookingSnapshotItem `json:"items,omitempty"`
	// Coordination id: special_date_section_menus_v1 - set when this line is
	// one section of a special-type menu.
	SectionID *int64 `json:"section_id,omitempty"`
}

// specialBookingSnapshotItem is one dish selection inside a special menu.
type specialBookingSnapshotItem struct {
	DishID int64  `json:"dish_id"`
	Name   string `json:"name"`
}

// specialBookingAdelantoPaid is one method/amount entry the backoffice
// records when the customer pays part of the deposit.
type specialBookingAdelantoPaid struct {
	Method string  `json:"method"`
	Amount float64 `json:"amount"`
}

// specialBookingSnapshot is the on-disk shape stored in bookings.special_json
// (SPEC §3) and re-emitted verbatim by the response helpers.
type specialBookingSnapshot struct {
	Title         string                       `json:"title"`
	Menus         []specialBookingSnapshotMenu `json:"menus"`
	PaymentMethod *string                      `json:"payment_method,omitempty"`
	AdelantosPaid []specialBookingAdelantoPaid `json:"adelantos_paid,omitempty"`
}

// --- request DTOs ------------------------------------------------------------

// specialBookingMenuReq is one element of the `special.menus[]` array sent by
// the admin / public form. Server fields like label / unit_price are NOT
// accepted from the client (the server snapshots them from special_dates).
type specialBookingMenuReq struct {
	SpecialDateMenuID     int64                 `json:"special_date_menu_id"`
	Count                 int                   `json:"count"`
	AdelantoPaymentMethod *string               `json:"adelanto_payment_method,omitempty"`
	Items                 specialBookingItemIDs `json:"items,omitempty"`
	// Coordination id: special_date_section_menus_v1 - guests per section of a
	// special-type menu (then Count is the sum of the section counts).
	Sections []specialBookingSectionReq `json:"sections,omitempty"`
}

// specialBookingSectionReq is one section of a special-type menu in a booking.
type specialBookingSectionReq struct {
	SectionID int64                 `json:"section_id"`
	Count     int                   `json:"count"`
	Items     specialBookingItemIDs `json:"items,omitempty"`
}

// specialBookingItemIDs accepts both [12, 13] and [{"dish_id": 12}, ...]: the
// public wizard and the backoffice editor send the object form.
// Coordination id: special_menu_principales_v1
type specialBookingItemIDs []int64

func (ids *specialBookingItemIDs) UnmarshalJSON(raw []byte) error {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return err
	}
	out := make([]int64, 0, len(entries))
	for _, e := range entries {
		var id int64
		if json.Unmarshal(e, &id) != nil {
			var obj struct {
				DishID int64 `json:"dish_id"`
			}
			if err := json.Unmarshal(e, &obj); err != nil {
				return err
			}
			id = obj.DishID
		}
		if id > 0 {
			out = append(out, id)
		}
	}
	*ids = out
	return nil
}

// specialBookingReq is the shape of the optional `special` block on
// admin booking upsert/patch and on the public booking insert. title is NOT
// accepted — the server snapshots it from the date's settings.
type specialBookingReq struct {
	Menus         []specialBookingMenuReq       `json:"menus"`
	PaymentMethod *string                       `json:"payment_method,omitempty"`
	AdelantosPaid []specialBookingAdelantoPaid  `json:"adelantos_paid,omitempty"`
}

// --- server-side loaders -----------------------------------------------------

// specialDateMenuRecord mirrors one row of special_date_menus with the
// additional denormalised title/price the snapshot needs.
type specialDateMenuRecord struct {
	ID             int64
	MenuID         sql.NullInt64
	CustomTitle    sql.NullString
	CustomImageURL sql.NullString
	AdelantoAmount sql.NullFloat64
	Position       int
	MenuTitle      string
	MenuPrice      float64
	// Coordination id: special_menu_principales_v1 - special-type menus take
	// their principales from special_menu_section_principales.
	MenuType string
}

// specialDateSettings carries the fields the snapshot helper needs from a
// special_dates row. Anything not represented here is intentionally out of
// scope for booking-snapshot logic.
type specialDateSettings struct {
	Title                  string
	IsActive               bool
	PrereservaEnabled      bool
	RequiresAdelanto       bool
	AdelantoPaymentMethods []string
	AdelantoUnified        bool
	AdelantoUnifiedAmount  *float64
}

// loadSpecialDateSettings returns the active special-date row + menu records
// for the given (restaurant, date). Returns nil, nil, nil when the date has
// no row or is_active is 0.
func (s *Server) loadSpecialDateSettings(ctx context.Context, restaurantID int, date string) (*specialDateSettings, []specialDateMenuRecord, error) {
	var (
		id                                                          int64
		isActive, prereservaEnabled, requiresAdelanto, adelantoUnified int
		title                                                       string
		adelantoMethodsRaw                                          sql.NullString
		adelantoUnifiedAmount                                       sql.NullFloat64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, is_active, title, prereserva_enabled, requires_adelanto,
		       adelanto_payment_methods, adelanto_unified, adelanto_unified_amount
		FROM special_dates
		WHERE restaurant_id = ? AND date = ?
		LIMIT 1
	`, restaurantID, date).Scan(
		&id, &isActive, &title, &prereservaEnabled, &requiresAdelanto,
		&adelantoMethodsRaw, &adelantoUnified, &adelantoUnifiedAmount,
	)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if isActive == 0 {
		return nil, nil, nil
	}
	var adelantoMethods []string
	if adelantoMethodsRaw.Valid && strings.TrimSpace(adelantoMethodsRaw.String) != "" {
		_ = json.Unmarshal([]byte(adelantoMethodsRaw.String), &adelantoMethods)
	}
	if adelantoMethods == nil {
		adelantoMethods = []string{}
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT sdm.id, sdm.menu_id, sdm.custom_title, sdm.custom_image_url,
		       sdm.adelanto_amount, sdm.price, sdm.position,
		       COALESCE(m.menu_title, '') AS menu_title,
		       COALESCE(m.price, 0) AS menu_price,
		       COALESCE(m.menu_type, '') AS menu_type
		FROM special_date_menus sdm
		LEFT JOIN menus m
		  ON m.id = sdm.menu_id AND m.restaurant_id = sdm.restaurant_id
		  AND m.active = 1 AND m.is_draft = 0
		WHERE sdm.restaurant_id = ? AND sdm.special_date_id = ?
		ORDER BY sdm.position ASC, sdm.id ASC
	`, restaurantID, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	out := make([]specialDateMenuRecord, 0, 4)
	for rows.Next() {
		var rec specialDateMenuRecord
		var customPrice sql.NullFloat64
		if err := rows.Scan(
			&rec.ID, &rec.MenuID, &rec.CustomTitle, &rec.CustomImageURL,
			&rec.AdelantoAmount, &customPrice, &rec.Position,
			&rec.MenuTitle, &rec.MenuPrice, &rec.MenuType,
		); err != nil {
			return nil, nil, err
		}
		// Prefer the operator-defined price (customPrice) over the catalogue price
		// (MenuPrice) when present so the booking snapshot reflects the deal.
		if customPrice.Valid {
			rec.MenuPrice = customPrice.Float64
		} else if !rec.MenuID.Valid {
			// Custom uploaded menus without an explicit price snap to 0.
			rec.MenuPrice = 0
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	settings := &specialDateSettings{
		Title:                  title,
		IsActive:               true,
		PrereservaEnabled:      prereservaEnabled != 0,
		RequiresAdelanto:       requiresAdelanto != 0,
		AdelantoPaymentMethods: adelantoMethods,
		AdelantoUnified:        adelantoUnified != 0,
	}
	if adelantoUnifiedAmount.Valid {
		v := adelantoUnifiedAmount.Float64
		settings.AdelantoUnifiedAmount = &v
	}
	return settings, out, nil
}

// --- parse + validate --------------------------------------------------------

// resolveSpecialBookingInput validates the `special` block against the date's
// active settings and returns the snapshot + the bookkeeping flags to store.
//
// `partySize` must be > 0; `date` must be an ISO date with an active
// special_dates row; every menu id must belong to that date; counts must be
// > 0; when any menu is chosen Σ(counts) must equal partySize;
// adelanto_payment_method (per menu) must belong to the date's accepted
// methods; adelantos_paid amounts must be ≥ 0; payment_method must belong to
// the date's accepted methods; items[] is allowed only for non-custom menus
// and every dish must exist for that menu (legacy DIA/FINDE principal+arroz
// for custom menus).
//
// When the caller passes nil `req`, the function returns nil, nil, nil — the
// caller should treat the booking as a regular non-special one.
func (s *Server) resolveSpecialBookingInput(
	ctx context.Context,
	restaurantID int,
	date string,
	partySize int,
	req *specialBookingReq,
) (*specialBookingSnapshot, *bool, error) {
	if req == nil {
		return nil, nil, nil
	}
	if partySize <= 0 {
		return nil, nil, errors.New("Número de personas inválido")
	}

	settings, menuRecords, err := s.loadSpecialDateSettings(ctx, restaurantID, date)
	if err != nil {
		return nil, nil, err
	}
	if settings == nil {
		return nil, nil, errors.New("La fecha seleccionada no es una fecha especial activa")
	}
	if len(req.Menus) == 0 {
		return nil, nil, errors.New("Debe seleccionar al menos un menú especial")
	}

	menusByID := make(map[int64]specialDateMenuRecord, len(menuRecords))
	for _, m := range menuRecords {
		menusByID[m.ID] = m
	}

	acceptedMethods := make(map[string]bool, len(settings.AdelantoPaymentMethods))
	for _, m := range settings.AdelantoPaymentMethods {
		m = strings.TrimSpace(m)
		if m != "" {
			acceptedMethods[m] = true
		}
	}

	// payment_method (public booking single choice) subset of accepted methods.
	if req.PaymentMethod != nil {
		pm := strings.TrimSpace(*req.PaymentMethod)
		if pm == "" {
			req.PaymentMethod = nil
		} else if !acceptedMethods[pm] {
			return nil, nil, fmt.Errorf("Método de pago inválido: %s", pm)
		}
	}

	// adelantos_paid amounts must be >= 0 (any method allowed; the snapshot is
	// the source of truth and the totals helper recomputes the balance).
	for i := range req.AdelantosPaid {
		if req.AdelantosPaid[i].Amount < 0 {
			return nil, nil, errors.New("Los importes pagados no pueden ser negativos")
		}
		method := strings.TrimSpace(req.AdelantosPaid[i].Method)
		if method == "" {
			return nil, nil, errors.New("Falta el método de pago en los adelantos pagados")
		}
		if !acceptedMethods[method] {
			return nil, nil, fmt.Errorf("Método de pago inválido en adelantos: %s", method)
		}
		req.AdelantosPaid[i].Method = method
	}

	totalCount := 0
	snapshotMenus := make([]specialBookingSnapshotMenu, 0, len(req.Menus))
	for _, m := range req.Menus {
		rec, ok := menusByID[m.SpecialDateMenuID]
		if !ok {
			return nil, nil, fmt.Errorf("Menú especial %d no disponible", m.SpecialDateMenuID)
		}
		// Coordination id: special_date_section_menus_v1 - a special-type menu
		// is booked per section: one snapshot line per section, priced and
		// charged with that section's price and adelanto.
		if rec.MenuID.Valid && rec.MenuType == "special" {
			var unified *float64
			if settings.AdelantoUnified && settings.AdelantoUnifiedAmount != nil {
				unified = settings.AdelantoUnifiedAmount
			}
			lines, count, err := s.specialMenuSectionSnapshotLines(ctx, restaurantID, rec, m, acceptedMethods, unified)
			if err != nil {
				return nil, nil, err
			}
			totalCount += count
			snapshotMenus = append(snapshotMenus, lines...)
			continue
		}
		if m.Count <= 0 {
			return nil, nil, fmt.Errorf("La cantidad del menú %d debe ser mayor que 0", m.SpecialDateMenuID)
		}
		totalCount += m.Count

		isCustom := !rec.MenuID.Valid

		// Items only allowed for non-custom menus; for custom menus dishes can
		// be any principal|arroz from DIA/FINDE/menu_dishes_catalog.
		var snapItems []specialBookingSnapshotItem
		if len(m.Items) > 0 {
			if isCustom {
				ok, err := s.allDishesExistForTenant(ctx, restaurantID, m.Items)
				if err != nil {
					return nil, nil, err
				}
				if !ok {
					return nil, nil, errors.New("Algunos platos seleccionados no existen")
				}
				snapItems = make([]specialBookingSnapshotItem, 0, len(m.Items))
				for _, id := range m.Items {
					name, _ := s.loadDishNameForTenant(ctx, restaurantID, id)
					snapItems = append(snapItems, specialBookingSnapshotItem{DishID: id, Name: name})
				}
			} else if rec.MenuType == "special" {
				items, ok := s.validateSpecialMenuPrincipalItems(ctx, restaurantID, rec.MenuID.Int64, m.Items)
				if !ok {
					return nil, nil, errors.New("Algunos platos seleccionados no pertenecen al menú")
				}
				snapItems = items
			} else {
				if !s.allMenuDishesExist(ctx, restaurantID, rec.MenuID.Int64, m.Items) {
					return nil, nil, errors.New("Algunos platos seleccionados no pertenecen al menú")
				}
				snapItems = make([]specialBookingSnapshotItem, 0, len(m.Items))
				for _, id := range m.Items {
					name, _ := s.loadMenuDishName(ctx, restaurantID, rec.MenuID.Int64, id)
					snapItems = append(snapItems, specialBookingSnapshotItem{DishID: id, Name: name})
				}
			}
		}

		// Snapshot the per-unit adelanto: date unified overrides per-menu when
		// the date has a unified amount; otherwise per-menu's adelanto_amount.
		adelantoPerUnit := 0.0
		if settings.AdelantoUnified && settings.AdelantoUnifiedAmount != nil {
			adelantoPerUnit = *settings.AdelantoUnifiedAmount
		} else if rec.AdelantoAmount.Valid {
			adelantoPerUnit = rec.AdelantoAmount.Float64
		}

		// Per-menu payment method (backoffice editable).
		var snapMethod *string
		if m.AdelantoPaymentMethod != nil {
			pm := strings.TrimSpace(*m.AdelantoPaymentMethod)
			if pm != "" {
				if !acceptedMethods[pm] {
					return nil, nil, fmt.Errorf("Método de pago inválido en menú %d", m.SpecialDateMenuID)
				}
				snapMethod = &pm
			}
		}

		label := strings.TrimSpace(rec.CustomTitle.String)
		unitPrice := 0.0
		var menuIDPtr *int64
		if !isCustom {
			mid := rec.MenuID.Int64
			menuIDPtr = &mid
			if strings.TrimSpace(rec.MenuTitle) != "" {
				label = strings.TrimSpace(rec.MenuTitle)
			}
			unitPrice = rec.MenuPrice
		}

		snapshotMenus = append(snapshotMenus, specialBookingSnapshotMenu{
			SpecialDateMenuID:     rec.ID,
			MenuID:                menuIDPtr,
			Label:                 label,
			UnitPrice:             unitPrice,
			Count:                 m.Count,
			AdelantoPerUnit:       adelantoPerUnit,
			AdelantoPaymentMethod: snapMethod,
			Items:                 snapItems,
		})
	}

	if totalCount != partySize {
		return nil, nil, fmt.Errorf("La suma de menús especiales (%d) debe coincidir con el número de comensales (%d)", totalCount, partySize)
	}

	prereserva := settings.PrereservaEnabled

	snap := &specialBookingSnapshot{
		Title:         settings.Title,
		Menus:         snapshotMenus,
		PaymentMethod: req.PaymentMethod,
	}
	if len(req.AdelantosPaid) > 0 {
		snap.AdelantosPaid = append([]specialBookingAdelantoPaid(nil), req.AdelantosPaid...)
	}
	return snap, &prereserva, nil
}

// --- response helpers --------------------------------------------------------

// buildSpecialBookingResponse builds the rich `special` block returned to
// callers per SPEC §4: items resolved to dish names, totals per method,
// pending amounts, amount_left, adelanto_status, title, is_prereserva.
// Returns nil when the booking is not a special booking (isSpecialBooking ==
// false or special_json empty).
func (s *Server) buildSpecialBookingResponse(ctx context.Context, restaurantID int, isSpecialBooking bool, isPrereserva bool, specialRaw string) map[string]any {
	if !isSpecialBooking {
		return nil
	}
	specialRaw = strings.TrimSpace(specialRaw)
	if specialRaw == "" {
		return nil
	}
	var snap specialBookingSnapshot
	if err := json.Unmarshal([]byte(specialRaw), &snap); err != nil {
		return nil
	}

	// Method rollups. We accumulate required + paid per method; the response
	// surfaces only methods with either required or paid > 0.
	requiredByMethod := map[string]float64{}
	paidByMethod := map[string]float64{}
	requiredTotal := 0.0
	paidTotal := 0.0
	amountTotal := 0.0

	for _, m := range snap.Menus {
		amountTotal += m.UnitPrice * float64(m.Count)
		if m.AdelantoPerUnit > 0 && m.AdelantoPaymentMethod != nil {
			required := m.AdelantoPerUnit * float64(m.Count)
			requiredByMethod[*m.AdelantoPaymentMethod] += required
			requiredTotal += required
		}
	}

	for _, p := range snap.AdelantosPaid {
		paidByMethod[p.Method] += p.Amount
		paidTotal += p.Amount
	}

	keys := make([]string, 0, len(requiredByMethod)+len(paidByMethod))
	seen := map[string]bool{}
	for k := range requiredByMethod {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range paidByMethod {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	byMethod := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		req := requiredByMethod[k]
		paid := paidByMethod[k]
		pending := req - paid
		if pending < 0 {
			pending = 0
		}
		byMethod = append(byMethod, map[string]any{
			"method":   k,
			"required": round2(req),
			"paid":     round2(paid),
			"pending":  round2(pending),
		})
	}

	pendingTotal := requiredTotal - paidTotal
	if pendingTotal < 0 {
		pendingTotal = 0
	}
	status := "paid"
	if pendingTotal > 0 {
		status = "pending"
	}
	amountLeft := amountTotal - paidTotal
	if amountLeft < 0 {
		amountLeft = 0
	}

	resolvedMenus := make([]map[string]any, 0, len(snap.Menus))
	for _, m := range snap.Menus {
		isCustom := m.MenuID == nil
		menuOut := map[string]any{
			"special_date_menu_id":  m.SpecialDateMenuID,
			"label":                 m.Label,
			"unit_price":            round2(m.UnitPrice),
			"count":                 m.Count,
			"adelanto_per_unit":     round2(m.AdelantoPerUnit),
		}
		if m.MenuID != nil {
			menuOut["menu_id"] = *m.MenuID
		}
		// Coordination id: special_date_section_menus_v1
		if m.SectionID != nil {
			menuOut["section_id"] = *m.SectionID
		}
		if m.AdelantoPaymentMethod != nil {
			menuOut["adelanto_payment_method"] = *m.AdelantoPaymentMethod
		}
		if len(m.Items) > 0 {
			items := make([]map[string]any, 0, len(m.Items))
			for _, it := range m.Items {
				name := strings.TrimSpace(it.Name)
				if name == "" {
					if isCustom {
						if lookupName, err := s.loadDishNameForTenant(ctx, restaurantID, it.DishID); err == nil {
							name = lookupName
						}
					} else if m.MenuID != nil {
						if lookupName, err := s.loadMenuDishName(ctx, restaurantID, *m.MenuID, it.DishID); err == nil {
							name = lookupName
						}
					}
				}
				items = append(items, map[string]any{
					"dish_id": it.DishID,
					"name":    name,
				})
			}
			menuOut["items"] = items
		}
		resolvedMenus = append(resolvedMenus, menuOut)
	}

	return map[string]any{
		"title":                   snap.Title,
		"is_prereserva":           isPrereserva,
		"menus":                   resolvedMenus,
		"adelanto_required_total": round2(requiredTotal),
		"adelanto_paid_total":     round2(paidTotal),
		"adelanto_pending_total":  round2(pendingTotal),
		"adelanto_status":         status,
		"adelanto_by_method":      byMethod,
		"amount_left":             round2(amountLeft),
	}
}

// --- dish existence helpers --------------------------------------------------

// loadDishNameForTenant returns the dish name from DIA|FINDE/menu_dishes_catalog
// (whichever has the row) for the given restaurant; used to resolve
// custom-menu items.
func (s *Server) loadDishNameForTenant(ctx context.Context, restaurantID int, dishID int64) (string, error) {
	var name sql.NullString
	if err := s.scanDishName(ctx, restaurantID, dishID, &name); err != nil {
		return "", err
	}
	if !name.Valid {
		return "", nil
	}
	return strings.TrimSpace(name.String), nil
}

func (s *Server) scanDishName(ctx context.Context, restaurantID int, dishID int64, dst *sql.NullString) error {
	row := s.db.QueryRowContext(ctx, `
		SELECT DESCRIPCION FROM DIA WHERE restaurant_id = ? AND NUM = ? LIMIT 1
	`, restaurantID, dishID)
	if err := row.Scan(dst); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	row = s.db.QueryRowContext(ctx, `
		SELECT DESCRIPCION FROM FINDE WHERE restaurant_id = ? AND NUM = ? LIMIT 1
	`, restaurantID, dishID)
	if err := row.Scan(dst); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	row = s.db.QueryRowContext(ctx, `
		SELECT name FROM menu_dishes_catalog WHERE restaurant_id = ? AND id = ? LIMIT 1
	`, restaurantID, dishID)
	if err := row.Scan(dst); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	dst.Valid = false
	return nil
}

// allDishesExistForTenant returns true when every dish id exists in DIA,
// FINDE, or menu_dishes_catalog for the given tenant. Used by custom menus.
func (s *Server) allDishesExistForTenant(ctx context.Context, restaurantID int, ids []int64) (bool, error) {
	if len(ids) == 0 {
		return true, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = strings.TrimRight(placeholders, ",")

	// Single pass: collect rows from any of the three sources and de-duplicate
	// by id. We avoid a UNION ALL count hack because that would double-count
	// ids that exist in multiple tables for the same restaurant.
	combined := make(map[int64]struct{}, len(ids))
	lookup := func(table, col string) error {
		rows, err := s.db.QueryContext(ctx,
			"SELECT "+col+" FROM "+table+" WHERE restaurant_id = ? AND "+col+" IN ("+placeholders+")",
			append([]any{restaurantID}, toAnySlice(ids)...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			combined[id] = struct{}{}
		}
		return rows.Err()
	}
	if err := lookup("DIA", "NUM"); err != nil {
		return false, err
	}
	if err := lookup("FINDE", "NUM"); err != nil {
		return false, err
	}
	if err := lookup("menu_dishes_catalog", "id"); err != nil {
		return false, err
	}
	return len(combined) >= len(ids), nil
}

// loadMenuDishName returns the snapshot title for a dish inside a menu.
// Falls back to the catalog title when the snapshot is empty.
func (s *Server) loadMenuDishName(ctx context.Context, restaurantID int, menuID int64, dishID int64) (string, error) {
	var name sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT d.title_snapshot
		FROM group_menu_section_dishes_v2 d
		WHERE d.restaurant_id = ? AND d.menu_id = ? AND d.id = ?
		LIMIT 1
	`, restaurantID, menuID, dishID).Scan(&name)
	if err == nil && name.Valid && strings.TrimSpace(name.String) != "" {
		return strings.TrimSpace(name.String), nil
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT c.title FROM menu_dishes_catalog c
		INNER JOIN group_menu_section_dishes_v2 d ON d.catalog_dish_id = c.id
		WHERE c.restaurant_id = ? AND d.restaurant_id = ? AND d.menu_id = ? AND d.id = ?
		LIMIT 1
	`, restaurantID, restaurantID, menuID, dishID)
	if err := row.Scan(&name); err == nil && name.Valid {
		return strings.TrimSpace(name.String), nil
	}
	return "", nil
}

// allMenuDishesExist returns true when every id in `ids` is a row of
// group_menu_section_dishes_v2 for the given tenant + menu.
func (s *Server) allMenuDishesExist(ctx context.Context, restaurantID int, menuID int64, ids []int64) bool {
	if len(ids) == 0 {
		return true
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = strings.TrimRight(placeholders, ",")
	args := append([]any{restaurantID, menuID}, toAnySlice(ids)...)
	var n int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM group_menu_section_dishes_v2
		WHERE restaurant_id = ? AND menu_id = ? AND id IN (`+placeholders+`)
	`, args...).Scan(&n); err != nil {
		return false
	}
	return n >= len(ids)
}

// toAnySlice turns a []int64 into the []any that database/sql expects when
// building IN (...) lists.
func toAnySlice(ids []int64) []any {
	out := make([]any, len(ids))
	for i, v := range ids {
		out[i] = v
	}
	return out
}

// round2 is provided by backoffice_members.go (math.Round to 2 decimals).