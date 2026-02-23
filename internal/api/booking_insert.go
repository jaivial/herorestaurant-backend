package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleInsertBookingFront(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := parseLegacyForm(r, 5<<20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Faltan campos requeridos",
		})
		return
	}

	// Honeypot (bot protection).
	if strings.TrimSpace(r.FormValue("website_url")) != "" {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{
			"success": false,
			"message": "Spam detected.",
		})
		return
	}
	// Minimum time-to-submit: 5 seconds (best-effort).
	if raw := strings.TrimSpace(r.FormValue("form_load_time")); raw != "" {
		if ts, err := strconv.ParseInt(raw, 10, 64); err == nil {
			if time.Now().Unix()-ts < 5 {
				httpx.WriteJSON(w, http.StatusForbidden, map[string]any{
					"success": false,
					"message": "Spam detected. Submission too fast.",
				})
				return
			}
		}
	}

	resDate := strings.TrimSpace(r.FormValue("reservation_date"))
	partySize := clampInt(r.FormValue("party_size"), 1, 10_000, 0)
	resTimeRaw := strings.TrimSpace(r.FormValue("reservation_time"))
	customerName := strings.TrimSpace(r.FormValue("customer_name"))
	contactPhoneRaw := strings.TrimSpace(r.FormValue("contact_phone"))
	countryCodeRaw := strings.TrimSpace(r.FormValue("country_code"))

	cc, nationalPhone, phoneE164, ok := normalizePhoneParts(countryCodeRaw, contactPhoneRaw)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Teléfono inválido",
		})
		return
	}

	if resDate == "" || !isValidISODate(resDate) || partySize < 2 || resTimeRaw == "" || customerName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Faltan campos requeridos",
		})
		return
	}
	resTime, err := ensureHHMMSS(resTimeRaw)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Hora inválida",
		})
		return
	}

	commentary := strings.TrimSpace(r.FormValue("commentary"))
	babyStrollers := clampInt(r.FormValue("baby_strollers"), 0, 100, 0)
	highChairs := clampInt(r.FormValue("high_chairs"), 0, 100, 0)
	contactEmail := strings.TrimSpace(r.FormValue("contact_email"))
	if contactEmail == "" {
		contactEmail = s.restaurantFallbackEmail(r.Context(), restaurantID)
	}

	children, err := parseChildrenFromForm(r, partySize)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Group menu (special menu) selection.
	specialMenu := clampInt(r.FormValue("menu_de_grupo_selected"), 0, 1, 0) == 1
	menuDeGrupoID := 0
	var principalesJSON any = nil
	toggleArroz := strings.TrimSpace(r.FormValue("toggleArroz"))

	var arrozTypeJSON any = nil
	var arrozServingsJSON any = nil
	preferredFloorNumber, err := s.resolvePreferredFloorNumberForFront(r.Context(), restaurantID, resDate, strings.TrimSpace(r.FormValue("preferred_floor_number")))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if specialMenu {
		menuDeGrupoID = clampInt(r.FormValue("menu_de_grupo_id"), 1, 1_000_000_000, 0)
		if menuDeGrupoID <= 0 {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "Debe seleccionar un menú de grupo",
			})
			return
		}

		menuTitle, menuPrincipalesRaw, err := s.fetchActiveGroupMenuTitleAndPrincipales(r, menuDeGrupoID)
		if err != nil || strings.TrimSpace(menuTitle) == "" {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "Menú de grupo no válido o inactivo",
			})
			return
		}

		// Store menu title and party size in arroz_* JSON arrays (legacy behavior).
		bt, _ := json.Marshal([]string{menuTitle})
		bs, _ := json.Marshal([]int{partySize})
		arrozTypeJSON = string(bt)
		arrozServingsJSON = string(bs)
		toggleArroz = "false"

		// Commentary is reserved for principales summary.
		commentary = ""

		principalesEnabled := strings.TrimSpace(r.FormValue("principales_enabled")) == "1"
		rowsRaw := strings.TrimSpace(r.FormValue("principales_json"))
		if rowsRaw == "" {
			rowsRaw = "[]"
		}

		if principalesEnabled {
			summary, storedJSON, err := buildPrincipalesSummaryAndJSON(menuPrincipalesRaw, rowsRaw, partySize)
			if err != nil {
				httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
					"success": false,
					"message": err.Error(),
				})
				return
			}
			commentary = summary
			if storedJSON != "" {
				principalesJSON = storedJSON
			}
		}
	} else {
		// Regular booking: arroz follows toggleArroz.
		if strings.TrimSpace(toggleArroz) == "true" {
			arrozTypeJSON, arrozServingsJSON, err = parseArrozFromForm(r, partySize)
			if err != nil {
				httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
					"success": false,
					"message": err.Error(),
				})
				return
			}
		}

	}

	bookingID, err := s.insertBooking(r, bookingInsertParams{
		ReservationDate:   resDate,
		ReservationTime:   resTime,
		PartySize:         partySize,
		Children:          children,
		CustomerName:      customerName,
		ContactPhone:      nationalPhone,
		ContactPhoneCC:    cc,
		ContactEmail:      contactEmail,
		Commentary:        commentary,
		BabyStrollers:     babyStrollers,
		HighChairs:        highChairs,
		ArrozTypeJSON:     arrozTypeJSON,
		ArrozServingsJSON: arrozServingsJSON,
		SpecialMenu:       boolToTinyint(specialMenu),
		MenuDeGrupoID:     nullIntOrNil(menuDeGrupoID),
		PrincipalesJSON:   principalesJSON,
		PreferredFloorNum: preferredFloorNumber,
	})
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success":    false,
			"message":    "Error: " + err.Error(),
			"error_code": "BOOKING_INSERT_FAILED",
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":            true,
		"message":            "¡Reserva realizada con éxito!",
		"booking_id":         bookingID,
		"notifications_sent": false,
		"email_sent":         false,
		"whatsapp_sent":      false,
	})

	s.emitN8nWebhookAsync(restaurantID, "booking.created", map[string]any{
		"source":                  "front",
		"bookingId":               bookingID,
		"reservationDate":         resDate,
		"reservationTime":         resTime,
		"partySize":               partySize,
		"children":                children,
		"customerName":            customerName,
		"contactPhone":            nationalPhone,
		"contactPhoneCountryCode": cc,
		"contactPhoneE164":        phoneE164,
		"contactEmail":            contactEmail,
		"specialMenu":             specialMenu,
		"menuDeGrupoId":           menuDeGrupoID,
		"preferredFloorNumber":    preferredFloorNumber,
	})
}

func (s *Server) handleInsertBookingAdmin(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := parseLegacyForm(r, 5<<20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid input",
		})
		return
	}

	resDate := strings.TrimSpace(r.FormValue("date"))
	partySize := clampInt(r.FormValue("party_size"), 1, 10_000, 0)
	resTimeRaw := strings.TrimSpace(r.FormValue("time"))
	customerName := strings.TrimSpace(r.FormValue("nombre"))
	contactPhoneRaw := strings.TrimSpace(r.FormValue("phone"))
	countryCodeRaw := strings.TrimSpace(r.FormValue("country_code"))

	cc, nationalPhone, phoneE164, ok := normalizePhoneParts(countryCodeRaw, contactPhoneRaw)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Teléfono inválido",
		})
		return
	}

	if resDate == "" || !isValidISODate(resDate) || partySize < 1 || resTimeRaw == "" || customerName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid input",
		})
		return
	}
	resTime, err := ensureHHMMSS(resTimeRaw)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Hora inválida",
		})
		return
	}

	commentary := strings.TrimSpace(r.FormValue("commentary"))
	babyStrollers := clampInt(r.FormValue("baby_strollers"), 0, 100, 0)
	highChairs := clampInt(r.FormValue("high_chairs"), 0, 100, 0)
	contactEmail := strings.TrimSpace(r.FormValue("contact_email"))
	if contactEmail == "" {
		contactEmail = s.restaurantFallbackEmail(r.Context(), restaurantID)
	}

	children, err := parseChildrenFromForm(r, partySize)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	specialMenu := strings.TrimSpace(r.FormValue("special_menu")) == "1"
	menuDeGrupoID := 0
	var principalesJSON any = nil
	toggleArroz := strings.TrimSpace(r.FormValue("toggleArroz"))

	var arrozTypeJSON any = nil
	var arrozServingsJSON any = nil

	if specialMenu {
		menuDeGrupoID = clampInt(r.FormValue("menu_de_grupo_id"), 1, 1_000_000_000, 0)
		if menuDeGrupoID <= 0 {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "Debe seleccionar un menú de grupo",
			})
			return
		}

		menuTitle, menuPrincipalesRaw, err := s.fetchActiveGroupMenuTitleAndPrincipales(r, menuDeGrupoID)
		if err != nil || strings.TrimSpace(menuTitle) == "" {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "Menú de grupo no válido o inactivo",
			})
			return
		}

		bt, _ := json.Marshal([]string{menuTitle})
		bs, _ := json.Marshal([]int{partySize})
		arrozTypeJSON = string(bt)
		arrozServingsJSON = string(bs)
		toggleArroz = "false"
		commentary = ""

		principalesEnabled := strings.TrimSpace(r.FormValue("principales_enabled")) == "1"
		rowsRaw := strings.TrimSpace(r.FormValue("principales_json"))
		if rowsRaw == "" {
			rowsRaw = "[]"
		}
		if principalesEnabled {
			summary, storedJSON, err := buildPrincipalesSummaryAndJSON(menuPrincipalesRaw, rowsRaw, partySize)
			if err != nil {
				httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
					"success": false,
					"message": err.Error(),
				})
				return
			}
			commentary = summary
			if storedJSON != "" {
				principalesJSON = storedJSON
			}
		}
	} else {
		// Regular booking: arroz can be selected.
		wantsArroz := strings.TrimSpace(toggleArroz) == "true"
		if wantsArroz {
			arrozTypeJSON, arrozServingsJSON, err = parseArrozFromForm(r, partySize)
			if err != nil {
				httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
					"success": false,
					"message": err.Error(),
				})
				return
			}
		}
	}

	bookingID, err := s.insertBooking(r, bookingInsertParams{
		ReservationDate:   resDate,
		ReservationTime:   resTime,
		PartySize:         partySize,
		Children:          children,
		CustomerName:      customerName,
		ContactPhone:      nationalPhone,
		ContactPhoneCC:    cc,
		ContactEmail:      contactEmail,
		Commentary:        commentary,
		BabyStrollers:     babyStrollers,
		HighChairs:        highChairs,
		ArrozTypeJSON:     arrozTypeJSON,
		ArrozServingsJSON: arrozServingsJSON,
		SpecialMenu:       boolToTinyint(specialMenu),
		MenuDeGrupoID:     nullIntOrNil(menuDeGrupoID),
		PrincipalesJSON:   principalesJSON,
	})
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error: " + err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"booking_id":    bookingID,
		"whatsapp_sent": false,
	})

	s.emitN8nWebhookAsync(restaurantID, "booking.created", map[string]any{
		"source":                  "admin",
		"bookingId":               bookingID,
		"reservationDate":         resDate,
		"reservationTime":         resTime,
		"partySize":               partySize,
		"children":                children,
		"customerName":            customerName,
		"contactPhone":            nationalPhone,
		"contactPhoneCountryCode": cc,
		"contactPhoneE164":        phoneE164,
		"contactEmail":            contactEmail,
		"specialMenu":             specialMenu,
		"menuDeGrupoId":           menuDeGrupoID,
	})
}

type bookingInsertParams struct {
	ReservationDate   string
	ReservationTime   string
	PartySize         int
	Children          int
	CustomerName      string
	ContactPhone      string
	ContactPhoneCC    string
	ContactEmail      string
	Commentary        string
	BabyStrollers     int
	HighChairs        int
	ArrozTypeJSON     any
	ArrozServingsJSON any
	SpecialMenu       int
	MenuDeGrupoID     any
	PrincipalesJSON   any
	PreferredFloorNum any
}

func (s *Server) insertBooking(r *http.Request, p bookingInsertParams) (int64, error) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return 0, errors.New("unknown restaurant")
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(r.Context(), `
		INSERT INTO bookings (
			restaurant_id,
			reservation_date,
			party_size,
			children,
			reservation_time,
			customer_name,
			contact_phone,
			contact_phone_country_code,
			commentary,
			arroz_type,
			arroz_servings,
			babyStrollers,
			highChairs,
			contact_email,
			special_menu,
			menu_de_grupo_id,
			principales_json,
			preferred_floor_number
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, restaurantID, p.ReservationDate, p.PartySize, p.Children, p.ReservationTime, p.CustomerName, p.ContactPhone, p.ContactPhoneCC, p.Commentary, p.ArrozTypeJSON, p.ArrozServingsJSON, p.BabyStrollers, p.HighChairs, p.ContactEmail, p.SpecialMenu, p.MenuDeGrupoID, p.PrincipalesJSON, p.PreferredFloorNum)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Server) resolvePreferredFloorNumberForFront(ctx context.Context, restaurantID int, date string, raw string) (any, error) {
	floors, err := s.loadDateFloors(ctx, restaurantID, date)
	if err != nil {
		return nil, errors.New("No se pudo consultar las plantas activas")
	}
	active := make([]boConfigFloor, 0, len(floors))
	for _, floor := range floors {
		if floor.Active {
			active = append(active, floor)
		}
	}
	if len(active) == 0 {
		return nil, errors.New("No hay salones activos para la fecha seleccionada")
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		if len(active) == 1 {
			return active[0].FloorNumber, nil
		}
		return nil, errors.New("Debe seleccionar un salón")
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil, errors.New("Salón inválido")
	}
	for _, floor := range active {
		if floor.FloorNumber == n {
			return n, nil
		}
	}
	return nil, errors.New("El salón seleccionado no está disponible para la fecha")
}

func normalizePhoneParts(countryCodeRaw, phoneRaw string) (countryCode string, national string, e164Digits string, ok bool) {
	cc := onlyDigits(countryCodeRaw)
	phone := onlyDigits(phoneRaw)

	if cc == "" {
		cc = "34"
	}
	// Country calling codes are 1-4 digits in E.164.
	if len(cc) < 1 || len(cc) > 4 {
		return "", "", "", false
	}

	// If the user provided a full E.164 number in the phone field, avoid double-prefixing.
	if len(phone) >= 8 && len(phone) <= 15 && strings.HasPrefix(phone, cc) && len(phone) > 9 {
		n := phone[len(cc):]
		if n == "" {
			return "", "", "", false
		}
		return cc, n, phone, true
	}

	// National number length varies widely; keep a permissive range but enforce E.164 max length.
	if len(phone) < 6 || len(phone) > 15 {
		return "", "", "", false
	}
	if len(cc)+len(phone) > 15 {
		return "", "", "", false
	}
	return cc, phone, cc + phone, true
}

func parseChildrenFromForm(r *http.Request, partySize int) (int, error) {
	// Preferred: adults -> children = partySize - adults.
	adultsRaw := strings.TrimSpace(r.FormValue("adults"))
	if adultsRaw != "" {
		n, err := strconv.Atoi(adultsRaw)
		if err != nil || n < 1 || n > partySize {
			return 0, errors.New("Número de adultos inválido")
		}
		return partySize - n, nil
	}

	// Backwards-compatible fallback: children field directly.
	childrenRaw := strings.TrimSpace(r.FormValue("children"))
	if childrenRaw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(childrenRaw)
	if err != nil || n < 0 || n > partySize {
		return 0, errors.New("Número de niños inválido")
	}
	return n, nil
}

func (s *Server) fetchActiveGroupMenuTitleAndPrincipales(r *http.Request, menuID int) (title string, principalesRaw string, err error) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return "", "", errors.New("unknown restaurant")
	}

	var t string
	var principales sql.NullString
	err = s.db.QueryRowContext(r.Context(), "SELECT menu_title, principales FROM menusDeGrupos WHERE restaurant_id = ? AND id = ? AND active = 1 LIMIT 1", restaurantID, menuID).Scan(&t, &principales)
	if err != nil {
		return "", "", err
	}
	return t, principales.String, nil
}

func parseArrozFromForm(r *http.Request, partySize int) (arrozTypeJSON any, arrozServingsJSON any, err error) {
	typesRaw := strings.TrimSpace(r.FormValue("arroz_types_json"))
	servRaw := strings.TrimSpace(r.FormValue("arroz_servings_json"))

	var types []any
	var servs []any
	if typesRaw != "" && servRaw != "" {
		_ = json.Unmarshal([]byte(typesRaw), &types)
		_ = json.Unmarshal([]byte(servRaw), &servs)
	}

	// Backward compatibility: single arroz.
	if len(types) == 0 || len(servs) == 0 || len(types) != len(servs) {
		singleType := strings.TrimSpace(r.FormValue("arroz_type"))
		singleServ := clampInt(r.FormValue("arroz_servings"), 0, 10_000, 0)
		if singleType != "" && singleServ > 0 {
			types = []any{singleType}
			servs = []any{singleServ}
		} else {
			types = nil
			servs = nil
		}
	}

	seen := map[string]bool{}
	cleanTypes := make([]string, 0, len(types))
	cleanServs := make([]int, 0, len(types))
	sum := 0
	for i := 0; i < len(types); i++ {
		t := strings.TrimSpace(anyToString(types[i]))
		sv := 0
		if i < len(servs) {
			sv, _ = anyToInt(servs[i])
		}
		if t == "" || sv <= 0 {
			continue
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		sum += sv
		cleanTypes = append(cleanTypes, t)
		cleanServs = append(cleanServs, sv)
	}

	if sum > partySize {
		return nil, nil, errors.New("Las raciones de arroz superan el número de comensales")
	}

	if len(cleanTypes) == 0 {
		return nil, nil, nil
	}
	bt, _ := json.Marshal(cleanTypes)
	bs, _ := json.Marshal(cleanServs)
	return string(bt), string(bs), nil
}

func buildPrincipalesSummaryAndJSON(menuPrincipalesRaw string, rowsRaw string, partySize int) (summary string, storedJSON string, err error) {
	// Allowed list from menu.
	allowed := map[string]bool{}
	if strings.TrimSpace(menuPrincipalesRaw) != "" {
		var mp struct {
			Items []string `json:"items"`
		}
		if err := json.Unmarshal([]byte(menuPrincipalesRaw), &mp); err == nil {
			for _, it := range mp.Items {
				it = strings.TrimSpace(it)
				if it == "" {
					continue
				}
				allowed[it] = true
			}
		}
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(rowsRaw), &rows); err != nil {
		rows = []map[string]any{}
	}

	seen := map[string]bool{}
	total := 0
	parts := []string{}
	clean := make([]map[string]any, 0, len(rows))

	for _, row := range rows {
		name := strings.TrimSpace(anyToString(row["name"]))
		servings, _ := anyToInt(row["servings"])
		if name == "" || servings <= 0 {
			continue
		}
		if seen[name] {
			continue
		}
		if len(allowed) > 0 && !allowed[name] {
			continue
		}
		seen[name] = true
		total += servings
		parts = append(parts, name+" x "+strconv.Itoa(servings))
		clean = append(clean, map[string]any{"name": name, "servings": servings})
	}

	if total > partySize {
		return "", "", errors.New("Las raciones de principales superan el número de comensales")
	}

	if len(clean) > 0 {
		b, _ := json.Marshal(clean)
		storedJSON = string(b)
	}
	return strings.Join(parts, ", "), storedJSON, nil
}

func nullIntOrNil(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}
