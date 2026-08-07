package api

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// notifyScheduleChange delivers a best-effort schedule notification. It is
// deliberately asynchronous: a provider outage must never make a schedule
// mutation fail after its database commit.
func (s *Server) notifyScheduleChange(restaurantID, memberID int, schedule boFichajeSchedule, updated bool) {
	go s.sendScheduleChangeNotification(context.Background(), restaurantID, memberID, schedule, updated)
}

func scheduleChangeMessage(brand string, schedule boFichajeSchedule, updated bool) (string, string, string) {
	verb := "asignado"
	if updated {
		verb = "actualizado"
	}
	subject := fmt.Sprintf("%s · Horario %s", brand, verb)
	text := fmt.Sprintf("Hola, tu horario ha sido %s para el %s: %s - %s.", verb, schedule.Date, schedule.StartTime, schedule.EndTime)
	html := fmt.Sprintf("<p>Hola%s,</p><p>Tu horario ha sido <strong>%s</strong> para el <strong>%s</strong>: <strong>%s - %s</strong>.</p>", memberGreeting(schedule.MemberName), verb, schedule.Date, schedule.StartTime, schedule.EndTime)
	return subject, text, html
}

func memberGreeting(name string) string {
	if n := strings.TrimSpace(name); n != "" {
		return " " + n
	}
	return ""
}

func (s *Server) sendScheduleChangeNotification(ctx context.Context, restaurantID, memberID int, schedule boFichajeSchedule, updated bool) {
	var email, phone, whatsapp string
	var verified bool
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(email,''), COALESCE(phone,''), COALESCE(whatsapp_number,''), COALESCE(whatsapp_verified_at IS NOT NULL, 0) FROM restaurant_members WHERE restaurant_id=? AND id=? LIMIT 1`, restaurantID, memberID).Scan(&email, &phone, &whatsapp, &verified); err != nil {
		log.Printf("schedule notification contact lookup failed restaurant=%d member=%d: %v", restaurantID, memberID, err)
		return
	}
	brand := s.restaurantNameFallback(ctx, restaurantID)
	subject, text, html := scheduleChangeMessage(brand, schedule, updated)
	if strings.TrimSpace(email) != "" {
		if err := s.sendBackofficeAppEmail(ctx, restaurantID, email, subject, html); err != nil {
			log.Printf("schedule email failed member=%d: %v", memberID, err)
		}
	}
	// WhatsApp is an optional paid feature. A missing subscription/provider is
	// intentionally treated as a no-op, not as a mutation error.
	entitled, err := s.hasActiveRecurringFeature(ctx, restaurantID, boPremiumWhatsAppFeatureKey)
	if err != nil || !entitled {
		return
	}
	if !verified {
		return
	}
	if strings.TrimSpace(whatsapp) == "" {
		whatsapp = phone
	}
	if strings.TrimSpace(whatsapp) == "" {
		return
	}
	gateway, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return
	}
	if err := gateway.SendText(ctx, normalizeWhatsAppNumber(whatsapp), text); err != nil {
		log.Printf("schedule WhatsApp failed member=%d: %v", memberID, err)
	}
}
