package api

import (
	"net/http"
	"sort"
	"strings"

	"preactvillacarmen/internal/httpx"
)

// Special-date statistics: people booked, menus (count) with nested principales
// counts, adelanto paid total + per payment method. Computed from the same
// `special` response block the reservas table uses, so both always agree.
// GET /api/admin/config/special-dates/stats?date=YYYY-MM-DD
// Coordination id: special_date_stats_v1

type specialStatsDish struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type specialStatsMenu struct {
	Label       string             `json:"label"`
	Count       int                `json:"count"`
	Principales []specialStatsDish `json:"principales"`
}

type specialStatsMethod struct {
	Method string  `json:"method"`
	Amount float64 `json:"amount"`
}

func (s *Server) handleBOSpecialDateStats(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if !isValidISODate(date) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Invalid date"})
		return
	}
	rid := a.ActiveRestaurantID
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT party_size, COALESCE(is_prereserva, 0), COALESCE(special_json, '')
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date = ? AND COALESCE(is_special_booking, 0) = 1`, rid, date)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error consultando estadísticas")
		return
	}
	defer rows.Close()

	people, bookings := 0, 0
	paidTotal := 0.0
	menuOrder := []string{}
	menus := map[string]*specialStatsMenu{}
	dishIdx := map[string]map[string]int{}
	byMethod := map[string]float64{}
	for rows.Next() {
		var party, prereserva int
		var raw string
		if err := rows.Scan(&party, &prereserva, &raw); err != nil {
			continue
		}
		bookings++
		people += party
		block := s.buildSpecialBookingResponse(r.Context(), rid, true, prereserva != 0, raw)
		if block == nil {
			continue
		}
		if m, ok := block["adelanto_by_method"].([]map[string]any); ok {
			for _, row := range m {
				if paid, _ := numericField(row, "paid"); paid > 0 {
					byMethod[anyToString(row["method"])] += paid
					paidTotal += paid
				}
			}
		}
		sum, ok := buildSpecialBookingSummary(map[string]any{"is_special_booking": true, "special": block})
		if !ok {
			continue
		}
		for _, line := range sum.Menus {
			m, seen := menus[line.Label]
			if !seen {
				m = &specialStatsMenu{Label: line.Label, Principales: []specialStatsDish{}}
				menus[line.Label] = m
				dishIdx[line.Label] = map[string]int{}
				menuOrder = append(menuOrder, line.Label)
			}
			m.Count += line.Count
			for _, d := range line.Principales {
				if i, ok := dishIdx[line.Label][d.Name]; ok {
					m.Principales[i].Count += d.Count
					continue
				}
				dishIdx[line.Label][d.Name] = len(m.Principales)
				m.Principales = append(m.Principales, specialStatsDish{Name: d.Name, Count: d.Count})
			}
		}
	}

	outMenus := make([]specialStatsMenu, 0, len(menuOrder))
	for _, label := range menuOrder {
		m := menus[label]
		sort.SliceStable(m.Principales, func(i, j int) bool { return m.Principales[i].Count > m.Principales[j].Count })
		outMenus = append(outMenus, *m)
	}
	methods := make([]specialStatsMethod, 0, len(byMethod))
	for k, v := range byMethod {
		methods = append(methods, specialStatsMethod{Method: k, Amount: round2(v)})
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].Method < methods[j].Method })

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"stats": map[string]any{
			"date":                date,
			"bookings":            bookings,
			"people":              people,
			"menus":               outMenus,
			"adelanto_paid_total": round2(paidTotal),
			"adelanto_by_method":  methods,
		},
	})
}
