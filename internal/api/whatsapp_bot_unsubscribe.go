package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
)

// Coordination id: wa_bot_unsubscribe_v1
//
// unsubscribe_marketing lets the WhatsApp bot put the customer who writes it on
// the campaign do-not-contact list (campaign_suppressions, the same table the
// public /baja-publicidad page writes and every campaign send re-checks right
// before each delivery). The target is always the phone that is writing; the
// model cannot pass another number.

func botToolDefUnsubscribe() botToolDef {
	return botToolDef{
		Name:        "unsubscribe_marketing",
		Description: "Da de baja al cliente que escribe de TODAS las campañas y publicidad del restaurante (WhatsApp y email de sus reservas: la baja siempre cubre ambos canales). ÚSALA cuando el cliente diga claramente que no quiere recibir más mensajes, promociones o campañas (por ejemplo: \"no quiero recibir más mensajes\", \"darme de baja\", \"quitadme de la lista\", \"stop\"). No la uses para cancelar una reserva ni si solo se queja de un mensaje concreto sin pedir la baja. Si la intención es ambigua, pregunta antes y usa confirmed=true solo cuando el cliente lo haya dejado claro. Después confirma al cliente con send_message que ya no recibirá más campañas (las confirmaciones y recordatorios de sus reservas se siguen enviando).",
		InputSchema: botSchema(`{"type":"object","properties":{"confirmed":{"type":"boolean","description":"DEBE ser true: el cliente ha pedido claramente la baja"}},"required":["confirmed"]}`),
	}
}

func (s *Server) botToolUnsubscribeMarketing(ctx context.Context, restaurantID int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	var in struct {
		Channel   string `json:"channel"`
		Confirmed bool   `json:"confirmed"`
	}
	_ = json.Unmarshal(input, &in)
	if !in.Confirmed {
		return botJSON(map[string]any{"error": "requiere confirmed=true cuando el cliente pide claramente la baja"}), nil
	}
	// Product rule: a manual baja silences the whole contact (WhatsApp and
	// e-mail), whatever the model passed as channel.
	channel := "all"
	phone := normalizeWhatsAppNumber(msg.Sender)
	if phone == "" {
		return botJSON(map[string]any{"error": "no se pudo identificar el teléfono del cliente"}), nil
	}

	// Newest booking of this phone: traceability anchor and source of the
	// email(s) to suppress. Same phone matching as get_bookings.
	national, digits := botPhoneVariants(msg.Sender)
	var bookingID int64
	emails := []string{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, LOWER(TRIM(COALESCE(contact_email, '')))
		FROM bookings
		WHERE restaurant_id = ?
			AND (contact_phone = ? OR contact_phone = ? OR CONCAT(COALESCE(contact_phone_country_code,''), contact_phone) = ?)
		ORDER BY id DESC LIMIT 20
	`, restaurantID, national, digits, digits)
	if err == nil {
		seen := map[string]bool{}
		for rows.Next() {
			var id int64
			var mail string
			if rows.Scan(&id, &mail) != nil {
				continue
			}
			if bookingID == 0 {
				bookingID = id
			}
			if strings.Contains(mail, "@") && len(mail) <= 190 && !seen[mail] {
				seen[mail] = true
				emails = append(emails, mail)
			}
		}
		rows.Close()
	}

	type pair struct{ channel, target string }
	pairs := []pair{}
	if channel == "whatsapp" || channel == "all" {
		pairs = append(pairs, pair{"whatsapp", phone})
	}
	if channel == "email" || channel == "all" {
		for _, m := range emails {
			pairs = append(pairs, pair{"email", m})
		}
	}
	stored := 0
	for _, p := range pairs {
		if _, err := s.db.ExecContext(ctx, `
			INSERT IGNORE INTO campaign_suppressions (restaurant_id, channel, target, booking_id, reason, source)
			VALUES (?, ?, ?, NULLIF(?, 0), 'whatsapp_bot', 'unsubscribe')
		`, restaurantID, p.channel, p.target, bookingID); err != nil {
			slog.Default().Warn("wa_bot.unsubscribe.insert_failed", "coord_id", "wa_bot_unsubscribe_v1",
				"restaurant_id", restaurantID, "channel", p.channel, "err", err.Error())
			continue
		}
		stored++
	}
	slog.Default().Info("wa_bot.unsubscribe.done", "coord_id", "wa_bot_unsubscribe_v1",
		"restaurant_id", restaurantID, "channel", channel, "booking_id", bookingID, "stored", stored)
	if stored == 0 {
		return botJSON(map[string]any{"error": "no se pudo registrar la baja; avisa al cliente de que lo gestionará el equipo y usa send_contact"}), nil
	}
	return botJSON(map[string]any{"success": true, "unsubscribed": true, "channel": channel,
		"note": "Baja registrada. Confirma al cliente con send_message; las confirmaciones y recordatorios de sus reservas se siguen enviando."}), nil
}
