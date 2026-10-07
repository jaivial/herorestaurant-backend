package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"preactvillacarmen/internal/httpx"
)

// A terminal PIN is typed many times a shift by someone in a hurry, so it is
// deliberately short. Four digits is the most a keypad offers without becoming
// annoying, and the threat it defends against is "a colleague undoes my order",
// not a determined attacker with the tablet in hand.
var posPinPattern = regexp.MustCompile(`^\d{4,6}$`)

const (
	posPinMaxAttempts = 5
	posPinLockWindow  = 15 * time.Minute
)

// handleBOPOSPinSet lets a member choose their own PIN. It is deliberately not
// reachable by a manager for someone else: a PIN the holder does not know is
// only ever an inconvenience, and letting an admin set it turns the PIN into
// something the waiter cannot rely on to prove who they were.
func (s *Server) handleBOPOSPinSet(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Pin        string `json:"pin"`
		CurrentPin string `json:"currentPin"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	pin := strings.TrimSpace(in.Pin)
	if !posPinPattern.MatchString(pin) {
		httpx.WriteError(w, http.StatusBadRequest, "El PIN debe tener 4 a 6 dígitos")
		return
	}
	// A member who already has a PIN must type the old one to replace it, so a
	// borrowed tablet cannot quietly reassign the account that the audit trail
	// will then blame for the actions.
	var current string
	err := s.db.QueryRowContext(r.Context(), `SELECT COALESCE(pos_pin_hash,'') FROM restaurant_members WHERE restaurant_id=? AND bo_user_id=? AND is_active=1`, a.ActiveRestaurantID, a.User.ID).Scan(&current)
	if err == sql.ErrNoRows {
		httpx.WriteError(w, http.StatusNotFound, "No staff record for this user")
		return
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading PIN")
		return
	}
	if strings.TrimSpace(current) != "" {
		typed := strings.TrimSpace(in.CurrentPin)
		if !posPinPattern.MatchString(typed) || bcrypt.CompareHashAndPassword([]byte(current), []byte(typed)) != nil {
			httpx.WriteError(w, http.StatusForbidden, "El PIN actual no es correcto")
			return
		}
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error hashing PIN")
		return
	}
	if _, err = s.db.ExecContext(r.Context(), `UPDATE restaurant_members SET pos_pin_hash=?,pos_pin_set_at=NOW() WHERE restaurant_id=? AND bo_user_id=?`, string(hashed), a.ActiveRestaurantID, a.User.ID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error saving PIN")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "hasPin": true})
}

// handleBOPOSPinVerify checks a typed PIN and, on success, tells the caller who
// it belongs to. It exists so a manager can approve a void on someone else's
// ticket: the POS asks for a PIN, gets the manager's name, and records it next
// to the action. A failure does not say whether the member exists.
func (s *Server) handleBOPOSPinVerify(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		Pin string `json:"pin"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	pin := strings.TrimSpace(in.Pin)
	if !posPinPattern.MatchString(pin) {
		httpx.WriteError(w, http.StatusBadRequest, "El PIN debe tener 4 a 6 dígitos")
		return
	}
	// Throttled per terminal first: after a few wrong guesses this session stops
	// being allowed to try, so a PIN cannot be ground out one guess at a time.
	var lockedUntil sql.NullTime
	err := s.db.QueryRowContext(r.Context(), `SELECT locked_until FROM pos_pin_attempts WHERE restaurant_id=? AND attempted_by=?`, a.ActiveRestaurantID, a.User.ID).Scan(&lockedUntil)
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading PIN attempts")
		return
	}
	if lockedUntil.Valid && time.Now().Before(lockedUntil.Time) {
		httpx.WriteError(w, http.StatusTooManyRequests, "Demasiados intentos fallidos. Espera unos minutos.")
		return
	}
	// Only active members may be used to approve an action: an account that has
	// left the restaurant must not be able to sign off tonight's voids.
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,pos_pin_hash FROM restaurant_members WHERE restaurant_id=? AND is_active=1 AND pos_pin_hash IS NOT NULL`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading PINs")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var hashed string
		if err = rows.Scan(&id, &hashed); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error reading PINs")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(hashed), []byte(pin)) == nil {
			// Reset the terminal's counter on success: one bad guess while
			// closing the till must not lock the waiter out of their own PIN.
			_, _ = s.db.ExecContext(r.Context(), `DELETE FROM pos_pin_attempts WHERE restaurant_id=? AND attempted_by=?`, a.ActiveRestaurantID, a.User.ID)
			var name string
			_ = s.db.QueryRowContext(r.Context(), `SELECT TRIM(CONCAT(first_name,' ',last_name)) FROM restaurant_members WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, id).Scan(&name)
			httpx.WriteJSON(w, 200, map[string]any{"success": true, "memberId": id, "displayName": name})
			return
		}
	}
	if err = rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading PINs")
		return
	}
	// Throttle against this terminal, not against the member: a wrong PIN
	// cannot honestly be blamed on the person it was meant for.
	if err = s.countPINFailure(r, int64(a.ActiveRestaurantID), int64(a.User.ID)); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error recording failed attempt")
		return
	}
	httpx.WriteError(w, http.StatusUnauthorized, "PIN incorrecto")
}

// countPINFailure records a failed attempt.
//
// bcrypt salts every hash, so a wrong PIN cannot be attributed to the member it
// was meant for, and pretending otherwise would be a lie in the audit trail. So
// the counter lives on the terminal (the signed-in user), not on a person: after
// a few wrong guesses this session stops accepting PINs for a while. That is the
// honest tradeoff — a guessed PIN never gets blamed on an innocent colleague, and
// a determined attacker is throttled anyway.
func (s *Server) countPINFailure(r *http.Request, restaurantID int64, userID int64) error {
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO pos_pin_attempts (restaurant_id,attempted_by,failed_attempts,locked_until)
		VALUES (?,?,1,DATE_ADD(NOW(),INTERVAL ? MINUTE))
		ON DUPLICATE KEY UPDATE
		  failed_attempts = IF(locked_until IS NOT NULL AND locked_until <= NOW(), 1, failed_attempts + 1),
		  locked_until = IF(locked_until IS NOT NULL AND locked_until <= NOW(), DATE_ADD(NOW(),INTERVAL ? MINUTE),
		                    IF(failed_attempts + 1 >= ?, DATE_ADD(NOW(),INTERVAL ? MINUTE), locked_until))`,
		restaurantID, userID, int(posPinLockWindow.Minutes()), int(posPinLockWindow.Minutes()), posPinMaxAttempts)
	return err
}

// handleBOPOSPinStatus tells the signed-in user whether they have a PIN, so the
// UI can ask for one at a sensible moment instead of failing at the till.
func (s *Server) handleBOPOSPinStatus(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var exists int
	err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM restaurant_members WHERE restaurant_id=? AND bo_user_id=? AND is_active=1 AND pos_pin_hash IS NOT NULL`, a.ActiveRestaurantID, a.User.ID).Scan(&exists)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading PIN")
		return
	}
	var count int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM restaurant_members WHERE restaurant_id=? AND is_active=1 AND pos_pin_hash IS NOT NULL`, a.ActiveRestaurantID).Scan(&count)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "hasPin": exists > 0, "staffWithPin": count})
}
