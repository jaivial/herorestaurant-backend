package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"preactvillacarmen/internal/httpx"
)

// Coordination id: hour_split_realtime_v1
//
// REST streaming (Server-Sent Events) for the "reparto por hora" occupancy of
// one date. The backoffice booking editor (create + edit) and the reservas day
// page subscribe to GET /admin/config/hour-split/stream?date= and re-render on
// every snapshot. A snapshot is pushed on connect, right after any booking or
// hour-split change of the restaurant (in-process notify), and by a safety
// poll that only emits when the data changed (covers writers that do not
// notify). A comment heartbeat keeps proxies from closing idle streams.

const (
	hourSplitStreamPoll      = 20 * time.Second
	hourSplitStreamHeartbeat = 15 * time.Second
	hourSplitStreamDebounce  = 250 * time.Millisecond
)

// hourSplitNotifyHub fans "something changed" pings out to the open streams of
// a restaurant. Pings are coalesced: a slow stream never blocks a writer.
type hourSplitNotifyHub struct {
	mu   sync.Mutex
	subs map[int]map[chan struct{}]struct{}
}

var hourSplitHub = &hourSplitNotifyHub{subs: map[int]map[chan struct{}]struct{}{}}

func (h *hourSplitNotifyHub) subscribe(restaurantID int) (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[restaurantID] == nil {
		h.subs[restaurantID] = map[chan struct{}]struct{}{}
	}
	h.subs[restaurantID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[restaurantID], ch)
		if len(h.subs[restaurantID]) == 0 {
			delete(h.subs, restaurantID)
		}
		h.mu.Unlock()
	}
}

func (h *hourSplitNotifyHub) notify(restaurantID int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[restaurantID] {
		select {
		case ch <- struct{}{}:
		default: // a ping is already pending for this stream
		}
	}
}

// notifyHourSplitChanged is called by every booking / hour-split writer.
func (s *Server) notifyHourSplitChanged(restaurantID int) {
	if restaurantID > 0 {
		invalidateCalendarCache(restaurantID)
		hourSplitHub.notify(restaurantID)
	}
}

// invalidateCalendarCache drops the cached month calendars (backoffice and
// public share the "rid:year-month" keys) so the live refetch that follows a
// booking write reads fresh day counters. Coordination id: bookings_realtime_v1
func invalidateCalendarCache(restaurantID int) {
	prefix := strconv.Itoa(restaurantID) + ":"
	calendarCacheMu.Lock()
	for k := range calendarCache {
		if strings.HasPrefix(k, prefix) {
			delete(calendarCache, k)
		}
	}
	calendarCacheMu.Unlock()
}

// hourSplitSnapshot is the payload shared by the GET and the stream.
func (s *Server) hourSplitSnapshot(ctx context.Context, r *http.Request, restaurantID int, date string) (map[string]any, error) {
	enabled, source, err := s.resolveHourSplitEnabled(ctx, restaurantID, date)
	if err != nil {
		return nil, fmt.Errorf("hour-split: %w", err)
	}
	activeHours, err := s.getOpeningHoursForDate(r, date)
	if err != nil {
		return nil, fmt.Errorf("horarios: %w", err)
	}
	sortServiceHours(activeHours)
	percentages, _, err := s.loadHourPercentages(ctx, restaurantID, date, activeHours)
	if err != nil {
		return nil, fmt.Errorf("porcentajes: %w", err)
	}
	dailyLimit := s.dailyLimitFor(r, restaurantID, date)
	bookingsByHour, totalPeople, err := s.fetchBookingsByHourHHMM(r, date)
	if err != nil {
		return nil, fmt.Errorf("reservas: %w", err)
	}
	return map[string]any{
		"success":          true,
		"date":             date,
		"enabled":          enabled,
		"source":           source,
		"dailyLimit":       dailyLimit,
		"totalPeople":      totalPeople,
		"activeHours":      activeHours,
		"percentages":      percentages,
		"hourlyCapacities": percentagesToPeople(percentages, dailyLimit),
		"bookingsByHour":   bookingsByHour,
	}, nil
}

// handleBOConfigHourSplitStream: GET /api/admin/config/hour-split/stream?date=YYYY-MM-DD
func (s *Server) handleBOConfigHourSplitStream(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" || !isValidISODate(date) {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid date")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}
	restaurantID := a.ActiveRestaurantID
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ping, unsubscribe := hourSplitHub.subscribe(restaurantID)
	defer unsubscribe()
	log.Printf("[hour_split_realtime_v1] stream open restaurant=%d date=%s", restaurantID, date)
	defer log.Printf("[hour_split_realtime_v1] stream closed restaurant=%d date=%s", restaurantID, date)

	ctx := r.Context()
	last := ""
	send := func(force bool) bool {
		snap, err := s.hourSplitSnapshot(ctx, r, restaurantID, date)
		if err != nil {
			if ctx.Err() != nil {
				return false
			}
			log.Printf("[hour_split_realtime_v1] snapshot failed restaurant=%d date=%s err=%v", restaurantID, date, err)
			_, werr := fmt.Fprintf(w, "event: error\ndata: {\"success\":false}\n\n")
			flusher.Flush()
			return werr == nil
		}
		body, _ := json.Marshal(snap)
		if !force && string(body) == last {
			return true
		}
		last = string(body)
		if _, err := fmt.Fprintf(w, "event: hour_split\ndata: %s\n\n", body); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if _, err := fmt.Fprintf(w, "retry: 3000\n\n"); err != nil {
		return
	}
	if !send(true) {
		return
	}
	poll := time.NewTicker(hourSplitStreamPoll)
	defer poll.Stop()
	beat := time.NewTicker(hourSplitStreamHeartbeat)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping:
			// Coalesce bursts (a booking save often fires several writers).
			select {
			case <-ctx.Done():
				return
			case <-time.After(hourSplitStreamDebounce):
			}
			if !send(false) {
				return
			}
		case <-poll.C:
			if !send(false) {
				return
			}
		case <-beat.C:
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
