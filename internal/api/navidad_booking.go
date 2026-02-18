package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"preactvillacarmen/internal/httpx"
)

type fixedWindowLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *fixedWindowLimiter) allow(key string, max int, window time.Duration) bool {
	if max <= 0 {
		return true
	}
	now := time.Now()
	cutoff := now.Add(-window)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	arr := l.hits[key]
	out := arr[:0]
	for _, t := range arr {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	if len(out) >= max {
		l.hits[key] = out
		return false
	}
	out = append(out, now)
	l.hits[key] = out
	return true
}

var navidadLimiter fixedWindowLimiter

func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func sendUazAPI(ctx context.Context, endpoint string, payload any) (body string, status int, err error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return string(raw), resp.StatusCode, nil
}

func (s *Server) handleNavidadBooking(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	// Global circuit breaker: max 10 per hour overall.
	if !navidadLimiter.allow("global:navidad_booking_attempt", 10, time.Hour) {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success":    false,
			"message":    "El sistema está experimentando un alto volumen de tráfico. Por favor, inténtelo más tarde o contacte por teléfono.",
			"error_code": "SECURITY_BLOCK",
		})
		return
	}

	ip := clientIP(r)
	// Individual rate limiting: 3 per hour.
	if !navidadLimiter.allow("ip:"+ip+":navidad_booking_attempt", 3, time.Hour) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, map[string]any{
			"success":    false,
			"message":    "Too many requests. Please wait before trying again.",
			"error_code": "SECURITY_BLOCK",
		})
		return
	}

	var input struct {
		WebsiteURL   string `json:"website_url"`
		FormLoadTime *int64 `json:"form_load_time"`

		Name        string `json:"name"`
		Phone       string `json:"phone"`
		CountryCode string `json:"country_code"`
		People      any    `json:"people"`
		Type        string `json:"type"`
		Time        string `json:"time"`
		Date        string `json:"date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Server error: " + err.Error(),
			"error_code": "PHP_EXCEPTION",
		})
		return
	}

	if strings.TrimSpace(input.WebsiteURL) != "" {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{
			"success":    false,
			"message":    "Spam detected.",
			"error_code": "SECURITY_BLOCK",
		})
		return
	}

	if input.FormLoadTime != nil {
		duration := time.Now().Unix() - *input.FormLoadTime
		if duration < 5 {
			httpx.WriteJSON(w, http.StatusForbidden, map[string]any{
				"success":    false,
				"message":    "Spam detected. Submission too fast.",
				"error_code": "SECURITY_BLOCK",
			})
			return
		}
	}

	name := strings.TrimSpace(input.Name)
	phone := strings.TrimSpace(input.Phone)
	people := strings.TrimSpace(anyToString(input.People))
	typ := strings.TrimSpace(input.Type)
	hour := strings.TrimSpace(input.Time)
	date := strings.TrimSpace(input.Date)
	countryCode := strings.TrimSpace(input.CountryCode)
	if countryCode == "" {
		countryCode = "+34"
	}

	if name == "" || phone == "" || people == "" || typ == "" || hour == "" || date == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"message": "Faltan parámetros requeridos",
		})
		return
	}

	uazURL, uazToken := s.uazapiBaseAndToken(r.Context(), restaurantID)
	if uazURL == "" || uazToken == "" {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success": false,
			"message": "El servicio de notificaciones no está disponible.",
			"error_code": "NOTIFICATION_SERVICE_UNAVAILABLE",
		})
		return
	}

	branding, _ := s.loadRestaurantBranding(r.Context(), restaurantID)
	brandName := strings.TrimSpace(branding.BrandName)
	if brandName == "" {
		brandName = "Restaurante"
	}
	baseURL := publicBaseURL(r)

	typeDisplay := "Cena"
	if strings.EqualFold(typ, "comida") {
		typeDisplay = "Comida"
	}

	formattedDate := date
	if t, err := time.ParseInLocation("2006-01-02", date, time.Local); err == nil {
		formattedDate = t.Format("02/01/2006")
	}

	fullPhone := countryCode + " " + phone

	// Clean phone: digits only with country code (no '+').
	cleanPhone := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	cc := strings.TrimPrefix(countryCode, "+")
	cc = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, cc)
	cleanPhone = cc + cleanPhone

	notifications := map[string]any{}

	// 1) WhatsApp to client (button).
	clientMessage := "Consulta de " + typeDisplay + " de empresa recibida:\n\n"
	clientMessage += "📅 Fecha: " + formattedDate + "\n"
	clientMessage += "🕐 Hora: " + hour + "\n"
	clientMessage += "👥 Número de personas: " + people + "\n\n"
	clientMessage += brandName + " les desea felices fiestas.\n\n"
	clientMessage += "Puede consultar nuestros menús de grupos en el siguiente enlace."

	clientPayload := map[string]any{
		"number":  cleanPhone,
		"type":    "button",
		"text":    clientMessage,
		"choices": []string{"Ver Menús de Grupos|" + strings.TrimRight(baseURL, "/") + "/menudegrupos.php"},
	}
	clientEndpoint := uazURL + "/send/menu?token=" + url.QueryEscape(uazToken)

	clientRespBody, clientHTTP, clientErr := sendUazAPI(r.Context(), clientEndpoint, clientPayload)
	clientSent := clientErr == nil && (clientHTTP == 200 || clientHTTP == 201)
	notifications["client_whatsapp"] = map[string]any{
		"sent":      clientSent,
		"to":        cleanPhone,
		"response":  clientRespBody,
		"http_code": clientHTTP,
		"error":     errString(clientErr),
		"data_sent": clientPayload,
	}

	// 2) WhatsApp to restaurant numbers.
	restaurantMessage := "🔔 CONSULTA DE " + strings.ToUpper(typeDisplay) + " DE EMPRESA\n\n"
	restaurantMessage += "Aquí tiene los detalles del cliente:\n\n"
	restaurantMessage += "👤 Nombre: " + name + "\n"
	restaurantMessage += "📱 Teléfono: " + fullPhone + "\n"
	restaurantMessage += "📅 Fecha solicitada: " + formattedDate + "\n"
	restaurantMessage += "🕐 Hora: " + hour + "\n"
	restaurantMessage += "👥 Número de personas: " + people + "\n"
	restaurantMessage += "🍽️ Tipo: " + typeDisplay + "\n\n"
	restaurantMessage += "Fecha de consulta: " + time.Now().Format("02/01/2006 15:04")

	restaurantEndpoint, restaurantNumbers := s.uazapiSendTextURL(r.Context(), restaurantID)
	results := []map[string]any{}
	sentAny := false
	for _, n := range restaurantNumbers {
		payload := map[string]any{"number": n, "text": restaurantMessage}
		body, code, err := sendUazAPI(r.Context(), restaurantEndpoint, payload)
		sent := err == nil && (code == 200 || code == 201)
		if sent {
			sentAny = true
		}
		results = append(results, map[string]any{
			"number":    n,
			"sent":      sent,
			"response":  body,
			"http_code": code,
			"error":     errString(err),
		})
	}
	notifications["restaurant_whatsapp"] = map[string]any{
		"sent":    sentAny,
		"results": results,
	}

	// 3) Log to file (best-effort).
	logPath := filepath.Join("logs", "navidad_bookings.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		line := "[" + time.Now().Format("2006-01-02 15:04:05") + "] "
		line += "Navidad Booking - " + name + " (" + fullPhone + ") - " + typeDisplay + " - "
		line += formattedDate + " " + hour + " - " + people + " personas\n"
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			_, _ = f.WriteString(line)
			_ = f.Close()
			notifications["log"] = map[string]any{"saved": true, "file": logPath}
		} else {
			notifications["log"] = map[string]any{"saved": false, "file": logPath}
		}
	} else {
		notifications["log"] = map[string]any{"saved": false, "file": logPath}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"message":       "Consulta enviada correctamente",
		"notifications": notifications,
	})
}

func errString(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}
