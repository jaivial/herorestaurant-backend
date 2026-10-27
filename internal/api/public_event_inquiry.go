package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

const (
	eventInquiryMaxBodyBytes = 32 << 10
	eventInquiryMaxGuests    = 500
	eventInquiryRateBurst    = 5
)

var eventInquiryInternalWhatsApp = []string{"34692747052", "34686969914", "34638857294"}

type eventInquiryRequest struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	EmailRepeat string `json:"emailRepeat"`
	CountryCode string `json:"countryCode"`
	Phone       string `json:"phone"`
	Adults      int    `json:"adults"`
	Children    int    `json:"children"`
	EventType   string `json:"eventType"`
	Date        string `json:"date"`
	Notes       string `json:"notes"`
	Language    string `json:"language"`
	Website     string `json:"website"` // honeypot; never rendered or forwarded
}

type normalizedEventInquiry struct {
	eventInquiryRequest
	PhoneE164 string
	EventName string
}

type eventInquiryDelivery struct {
	CustomerEmail      bool `json:"customerEmail"`
	RestaurantEmail    bool `json:"restaurantEmail"`
	CustomerWhatsApp   bool `json:"customerWhatsApp"`
	RestaurantWhatsApp int  `json:"restaurantWhatsApp"`
}

func eventInquiryText(lang, es, en string) string {
	if lang == "en" {
		return en
	}
	return es
}

func validateEventInquiry(in eventInquiryRequest, now time.Time) (normalizedEventInquiry, string) {
	out := normalizedEventInquiry{eventInquiryRequest: in}
	out.Name = strings.TrimSpace(in.Name)
	out.Email = strings.TrimSpace(in.Email)
	out.EmailRepeat = strings.TrimSpace(in.EmailRepeat)
	out.Notes = strings.TrimSpace(in.Notes)
	out.Language = strings.ToLower(strings.TrimSpace(in.Language))
	if out.Language != "en" {
		out.Language = "es"
	}
	if len([]rune(out.Name)) < 2 || len([]rune(out.Name)) > 120 {
		return out, eventInquiryText(out.Language, "Escribe tu nombre y apellidos.", "Enter your full name.")
	}
	address, err := mail.ParseAddress(out.Email)
	if err != nil || !strings.EqualFold(address.Address, out.Email) || len(out.Email) > 254 {
		return out, eventInquiryText(out.Language, "Escribe un email válido.", "Enter a valid email address.")
	}
	if !strings.EqualFold(out.Email, out.EmailRepeat) {
		return out, eventInquiryText(out.Language, "Los dos emails no coinciden.", "The two email addresses do not match.")
	}
	cc, national, e164, ok := normalizePhoneParts(in.CountryCode, in.Phone)
	if !ok {
		return out, eventInquiryText(out.Language, "Escribe un teléfono válido con su prefijo.", "Enter a valid phone number and country code.")
	}
	out.CountryCode, out.Phone, out.PhoneE164 = cc, national, e164
	if in.Adults < 1 || in.Adults > eventInquiryMaxGuests || in.Children < 0 || in.Children > eventInquiryMaxGuests || in.Adults+in.Children > eventInquiryMaxGuests {
		return out, eventInquiryText(out.Language, "Indica entre 1 y 500 personas en total; los niños no pueden ser negativos.", "Enter between 1 and 500 guests in total; children cannot be negative.")
	}
	eventNames := map[string][2]string{
		"wedding": {"Boda", "Wedding"}, "communion": {"Comunión", "Communion"},
		"birthday": {"Cumpleaños", "Birthday"}, "company": {"Comida de empresa", "Company meal"},
		"baptism": {"Bautizo", "Baptism"},
	}
	names, ok := eventNames[in.EventType]
	if !ok {
		return out, eventInquiryText(out.Language, "Selecciona el tipo de evento.", "Select the event type.")
	}
	if out.Language == "en" {
		out.EventName = names[1]
	} else {
		out.EventName = names[0]
	}
	date, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		return out, eventInquiryText(out.Language, "Selecciona una fecha válida.", "Select a valid date.")
	}
	loc, locErr := time.LoadLocation("Europe/Madrid")
	if locErr != nil {
		loc = time.UTC
	}
	today := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, time.UTC)
	if date.Before(today) || date.After(today.AddDate(2, 0, 0)) {
		return out, eventInquiryText(out.Language, "La fecha debe estar entre hoy y los próximos dos años.", "Choose a date between today and the next two years.")
	}
	if len([]rune(out.Notes)) > 2000 {
		return out, eventInquiryText(out.Language, "Los detalles no pueden superar los 2000 caracteres.", "Details cannot exceed 2000 characters.")
	}
	return out, ""
}

func eventInquiryDisclaimer(lang string) string {
	return eventInquiryText(lang, "Esto es solo una solicitud de información y NO una reserva.", "This is an information request only and NOT a reservation.")
}

func buildEventInquiryEmail(brandName, logoURL string, in normalizedEventInquiry, internal bool, contact restaurantBrandingCfg) string {
	brandName = htmlEscape(brandName)
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body style="margin:0;background:#f5f3ef;font-family:Arial,sans-serif;color:#292825"><div style="max-width:640px;margin:0 auto;padding:28px 16px"><div style="background:#fff;border:1px solid #e7e1d8;border-radius:18px;overflow:hidden">`)
	if logoURL != "" {
		fmt.Fprintf(&b, `<div style="padding:24px 28px;text-align:center"><img src="%s" alt="%s" style="max-width:190px;max-height:70px"></div>`, htmlEscape(logoURL), brandName)
	}
	fmt.Fprintf(&b, `<div style="padding:28px"><div style="font-size:12px;letter-spacing:2px;text-transform:uppercase;color:#8c6a3f">%s</div>`, brandName)
	if internal {
		fmt.Fprintf(&b, `<h1 style="font-size:25px;line-height:1.25">%s</h1>`, eventInquiryText(in.Language, "Nueva solicitud de información de evento", "New event information request"))
	} else {
		fmt.Fprintf(&b, `<h1 style="font-size:25px;line-height:1.25">%s</h1><p>%s</p>`, eventInquiryText(in.Language, "Hemos recibido tu consulta", "We have received your enquiry"), eventInquiryText(in.Language, "Gracias por escribirnos. Nuestro equipo se pondrá en contacto contigo en breve para responder a tu petición de información.", "Thank you for contacting us. Our team will contact you shortly to answer your information request."))
	}
	fmt.Fprintf(&b, `<p style="padding:12px;background:#fff6e5;border-left:3px solid #b68a4a"><strong>%s</strong></p>`, eventInquiryDisclaimer(in.Language))
	rows := [][2]string{{eventInquiryText(in.Language, "Nombre", "Name"), in.Name}, {"Email", in.Email}, {eventInquiryText(in.Language, "Teléfono", "Phone"), "+" + in.PhoneE164}, {eventInquiryText(in.Language, "Tipo de evento", "Event type"), in.EventName}, {eventInquiryText(in.Language, "Fecha orientativa", "Preferred date"), in.Date}, {eventInquiryText(in.Language, "Adultos", "Adults"), fmt.Sprint(in.Adults)}, {eventInquiryText(in.Language, "Niños (menores de 12 años)", "Children (under 12)"), fmt.Sprint(in.Children)}}
	b.WriteString(`<table role="presentation" style="width:100%;border-collapse:collapse;font-size:14px">`)
	for _, row := range rows {
		fmt.Fprintf(&b, `<tr><td style="padding:9px 0;border-bottom:1px solid #eee;color:#716b62">%s</td><td style="padding:9px 0;border-bottom:1px solid #eee;text-align:right;font-weight:600">%s</td></tr>`, htmlEscape(row[0]), htmlEscape(row[1]))
	}
	b.WriteString(`</table>`)
	if internal || in.Notes != "" {
		fmt.Fprintf(&b, `<h2 style="font-size:16px;margin-top:24px">%s</h2><p style="white-space:pre-wrap">%s</p>`, eventInquiryText(in.Language, "Alergias, intolerancias y detalles", "Allergies, intolerances and details"), htmlEscape(in.Notes))
	}
	fmt.Fprintf(&b, `<p style="margin-top:24px">%s</p></div>`, eventInquiryDisclaimer(in.Language))
	if contact.Address != "" || contact.Phone != "" || contact.Email != "" {
		fmt.Fprintf(&b, `<div style="padding:18px 28px;background:#faf8f4;color:#716b62;font-size:12px">%s<br>%s<br>%s</div>`, htmlEscape(contact.Address), htmlEscape(contact.Phone), htmlEscape(contact.Email))
	}
	b.WriteString(`</div></div></body></html>`)
	return b.String()
}

func eventInquiryWhatsApp(in normalizedEventInquiry, internal bool) string {
	if !internal {
		return fmt.Sprintf("%s\n\n%s\n%s: %s · %s: %s\n\n%s", eventInquiryDisclaimer(in.Language), eventInquiryText(in.Language, "Hemos recibido tu consulta. El equipo se pondrá en contacto contigo en breve.", "We have received your enquiry. Our team will contact you shortly."), eventInquiryText(in.Language, "Evento", "Event"), in.EventName, eventInquiryText(in.Language, "Fecha", "Date"), in.Date, eventInquiryDisclaimer(in.Language))
	}
	lines := []string{
		eventInquiryDisclaimer(in.Language), "",
		eventInquiryText(in.Language, "Nueva solicitud de evento", "New event enquiry"),
		eventInquiryText(in.Language, "Nombre", "Name") + ": " + in.Name,
		"Email: " + in.Email,
		eventInquiryText(in.Language, "Teléfono", "Phone") + ": +" + in.PhoneE164,
		eventInquiryText(in.Language, "Evento", "Event") + ": " + in.EventName,
		eventInquiryText(in.Language, "Fecha", "Date") + ": " + in.Date,
		fmt.Sprintf("%s: %d", eventInquiryText(in.Language, "Adultos", "Adults"), in.Adults),
		fmt.Sprintf("%s: %d", eventInquiryText(in.Language, "Niños", "Children"), in.Children),
		eventInquiryText(in.Language, "Detalles", "Details") + ": " + in.Notes,
		"", eventInquiryDisclaimer(in.Language),
	}
	return strings.Join(lines, "\n")
}

// Indirection keeps all provider calls replaceable by sandbox tests.
var eventInquiryEmailConfig = resolveSMTPConfigForRestaurant
var eventInquiryBranding = func(ctx context.Context, s *Server, id int) (restaurantBrandingCfg, error) {
	return s.loadRestaurantBranding(ctx, id)
}
var eventInquirySendEmail = sendViaConfig
var eventInquiryGateway = func(ctx context.Context, s *Server, id int) (WhatsAppGateway, bool) { return s.botGatewayFor(ctx, id) }
var eventInquirySendWhatsApp = func(ctx context.Context, s *Server, id int, gw WhatsAppGateway, to, text string) error {
	return s.sendWhatsAppTextTracked(ctx, id, gw, to, text, "event_inquiry")
}

func deliverEventInquiry(ctx context.Context, s *Server, restaurantID int, in normalizedEventInquiry) eventInquiryDelivery {
	var result eventInquiryDelivery
	branding, _ := eventInquiryBranding(ctx, s, restaurantID)
	cfg, err := eventInquiryEmailConfig(ctx, s, restaurantID)
	if err == nil && cfg.ID != 0 && cfg.IsActive {
		fromName := branding.EmailFromName
		if fromName == "" {
			fromName = branding.BrandName
		}
		if fromName == "" {
			fromName = "Villa Carmen"
		}
		fromAddr := branding.EmailFromAddress
		if fromAddr == "" {
			if strings.EqualFold(cfg.Provider, "gmail") {
				fromAddr = cfg.GmailFromEmail
			} else {
				fromAddr = cfg.SMTPFromEmail
			}
		}
		internalAddr := strings.TrimSpace(branding.Email)
		if internalAddr == "" {
			internalAddr = fromAddr
		}
		brandName := branding.BrandName
		if brandName == "" {
			brandName = "Villa Carmen"
		}
		logoURL := branding.LogoURL
		if logoURL == "" && s.db != nil {
			logoURL = publicBaseURLFromContext(ctx, s, restaurantID) + "/media/logos/logo-negro.png"
		}
		if fromAddr != "" && internalAddr != "" {
			if err := eventInquirySendEmail(ctx, cfg, fromName, fromAddr, in.Email, eventInquiryText(in.Language, "Hemos recibido tu solicitud de información - ", "We have received your information request - ")+brandName, buildEventInquiryEmail(brandName, logoURL, in, false, branding)); err != nil {
				log.Printf("[event-inquiry] customer email restaurant_id=%d: %v", restaurantID, err)
			} else {
				result.CustomerEmail = true
			}
			if err := eventInquirySendEmail(ctx, cfg, fromName, fromAddr, internalAddr, eventInquiryText(in.Language, "Nueva consulta de evento - ", "New event enquiry - ")+brandName, buildEventInquiryEmail(brandName, logoURL, in, true, branding)); err != nil {
				log.Printf("[event-inquiry] internal email restaurant_id=%d: %v", restaurantID, err)
			} else {
				result.RestaurantEmail = true
			}
		}
	} else if err != nil {
		log.Printf("[event-inquiry] load email config restaurant_id=%d: %v", restaurantID, err)
	}
	gw, ok := eventInquiryGateway(ctx, s, restaurantID)
	if ok && gw != nil {
		if err := eventInquirySendWhatsApp(ctx, s, restaurantID, gw, in.PhoneE164, eventInquiryWhatsApp(in, false)); err != nil {
			log.Printf("[event-inquiry] customer WhatsApp restaurant_id=%d: %v", restaurantID, err)
		} else {
			result.CustomerWhatsApp = true
		}
		for _, to := range eventInquiryInternalWhatsApp {
			if err := eventInquirySendWhatsApp(ctx, s, restaurantID, gw, to, eventInquiryWhatsApp(in, true)); err != nil {
				log.Printf("[event-inquiry] internal WhatsApp restaurant_id=%d: %v", restaurantID, err)
			} else {
				result.RestaurantWhatsApp++
			}
		}
	}
	return result
}

func (s *Server) handlePublicEventInquiry(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := s.resolveRestaurantID(r)
	if !ok {
		httpxWriteError(w, http.StatusBadRequest, "restaurant not resolved")
		return
	}
	if !s.checkScopedRateLimit("event-inquiry", httpx.ClientIP(r), restaurantID, eventInquiryRateBurst) {
		httpxWriteError(w, http.StatusTooManyRequests, "Demasiadas solicitudes. Inténtalo de nuevo en unos minutos.")
		return
	}
	var in eventInquiryRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, eventInquiryMaxBodyBytes))
	if err := dec.Decode(&in); err != nil {
		httpxWriteError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if strings.TrimSpace(in.Website) != "" {
		writeJSON(w, map[string]any{"success": true, "notifications": eventInquiryDelivery{}})
		return
	}
	normalized, validationMessage := validateEventInquiry(in, time.Now())
	if validationMessage != "" {
		httpxWriteError(w, http.StatusUnprocessableEntity, validationMessage)
		return
	}
	delivery := deliverEventInquiry(r.Context(), s, restaurantID, normalized)
	if !delivery.RestaurantEmail && delivery.RestaurantWhatsApp == 0 {
		httpxWriteError(w, http.StatusServiceUnavailable, eventInquiryText(normalized.Language, "No hemos podido enviar tu consulta. Escríbenos por el canal de contacto habitual e inténtalo de nuevo más tarde.", "We could not send your enquiry. Please use our usual contact channel and try again later."))
		return
	}
	writeJSON(w, map[string]any{"success": true, "message": eventInquiryText(normalized.Language, "Hemos recibido tu solicitud de información. NO es una reserva. Nuestro equipo te responderá en breve.", "We have received your information request. It is NOT a reservation. Our team will reply shortly."), "notifications": delivery})
}
