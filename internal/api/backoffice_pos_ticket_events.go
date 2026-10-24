package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// posTicketEventRecorder captures the status of a ticket-mutating handler so the
// broadcast only fires on success, without touching each handler's body.
type posTicketEventRecorder struct {
	http.ResponseWriter
	status int
}

func (r *posTicketEventRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *posTicketEventRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// withPOSTicketEvent announces that a ticket (route param {id}) changed, so
// every other terminal showing the same table reloads it instead of editing a
// stale copy and getting STALE_TICKET. The event carries ids only: the client
// re-reads the ticket through the API it already uses.
// Coordination id: pos_ticket_updated_v1
func (s *Server) withPOSTicketEvent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &posTicketEventRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status < 200 || rec.status >= 300 {
			return
		}
		a, ok := boAuthFromContext(r.Context())
		if !ok {
			return
		}
		ticketID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if ticketID <= 0 {
			return
		}
		go s.broadcastPOSTicketUpdated(a.ActiveRestaurantID, ticketID, a.User.ID)
	})
}

func (s *Server) broadcastPOSTicketUpdated(restaurantID int, ticketID int64, actorUserID int) {
	if s == nil || s.tablesHub == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var visitID int64
	var version int
	_ = s.db.QueryRowContext(ctx, `SELECT visit_id,version FROM pos_tickets WHERE restaurant_id=? AND id=?`, restaurantID, ticketID).Scan(&visitID, &version)
	s.broadcastBOTablesEvent(restaurantID, "pos_ticket_updated", map[string]any{"ticketId": ticketID, "visitId": visitID, "version": version, "byUserId": actorUserID})
}
