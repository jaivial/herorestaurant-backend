package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// posPINApproval decides whether an action needs a manager PIN and, when one is
// offered, verifies it. It is the single rule for every money-reducing action
// (line void, comp, discount, refund, cash movement) so they cannot drift.
//
// The returned name is "" when no PIN was offered and none was required. When
// the policy requires a PIN and none was sent, the answer is a 403 with code
// PIN_REQUIRED so the till can ask for it instead of showing a bare error.
//
// Any active member with a PIN can approve: restaurant_members has no manager
// flag, so "who has a PIN" IS the manager list. Say so rather than pretend.
func (s *Server) posPINApproval(w http.ResponseWriter, r *http.Request, restaurantID int, pin string, amountCents int64, isDiscount bool) (string, bool) {
	pin = strings.TrimSpace(pin)
	settings, err := s.loadPOSSettings(r.Context(), restaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading POS settings")
		return "", false
	}
	required := (isDiscount && settings.PinRequiredForDiscount) || (settings.PinThresholdCents != nil && *settings.PinThresholdCents > 0 && amountCents >= *settings.PinThresholdCents)
	if pin == "" {
		if required {
			reason := "Esta acción necesita el PIN de un responsable"
			if !(isDiscount && settings.PinRequiredForDiscount) {
				reason = "Por encima del importe configurado hace falta el PIN de un responsable"
			}
			httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"success": false, "code": "PIN_REQUIRED", "message": reason})
			return "", false
		}
		return "", true
	}
	// The same per-terminal throttle as /pin/verify. Before this, a PIN sent
	// inline with a void or a cash movement skipped it, so the throttle could
	// be walked around one void at a time.
	a, _ := boAuthFromContext(r.Context())
	var lockedUntil sql.NullTime
	if err = s.db.QueryRowContext(r.Context(), `SELECT locked_until FROM pos_pin_attempts WHERE restaurant_id=? AND attempted_by=?`, restaurantID, a.User.ID).Scan(&lockedUntil); err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading PIN attempts")
		return "", false
	}
	if lockedUntil.Valid && time.Now().Before(lockedUntil.Time) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, map[string]any{"success": false, "code": "PIN_LOCKED", "message": "Demasiados intentos fallidos. Espera unos minutos."})
		return "", false
	}
	name, err := s.verifyPOSApprovalPIN(r, restaurantID, pin)
	if err != nil {
		if countErr := s.countPINFailure(r, int64(restaurantID), int64(a.User.ID)); countErr != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error recording failed attempt")
			return "", false
		}
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{"success": false, "code": "PIN_INVALID", "message": "PIN de aprobación incorrecto"})
		return "", false
	}
	_, _ = s.db.ExecContext(r.Context(), `DELETE FROM pos_pin_attempts WHERE restaurant_id=? AND attempted_by=?`, restaurantID, a.User.ID)
	return name, true
}
