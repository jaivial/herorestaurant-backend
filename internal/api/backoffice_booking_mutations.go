package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleBOBookingsExport(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}

	bookings, err := s.boFetchBookingsForExport(r.Context(), a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando bookings")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"bookings": bookings,
	})
}

func (s *Server) handleBOBookingGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid booking id",
		})
		return
	}

	b, err := s.boFetchBookingByID(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Booking not found",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando booking")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"booking": b,
	})
}

type boBookingUpsertReq struct {
	ReservationDate string `json:"reservation_date"`
	ReservationTime string `json:"reservation_time"`
	PartySize       int    `json:"party_size"`
	CustomerName    string `json:"customer_name"`
	ContactPhone    string `json:"contact_phone"`
	ContactPhoneCC  string `json:"contact_phone_country_code"`

	ContactEmail  *string `json:"contact_email,omitempty"`
	TableNumber   *string `json:"table_number,omitempty"`
	Commentary    *string `json:"commentary,omitempty"`
	BabyStrollers *int    `json:"babyStrollers,omitempty"`
	HighChairs    *int    `json:"highChairs,omitempty"`

	// Multi-arroz (non group menu).
	ArrozTypes    []string `json:"arroz_types,omitempty"`
	ArrozServings []int    `json:"arroz_servings,omitempty"`

	// Menú de grupo.
	SpecialMenu    bool            `json:"special_menu"`
	MenuDeGrupoID  *int            `json:"menu_de_grupo_id,omitempty"`
	PrincipalesRaw json.RawMessage `json:"principales_json,omitempty"`
}

func (s *Server) handleBOBookingCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boBookingUpsertReq
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	booking, err := s.boNormalizeAndValidateBookingInput(r.Context(), a.ActiveRestaurantID, boNormalizeInput{
		ReservationDate:         req.ReservationDate,
		ReservationTime:         req.ReservationTime,
		PartySize:               req.PartySize,
		CustomerName:            req.CustomerName,
		ContactPhone:            req.ContactPhone,
		ContactPhoneCountryCode: req.ContactPhoneCC,
		ContactEmail:            req.ContactEmail,
		TableNumber:             req.TableNumber,
		Commentary:              req.Commentary,
		BabyStrollers:           req.BabyStrollers,
		HighChairs:              req.HighChairs,
		ArrozTypes:              req.ArrozTypes,
		ArrozServings:           req.ArrozServings,
		ArrozTypesTouched:       len(req.ArrozTypes) > 0 || len(req.ArrozServings) > 0,
		ArrozServingsTouched:    len(req.ArrozTypes) > 0 || len(req.ArrozServings) > 0,
		SpecialMenu:             req.SpecialMenu,
		MenuDeGrupoID:           req.MenuDeGrupoID,
		PrincipalesRaw:          req.PrincipalesRaw,
	})
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	id, err := s.boInsertBooking(r.Context(), a.ActiveRestaurantID, booking)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando booking")
		return
	}

	out, err := s.boFetchBookingByID(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"id":      id,
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"booking": out,
	})

	s.emitN8nWebhookAsync(a.ActiveRestaurantID, "booking.created", map[string]any{
		"source":          "backoffice",
		"bookingId":       id,
		"reservationDate": booking.ReservationDate,
		"reservationTime": booking.ReservationTime,
		"partySize":       booking.PartySize,
		"customerName":    booking.CustomerName,
		"contactPhone":    booking.ContactPhone,
		"contactEmail":    booking.ContactEmail,
		"specialMenu":     booking.SpecialMenu,
		"menuDeGrupoId":   booking.MenuDeGrupoID,
	})
}

type boBookingPatchReq struct {
	ReservationDate *string `json:"reservation_date,omitempty"`
	ReservationTime *string `json:"reservation_time,omitempty"`
	PartySize       *int    `json:"party_size,omitempty"`
	CustomerName    *string `json:"customer_name,omitempty"`
	ContactPhone    *string `json:"contact_phone,omitempty"`
	ContactPhoneCC  *string `json:"contact_phone_country_code,omitempty"`
	ContactEmail    *string `json:"contact_email,omitempty"`
	TableNumber     *string `json:"table_number,omitempty"`
	Commentary      *string `json:"commentary,omitempty"`
	BabyStrollers   *int    `json:"babyStrollers,omitempty"`
	HighChairs      *int    `json:"highChairs,omitempty"`

	ArrozTypes    *[]string `json:"arroz_types,omitempty"`
	ArrozServings *[]int    `json:"arroz_servings,omitempty"`

	SpecialMenu    *bool            `json:"special_menu,omitempty"`
	MenuDeGrupoID  *int             `json:"menu_de_grupo_id,omitempty"`
	PrincipalesRaw *json.RawMessage `json:"principales_json,omitempty"`
}

func (s *Server) handleBOBookingPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid booking id",
		})
		return
	}

	current, err := s.boFetchBookingByID(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Booking not found",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando booking")
		return
	}

	var req boBookingPatchReq
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	// Apply patch to current (typed as map[string]any) into normalize input.
	input := boNormalizeInput{
		ReservationDate:         current["reservation_date"].(string),
		ReservationTime:         current["reservation_time"].(string),
		PartySize:               int(current["party_size"].(int64)),
		CustomerName:            current["customer_name"].(string),
		ContactPhone:            anyToString(current["contact_phone"]),
		ContactPhoneCountryCode: anyToString(current["contact_phone_country_code"]),
	}

	if v := strings.TrimSpace(anyToString(current["contact_email"])); v != "" {
		input.ContactEmail = &v
	}
	if v := strings.TrimSpace(anyToString(current["table_number"])); v != "" {
		input.TableNumber = &v
	}
	if v := strings.TrimSpace(anyToString(current["commentary"])); v != "" {
		input.Commentary = &v
	}
	if v, ok := current["babyStrollers"].(int64); ok {
		n := int(v)
		input.BabyStrollers = &n
	}
	if v, ok := current["highChairs"].(int64); ok {
		n := int(v)
		input.HighChairs = &n
	}
	if v, ok := current["special_menu"].(bool); ok {
		input.SpecialMenu = v
	}
	if v, ok := current["menu_de_grupo_id"].(int64); ok && v > 0 {
		n := int(v)
		input.MenuDeGrupoID = &n
	}
	if v := strings.TrimSpace(anyToString(current["principales_json"])); v != "" {
		input.PrincipalesRaw = json.RawMessage(v)
	}

	// Apply patch fields.
	if req.ReservationDate != nil {
		input.ReservationDate = *req.ReservationDate
	}
	if req.ReservationTime != nil {
		input.ReservationTime = *req.ReservationTime
	}
	if req.PartySize != nil {
		input.PartySize = *req.PartySize
	}
	if req.CustomerName != nil {
		input.CustomerName = *req.CustomerName
	}
	if req.ContactPhone != nil {
		input.ContactPhone = *req.ContactPhone
	}
	if req.ContactPhoneCC != nil {
		input.ContactPhoneCountryCode = *req.ContactPhoneCC
	}
	if req.ContactEmail != nil {
		input.ContactEmail = req.ContactEmail
	}
	if req.TableNumber != nil {
		input.TableNumber = req.TableNumber
	}
	if req.Commentary != nil {
		input.Commentary = req.Commentary
	}
	if req.BabyStrollers != nil {
		input.BabyStrollers = req.BabyStrollers
	}
	if req.HighChairs != nil {
		input.HighChairs = req.HighChairs
	}
	if req.SpecialMenu != nil {
		input.SpecialMenu = *req.SpecialMenu
	}
	if req.MenuDeGrupoID != nil {
		input.MenuDeGrupoID = req.MenuDeGrupoID
	}
	if req.PrincipalesRaw != nil {
		input.PrincipalesRaw = json.RawMessage(*req.PrincipalesRaw)
	}

	if req.ArrozTypes != nil {
		input.ArrozTypes = *req.ArrozTypes
		input.ArrozTypesTouched = true
	}
	if req.ArrozServings != nil {
		input.ArrozServings = *req.ArrozServings
		input.ArrozServingsTouched = true
	}

	next, err := s.boNormalizeAndValidateBookingInput(r.Context(), a.ActiveRestaurantID, input)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Preserve arroz if the patch didn't touch it and the booking wasn't a group menu (both before and after).
	arrozTouched := input.ArrozTypesTouched || input.ArrozServingsTouched
	currentIsSpecial, _ := current["special_menu"].(bool)
	if !next.SpecialMenu && !arrozTouched && !currentIsSpecial {
		next.ArrozTypeJSON = current["arroz_type"]
		next.ArrozServingsJSON = current["arroz_servings"]
	}

	if err := s.boUpdateBooking(r.Context(), a.ActiveRestaurantID, id, next); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error actualizando booking")
		return
	}

	out, err := s.boFetchBookingByID(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"booking": out,
	})
}

func (s *Server) handleBOArrozTypes(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT DESCRIPCION
		FROM FINDE
		WHERE restaurant_id = ? AND TIPO = 'ARROZ' AND (active = 1 OR active IS NULL)
		ORDER BY DESCRIPCION ASC
	`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando FINDE")
		return
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var desc sql.NullString
		if err := rows.Scan(&desc); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo FINDE")
			return
		}
		if !desc.Valid {
			continue
		}
		s := strings.TrimSpace(desc.String)
		if s == "" {
			continue
		}
		out = append(out, s)
	}

	// Return a bare JSON array (legacy compatible).
	httpx.WriteJSON(w, http.StatusOK, out)
}

// --- Data helpers ---

type boNormalizedBooking struct {
	ReservationDate         string
	ReservationTime         string
	PartySize               int
	CustomerName            string
	ContactPhone            string
	ContactPhoneCountryCode string
	ContactEmail            string
	TableNumber             sql.NullString
	Commentary              sql.NullString
	BabyStrollers           int
	HighChairs              int

	ArrozTypeJSON     any
	ArrozServingsJSON any

	SpecialMenu     bool
	MenuDeGrupoID   any
	PrincipalesJSON any
}

type boNormalizeInput struct {
	ReservationDate         string
	ReservationTime         string
	PartySize               int
	CustomerName            string
	ContactPhone            string
	ContactPhoneCountryCode string

	ContactEmail  *string
	TableNumber   *string
	Commentary    *string
	BabyStrollers *int
	HighChairs    *int

	ArrozTypes           []string
	ArrozServings        []int
	ArrozTypesTouched    bool
	ArrozServingsTouched bool

	SpecialMenu    bool
	MenuDeGrupoID  *int
	PrincipalesRaw json.RawMessage
}

func (s *Server) boNormalizeAndValidateBookingInput(ctx context.Context, restaurantID int, in boNormalizeInput) (boNormalizedBooking, error) {
	out := boNormalizedBooking{}

	date := strings.TrimSpace(in.ReservationDate)
	if date == "" || !isValidISODate(date) {
		return out, errors.New("Fecha inválida (YYYY-MM-DD)")
	}

	partySize := in.PartySize
	if partySize <= 0 {
		return out, errors.New("Número de personas inválido")
	}

	resTime, err := ensureHHMMSS(in.ReservationTime)
	if err != nil || resTime == "" {
		return out, errors.New("Hora inválida")
	}

	name := strings.TrimSpace(in.CustomerName)
	if name == "" {
		return out, errors.New("Nombre inválido")
	}

	cc, nationalPhone, _, ok := normalizePhoneParts(in.ContactPhoneCountryCode, in.ContactPhone)
	if !ok {
		return out, errors.New("Teléfono inválido")
	}

	email := ""
	if in.ContactEmail != nil {
		email = strings.TrimSpace(*in.ContactEmail)
	}
	if email == "" {
		email = s.restaurantFallbackEmail(ctx, restaurantID)
	}

	out.ReservationDate = date
	out.ReservationTime = resTime
	out.PartySize = partySize
	out.CustomerName = name
	out.ContactPhone = nationalPhone
	out.ContactPhoneCountryCode = cc
	out.ContactEmail = email

	if in.TableNumber != nil {
		v := strings.TrimSpace(*in.TableNumber)
		if v != "" {
			out.TableNumber = sql.NullString{String: v, Valid: true}
		}
	}

	if in.Commentary != nil {
		v := strings.TrimSpace(*in.Commentary)
		if v != "" {
			out.Commentary = sql.NullString{String: v, Valid: true}
		}
	}

	if in.BabyStrollers != nil && *in.BabyStrollers >= 0 {
		out.BabyStrollers = *in.BabyStrollers
	}
	if in.HighChairs != nil && *in.HighChairs >= 0 {
		out.HighChairs = *in.HighChairs
	}

	out.SpecialMenu = in.SpecialMenu

	if in.SpecialMenu {
		menuID := 0
		if in.MenuDeGrupoID != nil {
			menuID = *in.MenuDeGrupoID
		}
		if menuID <= 0 {
			return out, errors.New("Debe seleccionar un menú de grupo")
		}

		menuTitle, menuPrincipalesRaw, err := s.boFetchActiveGroupMenuTitleAndPrincipales(ctx, restaurantID, menuID)
		if err != nil || strings.TrimSpace(menuTitle) == "" {
			return out, errors.New("Menú de grupo no válido o inactivo")
		}

		bt, _ := json.Marshal([]string{menuTitle})
		bs, _ := json.Marshal([]int{partySize})
		out.ArrozTypeJSON = string(bt)
		out.ArrozServingsJSON = string(bs)

		out.MenuDeGrupoID = menuID

		rowsRaw := strings.TrimSpace(string(in.PrincipalesRaw))
		if rowsRaw == "" {
			rowsRaw = "[]"
		}
		summary, storedJSON, err := buildPrincipalesSummaryAndJSON(menuPrincipalesRaw, rowsRaw, partySize)
		if err != nil {
			return out, err
		}
		if strings.TrimSpace(summary) != "" {
			out.Commentary = sql.NullString{String: summary, Valid: true}
		} else {
			out.Commentary = sql.NullString{}
		}
		if strings.TrimSpace(storedJSON) != "" {
			out.PrincipalesJSON = storedJSON
		} else {
			out.PrincipalesJSON = nil
		}

		return out, nil
	}

	// Non group-menu: arroz (only if explicitly provided by the caller).
	if in.ArrozTypesTouched || in.ArrozServingsTouched {
		types := in.ArrozTypes
		servs := in.ArrozServings
		arrozTypeJSON, arrozServJSON, err := parseArrozFromArrays(types, servs, partySize)
		if err != nil {
			return out, err
		}
		out.ArrozTypeJSON = arrozTypeJSON
		out.ArrozServingsJSON = arrozServJSON
	}

	return out, nil
}

func parseArrozFromArrays(types []string, servs []int, partySize int) (arrozTypeJSON any, arrozServingsJSON any, err error) {
	if len(types) == 0 && len(servs) == 0 {
		return nil, nil, nil
	}
	if len(types) != len(servs) {
		return nil, nil, errors.New("Selección de arroz inválida")
	}
	if len(types) == 0 {
		return nil, nil, nil
	}

	seen := map[string]bool{}
	cleanTypes := make([]string, 0, len(types))
	cleanServs := make([]int, 0, len(types))
	sum := 0
	for i := 0; i < len(types); i++ {
		t := strings.TrimSpace(types[i])
		sv := 0
		if i < len(servs) {
			sv = servs[i]
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

func (s *Server) boFetchActiveGroupMenuTitleAndPrincipales(ctx context.Context, restaurantID int, menuID int) (title string, principalesRaw string, err error) {
	var t string
	var principales sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT menu_title, principales
		FROM menusDeGrupos
		WHERE restaurant_id = ? AND id = ? AND active = 1
		LIMIT 1
	`, restaurantID, menuID).Scan(&t, &principales)
	if err != nil {
		return "", "", err
	}
	return t, principales.String, nil
}

func (s *Server) boInsertBooking(ctx context.Context, restaurantID int, b boNormalizedBooking) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO bookings (
			restaurant_id,
			reservation_date,
			party_size,
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
			table_number
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		restaurantID,
		b.ReservationDate,
		b.PartySize,
		b.ReservationTime,
		b.CustomerName,
		b.ContactPhone,
		b.ContactPhoneCountryCode,
		nullableStringOrNil(b.Commentary),
		b.ArrozTypeJSON,
		b.ArrozServingsJSON,
		b.BabyStrollers,
		b.HighChairs,
		b.ContactEmail,
		boolToTinyint(b.SpecialMenu),
		b.MenuDeGrupoID,
		b.PrincipalesJSON,
		nullableStringOrNil(b.TableNumber),
	)
	if err != nil {
		return 0, err
	}
	id64, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(id64), nil
}

func (s *Server) boUpdateBooking(ctx context.Context, restaurantID int, id int, b boNormalizedBooking) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE bookings SET
			reservation_date = ?,
			reservation_time = ?,
			party_size = ?,
			customer_name = ?,
			contact_phone = ?,
			contact_phone_country_code = ?,
			contact_email = ?,
			table_number = ?,
			commentary = ?,
			babyStrollers = ?,
			highChairs = ?,
			arroz_type = ?,
			arroz_servings = ?,
			special_menu = ?,
			menu_de_grupo_id = ?,
			principales_json = ?
		WHERE restaurant_id = ? AND id = ?
	`,
		b.ReservationDate,
		b.ReservationTime,
		b.PartySize,
		b.CustomerName,
		b.ContactPhone,
		b.ContactPhoneCountryCode,
		b.ContactEmail,
		nullableStringOrNil(b.TableNumber),
		nullableStringOrNil(b.Commentary),
		b.BabyStrollers,
		b.HighChairs,
		b.ArrozTypeJSON,
		b.ArrozServingsJSON,
		boolToTinyint(b.SpecialMenu),
		b.MenuDeGrupoID,
		b.PrincipalesJSON,
		restaurantID,
		id,
	)
	return err
}

func (s *Server) boFetchBookingsForExport(ctx context.Context, restaurantID int, date string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id,
			customer_name,
			contact_email,
			DATE_FORMAT(reservation_date, '%Y-%m-%d') AS reservation_date,
			TIME_FORMAT(reservation_time, '%H:%i:%s') AS reservation_time,
			party_size,
			contact_phone,
			contact_phone_country_code,
			status,
			arroz_type,
			arroz_servings,
			commentary,
			babyStrollers,
			highChairs,
			table_number,
			DATE_FORMAT(added_date, '%Y-%m-%d %H:%i:%s') AS added_date,
			special_menu,
			menu_de_grupo_id,
			principales_json
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date = ?
		ORDER BY reservation_time ASC, id ASC
	`, restaurantID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type row struct {
		ID              int
		CustomerName    string
		ContactEmail    string
		ReservationDate string
		ReservationTime string
		PartySize       int
		ContactPhone    sql.NullString
		ContactPhoneCC  sql.NullString
		Status          sql.NullString
		ArrozType       sql.NullString
		ArrozServings   sql.NullString
		Commentary      sql.NullString
		BabyStrollers   sql.NullInt64
		HighChairs      sql.NullInt64
		TableNumber     sql.NullString
		AddedDate       sql.NullString
		SpecialMenu     sql.NullInt64
		MenuDeGrupoID   sql.NullInt64
		PrincipalesJSON sql.NullString
	}

	out := make([]map[string]any, 0)
	for rows.Next() {
		var b row
		if err := rows.Scan(
			&b.ID,
			&b.CustomerName,
			&b.ContactEmail,
			&b.ReservationDate,
			&b.ReservationTime,
			&b.PartySize,
			&b.ContactPhone,
			&b.ContactPhoneCC,
			&b.Status,
			&b.ArrozType,
			&b.ArrozServings,
			&b.Commentary,
			&b.BabyStrollers,
			&b.HighChairs,
			&b.TableNumber,
			&b.AddedDate,
			&b.SpecialMenu,
			&b.MenuDeGrupoID,
			&b.PrincipalesJSON,
		); err != nil {
			return nil, err
		}

		isSpecialMenu := b.SpecialMenu.Valid && b.SpecialMenu.Int64 != 0

		out = append(out, map[string]any{
			"id":                         b.ID,
			"customer_name":              b.CustomerName,
			"contact_email":              b.ContactEmail,
			"reservation_date":           b.ReservationDate,
			"reservation_time":           b.ReservationTime,
			"party_size":                 b.PartySize,
			"contact_phone":              nullStringOrNil(b.ContactPhone),
			"contact_phone_country_code": defaultString(b.ContactPhoneCC, "34"),
			"status":                     defaultString(b.Status, "pending"),
			"arroz_type":                 nullStringOrNil(b.ArrozType),
			"arroz_servings":             nullStringOrNil(b.ArrozServings),
			"commentary":                 nullStringOrNil(b.Commentary),
			"babyStrollers":              nullInt64OrNil(b.BabyStrollers),
			"highChairs":                 nullInt64OrNil(b.HighChairs),
			"table_number":               nullStringOrNil(b.TableNumber),
			"added_date":                 nullStringOrNil(b.AddedDate),
			"special_menu":               isSpecialMenu,
			"menu_de_grupo_id":           nullInt64OrNil(b.MenuDeGrupoID),
			"principales_json":           nullStringOrNil(b.PrincipalesJSON),
		})
	}
	return out, nil
}

func (s *Server) boFetchBookingByID(ctx context.Context, restaurantID int, id int) (map[string]any, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			id,
			customer_name,
			contact_email,
			DATE_FORMAT(reservation_date, '%Y-%m-%d') AS reservation_date,
			TIME_FORMAT(reservation_time, '%H:%i:%s') AS reservation_time,
			party_size,
			contact_phone,
			contact_phone_country_code,
			status,
			arroz_type,
			arroz_servings,
			commentary,
			babyStrollers,
			highChairs,
			table_number,
			DATE_FORMAT(added_date, '%Y-%m-%d %H:%i:%s') AS added_date,
			special_menu,
			menu_de_grupo_id,
			principales_json
		FROM bookings
		WHERE restaurant_id = ? AND id = ?
		LIMIT 1
	`, restaurantID, id)

	var (
		bookingID       int
		customerName    string
		contactEmail    string
		resDate         string
		resTime         string
		partySize       int
		contactPhone    sql.NullString
		contactPhoneCC  sql.NullString
		status          sql.NullString
		arrozType       sql.NullString
		arrozServings   sql.NullString
		commentary      sql.NullString
		babyStrollers   sql.NullInt64
		highChairs      sql.NullInt64
		tableNumber     sql.NullString
		addedDate       sql.NullString
		specialMenu     sql.NullInt64
		menuDeGrupoID   sql.NullInt64
		principalesJSON sql.NullString
	)
	if err := row.Scan(
		&bookingID,
		&customerName,
		&contactEmail,
		&resDate,
		&resTime,
		&partySize,
		&contactPhone,
		&contactPhoneCC,
		&status,
		&arrozType,
		&arrozServings,
		&commentary,
		&babyStrollers,
		&highChairs,
		&tableNumber,
		&addedDate,
		&specialMenu,
		&menuDeGrupoID,
		&principalesJSON,
	); err != nil {
		return nil, err
	}

	isSpecialMenu := specialMenu.Valid && specialMenu.Int64 != 0

	return map[string]any{
		"id":                         bookingID,
		"customer_name":              customerName,
		"contact_email":              contactEmail,
		"reservation_date":           resDate,
		"reservation_time":           resTime,
		"party_size":                 int64(partySize),
		"contact_phone":              nullStringOrNil(contactPhone),
		"contact_phone_country_code": defaultString(contactPhoneCC, "34"),
		"status":                     defaultString(status, "pending"),
		"arroz_type":                 nullStringOrNil(arrozType),
		"arroz_servings":             nullStringOrNil(arrozServings),
		"commentary":                 nullStringOrNil(commentary),
		"babyStrollers":              nullInt64OrNil(babyStrollers),
		"highChairs":                 nullInt64OrNil(highChairs),
		"table_number":               nullStringOrNil(tableNumber),
		"added_date":                 nullStringOrNil(addedDate),
		"special_menu":               isSpecialMenu,
		"menu_de_grupo_id":           nullInt64OrNil(menuDeGrupoID),
		"principales_json":           nullStringOrNil(principalesJSON),
	}, nil
}

func nullableStringOrNil(ns sql.NullString) any {
	if !ns.Valid {
		return nil
	}
	v := strings.TrimSpace(ns.String)
	if v == "" {
		return nil
	}
	return v
}
