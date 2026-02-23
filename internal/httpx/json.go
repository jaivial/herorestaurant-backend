package httpx

import (
	"encoding/json"
	"net/http"
	"strings"
)

const MovingExpirationHeader = "X-Moving-Expiration-Date"

func WriteJSON(w http.ResponseWriter, status int, body any) {
	body = withMovingExpiration(body, w.Header().Get(MovingExpirationHeader))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]any{
		"success": false,
		"message": message,
	})
}

func withMovingExpiration(body any, movingExpiration string) any {
	movingExpiration = strings.TrimSpace(movingExpiration)
	if movingExpiration == "" {
		return body
	}

	payload, ok := body.(map[string]any)
	if !ok || payload == nil {
		return body
	}
	if _, exists := payload["moving_expiration_date"]; exists {
		return body
	}

	clone := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		clone[key] = value
	}
	clone["moving_expiration_date"] = movingExpiration
	return clone
}
