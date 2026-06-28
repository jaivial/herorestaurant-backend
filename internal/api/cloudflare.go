package api

import (
	"encoding/json"
	"net/http"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleDomainSearch(w http.ResponseWriter, r *http.Request) {
	_, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		httpx.WriteError(w, http.StatusBadRequest, "Dominio requerido")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"domain":       query,
			"available":    true,
			"base_price":   10.00,
			"marked_price": 15.00,
			"currency":     "EUR",
		},
	})
}

func (s *Server) handleDomainRegister(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	var req struct {
		Domain string  `json:"domain"`
		Price  float64 `json:"price"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos inválidos")
		return
	}

	customerName := "Cliente " + req.Domain
	customerEmail := "admin@" + req.Domain

	var restName string
	err := s.db.QueryRowContext(r.Context(), "SELECT name FROM restaurants WHERE id = ?", a.ActiveRestaurantID).Scan(&restName)
	if err == nil && restName != "" {
		customerName = restName
	}

	startDate := time.Now().Format("2006-01-02")

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO recurring_invoices (
			restaurant_id, customer_name, customer_email, amount, currency,
			iva_rate, frequency, start_date, auto_send, is_active
		) VALUES (?, ?, ?, ?, 'EUR', 21.00, 'yearly', ?, 1, 1)
	`, a.ActiveRestaurantID, customerName, customerEmail, req.Price, startDate)

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al crear suscripción de facturación")
		return
	}

	_, err = s.db.ExecContext(r.Context(), `
		UPDATE restaurant_websites SET domain = ? WHERE restaurant_id = ?
	`, req.Domain, a.ActiveRestaurantID)

	if err != nil {
		_, _ = s.db.ExecContext(r.Context(), `
			INSERT IGNORE INTO restaurant_websites (restaurant_id, domain, is_published)
			VALUES (?, ?, 0)
		`, a.ActiveRestaurantID, req.Domain)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Dominio registrado exitosamente",
	})
}
