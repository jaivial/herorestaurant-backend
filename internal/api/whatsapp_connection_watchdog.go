package api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Coordination id: wa_connection_watchdog_v1.
//
// The Evolution fork now reconnects by itself (wa_reconnect_supervisor_v1),
// but a real WhatsApp logout (401: device unlinked from the phone, session
// revoked) can only be fixed by re-linking. On 2026-09-22 that state went
// unnoticed for ~19h while bookings kept arriving. The watchdog reads the
// provider status (a READ: it never calls connect) and emails the restaurant
// once per outage when a previously linked number stays down.
const (
	whatsappWatchdogEvery    = 2 * time.Minute
	whatsappWatchdogAlertAge = 10 * time.Minute
)

type whatsappWatchdogState struct {
	downSince time.Time
	alerted   bool
}

var (
	whatsappWatchdogMu    sync.Mutex
	whatsappWatchdogByRID = map[int]*whatsappWatchdogState{}
)

func (s *Server) runWhatsAppWatchdogLoop(ctx context.Context) {
	ticker := time.NewTicker(whatsappWatchdogEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runWhatsAppWatchdogOnce(ctx)
		}
	}
}

func (s *Server) runWhatsAppWatchdogOnce(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT restaurant_id FROM restaurant_uazapi_instances
		WHERE is_active = 1 AND connected_at IS NOT NULL
	`)
	if err != nil {
		if !isSQLSchemaError(err) {
			log.Printf("[whatsapp][watchdog] list instances failed: %v", err)
		}
		return
	}
	ids := []int{}
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, restaurantID := range ids {
		s.checkWhatsAppConnection(ctx, restaurantID)
	}
}

func (s *Server) checkWhatsAppConnection(ctx context.Context, restaurantID int) {
	rec, found, err := s.loadRestaurantUAZAPIInstance(ctx, restaurantID)
	if err != nil || !found || !rec.IsActive || rec.Status == "suspended" {
		return
	}
	status := normalizeUAZAPIConnectionStatus(rec.Status)
	if st, err := s.gatewayForInstance(rec).Status(ctx); err == nil && st.Status != "" {
		status = st.Status
	}

	whatsappWatchdogMu.Lock()
	state := whatsappWatchdogByRID[restaurantID]
	if state == nil {
		state = &whatsappWatchdogState{}
		whatsappWatchdogByRID[restaurantID] = state
	}
	if isUAZAPIConnected(status) {
		if !state.downSince.IsZero() {
			log.Printf("[whatsapp][watchdog][CP-WA-RECOVERED] restaurant=%d down_for=%s", restaurantID, time.Since(state.downSince).Round(time.Second))
		}
		*state = whatsappWatchdogState{}
		whatsappWatchdogMu.Unlock()
		return
	}
	if state.downSince.IsZero() {
		state.downSince = time.Now()
	}
	downFor := time.Since(state.downSince)
	shouldAlert := !state.alerted && downFor >= whatsappWatchdogAlertAge
	if shouldAlert {
		state.alerted = true
	}
	whatsappWatchdogMu.Unlock()

	log.Printf("[whatsapp][watchdog][CP-WA-DOWN] restaurant=%d status=%s down_for=%s", restaurantID, status, downFor.Round(time.Second))
	if shouldAlert {
		s.alertWhatsAppDown(ctx, restaurantID, status, downFor)
	}
}

func (s *Server) alertWhatsAppDown(ctx context.Context, restaurantID int, status string, downFor time.Duration) {
	var email string
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(email, '') FROM restaurant_info WHERE restaurant_id = ?`, restaurantID).Scan(&email)
	email = strings.TrimSpace(email)
	if email == "" {
		log.Printf("[whatsapp][watchdog][CP-WA-ALERT-SKIPPED] restaurant=%d no restaurant email", restaurantID)
		return
	}
	pending := 0
	_ = s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM message_deliveries
		WHERE restaurant_id = ? AND channel = 'whatsapp' AND status = 'pending'
	`, restaurantID).Scan(&pending)

	body := fmt.Sprintf(`<p>El WhatsApp del restaurante lleva <strong>%d minutos</strong> desconectado (estado: %s).</p>
<p>Las confirmaciones y recordatorios de reservas se guardan en cola (%d pendientes) y se enviarán solos en cuanto vuelva a conectarse.</p>
<p>Para reconectarlo: Backoffice → Configuración → WhatsApp → vincular de nuevo (QR o código con número).</p>`,
		int(downFor.Minutes()), status, pending)
	if err := s.sendBackofficeAppEmail(ctx, restaurantID, email, "WhatsApp desconectado", body); err != nil {
		log.Printf("[whatsapp][watchdog][CP-WA-ALERT-FAILED] restaurant=%d %v", restaurantID, err)
		return
	}
	log.Printf("[whatsapp][watchdog][CP-WA-ALERT-SENT] restaurant=%d to=%s pending=%d", restaurantID, email, pending)
}
