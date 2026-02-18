package api

import (
	"database/sql"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

var (
	reTimeHHMMSS = regexp.MustCompile(`^\\d{2}:\\d{2}:\\d{2}$`)
)

func normalizeSearchName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}

	// Spanish accent folding (best-effort; avoids pulling extra deps).
	repl := strings.NewReplacer(
		"á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a",
		"é", "e", "è", "e", "ë", "e", "ê", "e",
		"í", "i", "ì", "i", "ï", "i", "î", "i",
		"ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o",
		"ú", "u", "ù", "u", "ü", "u", "û", "u",
		"ñ", "n",
		"ç", "c",
	)
	s = repl.Replace(s)

	// Keep [a-z0-9 ] and normalize spaces.
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		isSpace := r == ' ' || r == '\t' || r == '\n' || r == '\r'
		if isSpace {
			if b.Len() == 0 || prevSpace {
				continue
			}
			b.WriteByte(' ')
			prevSpace = true
			continue
		}
		prevSpace = false
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func (s *Server) handleCheckCancel(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, false)
		return
	}

	_ = r.ParseForm()

	resDate := strings.TrimSpace(r.FormValue("reservation_date"))
	resTime := strings.TrimSpace(r.FormValue("reservation_time"))
	partySizeRaw := strings.TrimSpace(r.FormValue("party_size"))
	custName := strings.TrimSpace(r.FormValue("customer_name"))

	if resDate == "" || resTime == "" || partySizeRaw == "" || custName == "" {
		httpx.WriteJSON(w, http.StatusOK, false)
		return
	}
	if !isValidISODate(resDate) {
		httpx.WriteJSON(w, http.StatusOK, false)
		return
	}
	if !reTimeHHMMSS.MatchString(resTime) {
		httpx.WriteJSON(w, http.StatusOK, false)
		return
	}
	if _, err := time.Parse("15:04:05", resTime); err != nil {
		httpx.WriteJSON(w, http.StatusOK, false)
		return
	}
	partySize, err := strconv.Atoi(partySizeRaw)
	if err != nil || partySize < 1 {
		httpx.WriteJSON(w, http.StatusOK, false)
		return
	}

	searchName := normalizeSearchName(custName)
	if searchName == "" {
		httpx.WriteJSON(w, http.StatusOK, false)
		return
	}

	var (
		id           int
		customerName string
		contactEmail string
		dateVal      time.Time
		timeVal      time.Time
		ps           int
		contactPhone sql.NullString
		baby         sql.NullInt64
		high         sql.NullInt64
		arrozType    sql.NullString
		arrozServs   sql.NullString
		status       sql.NullString
		commentary   sql.NullString
	)

	err = s.db.QueryRowContext(r.Context(), `
		SELECT
			id, customer_name, contact_email, reservation_date,
			reservation_time, party_size, contact_phone,
			babyStrollers, highChairs, arroz_type, arroz_servings,
			status, commentary
		FROM bookings
		WHERE restaurant_id = ?
		  AND reservation_date = ?
		  AND reservation_time = ?
		  AND party_size = ?
		  AND LOWER(TRIM(customer_name)) LIKE ?
		LIMIT 1
	`, restaurantID, resDate, resTime, partySize, "%"+searchName+"%").Scan(
		&id, &customerName, &contactEmail, &dateVal,
		&timeVal, &ps, &contactPhone,
		&baby, &high, &arrozType, &arrozServs,
		&status, &commentary,
	)
	if err == sql.ErrNoRows {
		httpx.WriteJSON(w, http.StatusOK, false)
		return
	}
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, false)
		return
	}

	var babyVal any = nil
	if baby.Valid {
		babyVal = int(baby.Int64)
	}
	var highVal any = nil
	if high.Valid {
		highVal = int(high.Int64)
	}
	var arrozTypeVal any = nil
	if arrozType.Valid {
		arrozTypeVal = arrozType.String
	}
	var arrozServsVal any = nil
	if arrozServs.Valid {
		arrozServsVal = arrozServs.String
	}
	var statusVal any = nil
	if status.Valid {
		statusVal = status.String
	}
	var commentaryVal any = nil
	if commentary.Valid {
		commentaryVal = commentary.String
	}
	var phoneVal any = nil
	if contactPhone.Valid {
		phoneVal = contactPhone.String
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id":               id,
		"customer_name":    customerName,
		"contact_email":    contactEmail,
		"reservation_date": dateVal.Format("2006-01-02"),
		"reservation_time": timeVal.Format("15:04:05"),
		"party_size":       ps,
		"contact_phone":    phoneVal,
		"babyStrollers":    babyVal,
		"highChairs":       highVal,
		"arroz_type":       arrozTypeVal,
		"arroz_servings":   arrozServsVal,
		"status":           statusVal,
		"commentary":       commentaryVal,
	})
}
