package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleFetchBookings(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid form data")
		return
	}

	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid date format. Use YYYY-MM-DD",
		})
		return
	}

	all := parseBoolParam(r.FormValue("all"), false)

	page := clampInt(r.FormValue("page"), 1, 1_000_000, 1)
	pageSize := clampInt(r.FormValue("page_size"), 1, 200, 15)

	timeSort := normalizeSortDir(r.FormValue("time_sort"))
	dateAddedSort := normalizeSortDir(r.FormValue("date_added_sort"))

	orderBy := "reservation_time ASC, id ASC"
	if timeSort != "none" {
		orderBy = "reservation_time " + strings.ToUpper(timeSort) + ", id ASC"
	} else if dateAddedSort != "none" {
		orderBy = "added_date " + strings.ToUpper(dateAddedSort) + ", id ASC"
	}

	var totalCount int
	var totalPeople int
	if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) AS total_count, COALESCE(SUM(party_size), 0) AS total_people FROM bookings WHERE restaurant_id = ? AND reservation_date = ?", restaurantID, date).Scan(&totalCount, &totalPeople); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error al cargar las reservas: " + err.Error(),
		})
		return
	}

	totalPages := 1
	if !all {
		if pageSize > 0 {
			totalPages = int(math.Ceil(float64(totalCount) / float64(pageSize)))
		}
		if totalPages <= 0 {
			totalPages = 1
		}
		if page > totalPages {
			page = totalPages
		}
	}
	offset := (page - 1) * pageSize

	sqlQuery := `
		SELECT
			id,
			reservation_date,
			customer_name,
			party_size,
			babyStrollers,
			highChairs,
			TIME_FORMAT(reservation_time, '%H:%i:%s') AS reservation_time,
			contact_phone,
			contact_email,
			arroz_type,
			arroz_servings,
			commentary,
			special_menu,
			menu_de_grupo_id,
			principales_json,
			added_date,
			re_confirmation,
			status,
			table_number
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date = ?
		ORDER BY ` + orderBy
	args := []any{restaurantID, date}
	if !all {
		sqlQuery += " LIMIT ? OFFSET ?"
		args = append(args, pageSize, offset)
	}

	rows, err := s.db.QueryContext(r.Context(), sqlQuery, args...)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error al cargar las reservas: " + err.Error(),
		})
		return
	}
	defer rows.Close()

	type bookingRow struct {
		ID              int
		ReservationDate string
		CustomerName    string
		PartySize       int
		BabyStrollers   sql.NullInt64
		HighChairs      sql.NullInt64
		ReservationTime string
		ContactPhone    sql.NullString
		ContactEmail    sql.NullString
		ArrozType       sql.NullString
		ArrozServings   sql.NullString
		Commentary      sql.NullString
		SpecialMenu     sql.NullInt64
		MenuDeGrupoID   sql.NullInt64
		PrincipalesJSON sql.NullString
		AddedDate       sql.NullString
		ReConfirmation  sql.NullInt64
		Status          sql.NullString
		TableNumber     sql.NullString
	}

	var bookings []map[string]any
	for rows.Next() {
		var b bookingRow
		if err := rows.Scan(
			&b.ID,
			&b.ReservationDate,
			&b.CustomerName,
			&b.PartySize,
			&b.BabyStrollers,
			&b.HighChairs,
			&b.ReservationTime,
			&b.ContactPhone,
			&b.ContactEmail,
			&b.ArrozType,
			&b.ArrozServings,
			&b.Commentary,
			&b.SpecialMenu,
			&b.MenuDeGrupoID,
			&b.PrincipalesJSON,
			&b.AddedDate,
			&b.ReConfirmation,
			&b.Status,
			&b.TableNumber,
		); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Error al cargar las reservas: " + err.Error(),
			})
			return
		}

		var arrozTypes any
		if b.ArrozType.Valid && strings.TrimSpace(b.ArrozType.String) != "" {
			arrozTypes = decodePossibleJSONValue(b.ArrozType.String)
		}
		var arrozServings any
		if b.ArrozServings.Valid && strings.TrimSpace(b.ArrozServings.String) != "" {
			arrozServings = decodePossibleJSONValue(b.ArrozServings.String)
		}

		var menuID any = nil
		if b.MenuDeGrupoID.Valid {
			menuID = b.MenuDeGrupoID.Int64
		}

		var principales any = nil
		if b.PrincipalesJSON.Valid && strings.TrimSpace(b.PrincipalesJSON.String) != "" {
			principales = b.PrincipalesJSON.String
		}

		bookings = append(bookings, map[string]any{
			"id":               b.ID,
			"reservation_date": b.ReservationDate,
			"customer_name":    b.CustomerName,
			"party_size":       b.PartySize,
			"babyStrollers":    nullIntToInt(b.BabyStrollers),
			"highChairs":       nullIntToInt(b.HighChairs),
			"reservation_time": b.ReservationTime,
			"contact_phone":    b.ContactPhone.String,
			"contact_email":    b.ContactEmail.String,
			"arroz_type":       arrozTypes,
			"arroz_servings":   arrozServings,
			"commentary":       b.Commentary.String,
			"special_menu":     int64OrZero(b.SpecialMenu),
			"menu_de_grupo_id": menuID,
			"principales_json": principales,
			"added_date":       b.AddedDate.String,
			"re_confirmation":  int64OrZero(b.ReConfirmation),
			"status":           defaultString(b.Status, "pending"),
			"table_number":     b.TableNumber.String,
		})
	}

	resp := map[string]any{
		"success":     true,
		"bookings":    bookings,
		"totalPeople": totalPeople,
		"count":       len(bookings),
		"page":        func() int { if all { return 1 }; return page }(),
		"page_size": func() int {
			if all {
				return totalCount
			}
			return pageSize
		}(),
		"total_count": totalCount,
		"total_pages": func() int { if all { return 1 }; return totalPages }(),
		"is_all":      all,
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetBooking(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Booking ID not provided.",
		})
		return
	}
	id := clampInt(r.FormValue("id"), 1, 1_000_000_000, 0)
	if id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid booking ID.",
		})
		return
	}

	row := s.db.QueryRowContext(r.Context(), `
		SELECT
			id,
			reservation_date,
			customer_name,
			party_size,
			babyStrollers,
			highChairs,
			TIME_FORMAT(reservation_time, '%H:%i:%s') AS reservation_time,
			contact_phone,
			contact_email,
			arroz_type,
			arroz_servings,
			commentary,
			added_date
		FROM bookings
		WHERE restaurant_id = ? AND id = ?
	`, restaurantID, id)

	var (
		bookingID int
		resDate string
		name string
		partySize int
		baby sql.NullInt64
		chairs sql.NullInt64
		resTime string
		phone sql.NullString
		email sql.NullString
		arrozType sql.NullString
		arrozServ sql.NullString
		commentary sql.NullString
		added sql.NullString
	)

	if err := row.Scan(&bookingID, &resDate, &name, &partySize, &baby, &chairs, &resTime, &phone, &email, &arrozType, &arrozServ, &commentary, &added); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "No booking found with the provided ID.",
			})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Error: " + err.Error(),
		})
		return
	}

	var arrozTypes any
	if arrozType.Valid && strings.TrimSpace(arrozType.String) != "" {
		arrozTypes = decodePossibleJSONValue(arrozType.String)
	}
	var arrozServings any
	if arrozServ.Valid && strings.TrimSpace(arrozServ.String) != "" {
		arrozServings = decodePossibleJSONValue(arrozServ.String)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"booking": map[string]any{
			"id":               bookingID,
			"reservation_date": resDate,
			"customer_name":    name,
			"party_size":       partySize,
			"babyStrollers":    nullIntToInt(baby),
			"highChairs":       nullIntToInt(chairs),
			"reservation_time": resTime,
			"contact_phone":    phone.String,
			"contact_email":    email.String,
			"arroz_type":       arrozTypes,
			"arroz_servings":   arrozServings,
			"commentary":       commentary.String,
			"added_date":       added.String,
		},
	})
}

func (s *Server) handleEditBooking(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid form data"})
		return
	}

	id := clampInt(r.FormValue("row_index"), 1, 1_000_000_000, 0)
	resDate := strings.TrimSpace(r.FormValue("date"))
	partySize := clampInt(r.FormValue("party_size"), 1, 10_000, 0)
	resTimeRaw := strings.TrimSpace(r.FormValue("time"))
	name := strings.TrimSpace(r.FormValue("nombre"))
	phoneDigits := onlyDigits(r.FormValue("phone"))

	if id <= 0 || !isValidISODate(resDate) || partySize <= 0 || resTimeRaw == "" || name == "" || len(phoneDigits) != 9 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Missing required fields"})
		return
	}

	resTime, err := ensureHHMMSS(resTimeRaw)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid reservation time"})
		return
	}

	commentary := strings.TrimSpace(r.FormValue("commentary"))
	babyStrollers := clampInt(r.FormValue("baby_strollers"), 0, 100, 0)
	highChairs := clampInt(r.FormValue("high_chairs"), 0, 100, 0)
	contactEmail := s.restaurantFallbackEmail(r.Context(), restaurantID)

	specialMenu := clampInt(r.FormValue("special_menu"), 0, 1, 0)

	var menuID any = nil
	var principalesJSON any = nil
	if specialMenu == 1 {
		if v := strings.TrimSpace(r.FormValue("menu_de_grupo_id")); v != "" {
			menuID = clampInt(v, 1, 1_000_000_000, 0)
			if menuID.(int) == 0 {
				menuID = nil
			}
		}
		raw := strings.TrimSpace(r.FormValue("principales_json"))
		if raw == "" {
			raw = "[]"
		}
		var arr []any
		if err := json.Unmarshal([]byte(raw), &arr); err == nil && len(arr) > 0 {
			encoded, _ := json.Marshal(arr)
			principalesJSON = string(encoded)
		}
	}

	var arrozTypeJSON any = nil
	var arrozServingsJSON any = nil
	if r.FormValue("arroz_types_json") != "" && r.FormValue("arroz_servings_json") != "" {
		var types []any
		var servs []any
		_ = json.Unmarshal([]byte(r.FormValue("arroz_types_json")), &types)
		_ = json.Unmarshal([]byte(r.FormValue("arroz_servings_json")), &servs)
		if len(types) > 0 && len(servs) > 0 {
			cleanTypes := make([]string, 0, len(types))
			cleanServs := make([]int, 0, len(types))
			for i := 0; i < len(types); i++ {
				t := strings.TrimSpace(anyToString(types[i]))
				sv := 0
				if i < len(servs) {
					sv, _ = anyToInt(servs[i])
				}
				if t == "" || sv <= 0 {
					continue
				}
				cleanTypes = append(cleanTypes, t)
				cleanServs = append(cleanServs, sv)
			}
			if len(cleanTypes) > 0 {
				bt, _ := json.Marshal(cleanTypes)
				bs, _ := json.Marshal(cleanServs)
				arrozTypeJSON = string(bt)
				arrozServingsJSON = string(bs)
			}
		}
	} else if strings.TrimSpace(r.FormValue("arroz_type")) != "" && strings.TrimSpace(r.FormValue("arroz_servings")) != "" {
		t := strings.TrimSpace(r.FormValue("arroz_type"))
		sv := clampInt(r.FormValue("arroz_servings"), 0, 10_000, 0)
		if t != "" && sv > 0 {
			bt, _ := json.Marshal([]string{t})
			bs, _ := json.Marshal([]int{sv})
			arrozTypeJSON = string(bt)
			arrozServingsJSON = string(bs)
		}
	}

	_, err = s.db.ExecContext(r.Context(), `
		UPDATE bookings SET
			reservation_date = ?,
			party_size = ?,
			reservation_time = ?,
			customer_name = ?,
			contact_phone = ?,
			commentary = ?,
			babyStrollers = ?,
			highChairs = ?,
			arroz_type = ?,
			arroz_servings = ?,
			contact_email = ?,
			special_menu = ?,
			menu_de_grupo_id = ?,
			principales_json = ?
		WHERE restaurant_id = ? AND id = ?
	`, resDate, partySize, resTime, name, phoneDigits, commentary, babyStrollers, highChairs, arrozTypeJSON, arrozServingsJSON, contactEmail, specialMenu, menuID, principalesJSON, restaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleDeleteBooking(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Booking ID not provided."})
		return
	}

	id := clampInt(r.FormValue("id"), 1, 1_000_000_000, 0)
	if id <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid booking ID."})
		return
	}

	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error: " + err.Error()})
		return
	}
	defer func() { _ = tx.Rollback() }()

	type booking struct {
		ID              int
		ReservationDate string
		PartySize       int
		ReservationTime string
		CustomerName    string
		ContactPhone    sql.NullString
		ContactEmail    sql.NullString
		Commentary      sql.NullString
		ArrozType       sql.NullString
		ArrozServings   sql.NullString
		BabyStrollers   sql.NullInt64
		HighChairs      sql.NullInt64
		SpecialMenu     sql.NullInt64
		MenuDeGrupoID   sql.NullInt64
		PrincipalesJSON sql.NullString
	}

	var b booking
	row := tx.QueryRowContext(ctx, `
		SELECT
			id,
			reservation_date,
			party_size,
			TIME_FORMAT(reservation_time, '%H:%i:%s') AS reservation_time,
			customer_name,
			contact_phone,
			contact_email,
			commentary,
			arroz_type,
			arroz_servings,
			babyStrollers,
			highChairs,
			special_menu,
			menu_de_grupo_id,
			principales_json
		FROM bookings
		WHERE restaurant_id = ? AND id = ?
	`, restaurantID, id)
	if err := row.Scan(
		&b.ID,
		&b.ReservationDate,
		&b.PartySize,
		&b.ReservationTime,
		&b.CustomerName,
		&b.ContactPhone,
		&b.ContactEmail,
		&b.Commentary,
		&b.ArrozType,
		&b.ArrozServings,
		&b.BabyStrollers,
		&b.HighChairs,
		&b.SpecialMenu,
		&b.MenuDeGrupoID,
		&b.PrincipalesJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No booking found with the provided ID."})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error: " + err.Error()})
		return
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO cancelled_bookings
			(restaurant_id, booking_id, reservation_date, party_size, reservation_time, customer_name,
			 contact_phone, contact_email, commentary, arroz_type, arroz_servings,
			 babyStrollers, highChairs, cancellation_date, cancelled_by,
			 special_menu, menu_de_grupo_id, principales_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), 'staff', ?, ?, ?)
	`,
		restaurantID,
		b.ID,
		b.ReservationDate,
		b.PartySize,
		b.ReservationTime,
		b.CustomerName,
		b.ContactPhone.String,
		b.ContactEmail.String,
		b.Commentary.String,
		nullStringOrNil(b.ArrozType),
		nullStringOrNil(b.ArrozServings),
		nullIntToInt(b.BabyStrollers),
		nullIntToInt(b.HighChairs),
		int64OrZero(b.SpecialMenu),
		nullInt64OrNil(b.MenuDeGrupoID),
		nullStringOrNil(b.PrincipalesJSON),
	)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error: " + err.Error()})
		return
	}

	res, err := tx.ExecContext(ctx, "DELETE FROM bookings WHERE restaurant_id = ? AND id = ?", restaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error: " + err.Error()})
		return
	}
	affected, _ := res.RowsAffected()
	if affected <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No booking found with the provided ID."})
		return
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error: " + err.Error()})
		return
	}

	s.emitN8nWebhookAsync(restaurantID, "booking.cancelled", map[string]any{
		"source":          "legacy_admin_delete_booking",
		"cancelledBy":     "staff",
		"bookingId":       b.ID,
		"reservationDate": b.ReservationDate,
		"reservationTime": b.ReservationTime,
		"partySize":       b.PartySize,
		"customerName":    b.CustomerName,
		"contactPhone":    b.ContactPhone.String,
		"contactEmail":    b.ContactEmail.String,
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Booking cancelled and saved successfully."})
}

func (s *Server) handleUpdateTableNumber(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid request method"})
		return
	}
	tableNumber := strings.TrimSpace(r.FormValue("table_number"))
	bookingID := clampInt(r.FormValue("booking_id"), 1, 1_000_000_000, 0)
	if bookingID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid booking ID"})
		return
	}

	if _, err := s.db.ExecContext(r.Context(), "UPDATE bookings SET table_number = ? WHERE restaurant_id = ? AND id = ?", tableNumber, restaurantID, bookingID); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Table number updated successfully"})
}

func (s *Server) handleGetReservations(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid form data")
		return
	}

	var (
		rows *sql.Rows
		err  error
	)

	if v := strings.TrimSpace(r.FormValue("specificdate")); v != "" {
		rows, err = s.db.QueryContext(r.Context(), `
			SELECT customer_name, contact_email, reservation_date, TIME_FORMAT(reservation_time, '%H:%i:%s') AS reservation_time, party_size, contact_phone, commentary, id
			FROM bookings
			WHERE restaurant_id = ? AND reservation_date = ?
		`, restaurantID, v)
	} else if a := strings.TrimSpace(r.FormValue("daterangestart")); a != "" && strings.TrimSpace(r.FormValue("daterangeend")) != "" {
		b := strings.TrimSpace(r.FormValue("daterangeend"))
		rows, err = s.db.QueryContext(r.Context(), `
			SELECT customer_name, contact_email, reservation_date, TIME_FORMAT(reservation_time, '%H:%i:%s') AS reservation_time, party_size, contact_phone, commentary, id
			FROM bookings
			WHERE restaurant_id = ? AND reservation_date BETWEEN ? AND ?
		`, restaurantID, a, b)
	} else {
		// No-op
		httpx.WriteJSON(w, http.StatusOK, map[string]any{})
		return
	}

	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}
	defer rows.Close()

	var (
		fechas []string
		nombres []string
		personas []int
		horas []string
		emails []string
		telefonos []string
		comentarios []string
		ids []int
	)

	for rows.Next() {
		var (
			customerName string
			contactEmail sql.NullString
			resDate string
			resTime string
			partySize int
			phone sql.NullString
			comment sql.NullString
			id int
		)
		if err := rows.Scan(&customerName, &contactEmail, &resDate, &resTime, &partySize, &phone, &comment, &id); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
			return
		}
		fechas = append(fechas, resDate)
		nombres = append(nombres, customerName)
		personas = append(personas, partySize)
		horas = append(horas, resTime)
		emails = append(emails, contactEmail.String)
		telefonos = append(telefonos, phone.String)
		comentarios = append(comentarios, comment.String)
		ids = append(ids, id)
	}

	// Legacy endpoint returns arrays without success wrapper.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"fechas":        fechas,
		"nombres":       nombres,
		"personas":      personas,
		"horas":         horas,
		"contact_email": emails,
		"telefonos":     telefonos,
		"comentarios":   comentarios,
		"id":            ids,
	})
}

func (s *Server) handleFetchCancelledBookings(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Date parameter is required"})
		return
	}
	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date format. Expected YYYY-MM-DD"})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			id,
			booking_id,
			reservation_date,
			party_size,
			reservation_time,
			customer_name,
			contact_phone,
			contact_email,
			commentary,
			arroz_type,
			arroz_servings,
			babyStrollers,
			highChairs,
			cancellation_date,
			cancelled_by
		FROM cancelled_bookings
		WHERE restaurant_id = ? AND reservation_date = ?
		ORDER BY cancellation_date DESC
	`, restaurantID, date)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error fetching cancelled bookings: " + err.Error()})
		return
	}
	defer rows.Close()

	var customer []map[string]any
	var staff []map[string]any
	for rows.Next() {
		var (
			id int
			bookingID int
			resDate string
			partySize int
			resTime string
			customerName string
			phone string
			email sql.NullString
			comment sql.NullString
			arrozType sql.NullString
			arrozServ sql.NullString
			baby sql.NullInt64
			chairs sql.NullInt64
			cancelDate sql.NullString
			cancelledBy sql.NullString
		)
		if err := rows.Scan(&id, &bookingID, &resDate, &partySize, &resTime, &customerName, &phone, &email, &comment, &arrozType, &arrozServ, &baby, &chairs, &cancelDate, &cancelledBy); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error fetching cancelled bookings: " + err.Error()})
			return
		}

		row := map[string]any{
			"id":               id,
			"booking_id":       bookingID,
			"reservation_date": resDate,
			"party_size":       partySize,
			"reservation_time": resTime,
			"customer_name":    customerName,
			"contact_phone":    phone,
			"contact_email":    email.String,
			"commentary":       comment.String,
			"arroz_type":       nullStringOrNil(arrozType),
			"arroz_servings":   nullStringOrNil(arrozServ),
			"babyStrollers":    nullIntToInt(baby),
			"highChairs":       nullIntToInt(chairs),
			"cancellation_date": cancelDate.String,
			"cancelled_by":     cancelledBy.String,
		}

		if cancelledBy.String == "staff" {
			staff = append(staff, row)
		} else {
			customer = append(customer, row)
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":                true,
		"customer_count":         len(customer),
		"staff_count":            len(staff),
		"total_count":            len(customer) + len(staff),
		"customer_cancellations": customer,
		"staff_cancellations":    staff,
	})
}

func (s *Server) handleReactivateBooking(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"success": false, "message": "Unknown restaurant"})
		return
	}

	if err := r.ParseForm(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "ID de reserva cancelada no proporcionado"})
		return
	}
	cancelledID := clampInt(r.FormValue("cancelled_booking_id"), 1, 1_000_000_000, 0)
	if cancelledID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "ID de reserva inválido"})
		return
	}

	ctx := r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}
	defer func() { _ = tx.Rollback() }()

	var (
		resDate string
		partySize int
		resTime string
		customerName string
		phone string
		email sql.NullString
		comment sql.NullString
		arrozType sql.NullString
		arrozServ sql.NullString
		baby sql.NullInt64
		chairs sql.NullInt64
		specialMenu sql.NullInt64
		menuID sql.NullInt64
		principales sql.NullString
	)

	err = tx.QueryRowContext(ctx, `
		SELECT reservation_date, party_size, reservation_time, customer_name, contact_phone, contact_email, commentary, arroz_type, arroz_servings, babyStrollers, highChairs, special_menu, menu_de_grupo_id, principales_json
		FROM cancelled_bookings
		WHERE restaurant_id = ? AND id = ?
	`, restaurantID, cancelledID).Scan(&resDate, &partySize, &resTime, &customerName, &phone, &email, &comment, &arrozType, &arrozServ, &baby, &chairs, &specialMenu, &menuID, &principales)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No se encontró la reserva cancelada"})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}

	resTimeNorm, _ := ensureHHMMSS(resTime)
	if resTimeNorm != "" {
		resTime = resTimeNorm
	}

	res2, err := tx.ExecContext(ctx, `
		INSERT INTO bookings (
			restaurant_id,
			customer_name,
			contact_email,
			reservation_date,
			reservation_time,
			party_size,
			contact_phone,
			commentary,
			babyStrollers,
			highChairs,
			arroz_type,
			arroz_servings,
			special_menu,
			menu_de_grupo_id,
			principales_json,
			status,
			reminder_sent,
			rice_reminder_sent
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'confirmed', 1, 1)
	`, restaurantID, customerName, email.String, resDate, resTime, partySize, phone, comment.String, nullIntToInt(baby), nullIntToInt(chairs), nullStringOrNil(arrozType), nullStringOrNil(arrozServ), int64OrZero(specialMenu), nullInt64OrNil(menuID), nullStringOrNil(principales))
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error reactivando reserva: " + err.Error()})
		return
	}
	newID, _ := res2.LastInsertId()

	if _, err := tx.ExecContext(ctx, "DELETE FROM cancelled_bookings WHERE restaurant_id = ? AND id = ?", restaurantID, cancelledID); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error eliminando registro de cancelación: " + err.Error()})
		return
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"message":        "Reserva reactivada correctamente",
		"new_booking_id": newID,
		"booking_details": map[string]any{
			"customer_name":    customerName,
			"reservation_date": resDate,
			"reservation_time": resTime,
			"party_size":       partySize,
		},
	})
}

func clampInt(raw string, min, max, def int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	i, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if i < min {
		return min
	}
	if i > max {
		return max
	}
	return i
}

func normalizeSortDir(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "asc" || v == "desc" {
		return v
	}
	return "none"
}

func onlyDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func nullIntToInt(v sql.NullInt64) int {
	if !v.Valid {
		return 0
	}
	return int(v.Int64)
}

func int64OrZero(v sql.NullInt64) int {
	if !v.Valid {
		return 0
	}
	return int(v.Int64)
}

func defaultString(v sql.NullString, def string) string {
	if !v.Valid || strings.TrimSpace(v.String) == "" {
		return def
	}
	return v.String
}

func nullStringOrNil(v sql.NullString) any {
	if !v.Valid || strings.TrimSpace(v.String) == "" {
		return nil
	}
	return v.String
}

func nullInt64OrNil(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

// Keep connection-scoped operations in one place for transactions.
func withTx(ctx context.Context, db *sql.DB, fn func(context.Context, *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
