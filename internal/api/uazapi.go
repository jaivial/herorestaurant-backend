package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	"preactvillacarmen/internal/httpx"
)

func (s *Server) handleUazapiSend(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	var req struct {
		MemberID int    `json:"member_id"`
		Message  string `json:"message"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos inválidos")
		return
	}

	var subActive int
	err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM recurring_invoices 
		WHERE restaurant_id = ? AND customer_name LIKE '%WhatsApp%' AND is_active = 1
	`, a.ActiveRestaurantID).Scan(&subActive)

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error verificando suscripción")
		return
	}

	if subActive == 0 {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{
			"success": false,
			"code":    "NEEDS_SUBSCRIPTION",
			"message": "Requiere paquete Premium WhatsApp",
		})
		return
	}

	member, err := s.getBOMemberByID(r.Context(), a.ActiveRestaurantID, req.MemberID)
	if err != nil || member.WhatsappNumber == nil || *member.WhatsappNumber == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "El miembro no tiene número de WhatsApp configurado",
		})
		return
	}

	uazapiUrl := s.cfg.UazapiUrl
	uazapiToken := s.cfg.UazapiToken

	if uazapiUrl == "" || uazapiToken == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"message": "Mensaje simulado (falta configuración Uazapi)",
		})
		return
	}

	bodyBytes, _ := json.Marshal(map[string]any{
		"number": *member.WhatsappNumber,
		"options": map[string]any{
			"delay":    1200,
			"presence": "composing",
		},
		"textMessage": map[string]any{
			"text": req.Message,
		},
	})

	uazapiReq, err := http.NewRequestWithContext(r.Context(), "POST", uazapiUrl+"/message/sendText/Restaurantes", bytes.NewBuffer(bodyBytes))
	if err == nil {
		uazapiReq.Header.Set("Content-Type", "application/json")
		uazapiReq.Header.Set("apikey", uazapiToken)

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(uazapiReq)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"success": true,
					"message": "Mensaje enviado por WhatsApp",
				})
				return
			}
		}
	}

	httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
		"success": false,
		"message": "Error al enviar mensaje a través de Uazapi",
	})
}

func (s *Server) handleUazapiSubscribe(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "No autorizado")
		return
	}

	startDate := time.Now().Format("2006-01-02")

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO recurring_invoices (
			restaurant_id, customer_name, customer_email, amount, currency,
			iva_rate, frequency, start_date, auto_send, is_active
		) VALUES (?, 'WhatsApp Premium', 'admin@restaurant', 29.99, 'EUR', 21.00, 'monthly', ?, 1, 1)
	`, a.ActiveRestaurantID, startDate)

	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error al crear suscripción WhatsApp")
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Suscripción a WhatsApp Premium activada",
	})
}
