package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

type bookingRow struct {
	ID              int
	CustomerName    string
	ContactEmail    string
	ReservationDate string
	ReservationTime string
	PartySize       int
	Children        int
	ContactPhone    sql.NullString
	ContactPhoneCC  sql.NullString
	Status          sql.NullString
	ArrozType       sql.NullString
	ArrozServings   sql.NullString
	Commentary      sql.NullString
	BabyStrollers   sql.NullInt64
	HighChairs      sql.NullInt64
	TableNumber     sql.NullString
	PreferredFloor  sql.NullInt64
	AddedDate       sql.NullString
	SpecialMenu     sql.NullInt64
	MenuDeGrupoID   sql.NullInt64
	PrincipalesJSON sql.NullString
}

func scanBookingRow(rows *sql.Rows) (map[string]any, bool) {
	var b bookingRow
	if err := rows.Scan(
		&b.ID,
		&b.CustomerName,
		&b.ContactEmail,
		&b.ReservationDate,
		&b.ReservationTime,
		&b.PartySize,
		&b.Children,
		&b.ContactPhone,
		&b.ContactPhoneCC,
		&b.Status,
		&b.ArrozType,
		&b.ArrozServings,
		&b.Commentary,
		&b.BabyStrollers,
		&b.HighChairs,
		&b.TableNumber,
		&b.PreferredFloor,
		&b.AddedDate,
		&b.SpecialMenu,
		&b.MenuDeGrupoID,
		&b.PrincipalesJSON,
	); err != nil {
		return nil, false
	}

	isSpecialMenu := b.SpecialMenu.Valid && b.SpecialMenu.Int64 != 0

	return map[string]any{
		"id":                         b.ID,
		"customer_name":              b.CustomerName,
		"contact_email":              b.ContactEmail,
		"reservation_date":           b.ReservationDate,
		"reservation_time":           b.ReservationTime,
		"party_size":                 b.PartySize,
		"children":                   b.Children,
		"contact_phone":              nullStringOrNil(b.ContactPhone),
		"contact_phone_country_code": defaultString(b.ContactPhoneCC, "34"),
		"status":                     defaultString(b.Status, "pending"),
		"arroz_type":                 nullStringOrNil(b.ArrozType),
		"arroz_servings":             nullStringOrNil(b.ArrozServings),
		"commentary":                 nullStringOrNil(b.Commentary),
		"babyStrollers":              nullInt64OrNil(b.BabyStrollers),
		"highChairs":                 nullInt64OrNil(b.HighChairs),
		"table_number":               nullStringOrNil(b.TableNumber),
		"preferred_floor_number":     nullInt64OrNil(b.PreferredFloor),
		"added_date":                 nullStringOrNil(b.AddedDate),
		"special_menu":               isSpecialMenu,
		"menu_de_grupo_id":           nullInt64OrNil(b.MenuDeGrupoID),
		"principales_json":           nullStringOrNil(b.PrincipalesJSON),
	}, true
}

func (s *Server) handleBOBookingsList(w http.ResponseWriter, r *http.Request) {
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

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	switch status {
	case "", "pending", "confirmed":
	default:
		status = ""
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))

	sortKey := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortKey != "added_date" && sortKey != "reservation_time" && sortKey != "" {
		sortKey = ""
	}
	if sortKey == "" {
		sortKey = "reservation_time"
	}
	dir := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("dir")))
	if dir != "asc" && dir != "desc" {
		dir = "asc"
	}

	page := 0
	if v := strings.TrimSpace(r.URL.Query().Get("page")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 1_000_000 {
			page = n
		}
	}
	count := 0
	if v := strings.TrimSpace(r.URL.Query().Get("count")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 25 {
			count = n
		}
	}
	usePageCount := page > 0 || count > 0

	limit := 50
	offset := 0
	if usePageCount {
		if page <= 0 {
			page = 1
		}
		if count <= 0 {
			count = 15
		}
		limit = count
		offset = (page - 1) * count
	} else {
		if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
				limit = n
			}
		}
		if v := strings.TrimSpace(r.URL.Query().Get("offset")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 1_000_000 {
				offset = n
			}
		}
		if limit > 0 {
			page = (offset / limit) + 1
			count = limit
		}
	}

	restaurantID := a.ActiveRestaurantID
	floors, err := s.loadDateFloors(r.Context(), restaurantID, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando plantas")
		return
	}

	baseWhere := "WHERE reservation_date = ? AND restaurant_id = ?"
	baseArgs := []any{date, restaurantID}
	if status != "" {
		baseWhere += " AND status = ?"
		baseArgs = append(baseArgs, status)
	}

	orderBy := "reservation_time " + dir + ", id " + dir
	if sortKey == "added_date" {
		orderBy = "added_date " + dir + ", id " + dir
	}

	searchPrefixClause, searchPrefixArgs := buildBookingSearchPrefixClause(q)
	searchFTClause, searchFTArgs, hasFTQuery := buildBookingSearchFulltextClause(q)
	runQuery := func(useFulltext bool) ([]map[string]any, int, error) {
		where := baseWhere
		args := append([]any{}, baseArgs...)
		if q != "" {
			if useFulltext && hasFTQuery {
				where += " AND ((" + searchFTClause + ") OR (" + searchPrefixClause + "))"
				args = append(args, searchFTArgs...)
				args = append(args, searchPrefixArgs...)
			} else {
				where += " AND (" + searchPrefixClause + ")"
				args = append(args, searchPrefixArgs...)
			}
		}

		var totalCount int
		if err := s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM bookings "+where, args...).Scan(&totalCount); err != nil {
			return nil, 0, err
		}

		sqlQuery := `
			SELECT
				id,
				customer_name,
				contact_email,
				DATE_FORMAT(reservation_date, '%Y-%m-%d') AS reservation_date,
				TIME_FORMAT(reservation_time, '%H:%i:%s') AS reservation_time,
				party_size,
				children,
				contact_phone,
				contact_phone_country_code,
				status,
				arroz_type,
				arroz_servings,
				commentary,
				babyStrollers,
				highChairs,
				table_number,
				preferred_floor_number,
				DATE_FORMAT(added_date, '%Y-%m-%d %H:%i:%s') AS added_date,
				special_menu,
				menu_de_grupo_id,
				principales_json
			FROM bookings
		` + where + `
			ORDER BY ` + orderBy + `
			LIMIT ? OFFSET ?
		`
		argsList := append(append([]any{}, args...), limit, offset)
		rows, err := s.db.QueryContext(r.Context(), sqlQuery, argsList...)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()

		bookings := make([]map[string]any, 0)
		for rows.Next() {
			b, ok := scanBookingRow(rows)
			if !ok {
				return nil, 0, rows.Err()
			}
			bookings = append(bookings, b)
		}

		return bookings, totalCount, rows.Err()
	}

	bookings, totalCount, err := runQuery(true)
	if err != nil && hasFTQuery {
		bookings, totalCount, err = runQuery(false)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando bookings")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"bookings":    bookings,
		"floors":      floors,
		"total_count": totalCount,
		"total":       totalCount,
		"page":        page,
		"count":       count,
	})
}

func buildBookingSearchPrefixClause(raw string) (string, []any) {
	q := strings.TrimSpace(raw)
	if q == "" {
		return "", nil
	}
	prefix := q + "%"
	args := []any{prefix, prefix}
	clause := "customer_name LIKE ? OR contact_email LIKE ?"
	if digits := bookingSearchDigitsOnly(q); digits != "" {
		clause += " OR contact_phone LIKE ?"
		args = append(args, digits+"%")
	}
	return clause, args
}

func buildBookingSearchFulltextClause(raw string) (string, []any, bool) {
	q := bookingSearchBooleanQuery(raw)
	if q == "" {
		return "", nil, false
	}
	return "MATCH(customer_name, contact_email, commentary) AGAINST (? IN BOOLEAN MODE)", []any{q}, true
}

func bookingSearchDigitsOnly(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func bookingSearchBooleanQuery(v string) string {
	rawTokens := strings.Fields(strings.ToLower(strings.TrimSpace(v)))
	if len(rawTokens) == 0 {
		return ""
	}
	tokens := make([]string, 0, len(rawTokens))
	for _, token := range rawTokens {
		var cleaned strings.Builder
		for _, r := range token {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r >= 128 {
				cleaned.WriteRune(r)
			}
		}
		t := cleaned.String()
		if len([]rune(t)) < 3 {
			continue
		}
		tokens = append(tokens, "+"+t+"*")
	}
	if len(tokens) == 0 {
		return ""
	}
	return strings.Join(tokens, " ")
}

func (s *Server) handleBOBookingCancel(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	id := strings.TrimSpace(chi.URLParam(r, "id"))
	bookingID, err := strconv.Atoi(id)
	if err != nil || bookingID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Invalid booking id",
		})
		return
	}

	restaurantID := a.ActiveRestaurantID

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

	var cancelled booking
	err = withTx(r.Context(), s.db, func(ctx context.Context, tx *sql.Tx) error {
		var b booking
		row := tx.QueryRowContext(ctx, `
			SELECT
				id,
				DATE_FORMAT(reservation_date, '%Y-%m-%d') AS reservation_date,
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
			WHERE id = ? AND restaurant_id = ?
		`, bookingID, restaurantID)
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
			return err
		}

		cancelled = b

		_, err := tx.ExecContext(ctx, `
			INSERT INTO cancelled_bookings
				(restaurant_id, booking_id, reservation_date, party_size, reservation_time, customer_name,
				 contact_phone, contact_email, commentary, arroz_type, arroz_servings,
				 babyStrollers, highChairs, cancellation_date, cancelled_by,
				 cancelled_by_user_id, cancelled_by_name,
				 special_menu, menu_de_grupo_id, principales_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), ?, ?, ?, ?, ?, ?)
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
			"staff",
			int64(a.User.ID),
			a.User.Name,
			int64OrZero(b.SpecialMenu),
			nullInt64OrNil(b.MenuDeGrupoID),
			nullStringOrNil(b.PrincipalesJSON),
		)
		if err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx, "DELETE FROM bookings WHERE id = ? AND restaurant_id = ?", bookingID, restaurantID)
		if err != nil {
			return err
		}
		affected, _ := res.RowsAffected()
		if affected <= 0 {
			return sql.ErrNoRows
		}
		return nil
	})
	if err != nil {
		if err == sql.ErrNoRows {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Booking not found",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error cancelando booking")
		return
	}

	s.emitN8nWebhookAsync(restaurantID, "booking.cancelled", map[string]any{
		"source":            "backoffice_cancel",
		"cancelledBy":       "staff",
		"cancelledByUserID": a.User.ID,
		"cancelledByName":   a.User.Name,
		"bookingId":         cancelled.ID,
		"reservationDate":   cancelled.ReservationDate,
		"reservationTime":   cancelled.ReservationTime,
		"partySize":         cancelled.PartySize,
		"customerName":      cancelled.CustomerName,
		"contactPhone":      cancelled.ContactPhone.String,
		"contactEmail":      cancelled.ContactEmail.String,
	})

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}

// ─── Cancelled by date (for Canceladas tab) ─────────────────────────────────

func (s *Server) handleBOBookingsCancelledByDate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date. Expected YYYY-MM-DD"})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, booking_id, reservation_date, party_size, reservation_time,
		       customer_name, contact_phone, contact_email, commentary,
		       arroz_type, arroz_servings, babyStrollers, highChairs,
		       cancellation_date, cancelled_by, cancelled_by_user_id, cancelled_by_name,
		       special_menu, menu_de_grupo_id, principales_json
		FROM cancelled_bookings
		WHERE restaurant_id = ? AND reservation_date = ?
		ORDER BY cancellation_date DESC
	`, a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error: " + err.Error()})
		return
	}
	defer rows.Close()

	type item struct {
		ID               int    `json:"id"`
		BookingID        int    `json:"booking_id"`
		ReservationDate  string `json:"reservation_date"`
		PartySize        int    `json:"party_size"`
		ReservationTime  string `json:"reservation_time"`
		CustomerName     string `json:"customer_name"`
		ContactPhone     string `json:"contact_phone"`
		ContactEmail     string `json:"contact_email"`
		Commentary       string `json:"commentary"`
		ArrozType        any    `json:"arroz_type"`
		ArrozServings    any    `json:"arroz_servings"`
		BabyStrollers    int    `json:"babyStrollers"`
		HighChairs       int    `json:"highChairs"`
		CancellationDate string `json:"cancellation_date"`
		CancelledBy      string `json:"cancelled_by"`
		CancelledByUser  *int   `json:"cancelled_by_user_id"`
		CancelledByName  string `json:"cancelled_by_name"`
	}

	var staff, customer, whatsapp []item
	for rows.Next() {
		var it item
		var phone, email, comment, arrozType, arrozServ sql.NullString
		var baby, chairs sql.NullInt64
		var cancelDate, cancelledBy sql.NullString
		var cancelledByUser sql.NullInt64
		var cancelledByName sql.NullString
		err := rows.Scan(&it.ID, &it.BookingID, &it.ReservationDate, &it.PartySize, &it.ReservationTime,
			&it.CustomerName, &phone, &email, &comment,
			&arrozType, &arrozServ, &baby, &chairs,
			&cancelDate, &cancelledBy, &cancelledByUser, &cancelledByName)
		if err != nil {
			continue
		}
		it.ContactPhone = phone.String
		it.ContactEmail = email.String
		it.Commentary = comment.String
		it.ArrozType = nullStringOrNil(arrozType)
		it.ArrozServings = nullStringOrNil(arrozServ)
		it.BabyStrollers = int(baby.Int64)
		it.HighChairs = int(chairs.Int64)
		it.CancellationDate = cancelDate.String
		it.CancelledBy = cancelledBy.String
		if cancelledByUser.Valid {
			uid := int(cancelledByUser.Int64)
			it.CancelledByUser = &uid
		}
		it.CancelledByName = cancelledByName.String

		switch it.CancelledBy {
		case "staff":
			staff = append(staff, it)
		case "whatsapp":
			whatsapp = append(whatsapp, it)
		default:
			customer = append(customer, it)
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"staff":    staff,
		"customer": customer,
		"whatsapp": whatsapp,
		"total":    len(staff) + len(customer) + len(whatsapp),
	})
}

// ─── Modified by date (for Modificadas tab) ─────────────────────────────────

func (s *Server) handleBOBookingsModifiedByDate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date. Expected YYYY-MM-DD"})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, booking_id, original_reservation_date, field_modified,
		       old_value, new_value, modified_by, modified_by_user_id, modified_by_name,
		       customer_name, contact_phone, modification_date
		FROM booking_modifications
		WHERE restaurant_id = ? AND original_reservation_date = ?
		ORDER BY modification_date DESC
	`, a.ActiveRestaurantID, date)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Error: " + err.Error()})
		return
	}
	defer rows.Close()

	type modItem struct {
		ID               int    `json:"id"`
		BookingID        int    `json:"booking_id"`
		OriginalDate     string `json:"original_reservation_date"`
		FieldModified    string `json:"field_modified"`
		OldValue         string `json:"old_value"`
		NewValue         string `json:"new_value"`
		ModifiedBy       string `json:"modified_by"`
		ModifiedByUser   *int   `json:"modified_by_user_id"`
		ModifiedByName   string `json:"modified_by_name"`
		CustomerName     string `json:"customer_name"`
		ContactPhone     string `json:"contact_phone"`
		ModificationDate string `json:"modification_date"`
	}

	var staff, customer, whatsapp []modItem
	for rows.Next() {
		var it modItem
		var oldVal, newVal, modDate sql.NullString
		var modByUser sql.NullInt64
		var modByName, custName, phone, field sql.NullString
		err := rows.Scan(&it.ID, &it.BookingID, &it.OriginalDate, &field,
			&oldVal, &newVal, &it.ModifiedBy, &modByUser, &modByName,
			&custName, &phone, &modDate)
		if err != nil {
			continue
		}
		it.FieldModified = field.String
		it.OldValue = oldVal.String
		it.NewValue = newVal.String
		it.ModifiedByName = modByName.String
		it.CustomerName = custName.String
		it.ContactPhone = phone.String
		it.ModificationDate = modDate.String
		if modByUser.Valid {
			uid := int(modByUser.Int64)
			it.ModifiedByUser = &uid
		}

		switch it.ModifiedBy {
		case "staff":
			staff = append(staff, it)
		case "whatsapp":
			whatsapp = append(whatsapp, it)
		default:
			customer = append(customer, it)
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"staff":    staff,
		"customer": customer,
		"whatsapp": whatsapp,
		"total":    len(staff) + len(customer) + len(whatsapp),
	})
}

// ─── Reactivate cancelled booking ───────────────────────────────────────────

func (s *Server) handleBOBookingReactivate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	idStr := strings.TrimSpace(chi.URLParam(r, "id"))
	cancelledID, err := strconv.Atoi(idStr)
	if err != nil || cancelledID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid cancelled booking id"})
		return
	}

	var row struct {
		bookingID       int
		reservationDate string
		partySize       int
		reservationTime string
		customerName    string
		phone           string
		email           string
		commentary      string
		arrozType       any
		arrozServings   any
		babyStrollers   int
		highChairs      int
		specialMenu     int
		menuDeGrupoID   any
		principales     any
	}

	err = withTx(r.Context(), s.db, func(ctx context.Context, tx *sql.Tx) error {
		var bID int
		var resDate, resTime, custName, phone, email, comment string
		var arrozType, arrozServ, principales sql.NullString
		var baby, chairs, specMenu, menuDeGrupo sql.NullInt64

		err := tx.QueryRowContext(ctx, `
			SELECT booking_id, reservation_date, party_size, reservation_time, customer_name,
			       contact_phone, contact_email, commentary, arroz_type, arroz_servings,
			       babyStrollers, highChairs, special_menu, menu_de_grupo_id, principales_json
			FROM cancelled_bookings
			WHERE id = ? AND restaurant_id = ?
		`, cancelledID, a.ActiveRestaurantID).Scan(
			&bID, &resDate, &row.partySize, &resTime, &custName,
			&phone, &email, &comment, &arrozType, &arrozServ,
			&baby, &chairs, &specMenu, &menuDeGrupo, &principales)
		if err != nil {
			return err
		}

		row.bookingID = bID
		row.reservationDate = resDate
		row.reservationTime = resTime
		row.customerName = custName
		row.phone = phone
		row.email = email
		row.commentary = comment
		row.arrozType = nullStringOrNil(arrozType)
		row.arrozServings = nullStringOrNil(arrozServ)
		row.babyStrollers = int(baby.Int64)
		row.highChairs = int(chairs.Int64)
		row.specialMenu = int(specMenu.Int64)
		row.menuDeGrupoID = nullInt64OrNil(menuDeGrupo)
		row.principales = nullStringOrNil(principales)

		// Check if day is closed — prevent reactivation.
		var dayOpen int
		tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM closed_days WHERE restaurant_id = ? AND date = ?", a.ActiveRestaurantID, resDate).Scan(&dayOpen)
		if dayOpen > 0 {
			return fmt.Errorf("El día %s está cerrado. Ábrelo antes de reactivar reservas.", resDate)
		}

		// Re-insert into bookings with a new id (auto-increment).
		_, err = tx.ExecContext(ctx, `
			INSERT INTO bookings
				(restaurant_id, reservation_date, party_size, reservation_time, customer_name,
				 contact_phone, contact_email, commentary, arroz_type, arroz_servings,
				 babyStrollers, highChairs, status, special_menu, menu_de_grupo_id, principales_json,
				 children)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, confirmed, ?, ?, ?, 0)
		`, a.ActiveRestaurantID, resDate, row.partySize, resTime, custName,
			phone, email, comment, arrozType, arrozServ, baby, chairs,
			specMenu, menuDeGrupo, principales)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, "DELETE FROM cancelled_bookings WHERE id = ?", cancelledID)
		return err
	})
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Reserva reactivada correctamente"})
}
