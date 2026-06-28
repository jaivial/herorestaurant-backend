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
