package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: bookings_realtime_v1
//
// REST streaming (SSE) "bookings changed" signal for a date range, used by the
// reservas page (calendar day counters, occupancy donut, list) and the booking
// editor calendar. Each event tells the page to refetch its own REST views,
// so every counter keeps a single source of truth. Events fire on any booking
// / daily-limit / hour-split write (shared in-process hub) and on a 20 s poll
// of a cheap range signature that only emits when it changed.

func (s *Server) bookingsRangeSignature(r *http.Request, restaurantID int, from, to string) string {
	var n, pax, maxID, limits, limitRows int64
	_ = s.db.QueryRowContext(r.Context(), portableSQL(`
		SELECT COUNT(*), COALESCE(SUM(party_size), 0), COALESCE(MAX(id), 0)
		FROM bookings WHERE restaurant_id = ? AND reservation_date BETWEEN ? AND ?
	`), restaurantID, from, to).Scan(&n, &pax, &maxID)
	_ = s.db.QueryRowContext(r.Context(), nativeSQL(`
		SELECT COALESCE(SUM(dailyLimit), 0), COUNT(*) FROM reservation_manager
		WHERE restaurant_id = ? AND reservationDate BETWEEN ? AND ?
	`, `
		SELECT COALESCE(SUM("dailyLimit"), 0), COUNT(*) FROM reservation_manager
		WHERE restaurant_id = ? AND "reservationDate" BETWEEN ? AND ?
	`), restaurantID, from, to).Scan(&limits, &limitRows)
	return fmt.Sprintf("%d:%d:%d:%d:%d", n, pax, maxID, limits, limitRows)
}

// handleBOBookingsStream: GET /api/admin/bookings/stream?from=YYYY-MM-DD&to=YYYY-MM-DD
func (s *Server) handleBOBookingsStream(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))
	if !isValidISODate(from) || !isValidISODate(to) || to < from {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid range")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}
	rid := a.ActiveRestaurantID
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ping, unsubscribe := hourSplitHub.subscribe(rid)
	defer unsubscribe()
	log.Printf("[bookings_realtime_v1] stream open restaurant=%d %s..%s", rid, from, to)
	defer log.Printf("[bookings_realtime_v1] stream closed restaurant=%d %s..%s", rid, from, to)

	ctx := r.Context()
	last := s.bookingsRangeSignature(r, rid, from, to)
	emit := func(reason string) bool {
		if _, err := fmt.Fprintf(w, "event: bookings_changed\ndata: {\"from\":%q,\"to\":%q,\"reason\":%q}\n\n", from, to, reason); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if _, err := fmt.Fprintf(w, "retry: 3000\n\nevent: ready\ndata: {}\n\n"); err != nil {
		return
	}
	flusher.Flush()
	poll := time.NewTicker(hourSplitStreamPoll)
	defer poll.Stop()
	beat := time.NewTicker(hourSplitStreamHeartbeat)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping:
			select {
			case <-ctx.Done():
				return
			case <-time.After(hourSplitStreamDebounce):
			}
			last = s.bookingsRangeSignature(r, rid, from, to)
			if !emit("write") {
				return
			}
		case <-poll.C:
			if sig := s.bookingsRangeSignature(r, rid, from, to); sig != last {
				last = sig
				if !emit("poll") {
					return
				}
			}
		case <-beat.C:
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
