package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// Coursing: a waiter rings several dishes, sends none of them, and fires the
// first course when the guests are ready. The kitchen only ever sees what has
// been fired, so an entrée rung at the start of service is not cooked twenty
// minutes early.
//
// The schema already had `pos_ticket_lines.course` and an unused FIRE action on
// the dispatch lines; this makes them reachable from the till.
const posDefaultCourse = "1"

// normalisePOSCourse keeps courses comparable. "2", " 2 " and "2.º" are the same
// course, and an empty course is the first one: a dish rung without choosing a
// course must not end up in a course nobody can fire.
func normalisePOSCourse(course string) string {
	lowered := strings.ToLower(strings.TrimSpace(course))
	lowered = strings.TrimPrefix(lowered, "curso ")
	lowered = strings.TrimPrefix(lowered, "course ")
	lowered = strings.NewReplacer("º", "", "ª", "", ".", "", " ", "").Replace(lowered)
	lowered = strings.TrimSpace(lowered)
	if lowered == "" {
		return posDefaultCourse
	}
	return lowered
}

// handleBOPOSCourseFire sends a single course to the kitchen.
//
// It reuses the normal dispatch path with a course filter rather than a second
// implementation of routing and delta calculation: what already sent counts as
// fired whatever course it was on, so firing a course twice sends nothing the
// second time instead of doubling the ticket.
func (s *Server) handleBOPOSCourseFire(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	// A closed cash day is a signed Z closure; mutating it afterwards would
	// invalidate an accounting document that has already been reported.
	if posWriteCashDayGuard(w, s.requireOpenCashDayForTicket(r.Context(), a.ActiveRestaurantID, ticketID)) {
		return
	}
	var in struct {
		Course string `json:"course"`
		// Optional. Left out, the key is derived from the course *and its current
		// contents*, so a double tap cannot fire twice but a dish added after the
		// first fire can still be sent — a fixed key would make that impossible.
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid course")
		return
	}
	course := normalisePOSCourse(in.Course)
	var ticketStatus string
	if err := s.db.QueryRowContext(r.Context(), `SELECT status FROM pos_tickets WHERE restaurant_id=? AND id=?`, a.ActiveRestaurantID, ticketID).Scan(&ticketStatus); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Ticket not found")
		return
	}
	if ticketStatus != "OPEN" {
		httpx.WriteError(w, http.StatusConflict, "Ticket is not open")
		return
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" {
		key = "course-fire:" + strconv.FormatInt(ticketID, 10) + ":" + course + ":" + s.posCourseFingerprint(r, a.ActiveRestaurantID, ticketID, course)
	}
	// The shared dispatch path is called directly rather than through the router:
	// rewriting r.Body would mutate the caller's request, and going back through
	// chi would re-run the cash-day guard this handler has already done.
	body, _ := json.Marshal(map[string]string{"course": course, "idempotencyKey": key})
	s.handleBOPOSKitchenDispatchCreate(w, r.WithContext(context.WithValue(r.Context(), posCourseFireBodyKey{}, body)))
}

// posCourseFingerprintBodyKey carries the synthesised dispatch body down to the
// dispatch handler without touching the inbound request.
type posCourseFireBodyKey struct{}

// posCourseFingerprint is a stable hash of what a course currently holds, so an
// identical double tap produces the same key (and is ignored) while a course
// that gained a dish produces a new one (and can be fired again).
func (s *Server) posCourseFingerprint(r *http.Request, restaurantID int, ticketID int64, course string) string {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,quantity FROM pos_ticket_lines WHERE restaurant_id=? AND ticket_id=? AND status='ACTIVE' AND COALESCE(NULLIF(course,''),'1')=? ORDER BY id`, restaurantID, ticketID, course)
	if err != nil {
		return "unknown"
	}
	defer rows.Close()
	parts := []string{}
	for rows.Next() {
		var id int64
		var qty string
		if err = rows.Scan(&id, &qty); err != nil {
			return "unknown"
		}
		parts = append(parts, strconv.FormatInt(id, 10)+":"+qty)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, ",")))
	return hex.EncodeToString(sum[:])[:16]
}

// handleBOPOSCourseList reports the courses on a ticket and which have been
// fired, so the waiter sees at a glance what the kitchen is still missing.
func (s *Server) handleBOPOSCourseList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	// The per-line sent quantity is aggregated in a subquery: nesting SUM() inside
	// another SUM() is not valid SQL, and a plain JOIN would also fan out the
	// line count once a dish has been sent to more than one station.
	rows, err := s.db.QueryContext(r.Context(), `SELECT COALESCE(NULLIF(l.course,''),'1') AS course,
		COUNT(*),
		COALESCE(SUM(CASE WHEN COALESCE(sent.qty,0) >= l.quantity THEN 1 ELSE 0 END),0)
		FROM pos_ticket_lines l
		LEFT JOIN (
			SELECT line_id AS ticket_line_id, SUM(q) AS qty FROM (
				SELECT dl.ticket_line_id AS line_id, dl.quantity_delta AS q
				FROM pos_kitchen_dispatch_lines dl
				JOIN pos_kitchen_dispatches d ON d.restaurant_id=dl.restaurant_id AND d.id=dl.dispatch_id
				WHERE dl.restaurant_id=? AND d.status<>'CANCELLED'
				UNION ALL SELECT to_line_id, quantity FROM pos_kitchen_line_transfers WHERE restaurant_id=?
				UNION ALL SELECT from_line_id, -quantity FROM pos_kitchen_line_transfers WHERE restaurant_id=?
			) moves GROUP BY line_id
		) sent ON sent.ticket_line_id = l.id
		WHERE l.restaurant_id=? AND l.ticket_id=? AND l.status='ACTIVE'
		GROUP BY course ORDER BY LENGTH(course),course`, a.ActiveRestaurantID, a.ActiveRestaurantID, a.ActiveRestaurantID, a.ActiveRestaurantID, ticketID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading courses")
		return
	}
	defer rows.Close()
	courses := []map[string]any{}
	for rows.Next() {
		var course string
		var total, fired int
		if err = rows.Scan(&course, &total, &fired); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading courses")
			return
		}
		courses = append(courses, map[string]any{"course": course, "lines": total, "firedLines": fired, "pendingLines": total - fired})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "courses": courses})
}
