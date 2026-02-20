package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

var boMadridTZ = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		return time.UTC
	}
	return loc
}()

type boMember struct {
	ID                  int     `json:"id"`
	BOUserID            *int    `json:"boUserId"`
	FirstName           string  `json:"firstName"`
	LastName            string  `json:"lastName"`
	Email               *string `json:"email"`
	DNI                 *string `json:"dni"`
	BankAccount         *string `json:"bankAccount"`
	Phone               *string `json:"phone"`
	WhatsAppNumber      *string `json:"whatsappNumber"`
	PhotoURL            *string `json:"photoUrl"`
	WeeklyContractHours float64 `json:"weeklyContractHours"`
}

type boMemberCreateRequest struct {
	FirstName           string   `json:"firstName"`
	LastName            string   `json:"lastName"`
	Email               *string  `json:"email"`
	DNI                 *string  `json:"dni"`
	BankAccount         *string  `json:"bankAccount"`
	Phone               *string  `json:"phone"`
	WhatsAppNumber      *string  `json:"whatsappNumber"`
	PhotoURL            *string  `json:"photoUrl"`
	RoleSlug            *string  `json:"roleSlug"`
	Username            *string  `json:"username"`
	TemporaryPassword   *string  `json:"temporaryPassword"`
	WeeklyContractHours *float64 `json:"weeklyContractHours"`
}

type boMemberPatchRequest struct {
	FirstName           *string  `json:"firstName"`
	LastName            *string  `json:"lastName"`
	Email               *string  `json:"email"`
	DNI                 *string  `json:"dni"`
	BankAccount         *string  `json:"bankAccount"`
	Phone               *string  `json:"phone"`
	WhatsAppNumber      *string  `json:"whatsappNumber"`
	PhotoURL            *string  `json:"photoUrl"`
	WeeklyContractHours *float64 `json:"weeklyContractHours"`
}

type boMemberStatsPoint struct {
	Date  string  `json:"date"`
	Label string  `json:"label"`
	Hours float64 `json:"hours"`
}

func (s *Server) handleBOMembersList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT
			m.id,
			m.bo_user_id,
			m.first_name,
			m.last_name,
			m.email,
			m.dni,
			m.bank_account,
			m.phone,
			m.whatsapp_number,
			m.photo_url,
			COALESCE(c.weekly_hours, 40.00) AS weekly_hours
		FROM restaurant_members m
		LEFT JOIN member_contracts c ON c.restaurant_member_id = m.id
		WHERE m.restaurant_id = ? AND m.is_active = 1
		ORDER BY m.last_name ASC, m.first_name ASC, m.id ASC
	`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando miembros")
		return
	}
	defer rows.Close()

	out := make([]boMember, 0, 32)
	for rows.Next() {
		m, err := scanBOMember(rows)
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembros")
			return
		}
		out = append(out, m)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"members": out,
	})
}

func (s *Server) handleBOMemberCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req boMemberCreateRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	out, err := s.createBOMemberAndBootstrapAccess(r.Context(), a, req, r)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error creando miembro")
		return
	}
	if !out.Success {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": out.Message,
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"member":       out.Member,
		"user":         out.User,
		"role":         out.RoleSlug,
		"invitation":   out.Invitation,
		"provisioning": out.Provisioning,
	})
}

func (s *Server) handleBOMemberGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}

	member, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"member":  member,
	})
}

func (s *Server) handleBOMemberPatch(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}

	current, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	var req boMemberPatchRequest
	if err := readJSONBody(r, &req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid JSON",
		})
		return
	}

	firstName := current.FirstName
	lastName := current.LastName
	email := ptrToValue(current.Email)
	dni := ptrToValue(current.DNI)
	bank := ptrToValue(current.BankAccount)
	phone := ptrToValue(current.Phone)
	whatsappNumber := ptrToValue(current.WhatsAppNumber)
	photo := ptrToValue(current.PhotoURL)
	weekly := current.WeeklyContractHours

	if req.FirstName != nil {
		firstName = strings.TrimSpace(*req.FirstName)
	}
	if req.LastName != nil {
		lastName = strings.TrimSpace(*req.LastName)
	}
	if req.Email != nil {
		email = strings.ToLower(strings.TrimSpace(*req.Email))
	}
	if req.DNI != nil {
		dni = strings.TrimSpace(*req.DNI)
	}
	if req.BankAccount != nil {
		bank = strings.TrimSpace(*req.BankAccount)
	}
	if req.Phone != nil {
		phone = strings.TrimSpace(*req.Phone)
	}
	if req.WhatsAppNumber != nil {
		whatsappNumber = strings.TrimSpace(*req.WhatsAppNumber)
	}
	if req.PhotoURL != nil {
		photo = strings.TrimSpace(*req.PhotoURL)
	}
	if req.WeeklyContractHours != nil {
		weekly = *req.WeeklyContractHours
	}

	if firstName == "" || lastName == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Nombre y apellidos son obligatorios",
		})
		return
	}
	if weekly < 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "weeklyContractHours debe ser >= 0",
		})
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error iniciando transaccion")
		return
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(r.Context(), `
		UPDATE restaurant_members
		SET
			first_name = ?,
			last_name = ?,
			email = ?,
			dni = ?,
			bank_account = ?,
			phone = ?,
			whatsapp_number = ?,
			photo_url = ?
		WHERE id = ? AND restaurant_id = ? AND is_active = 1
	`, firstName, lastName, nullableString(email), nullableString(dni), nullableString(bank), nullableString(phone), nullableString(whatsappNumber), nullableString(photo), memberID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "No se pudo actualizar el miembro",
		})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Miembro no encontrado",
		})
		return
	}

	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO member_contracts (restaurant_member_id, restaurant_id, weekly_hours)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			weekly_hours = VALUES(weekly_hours)
	`, memberID, a.ActiveRestaurantID, weekly); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error guardando contrato")
		return
	}

	if err := tx.Commit(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error finalizando transaccion")
		return
	}

	member, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"member":  member,
	})
}

func (s *Server) handleBOMemberStats(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}
	member, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	switch view {
	case "", "weekly", "monthly", "quarterly", "yearly":
	default:
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "view invalida",
		})
		return
	}
	if view == "" {
		view = "weekly"
	}

	refDate := boTodayDate()
	if raw := strings.TrimSpace(r.URL.Query().Get("date")); raw != "" {
		d, err := parseBODate(raw)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "date invalida",
			})
			return
		}
		refDate = d
	}

	start, end := boDateRangeForView(refDate, view)
	points, workedHours, err := s.queryBOMemberPoints(r.Context(), a.ActiveRestaurantID, memberID, start, end, view)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando estadisticas")
		return
	}

	daysInRange := int(end.Sub(start).Hours()/24) + 1
	expectedHours := (member.WeeklyContractHours / 7.0) * float64(daysInRange)
	progress := 0.0
	if expectedHours > 0 {
		progress = (workedHours / expectedHours) * 100.0
	}

	weekStart, weekEnd := boDateRangeForView(refDate, "weekly")
	weeklyWorked, err := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, weekStart, weekEnd)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando semana")
		return
	}
	weeklyProgress := 0.0
	if member.WeeklyContractHours > 0 {
		weeklyProgress = (weeklyWorked / member.WeeklyContractHours) * 100.0
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"view":      view,
		"date":      refDate.Format("2006-01-02"),
		"startDate": start.Format("2006-01-02"),
		"endDate":   end.Format("2006-01-02"),
		"points":    points,
		"summary": map[string]any{
			"workedHours":           round2(workedHours),
			"expectedHours":         round2(expectedHours),
			"progressPercent":       round2(progress),
			"weeklyWorkedHours":     round2(weeklyWorked),
			"weeklyContractHours":   round2(member.WeeklyContractHours),
			"weeklyProgressPercent": round2(weeklyProgress),
		},
	})
}

func (s *Server) handleBOMemberQuarterBalance(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}
	member, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	refDate := boTodayDate()
	if raw := strings.TrimSpace(r.URL.Query().Get("date")); raw != "" {
		d, err := parseBODate(raw)
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "date invalida",
			})
			return
		}
		refDate = d
	}

	quarterStart := boQuarterStart(refDate)
	quarterEnd := quarterStart.AddDate(0, 3, -1)
	cutoff := refDate
	if cutoff.After(quarterEnd) {
		cutoff = quarterEnd
	}

	workedHours, err := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, quarterStart, cutoff)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando bolsa")
		return
	}

	daysElapsed := int(cutoff.Sub(quarterStart).Hours()/24) + 1
	if daysElapsed < 0 {
		daysElapsed = 0
	}
	expectedHours := (member.WeeklyContractHours / 7.0) * float64(daysElapsed)
	balance := workedHours - expectedHours

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"quarter": map[string]any{
			"startDate":  quarterStart.Format("2006-01-02"),
			"endDate":    quarterEnd.Format("2006-01-02"),
			"cutoffDate": cutoff.Format("2006-01-02"),
			"label":      fmt.Sprintf("Q%d %d", ((int(quarterStart.Month())-1)/3)+1, quarterStart.Year()),
		},
		"weeklyContractHours": round2(member.WeeklyContractHours),
		"workedHours":         round2(workedHours),
		"expectedHours":       round2(expectedHours),
		"balanceHours":        round2(balance),
	})
}

func (s *Server) handleBOMemberStatsYear(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}
	member, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	yearStr := strings.TrimSpace(r.URL.Query().Get("year"))
	year := time.Now().Year()
	if yearStr != "" {
		y, err := strconv.Atoi(yearStr)
		if err != nil || y < 2000 || y > 2100 {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "año invalido",
			})
			return
		}
		year = y
	}

	start := time.Date(year, 1, 1, 0, 0, 0, 0, boMadridTZ)
	end := time.Date(year, 12, 31, 23, 59, 59, 0, boMadridTZ)

	totalWorked, err := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, start, end)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando horas trabajadas")
		return
	}

	daysInYear := 365
	if (year%4 == 0 && year%100 != 0) || year%400 == 0 {
		daysInYear = 366
	}
	expectedHours := (member.WeeklyContractHours / 7.0) * float64(daysInYear)
	balance := totalWorked - expectedHours

	monthNames := []string{"Enero", "Febrero", "Marzo", "Abril", "Mayo", "Junio", "Julio", "Agosto", "Septiembre", "Octubre", "Noviembre", "Diciembre"}

	// Generate monthly rows
	monthRows := make([]map[string]any, 0, 12)
	for month := 1; month <= 12; month++ {
		mStart := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, boMadridTZ)
		mEnd := mStart.AddDate(0, 1, -1)
		mWorked, _ := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, mStart, mEnd)
		mDays := int(mEnd.Sub(mStart).Hours()/24) + 1
		mExpected := (member.WeeklyContractHours / 7.0) * float64(mDays)
		monthRows = append(monthRows, map[string]any{
			"date":          mStart.Format("2006-01-02"),
			"label":         monthNames[month-1],
			"workedHours":   round2(mWorked),
			"expectedHours": round2(mExpected),
			"difference":    round2(mWorked - mExpected),
		})
	}

	// Generate quarterly rows
	quarterRows := make([]map[string]any, 0, 4)
	for q := 1; q <= 4; q++ {
		qStart := time.Date(year, time.Month((q-1)*3+1), 1, 0, 0, 0, 0, boMadridTZ)
		qEnd := qStart.AddDate(0, 3, -1)
		qWorked, _ := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, qStart, qEnd)
		qDays := int(qEnd.Sub(qStart).Hours()/24) + 1
		qExpected := (member.WeeklyContractHours / 7.0) * float64(qDays)
		quarterRows = append(quarterRows, map[string]any{
			"date":          qStart.Format("2006-01-02"),
			"label":         fmt.Sprintf("Q%d", q),
			"workedHours":   round2(qWorked),
			"expectedHours": round2(qExpected),
			"difference":    round2(qWorked - qExpected),
		})
	}

	// Generate weekly rows
	weekRows := make([]map[string]any, 0, 53)
	current := start
	weekNum := 1
	for current.Before(end) || current.Equal(end) {
		weekEnd := current.AddDate(0, 0, 6)
		if weekEnd.After(end) {
			weekEnd = end
		}
		wWorked, _ := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, current, weekEnd)
		wDays := int(weekEnd.Sub(current).Hours()/24) + 1
		wExpected := (member.WeeklyContractHours / 7.0) * float64(wDays)
		weekRows = append(weekRows, map[string]any{
			"date":          current.Format("2006-01-02"),
			"label":         fmt.Sprintf("S%d", weekNum),
			"workedHours":   round2(wWorked),
			"expectedHours": round2(wExpected),
			"difference":    round2(wWorked - wExpected),
		})
		current = current.AddDate(0, 0, 7)
		weekNum++
		if weekNum > 53 {
			break
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":            true,
		"year":               year,
		"totalWorkedHours":   round2(totalWorked),
		"totalExpectedHours": round2(expectedHours),
		"balance":            round2(balance),
		"months":             monthRows,
		"quarters":           quarterRows,
		"weeks":              weekRows,
	})
}

func (s *Server) handleBOMemberStatsRange(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}

	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromStr == "" || toStr == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "from y to son requeridos",
		})
		return
	}

	from, err := parseBODate(fromStr)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "from invalido",
		})
		return
	}
	to, err := parseBODate(toStr)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "to invalido",
		})
		return
	}

	member, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	worked, err := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, from, to)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error calculando horas")
		return
	}

	days := int(to.Sub(from).Hours()/24) + 1
	expected := (member.WeeklyContractHours / 7.0) * float64(days)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"rows": []map[string]any{
			{
				"date":          from.Format("2006-01-02"),
				"label":         from.Format("02/01/2006") + " - " + to.Format("02/01/2006"),
				"workedHours":   round2(worked),
				"expectedHours": round2(expected),
				"difference":    round2(worked - expected),
			},
		},
	})
}

func (s *Server) handleBOMemberTableData(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	memberID, err := parseBOIDParam(r, "id")
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "id invalido",
		})
		return
	}

	view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	if view == "" {
		view = "monthly"
	}
	if view != "weekly" && view != "monthly" && view != "quarterly" && view != "yearly" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "view invalida",
		})
		return
	}

	yearStr := strings.TrimSpace(r.URL.Query().Get("year"))
	year := time.Now().Year()
	if yearStr != "" {
		y, err := strconv.Atoi(yearStr)
		if err != nil || y < 2000 || y > 2100 {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "año invalido",
			})
			return
		}
		year = y
	}

	member, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, memberID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"success": false,
				"message": "Miembro no encontrado",
			})
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo miembro")
		return
	}

	var rows []map[string]any
	monthNamesShort := []string{"Ene", "Feb", "Mar", "Abr", "May", "Jun", "Jul", "Ago", "Sep", "Oct", "Nov", "Dic"}

	if view == "yearly" {
		// Return monthly rows for the year
		for month := 1; month <= 12; month++ {
			mStart := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, boMadridTZ)
			mEnd := mStart.AddDate(0, 1, -1)
			mWorked, _ := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, mStart, mEnd)
			mDays := int(mEnd.Sub(mStart).Hours()/24) + 1
			mExpected := (member.WeeklyContractHours / 7.0) * float64(mDays)
			rows = append(rows, map[string]any{
				"date":          mStart.Format("2006-01-02"),
				"label":         monthNamesShort[month-1],
				"workedHours":   round2(mWorked),
				"expectedHours": round2(mExpected),
				"difference":    round2(mWorked - mExpected),
			})
		}
	} else if view == "quarterly" {
		for q := 1; q <= 4; q++ {
			qStart := time.Date(year, time.Month((q-1)*3+1), 1, 0, 0, 0, 0, boMadridTZ)
			qEnd := qStart.AddDate(0, 3, -1)
			qWorked, _ := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, qStart, qEnd)
			qDays := int(qEnd.Sub(qStart).Hours()/24) + 1
			qExpected := (member.WeeklyContractHours / 7.0) * float64(qDays)
			rows = append(rows, map[string]any{
				"date":          qStart.Format("2006-01-02"),
				"label":         fmt.Sprintf("Q%d %d", q, year),
				"workedHours":   round2(qWorked),
				"expectedHours": round2(qExpected),
				"difference":    round2(qWorked - qExpected),
			})
		}
	} else if view == "monthly" {
		for month := 1; month <= 12; month++ {
			mStart := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, boMadridTZ)
			mEnd := mStart.AddDate(0, 1, -1)
			mWorked, _ := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, mStart, mEnd)
			mDays := int(mEnd.Sub(mStart).Hours()/24) + 1
			mExpected := (member.WeeklyContractHours / 7.0) * float64(mDays)
			rows = append(rows, map[string]any{
				"date":          mStart.Format("2006-01-02"),
				"label":         monthNamesShort[month-1],
				"workedHours":   round2(mWorked),
				"expectedHours": round2(mExpected),
				"difference":    round2(mWorked - mExpected),
			})
		}
	} else {
		// weekly - return weeks of the year
		current := time.Date(year, 1, 1, 0, 0, 0, 0, boMadridTZ)
		endDate := time.Date(year, 12, 31, 23, 59, 59, 0, boMadridTZ)
		weekNum := 1
		for current.Before(endDate) || current.Equal(endDate) {
			weekEnd := current.AddDate(0, 0, 6)
			if weekEnd.After(endDate) {
				weekEnd = endDate
			}
			wWorked, _ := s.queryBOMemberWorkedHours(r.Context(), a.ActiveRestaurantID, memberID, current, weekEnd)
			wDays := int(weekEnd.Sub(current).Hours()/24) + 1
			wExpected := (member.WeeklyContractHours / 7.0) * float64(wDays)
			rows = append(rows, map[string]any{
				"date":          current.Format("2006-01-02"),
				"label":         fmt.Sprintf("S%d", weekNum),
				"workedHours":   round2(wWorked),
				"expectedHours": round2(wExpected),
				"difference":    round2(wWorked - wExpected),
			})
			current = current.AddDate(0, 0, 7)
			weekNum++
			if weekNum > 53 {
				break
			}
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"rows":    rows,
	})
}

func (s *Server) getBOMemberByID(ctx context.Context, restaurantID, memberID int) (boMember, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			m.id,
			m.bo_user_id,
			m.first_name,
			m.last_name,
			m.email,
			m.dni,
			m.bank_account,
			m.phone,
			m.whatsapp_number,
			m.photo_url,
			COALESCE(c.weekly_hours, 40.00) AS weekly_hours
		FROM restaurant_members m
		LEFT JOIN member_contracts c ON c.restaurant_member_id = m.id
		WHERE m.restaurant_id = ? AND m.id = ? AND m.is_active = 1
		LIMIT 1
	`, restaurantID, memberID)
	return scanBOMember(row)
}

type boMemberScanner interface {
	Scan(dest ...any) error
}

func scanBOMember(scanner boMemberScanner) (boMember, error) {
	var (
		m          boMember
		boUserID   sql.NullInt64
		email      sql.NullString
		dni        sql.NullString
		bank       sql.NullString
		phone      sql.NullString
		whatsapp   sql.NullString
		photo      sql.NullString
		weeklyHour float64
	)
	err := scanner.Scan(
		&m.ID,
		&boUserID,
		&m.FirstName,
		&m.LastName,
		&email,
		&dni,
		&bank,
		&phone,
		&whatsapp,
		&photo,
		&weeklyHour,
	)
	if err != nil {
		return boMember{}, err
	}
	if boUserID.Valid {
		v := int(boUserID.Int64)
		m.BOUserID = &v
	}
	m.Email = nullStringPtr(email)
	m.DNI = nullStringPtr(dni)
	m.BankAccount = nullStringPtr(bank)
	m.Phone = nullStringPtr(phone)
	m.WhatsAppNumber = nullStringPtr(whatsapp)
	m.PhotoURL = nullStringPtr(photo)
	m.WeeklyContractHours = weeklyHour
	return m, nil
}

func (s *Server) queryBOMemberPoints(ctx context.Context, restaurantID, memberID int, start, end time.Time, view string) ([]boMemberStatsPoint, float64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DATE_FORMAT(work_date, '%Y-%m-%d') AS d, COALESCE(SUM(minutes_worked), 0) AS total_minutes
		FROM member_time_entries
		WHERE restaurant_id = ? AND restaurant_member_id = ? AND work_date BETWEEN ? AND ?
		GROUP BY work_date
	`, restaurantID, memberID, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	minutesByDate := make(map[string]int)
	for rows.Next() {
		var (
			dateISO string
			minutes int
		)
		if err := rows.Scan(&dateISO, &minutes); err != nil {
			return nil, 0, err
		}
		minutesByDate[dateISO] = minutes
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating member stats rows: %w", err)
	}

	out := make([]boMemberStatsPoint, 0, 64)
	totalHours := 0.0
	for cur := start; !cur.After(end); cur = cur.AddDate(0, 0, 1) {
		dateISO := cur.Format("2006-01-02")
		hours := float64(minutesByDate[dateISO]) / 60.0
		totalHours += hours
		out = append(out, boMemberStatsPoint{
			Date:  dateISO,
			Label: boPointLabel(cur, view),
			Hours: round2(hours),
		})
	}
	return out, round2(totalHours), nil
}

func (s *Server) queryBOMemberWorkedHours(ctx context.Context, restaurantID, memberID int, start, end time.Time) (float64, error) {
	var minutes sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(minutes_worked), 0)
		FROM member_time_entries
		WHERE restaurant_id = ? AND restaurant_member_id = ? AND work_date BETWEEN ? AND ?
	`, restaurantID, memberID, start.Format("2006-01-02"), end.Format("2006-01-02")).Scan(&minutes)
	if err != nil {
		return 0, err
	}
	return round2(float64(minutes.Int64) / 60.0), nil
}

func parseBOIDParam(r *http.Request, key string) (int, error) {
	raw := strings.TrimSpace(chi.URLParam(r, key))
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func boTodayDate() time.Time {
	now := time.Now().In(boMadridTZ)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, boMadridTZ)
}

func parseBODate(v string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(v), boMadridTZ)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, boMadridTZ), nil
}

func boDateRangeForView(ref time.Time, view string) (time.Time, time.Time) {
	ref = time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, boMadridTZ)
	switch view {
	case "monthly":
		start := time.Date(ref.Year(), ref.Month(), 1, 0, 0, 0, 0, boMadridTZ)
		end := start.AddDate(0, 1, -1)
		return start, end
	case "quarterly":
		start := boQuarterStart(ref)
		end := start.AddDate(0, 3, -1)
		return start, end
	case "yearly":
		start := time.Date(ref.Year(), 1, 1, 0, 0, 0, 0, boMadridTZ)
		end := time.Date(ref.Year(), 12, 31, 23, 59, 59, 0, boMadridTZ)
		return start, end
	default:
		weekday := int(ref.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		start := ref.AddDate(0, 0, -(weekday - 1))
		end := start.AddDate(0, 0, 6)
		return start, end
	}
}

func boQuarterStart(ref time.Time) time.Time {
	qStartMonth := time.Month(((int(ref.Month())-1)/3)*3 + 1)
	return time.Date(ref.Year(), qStartMonth, 1, 0, 0, 0, 0, boMadridTZ)
}

func boPointLabel(d time.Time, view string) string {
	if view == "weekly" {
		switch d.Weekday() {
		case time.Monday:
			return "L"
		case time.Tuesday:
			return "M"
		case time.Wednesday:
			return "X"
		case time.Thursday:
			return "J"
		case time.Friday:
			return "V"
		case time.Saturday:
			return "S"
		default:
			return "D"
		}
	}
	return d.Format("02")
}

func normalizeOptionalEmail(v *string) string {
	if v == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(*v))
}

func normalizeOptionalString(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.TrimSpace(v)
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := strings.TrimSpace(v.String)
	if s == "" {
		return nil
	}
	return &s
}

func ptrToValue(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
