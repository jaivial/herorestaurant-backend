package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
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
	// The PIN belongs to the signed-in backoffice user. Most restaurant_members
	// rows have no bo_user_id, so "my member row" cannot be the key: a manager
	// whose staff record was never linked would be unable to set a PIN at all.
	// The member row is created on demand so the PIN has a home and the name
	// shown next to an approved action is not blank.
	memberID, err := s.ensurePOSPinMember(r, a.ActiveRestaurantID, a.User.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading staff record")
		return
	}
	// A member who already has a PIN must type the old one to replace it, so a
	// borrowed tablet cannot quietly reassign the account that the audit trail
	// will then blame for the actions.
	var current string
	if err = s.db.QueryRowContext(r.Context(), `SELECT COALESCE(pos_pin_hash,'') FROM restaurant_members WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, memberID).Scan(&current); err != nil {
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
	if _, err = s.db.ExecContext(r.Context(), `UPDATE restaurant_members SET pos_pin_hash=?,pos_pin_set_at=NOW() WHERE restaurant_id=? AND id=?`, string(hashed), a.ActiveRestaurantID, memberID); err != nil {
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
	// Resolved through the same helper the line void uses, so the two paths
	// cannot drift apart in who they accept.
	name, nameErr := s.verifyPOSApprovalPIN(r, a.ActiveRestaurantID, pin)
	if nameErr == nil {
		// Reset the terminal's counter on success: one bad guess while closing
		// the till must not lock the waiter out of their own PIN.
		_, _ = s.db.ExecContext(r.Context(), `DELETE FROM pos_pin_attempts WHERE restaurant_id=? AND attempted_by=?`, a.ActiveRestaurantID, a.User.ID)
		httpx.WriteJSON(w, 200, map[string]any{"success": true, "displayName": name})
		return
	}
	// Throttle against this terminal, not against the member: a wrong PIN
	// cannot honestly be blamed on the person it was meant for.
	if err = s.countPINFailure(r, int64(a.ActiveRestaurantID), int64(a.User.ID)); err != nil {
		// Surfaced rather than swallowed: a throttle that silently stops
		// recording is a throttle that silently stops working.
		httpx.WriteError(w, http.StatusInternalServerError, "Error recording failed attempt: "+err.Error())
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
	// The lock window is interpolated, not bound: inside DATE_ADD(... INTERVAL ? MINUTE)
	// MySQL treats the ? as part of the INTERVAL keyword and never sees a placeholder,
	// so binding it there makes the driver complain about the argument count.
	window := strconv.Itoa(int(posPinLockWindow.Minutes()))
	// The lock is only set once the count actually reaches the threshold. Doing it
	// in the VALUES clause would arm the lockout on the very first wrong PIN and
	// lock the waiter out of their own PIN for a single typo.
	statement := `INSERT INTO pos_pin_attempts (restaurant_id,attempted_by,failed_attempts,locked_until)
		VALUES (?,?,1,IF(1>=?,DATE_ADD(NOW(),INTERVAL ` + window + ` MINUTE),NULL))
		ON DUPLICATE KEY UPDATE
		  failed_attempts = IF(updated_at < DATE_SUB(NOW(),INTERVAL ` + window + ` MINUTE) OR (locked_until IS NOT NULL AND locked_until <= NOW()), 1, failed_attempts + 1),
		  locked_until = IF(failed_attempts >= ?, DATE_ADD(NOW(),INTERVAL ` + window + ` MINUTE), NULL)`
	// MySQL applies these assignments left to right, and locked_until reads the
	// failed_attempts this same statement just wrote. The previous version
	// tested "failed_attempts + 1" AFTER incrementing, so it locked on the 4th
	// failure instead of the 5th, and failures never expired: three typos last
	// week plus one today locked the terminal for 15 minutes (measured on dev).
	// Now the count restarts after an expired lock or a quiet window.
	_, err := s.db.ExecContext(r.Context(), statement, restaurantID, userID, posPinMaxAttempts, posPinMaxAttempts)
	return err
}

// handleBOPOSPinStatus tells the signed-in user whether they have a PIN, so the
// UI can ask for one at a sensible moment instead of failing at the till.
func (s *Server) handleBOPOSPinStatus(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	memberID, err := s.lookupPOSPinMember(r, a.ActiveRestaurantID, a.User.ID)
	var exists int
	if err == nil {
		err = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM restaurant_members WHERE restaurant_id=? AND id=? AND pos_pin_hash IS NOT NULL`, a.ActiveRestaurantID, memberID).Scan(&exists)
	}
	if err != nil && err != sql.ErrNoRows {
		httpx.WriteError(w, http.StatusInternalServerError, "Error reading PIN")
		return
	}
	var count int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM restaurant_members WHERE restaurant_id=? AND is_active=1 AND pos_pin_hash IS NOT NULL`, a.ActiveRestaurantID).Scan(&count)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "hasPin": exists > 0, "staffWithPin": count})
}

// lookupPOSPinMember returns the staff row that carries this backoffice user's
// PIN, or sql.ErrNoRows when the user has never set one. It is a lookup only:
// asking whether you have a PIN must not create anything.
func (s *Server) lookupPOSPinMember(r *http.Request, restaurantID int, userID int) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(r.Context(), `SELECT id FROM restaurant_members WHERE restaurant_id=? AND bo_user_id=? AND is_active=1`, restaurantID, userID).Scan(&id)
	return id, err
}

// ensurePOSPinMember is lookupPOSPinMember plus a fallback for the manager whose
// staff record was never linked to their backoffice login. The row is created
// with the account's own name rather than left blank, so an approved action
// shows a real person in the audit trail instead of an empty cell.
func (s *Server) ensurePOSPinMember(r *http.Request, restaurantID int, userID int) (int64, error) {
	if id, err := s.lookupPOSPinMember(r, restaurantID, userID); err == nil {
		return id, nil
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	// bo_users carries a single `name`, not first/last, so the name is split once
	// here to fit the members table rather than inventing a surname.
	var full string
	if err := s.db.QueryRowContext(r.Context(), `SELECT COALESCE(NULLIF(name,''),email) FROM bo_users WHERE id=?`, userID).Scan(&full); err != nil {
		return 0, err
	}
	first, last := splitPOSPinName(full)
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO restaurant_members (restaurant_id,bo_user_id,first_name,last_name,is_active) VALUES (?,?,?,?,1)`, restaurantID, userID, first, last)
	if err != nil {
		// Lost a race with another request: fall back to the row that won.
		return s.lookupPOSPinMember(r, restaurantID, userID)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// splitPOSPinName turns a backoffice account name into the first/last pair the
// members table wants, falling back to something readable rather than empty so
// an approved action never shows a blank name in the audit trail.
func splitPOSPinName(full string) (string, string) {
	full = strings.TrimSpace(full)
	if full == "" {
		return "POS", "user"
	}
	if first, last, ok := strings.Cut(full, " "); ok && strings.TrimSpace(last) != "" {
		return strings.TrimSpace(first), strings.TrimSpace(last)
	}
	return full, "POS"
}

// verifyPOSApprovalPIN resolves an approval PIN to the manager's name. Shared by
// the standalone /pin/verify endpoint and by the line void, so both paths are
// checked by exactly the same rule.
func (s *Server) verifyPOSApprovalPIN(r *http.Request, restaurantID int, pin string) (string, error) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,pos_pin_hash FROM restaurant_members WHERE restaurant_id=? AND is_active=1 AND pos_pin_hash IS NOT NULL`, restaurantID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var hashed string
		if err = rows.Scan(&id, &hashed); err != nil {
			return "", err
		}
		if bcrypt.CompareHashAndPassword([]byte(hashed), []byte(pin)) == nil {
			return s.posPINMemberName(r, restaurantID, id)
		}
	}
	return "", sql.ErrNoRows
}

// posPINMemberName is the name shown next to an approved action.
func (s *Server) posPINMemberName(r *http.Request, restaurantID int, memberID int64) (string, error) {
	var name string
	err := s.db.QueryRowContext(r.Context(), `SELECT TRIM(CONCAT(first_name,' ',last_name)) FROM restaurant_members WHERE restaurant_id=? AND id=?`, restaurantID, memberID).Scan(&name)
	return name, err
}
