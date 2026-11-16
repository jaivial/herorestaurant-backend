package api

import (
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/sqldb"
)

func (s *Server) handleBOBookingsSearch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	searchName := strings.TrimSpace(r.URL.Query().Get("name"))
	searchPhone := strings.TrimSpace(r.URL.Query().Get("phone"))

	whereClause, whereArgs := buildBookingGeneralSearchWhere(searchName, searchPhone)
	if whereClause == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success":     true,
			"bookings":    []map[string]any{},
			"floors":      []any{},
			"total_count": 0,
			"total":       0,
			"page":        1,
			"count":       15,
		})
		return
	}

	page := generalSearchSanitizePage(0)
	if v := strings.TrimSpace(r.URL.Query().Get("page")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			page = n
		}
	}
	count := generalSearchSanitizeCount(0)
	if v := strings.TrimSpace(r.URL.Query().Get("count")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			count = generalSearchSanitizeCount(n)
		}
	}

	restaurantID := a.ActiveRestaurantID

	baseWhere := "WHERE restaurant_id = ? AND (" + whereClause + ")"
	baseArgs := append([]any{restaurantID}, whereArgs...)

	var totalCount int
	countSQL := "SELECT COUNT(*) FROM bookings " + baseWhere
	if err := s.db.QueryRowContext(r.Context(), portableSQL(countSQL), baseArgs...).Scan(&totalCount); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error counting bookings")
		return
	}

	offset := (page - 1) * count
	dataSQL := nativeSQL(`
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
			preferred_salon_id,
			DATE_FORMAT(added_date, '%Y-%m-%d %H:%i:%s') AS added_date,
			special_menu,
			menu_de_grupo_id,
			principales_json,
			COALESCE(extras_json, ''),
			COALESCE(is_special_booking, 0),
			COALESCE(is_prereserva, 0),
			COALESCE(special_json, ''),
			COALESCE(has_mobility_issues, 0),
			COALESCE(mobility_people, 0),
			qr_url,
			receipt_url
		FROM bookings
	`, bookingListSelectPG) + baseWhere + `
		ORDER BY reservation_date DESC, reservation_time DESC, id DESC
		LIMIT ? OFFSET ?
	`
	dataArgs := append(append([]any{}, baseArgs...), count, offset)

	rows, err := s.db.QueryContext(r.Context(), portableSQL(dataSQL), dataArgs...)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error searching bookings")
		return
	}
	defer rows.Close()

	bookings := make([]map[string]any, 0)
	for rows.Next() {
		b, ok := s.scanBookingRow(r.Context(), restaurantID, rows)
		if !ok {
			httpx.WriteError(w, http.StatusInternalServerError, "Error scanning booking row")
			return
		}
		bookings = append(bookings, b)
	}
	if err := rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error iterating bookings")
		return
	}

	s.attachBookingSalonNames(r.Context(), restaurantID, bookings)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"bookings":    bookings,
		"floors":      []any{},
		"total_count": totalCount,
		"total":       totalCount,
		"page":        page,
		"count":       count,
	})
}

func buildBookingGeneralSearchWhere(rawName, rawPhone string) (string, []any) {
	name := strings.TrimSpace(rawName)
	phoneDigits := bookingSearchDigitsOnly(strings.TrimSpace(rawPhone))

	if name == "" && phoneDigits == "" {
		return "", nil
	}

	var clauses []string
	var args []any

	if name != "" {
		pattern := "%" + name + "%"
		if sqlDialect == sqldb.Postgres {
			// PG17 forbids LIKE on the nondeterministic CI collation: ICU LOWER
			// then byte LIKE against a lowered pattern (case-insensitive;
			// accent-insensitivity of utf8mb4_general_ci NOT replicated - pending).
			clauses = append(clauses, `LOWER(customer_name) COLLATE "C" LIKE ?`, `LOWER(contact_email) COLLATE "C" LIKE ?`)
			pattern = strings.ToLower(pattern)
		} else {
			clauses = append(clauses, "customer_name LIKE ?", "contact_email LIKE ?")
		}
		args = append(args, pattern, pattern)
	}

	if phoneDigits != "" {
		// digits-only pattern: case/accent irrelevant.
		clauses = append(clauses, sqlFragment("contact_phone LIKE ?", `contact_phone COLLATE "C" LIKE ?`))
		args = append(args, phoneDigits+"%")
	}

	return strings.Join(clauses, " OR "), args
}

// bookingListSelectPG is the PG twin of the booking list SELECT ... FROM bookings
// head shared by the date list and general search (same column order as the
// MySQL text so scanBookingRow is unchanged). added_date is timestamptz: rendered
// in UTC like MySQL DATE_FORMAT under session time_zone UTC.
const bookingListSelectPG = `
		SELECT
			id,
			customer_name,
			contact_email,
			to_char(reservation_date, 'YYYY-MM-DD') AS reservation_date,
			to_char(reservation_time, 'HH24:MI:SS') AS reservation_time,
			party_size,
			children,
			contact_phone,
			contact_phone_country_code,
			status,
			arroz_type,
			arroz_servings,
			commentary,
			"babyStrollers",
			"highChairs",
			table_number,
			preferred_floor_number,
			preferred_salon_id,
			to_char(added_date AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS') AS added_date,
			special_menu,
			menu_de_grupo_id,
			principales_json,
			COALESCE(extras_json, ''),
			COALESCE(is_special_booking, 0),
			COALESCE(is_prereserva, 0),
			COALESCE(special_json, ''),
			COALESCE(has_mobility_issues, 0),
			COALESCE(mobility_people, 0),
			qr_url,
			receipt_url
		FROM bookings
	`

func generalSearchSanitizeCount(v int) int {
	if v <= 0 {
		return 15
	}
	if v > 100 {
		return 100
	}
	return v
}

func generalSearchSanitizePage(v int) int {
	if v <= 0 {
		return 1
	}
	return v
}
