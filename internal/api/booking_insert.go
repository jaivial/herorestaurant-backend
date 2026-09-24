package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"preactvillacarmen/internal/httpx"
)

// rateLimitState holds sliding-window counters per (IP + restaurantID).
type rateLimitState struct {
	tokens    int
	windowEnd int64 // Unix seconds at the end of the 60-second window
}

const (
	rateLimitWindowSecs = 60
	rateLimitMaxBurst   = 5
)

// rateLimit returns true if the request is allowed, false if rate-limited.
// It uses a simple token-bucket per (IP, restaurantID) pair.
// checkScopedRateLimit is a separate bucket (scope) with its own burst, e.g.
// the checkout success page polling, so it never eats booking submissions.
// Coordination id: stripe_connect_multitenant_v1
func (s *Server) checkScopedRateLimit(scope, ip string, restaurantID int, burst int) bool {
	if ip == "" {
		ip = "unknown"
	}
	key := scope + ":" + ip + ":" + strconv.Itoa(restaurantID)
	now := time.Now().Unix()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	entry, ok := s.rateLimit[key]
	if !ok || now >= entry.windowEnd {
		s.rateLimit[key] = &rateLimitState{windowEnd: now + rateLimitWindowSecs, tokens: burst - 1}
		return true
	}
	if entry.tokens <= 0 {
		return false
	}
	entry.tokens--
	return true
}

func (s *Server) checkRateLimit(ip string, restaurantID int) bool {
	// An empty/unknown IP must still be counted: it shares one "unknown"
	// bucket instead of skipping (or collapsing into an empty) key.
	if ip == "" {
		ip = "unknown"
	}
	key := ip + ":" + strconv.Itoa(restaurantID)
	now := time.Now().Unix()

	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	entry, ok := s.rateLimit[key]
	if !ok {
		entry = &rateLimitState{windowEnd: now + rateLimitWindowSecs, tokens: rateLimitMaxBurst - 1}
		s.rateLimit[key] = entry
		return true
	}

	// Advance the window if it has expired.
	if now >= entry.windowEnd {
		entry.windowEnd = now + rateLimitWindowSecs
		entry.tokens = rateLimitMaxBurst - 1
		return true
	}

	if entry.tokens <= 0 {
		return false
	}

	entry.tokens--
	return true
}

func (s *Server) handleInsertBookingFront(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	// IP-based rate limiting: 5 submissions per minute per (IP, restaurant).
	clientIP := httpx.ClientIP(r)
	if !s.checkRateLimit(clientIP, restaurantID) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, map[string]any{
			"success": false,
			"message": "Demasiadas solicitudes. Inténtalo de nuevo en un momento.",
		})
		return
	}

	if err := parseLegacyForm(r, 5<<20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Faltan campos requeridos",
		})
		return
	}

	// Honeypot (bot protection).
	if strings.TrimSpace(r.FormValue("website_url")) != "" {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]any{
			"success": false,
			"message": "Spam detected.",
		})
		return
	}
	// Minimum time-to-submit: 5 seconds (best-effort).
	if raw := strings.TrimSpace(r.FormValue("form_load_time")); raw != "" {
		if ts, err := strconv.ParseInt(raw, 10, 64); err == nil {
			if time.Now().Unix()-ts < 5 {
				httpx.WriteJSON(w, http.StatusForbidden, map[string]any{
					"success": false,
					"message": "Spam detected. Submission too fast.",
				})
				return
			}
		}
	}

	pb, err := s.prepareFrontBooking(r, restaurantID)
	if err != nil {
		var fe *frontBookingError
		if errors.As(err, &fe) {
			httpx.WriteJSON(w, fe.Status, fe.Body)
			return
		}
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": err.Error()})
		return
	}
	// Coordination id: stripe_prereserva_adelanto_v1 - a stripe-only
	// adelanto can only be booked through the checkout (paid first).
	if pb.requiresStripe {
		httpx.WriteJSON(w, http.StatusPaymentRequired, map[string]any{
			"success":    false,
			"message":    "Esta prereserva requiere el pago del adelanto con tarjeta",
			"error_code": "STRIPE_PAYMENT_REQUIRED",
		})
		return
	}
	_, status, resp := s.commitFrontBooking(r, pb, nil)
	httpx.WriteJSON(w, status, resp)
}

// frontBookingError carries the exact HTTP answer a failed validation gives,
// so the checkout path and the direct insert reply identically.
type frontBookingError struct {
	Status int
	Body   map[string]any
}

func (e *frontBookingError) Error() string { return anyToString(e.Body["message"]) }

// preparedFrontBooking is a fully validated public booking, ready to insert.
// Coordination id: stripe_prereserva_adelanto_v1 - shared by the direct
// insert and the Stripe checkout (which inserts only after payment).
type preparedFrontBooking struct {
	restaurantID    int
	params          bookingInsertParams
	toggleArroz     string
	phoneE164       string
	menuDeGrupoID   int
	specialSnapshot *specialBookingSnapshot
	requiresStripe  bool
}

// prepareFrontBooking validates a parsed public booking form. No writes.
func (s *Server) prepareFrontBooking(r *http.Request, restaurantID int) (*preparedFrontBooking, error) {
	resDate := strings.TrimSpace(r.FormValue("reservation_date"))
	partySize := clampInt(r.FormValue("party_size"), 1, 10_000, 0)
	resTimeRaw := strings.TrimSpace(r.FormValue("reservation_time"))
	customerName := strings.TrimSpace(r.FormValue("customer_name"))
	contactPhoneRaw := strings.TrimSpace(r.FormValue("contact_phone"))
	countryCodeRaw := strings.TrimSpace(r.FormValue("country_code"))

	cc, nationalPhone, phoneE164, ok := normalizePhoneParts(countryCodeRaw, contactPhoneRaw)
	if !ok {
		return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
			"success": false,
			"message": "Teléfono inválido",
		}}
	}

	if resDate == "" || !isValidISODate(resDate) || partySize < 2 || resTimeRaw == "" || customerName == "" {
		return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
			"success": false,
			"message": "Faltan campos requeridos",
		}}
	}
	resTime, err := ensureHHMMSS(resTimeRaw)
	if err != nil {
		return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
			"success": false,
			"message": "Hora inválida",
		}}
	}

	commentary := strings.TrimSpace(r.FormValue("commentary"))
	babyStrollers := clampInt(r.FormValue("baby_strollers"), 0, 100, 0)
	highChairs := clampInt(r.FormValue("high_chairs"), 0, 100, 0)
	contactEmail := strings.TrimSpace(r.FormValue("contact_email"))
	if contactEmail == "" {
		contactEmail = s.restaurantFallbackEmail(r.Context(), restaurantID)
	}

	children, err := parseChildrenFromForm(r, partySize)
	if err != nil {
		return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
			"success": false,
			"message": err.Error(),
		}}
	}

	// Coordination id: mobility_issues_v1
	hasMobilityIssues, mobilityPeople, err := parseMobilityFromForm(r, partySize)
	if err != nil {
		return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
			"success": false,
			"message": err.Error(),
		}}
	}

	// Group menu (special menu) selection.
	specialMenu := clampInt(r.FormValue("menu_de_grupo_selected"), 0, 1, 0) == 1
	menuDeGrupoID := 0
	var principalesJSON any = nil
	toggleArroz := strings.TrimSpace(r.FormValue("toggleArroz"))

	var arrozTypeJSON any = nil
	var arrozServingsJSON any = nil
	locationFlags, err := s.resolveLocationBooking(r.Context(), restaurantID, resDate)
	if err != nil {
		return nil, &frontBookingError{Status: http.StatusInternalServerError, Body: map[string]any{
			"success": false,
			"message": "No se pudo consultar la configuración de ubicación",
		}}
	}
	preferredFloorNumber, err := s.resolvePreferredFloorNumberForFront(r.Context(), restaurantID, resDate, strings.TrimSpace(r.FormValue("preferred_floor_number")), locationFlags.Floor.Value)
	if err != nil {
		return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
			"success": false,
			"message": err.Error(),
		}}
	}
	preferredSalonID, err := s.resolvePreferredSalonIDForFront(r.Context(), restaurantID, resDate, strings.TrimSpace(r.FormValue("preferred_salon_id")), preferredFloorNumber, locationFlags.Salon.Value)
	if err != nil {
		return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
			"success": false,
			"message": err.Error(),
		}}
	}

	if specialMenu {
		menuDeGrupoID = clampInt(r.FormValue("menu_de_grupo_id"), 1, 1_000_000_000, 0)
		if menuDeGrupoID <= 0 {
			return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
				"success": false,
				"message": "Debe seleccionar un menú de grupo",
			}}
		}

		menuTitle, menuPrincipalesRaw, err := s.fetchActiveGroupMenuTitleAndPrincipales(r, menuDeGrupoID)
		if err != nil || strings.TrimSpace(menuTitle) == "" {
			return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
				"success": false,
				"message": "Menú de grupo no válido o inactivo",
			}}
		}

		// Store menu title and party size in arroz_* JSON arrays (legacy behavior).
		bt, _ := json.Marshal([]string{menuTitle})
		bs, _ := json.Marshal([]int{partySize})
		arrozTypeJSON = string(bt)
		arrozServingsJSON = string(bs)
		toggleArroz = "false"

		// Commentary is reserved for principales summary.
		commentary = ""

		principalesEnabled := strings.TrimSpace(r.FormValue("principales_enabled")) == "1"
		rowsRaw := strings.TrimSpace(r.FormValue("principales_json"))
		if rowsRaw == "" {
			rowsRaw = "[]"
		}

		if principalesEnabled {
			summary, storedJSON, err := buildPrincipalesSummaryAndJSON(menuPrincipalesRaw, rowsRaw, partySize)
			if err != nil {
				return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
					"success": false,
					"message": err.Error(),
				}}
			}
			commentary = summary
			if storedJSON != "" {
				principalesJSON = storedJSON
			}
		}
	} else {
		// Regular booking: arroz follows toggleArroz.
		if strings.TrimSpace(toggleArroz) == "true" {
			arrozTypeJSON, arrozServingsJSON, err = parseArrozFromForm(r, partySize)
			if err != nil {
				return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
					"success": false,
					"message": err.Error(),
				}}
			}
		}

	}

	// Coordination id: special_booking_v1 - the front posts a JSON payload
	// under "special_json". When present, validate against the date's active
	// special_dates settings and snapshot into the booking row. Custom-menu
	// items are not required (dish_id arrays validated when sent).
	// Coordination id: stripe_prereserva_adelanto_v1
	var specialSnapshot *specialBookingSnapshot
	requiresStripe := false
	var (
		isSpecialBooking bool
		isPrereserva     bool
		specialJSON      any
	)
	specialRaw := strings.TrimSpace(r.FormValue("special_json"))
	if specialRaw != "" {
		var req specialBookingReq
		if err := json.Unmarshal([]byte(specialRaw), &req); err != nil {
			return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
				"success": false,
				"message": "JSON inválido en 'special_json'",
			}}
		}
		// The public form never declares payments: only the backoffice and a
		// verified Stripe checkout record adelantos as paid.
		req.AdelantosPaid = nil
		// Coordination id: stripe_prereserva_adelanto_v1 - a stripe-only
		// adelanto date is paid online: the method is forced to stripe.
		if settings, _, sErr := s.loadSpecialDateSettings(r.Context(), restaurantID, resDate); sErr == nil && specialDateRequiresStripe(settings) {
			requiresStripe = true
			method := adelantoMethodStripe
			req.PaymentMethod = &method
			for i := range req.Menus {
				req.Menus[i].AdelantoPaymentMethod = &method
			}
		}
		snap, prereserva, err := s.resolveSpecialBookingInput(r.Context(), restaurantID, resDate, partySize, &req)
		if err != nil {
			return nil, &frontBookingError{Status: http.StatusBadRequest, Body: map[string]any{
				"success": false,
				"message": err.Error(),
			}}
		}
		snapBytes, mErr := json.Marshal(snap)
		if mErr != nil {
			return nil, &frontBookingError{Status: http.StatusInternalServerError, Body: map[string]any{
				"success": false,
				"message": "No se pudo serializar el menú especial",
			}}
		}
		isSpecialBooking = true
		if prereserva != nil {
			isPrereserva = *prereserva
		}
		specialJSON = string(snapBytes)
		specialSnapshot = snap
	}

	return &preparedFrontBooking{
		restaurantID: restaurantID,
		params: bookingInsertParams{
			ReservationDate:   resDate,
			ReservationTime:   resTime,
			PartySize:         partySize,
			Children:          children,
			CustomerName:      customerName,
			ContactPhone:      nationalPhone,
			ContactPhoneCC:    cc,
			ContactEmail:      contactEmail,
			Commentary:        commentary,
			BabyStrollers:     babyStrollers,
			HighChairs:        highChairs,
			ArrozTypeJSON:     arrozTypeJSON,
			ArrozServingsJSON: arrozServingsJSON,
			SpecialMenu:       boolToTinyint(specialMenu),
			MenuDeGrupoID:     nullIntOrNil(menuDeGrupoID),
			PrincipalesJSON:   principalesJSON,
			PreferredFloorNum: preferredFloorNumber,
			PreferredSalonID:  preferredSalonID,
			IsSpecialBooking:  isSpecialBooking,
			IsPrereserva:      isPrereserva,
			SpecialJSON:       specialJSON,
			HasMobilityIssues: hasMobilityIssues,
			MobilityPeople:    mobilityPeople,
		},
		toggleArroz:     toggleArroz,
		phoneE164:       phoneE164,
		menuDeGrupoID:   menuDeGrupoID,
		specialSnapshot: specialSnapshot,
		requiresStripe:  requiresStripe,
	}, nil
}

// commitFrontBooking inserts a prepared booking and sends the customer and
// restaurant notifications. attachments (e.g. the Stripe receipt) go with the
// confirmation email and WhatsApp.
// Coordination id: stripe_prereserva_adelanto_v1
func (s *Server) commitFrontBooking(r *http.Request, pb *preparedFrontBooking, receipt *bookingReceipt) (int64, int, map[string]any) {
	p := pb.params
	bookingID, err := s.insertBooking(r, p)
	if err != nil {
		return 0, http.StatusInternalServerError, map[string]any{
			"success":    false,
			"message":    "Error: " + err.Error(),
			"error_code": "BOOKING_INSERT_FAILED",
		}
	}

	// Build booking data map for notifications.
	bookingData := map[string]any{
		"booking_id":                 bookingID,
		"reservation_date":           p.ReservationDate,
		"reservation_time":           p.ReservationTime,
		"party_size":                 p.PartySize,
		"children":                   p.Children,
		"customer_name":              p.CustomerName,
		"contact_phone":              p.ContactPhone,
		"contact_phone_country_code": p.ContactPhoneCC,
		"contact_email":              p.ContactEmail,
		"commentary":                 p.Commentary,
		"arroz_type":                 p.ArrozTypeJSON,
		"arroz_servings":             p.ArrozServingsJSON,
		"baby_strollers":             p.BabyStrollers,
		"high_chairs":                p.HighChairs,
		"toggleArroz":                pb.toggleArroz,
		"special_menu":               (p.SpecialMenu != 0),
		"menu_de_grupo_id":           pb.menuDeGrupoID,
		"principales_json":           p.PrincipalesJSON,
		"preferred_floor_number":     p.PreferredFloorNum,
		"preferred_salon_id":         p.PreferredSalonID,
		"is_special_booking":         p.IsSpecialBooking,
		"is_prereserva":              p.IsPrereserva,
		"special_json":               p.SpecialJSON,
		"has_mobility_issues":        p.HasMobilityIssues,
		"mobility_people":            p.MobilityPeople,
		"special":                    s.buildSpecialBookingResponse(r.Context(), pb.restaurantID, p.IsSpecialBooking, p.IsPrereserva, anyToString(p.SpecialJSON)),
	}
	s.enrichBookingLocationForNotifications(r.Context(), pb.restaurantID, bookingData)
	if receipt != nil {
		bookingData[bookingReceiptKey] = receipt
	}

	// Send WhatsApp confirmation to customer (best-effort).
	var whatsappSent bool
	var whatsappWarning string
	if err := sendBookingWhatsAppToCustomer(r.Context(), s, pb.restaurantID, bookingData, bookingID); err != nil {
		log.Printf("WhatsApp failed for booking #%d: %v", bookingID, err)
		if strings.Contains(err.Error(), "is not on WhatsApp") {
			whatsappWarning = "El número de teléfono no tiene servicio de WhatsApp."
		} else {
			whatsappWarning = "WhatsApp no pudo enviarse: " + err.Error()
		}
	} else {
		whatsappSent = true
	}

	// Send confirmation emails (synchronous, required).
	customerSent, restaurantSent, emailErr := sendBookingConfirmationEmails(r.Context(), s, pb.restaurantID, bookingData, bookingID)
	if emailErr != nil {
		log.Printf("Email failed for booking #%d: %v", bookingID, emailErr)
		// The row exists: return its id so callers (Stripe checkout) never
		// treat a stored, paid booking as failed.
		return bookingID, http.StatusServiceUnavailable, map[string]any{
			"success":          false,
			"message":          "Error enviando confirmación por email: " + emailErr.Error(),
			"error_code":       "EMAIL_FAILED",
			"booking_id":       bookingID,
			"whatsapp_sent":    whatsappSent,
			"whatsapp_warning": whatsappWarning,
		}
	}

	// Update response with actual notification status.
	resp := map[string]any{
		"success":            true,
		"message":            "¡Reserva realizada con éxito!",
		"booking_id":         bookingID,
		"notifications_sent": whatsappSent || customerSent || restaurantSent,
		"email_sent":         customerSent || restaurantSent,
		"whatsapp_sent":      whatsappSent,
	}
	if whatsappWarning != "" {
		resp["whatsapp_warning"] = whatsappWarning
	}

	s.emitN8nWebhookAsync(pb.restaurantID, "booking.created", map[string]any{
		"source":                  "front",
		"bookingId":               bookingID,
		"reservationDate":         p.ReservationDate,
		"reservationTime":         p.ReservationTime,
		"partySize":               p.PartySize,
		"children":                p.Children,
		"customerName":            p.CustomerName,
		"contactPhone":            p.ContactPhone,
		"contactPhoneCountryCode": p.ContactPhoneCC,
		"contactPhoneE164":        pb.phoneE164,
		"contactEmail":            p.ContactEmail,
		"specialMenu":             (p.SpecialMenu != 0),
		"menuDeGrupoId":           pb.menuDeGrupoID,
		"preferredFloorNumber":    p.PreferredFloorNum,
	})
	return bookingID, http.StatusOK, resp
}

func (s *Server) handleInsertBookingAdmin(w http.ResponseWriter, r *http.Request) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"message": "Unknown restaurant",
		})
		return
	}

	if err := parseLegacyForm(r, 5<<20); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid input",
		})
		return
	}

	resDate := strings.TrimSpace(r.FormValue("date"))
	partySize := clampInt(r.FormValue("party_size"), 1, 10_000, 0)
	resTimeRaw := strings.TrimSpace(r.FormValue("time"))
	customerName := strings.TrimSpace(r.FormValue("nombre"))
	contactPhoneRaw := strings.TrimSpace(r.FormValue("phone"))
	countryCodeRaw := strings.TrimSpace(r.FormValue("country_code"))

	cc, nationalPhone, phoneE164, ok := normalizePhoneParts(countryCodeRaw, contactPhoneRaw)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Teléfono inválido",
		})
		return
	}

	if resDate == "" || !isValidISODate(resDate) || partySize < 1 || resTimeRaw == "" || customerName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Invalid input",
		})
		return
	}
	resTime, err := ensureHHMMSS(resTimeRaw)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Hora inválida",
		})
		return
	}
	locationFlags, err := s.resolveLocationBooking(r.Context(), restaurantID, resDate)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "No se pudo consultar la configuración de ubicación",
		})
		return
	}
	preferredFloorNumber, err := s.resolvePreferredFloorNumberForFront(r.Context(), restaurantID, resDate, strings.TrimSpace(r.FormValue("preferred_floor_number")), locationFlags.Floor.Value)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	preferredSalonID, err := s.resolvePreferredSalonIDForFront(r.Context(), restaurantID, resDate, strings.TrimSpace(r.FormValue("preferred_salon_id")), preferredFloorNumber, locationFlags.Salon.Value)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	commentary := strings.TrimSpace(r.FormValue("commentary"))
	babyStrollers := clampInt(r.FormValue("baby_strollers"), 0, 100, 0)
	highChairs := clampInt(r.FormValue("high_chairs"), 0, 100, 0)
	contactEmail := strings.TrimSpace(r.FormValue("contact_email"))
	if contactEmail == "" {
		contactEmail = s.restaurantFallbackEmail(r.Context(), restaurantID)
	}

	children, err := parseChildrenFromForm(r, partySize)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Coordination id: mobility_issues_v1
	hasMobilityIssues, mobilityPeople, err := parseMobilityFromForm(r, partySize)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	specialMenu := strings.TrimSpace(r.FormValue("special_menu")) == "1"
	menuDeGrupoID := 0
	var principalesJSON any = nil
	toggleArroz := strings.TrimSpace(r.FormValue("toggleArroz"))

	var arrozTypeJSON any = nil
	var arrozServingsJSON any = nil

	if specialMenu {
		menuDeGrupoID = clampInt(r.FormValue("menu_de_grupo_id"), 1, 1_000_000_000, 0)
		if menuDeGrupoID <= 0 {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "Debe seleccionar un menú de grupo",
			})
			return
		}

		menuTitle, menuPrincipalesRaw, err := s.fetchActiveGroupMenuTitleAndPrincipales(r, menuDeGrupoID)
		if err != nil || strings.TrimSpace(menuTitle) == "" {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"success": false,
				"message": "Menú de grupo no válido o inactivo",
			})
			return
		}

		bt, _ := json.Marshal([]string{menuTitle})
		bs, _ := json.Marshal([]int{partySize})
		arrozTypeJSON = string(bt)
		arrozServingsJSON = string(bs)
		toggleArroz = "false"
		commentary = ""

		principalesEnabled := strings.TrimSpace(r.FormValue("principales_enabled")) == "1"
		rowsRaw := strings.TrimSpace(r.FormValue("principales_json"))
		if rowsRaw == "" {
			rowsRaw = "[]"
		}
		if principalesEnabled {
			summary, storedJSON, err := buildPrincipalesSummaryAndJSON(menuPrincipalesRaw, rowsRaw, partySize)
			if err != nil {
				httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
					"success": false,
					"message": err.Error(),
				})
				return
			}
			commentary = summary
			if storedJSON != "" {
				principalesJSON = storedJSON
			}
		}
	} else {
		// Regular booking: arroz can be selected.
		wantsArroz := strings.TrimSpace(toggleArroz) == "true"
		if wantsArroz {
			arrozTypeJSON, arrozServingsJSON, err = parseArrozFromForm(r, partySize)
			if err != nil {
				httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{
					"success": false,
					"message": err.Error(),
				})
				return
			}
		}
	}

	bookingID, err := s.insertBooking(r, bookingInsertParams{
		ReservationDate:   resDate,
		ReservationTime:   resTime,
		PartySize:         partySize,
		Children:          children,
		CustomerName:      customerName,
		ContactPhone:      nationalPhone,
		ContactPhoneCC:    cc,
		ContactEmail:      contactEmail,
		Commentary:        commentary,
		BabyStrollers:     babyStrollers,
		HighChairs:        highChairs,
		ArrozTypeJSON:     arrozTypeJSON,
		ArrozServingsJSON: arrozServingsJSON,
		SpecialMenu:       boolToTinyint(specialMenu),
		MenuDeGrupoID:     nullIntOrNil(menuDeGrupoID),
		PrincipalesJSON:   principalesJSON,
		PreferredFloorNum: preferredFloorNumber,
		PreferredSalonID:  preferredSalonID,
		// Coordination id: mobility_issues_v1
		HasMobilityIssues: hasMobilityIssues,
		MobilityPeople:    mobilityPeople,
	})
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": "Error: " + err.Error(),
		})
		return
	}

	// Build booking data map for notifications.
	adminBookingData := map[string]any{
		"has_mobility_issues":        hasMobilityIssues,
		"mobility_people":            mobilityPeople,
		"booking_id":                 bookingID,
		"reservation_date":           resDate,
		"reservation_time":           resTime,
		"party_size":                 partySize,
		"children":                   children,
		"customer_name":              customerName,
		"contact_phone":              nationalPhone,
		"contact_phone_country_code": cc,
		"contact_email":              contactEmail,
		"commentary":                 commentary,
		"arroz_type":                 arrozTypeJSON,
		"arroz_servings":             arrozServingsJSON,
		"baby_strollers":             babyStrollers,
		"high_chairs":                highChairs,
		"toggleArroz":                toggleArroz,
		"special_menu":               specialMenu,
		"menu_de_grupo_id":           menuDeGrupoID,
		"principales_json":           principalesJSON,
		"preferred_floor_number":     preferredFloorNumber,
		"preferred_salon_id":         preferredSalonID,
	}
	s.enrichBookingLocationForNotifications(r.Context(), restaurantID, adminBookingData)

	// Send WhatsApp confirmation to customer (best-effort).
	var adminWhatsappSent bool
	var adminWhatsappWarning string
	if err := sendBookingWhatsAppToCustomer(r.Context(), s, restaurantID, adminBookingData, bookingID); err != nil {
		log.Printf("WhatsApp failed for admin booking #%d: %v", bookingID, err)
		if strings.Contains(err.Error(), "is not on WhatsApp") {
			adminWhatsappWarning = "El número de teléfono no tiene servicio de WhatsApp."
		} else {
			adminWhatsappWarning = "WhatsApp no pudo enviarse: " + err.Error()
		}
	} else {
		adminWhatsappSent = true
	}

	// Send confirmation emails (synchronous, required).
	customerSent, restaurantSent, emailErr := sendBookingConfirmationEmails(r.Context(), s, restaurantID, adminBookingData, bookingID)
	if emailErr != nil {
		log.Printf("Email failed for admin booking #%d: %v", bookingID, emailErr)
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success":          false,
			"message":          "Error enviando confirmación por email: " + emailErr.Error(),
			"error_code":       "EMAIL_FAILED",
			"booking_id":       bookingID,
			"whatsapp_sent":    adminWhatsappSent,
			"whatsapp_warning": adminWhatsappWarning,
		})
		return
	}

	resp := map[string]any{
		"success":            true,
		"booking_id":         bookingID,
		"whatsapp_sent":      adminWhatsappSent,
		"email_sent":         customerSent || restaurantSent,
		"notifications_sent": adminWhatsappSent || customerSent || restaurantSent,
	}
	if adminWhatsappWarning != "" {
		resp["whatsapp_warning"] = adminWhatsappWarning
	}
	httpx.WriteJSON(w, http.StatusOK, resp)

	s.emitN8nWebhookAsync(restaurantID, "booking.created", map[string]any{
		"source":                  "admin",
		"bookingId":               bookingID,
		"reservationDate":         resDate,
		"reservationTime":         resTime,
		"partySize":               partySize,
		"children":                children,
		"customerName":            customerName,
		"contactPhone":            nationalPhone,
		"contactPhoneCountryCode": cc,
		"contactPhoneE164":        phoneE164,
		"contactEmail":            contactEmail,
		"specialMenu":             specialMenu,
		"menuDeGrupoId":           menuDeGrupoID,
	})
}

type bookingInsertParams struct {
	ReservationDate   string
	ReservationTime   string
	PartySize         int
	Children          int
	CustomerName      string
	ContactPhone      string
	ContactPhoneCC    string
	ContactEmail      string
	Commentary        string
	BabyStrollers     int
	HighChairs        int
	ArrozTypeJSON     any
	ArrozServingsJSON any
	SpecialMenu       int
	MenuDeGrupoID     any
	PrincipalesJSON   any
	PreferredFloorNum any
	PreferredSalonID  any
	// Coordination id: special_booking_v1 - pre-validated special booking
	// snapshot payload. When non-nil the row is marked as a special booking
	// with is_special_booking=1 and is_prereserva set from the date settings.
	IsSpecialBooking bool
	IsPrereserva     bool
	SpecialJSON      any
	// Coordination id: mobility_issues_v1 - "problemas de movilidad" answer.
	// Only asked when the date's special settings enable it; MobilityPeople
	// is how many of PartySize are affected.
	HasMobilityIssues bool
	MobilityPeople    int
}

func (s *Server) insertBooking(r *http.Request, p bookingInsertParams) (int64, error) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return 0, errors.New("unknown restaurant")
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(r.Context(), `
		INSERT INTO bookings (
			restaurant_id,
			reservation_date,
			party_size,
			children,
			reservation_time,
			customer_name,
			contact_phone,
			contact_phone_country_code,
			commentary,
			arroz_type,
			arroz_servings,
			babyStrollers,
			highChairs,
			contact_email,
			special_menu,
			menu_de_grupo_id,
			menu_de_grupo_assigned,
			principales_json,
			preferred_floor_number,
			preferred_salon_id,
			is_special_booking,
			is_prereserva,
			special_json,
			has_mobility_issues,
			mobility_people
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, restaurantID, p.ReservationDate, p.PartySize, p.Children, p.ReservationTime, p.CustomerName, p.ContactPhone, p.ContactPhoneCC, p.Commentary, p.ArrozTypeJSON, p.ArrozServingsJSON, p.BabyStrollers, p.HighChairs, p.ContactEmail, p.SpecialMenu, p.MenuDeGrupoID, menuDeGrupoAssignedTinyint(p.MenuDeGrupoID), p.PrincipalesJSON, p.PreferredFloorNum, p.PreferredSalonID, boolToTinyint(p.IsSpecialBooking), boolToTinyint(p.IsPrereserva), nullableStringOrNilFromAny(p.SpecialJSON), boolToTinyint(p.HasMobilityIssues), p.MobilityPeople)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	// Track occupancy for the selected floor/salon on the reservation date.
	if err := s.applyBookingLocationOccupancy(r.Context(), tx, restaurantID, p.ReservationDate, p.PreferredFloorNum, p.PreferredSalonID, p.PartySize, +1); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Server) enrichBookingLocationForNotifications(ctx context.Context, restaurantID int, booking map[string]any) {
	salonID, err := anyToInt(booking["preferred_salon_id"])
	if err != nil || salonID <= 0 || s.db == nil {
		return
	}
	var salonName sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT name FROM restaurant_salons WHERE restaurant_id = ? AND id = ? LIMIT 1
	`, restaurantID, salonID).Scan(&salonName); err == nil && salonName.Valid {
		booking["preferred_salon_name"] = strings.TrimSpace(salonName.String)
	}
}

func (s *Server) resolvePreferredFloorNumberForFront(ctx context.Context, restaurantID int, date string, raw string, allowFloorReservation bool) (any, error) {
	if !allowFloorReservation {
		return nil, nil
	}
	floors, err := s.loadDateFloors(ctx, restaurantID, date)
	if err != nil {
		return nil, errors.New("No se pudo consultar las plantas activas")
	}
	active := make([]boConfigFloor, 0, len(floors))
	for _, floor := range floors {
		if floor.Active {
			active = append(active, floor)
		}
	}
	if len(active) == 0 {
		return nil, errors.New("No hay salones activos para la fecha seleccionada")
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		if len(active) == 1 {
			return active[0].FloorNumber, nil
		}
		return nil, errors.New("Debe seleccionar un salón")
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil, errors.New("Salón inválido")
	}
	for _, floor := range active {
		if floor.FloorNumber == n {
			return n, nil
		}
	}
	return nil, errors.New("El salón seleccionado no está disponible para la fecha")
}

func normalizePhoneParts(countryCodeRaw, phoneRaw string) (countryCode string, national string, e164Digits string, ok bool) {
	cc := onlyDigits(countryCodeRaw)
	phone := onlyDigits(phoneRaw)

	if cc == "" {
		cc = "34"
	}
	// Country calling codes are 1-4 digits in E.164.
	if len(cc) < 1 || len(cc) > 4 {
		return "", "", "", false
	}

	// If the user provided a full E.164 number in the phone field, avoid double-prefixing.
	if len(phone) >= 8 && len(phone) <= 15 && strings.HasPrefix(phone, cc) && len(phone) > 9 {
		n := phone[len(cc):]
		if n == "" {
			return "", "", "", false
		}
		return cc, n, phone, true
	}

	// National number length varies widely; keep a permissive range but enforce E.164 max length.
	if len(phone) < 6 || len(phone) > 15 {
		return "", "", "", false
	}
	if len(cc)+len(phone) > 15 {
		return "", "", "", false
	}
	return cc, phone, cc + phone, true
}

// parseMobilityFromForm reads the "problemas de movilidad" answer.
// `mobility_people` is only meaningful when has_mobility_issues is true, and
// can never exceed the party size.
//
// Coordination id: mobility_issues_v1
func parseMobilityFromForm(r *http.Request, partySize int) (bool, int, error) {
	raw := strings.TrimSpace(r.FormValue("has_mobility_issues"))
	has := raw == "1" || strings.EqualFold(raw, "true")
	if !has {
		return false, 0, nil
	}
	peopleRaw := strings.TrimSpace(r.FormValue("mobility_people"))
	if peopleRaw == "" {
		return true, 0, nil
	}
	n, err := strconv.Atoi(peopleRaw)
	if err != nil || n < 0 || n > partySize {
		return false, 0, errors.New("Número de personas con problemas de movilidad inválido")
	}
	return true, n, nil
}

func parseChildrenFromForm(r *http.Request, partySize int) (int, error) {
	// Preferred: adults -> children = partySize - adults.
	adultsRaw := strings.TrimSpace(r.FormValue("adults"))
	if adultsRaw != "" {
		n, err := strconv.Atoi(adultsRaw)
		if err != nil || n < 1 || n > partySize {
			return 0, errors.New("Número de adultos inválido")
		}
		return partySize - n, nil
	}

	// Backwards-compatible fallback: children field directly.
	childrenRaw := strings.TrimSpace(r.FormValue("children"))
	if childrenRaw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(childrenRaw)
	if err != nil || n < 0 || n > partySize {
		return 0, errors.New("Número de niños inválido")
	}
	return n, nil
}

func (s *Server) fetchActiveGroupMenuTitleAndPrincipales(r *http.Request, menuID int) (title string, principalesRaw string, err error) {
	restaurantID, ok := restaurantIDFromContext(r.Context())
	if !ok {
		return "", "", errors.New("unknown restaurant")
	}

	var t string
	var principales sql.NullString
	err = s.db.QueryRowContext(r.Context(), "SELECT menu_title, principales FROM menus WHERE restaurant_id = ? AND id = ? AND active = 1 LIMIT 1", restaurantID, menuID).Scan(&t, &principales)
	if err != nil {
		return "", "", err
	}
	return t, principales.String, nil
}

func parseArrozFromForm(r *http.Request, partySize int) (arrozTypeJSON any, arrozServingsJSON any, err error) {
	typesRaw := strings.TrimSpace(r.FormValue("arroz_types_json"))
	servRaw := strings.TrimSpace(r.FormValue("arroz_servings_json"))

	var types []any
	var servs []any
	if typesRaw != "" && servRaw != "" {
		_ = json.Unmarshal([]byte(typesRaw), &types)
		_ = json.Unmarshal([]byte(servRaw), &servs)
	}

	// Backward compatibility: single arroz.
	if len(types) == 0 || len(servs) == 0 || len(types) != len(servs) {
		singleType := strings.TrimSpace(r.FormValue("arroz_type"))
		singleServ := clampInt(r.FormValue("arroz_servings"), 0, 10_000, 0)
		if singleType != "" && singleServ > 0 {
			types = []any{singleType}
			servs = []any{singleServ}
		} else {
			types = nil
			servs = nil
		}
	}

	seen := map[string]bool{}
	cleanTypes := make([]string, 0, len(types))
	cleanServs := make([]int, 0, len(types))
	sum := 0
	for i := 0; i < len(types); i++ {
		t := strings.TrimSpace(anyToString(types[i]))
		sv := 0
		if i < len(servs) {
			sv, _ = anyToInt(servs[i])
		}
		if t == "" || sv <= 0 {
			continue
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		sum += sv
		cleanTypes = append(cleanTypes, t)
		cleanServs = append(cleanServs, sv)
	}

	if sum > partySize {
		return nil, nil, errors.New("Las raciones de arroz superan el número de comensales")
	}

	if len(cleanTypes) == 0 {
		return nil, nil, nil
	}
	bt, _ := json.Marshal(cleanTypes)
	bs, _ := json.Marshal(cleanServs)
	return string(bt), string(bs), nil
}

func buildPrincipalesSummaryAndJSON(menuPrincipalesRaw string, rowsRaw string, partySize int) (summary string, storedJSON string, err error) {
	// Allowed list from menu.
	allowed := map[string]bool{}
	if strings.TrimSpace(menuPrincipalesRaw) != "" {
		var mp struct {
			Items []string `json:"items"`
		}
		if err := json.Unmarshal([]byte(menuPrincipalesRaw), &mp); err == nil {
			for _, it := range mp.Items {
				it = strings.TrimSpace(it)
				if it == "" {
					continue
				}
				allowed[it] = true
			}
		}
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(rowsRaw), &rows); err != nil {
		rows = []map[string]any{}
	}

	seen := map[string]bool{}
	total := 0
	parts := []string{}
	clean := make([]map[string]any, 0, len(rows))

	for _, row := range rows {
		name := strings.TrimSpace(anyToString(row["name"]))
		servings, _ := anyToInt(row["servings"])
		if name == "" || servings <= 0 {
			continue
		}
		if seen[name] {
			continue
		}
		if len(allowed) > 0 && !allowed[name] {
			continue
		}
		seen[name] = true
		total += servings
		parts = append(parts, name+" x "+strconv.Itoa(servings))
		clean = append(clean, map[string]any{"name": name, "servings": servings})
	}

	if total > partySize {
		return "", "", errors.New("Las raciones de principales superan el número de comensales")
	}

	if len(clean) > 0 {
		b, _ := json.Marshal(clean)
		storedJSON = string(b)
	}
	return strings.Join(parts, ", "), storedJSON, nil
}

func nullIntOrNil(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}

// resolvePreferredSalonIDForFront validates the optional preferred salon form
// value against the active salons for the date. Empty input stays nil (no
// salon chosen). When both salon and floor are provided, the salon must
// belong to that floor.
func (s *Server) resolvePreferredSalonIDForFront(ctx context.Context, restaurantID int, date string, raw string, preferredFloor any, allowSalonReservation bool) (any, error) {
	if !allowSalonReservation {
		return nil, nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	salonID, err := strconv.Atoi(raw)
	if err != nil || salonID <= 0 {
		return nil, errors.New("Salón no válido")
	}

	salons, err := s.loadSalons(ctx, restaurantID, date)
	if err != nil {
		return nil, errors.New("No se pudo consultar los salones activos")
	}
	for _, salon := range salons {
		if salon.ID != salonID {
			continue
		}
		if !salon.IsActive {
			return nil, errors.New("El salón seleccionado no está disponible")
		}
		if floorNum, ok := preferredFloor.(int); ok && floorNum > 0 {
			floors, ferr := s.loadDateFloors(ctx, restaurantID, date)
			if ferr != nil {
				return nil, errors.New("No se pudo consultar las plantas activas")
			}
			for _, floor := range floors {
				if floor.FloorNumber == floorNum && floor.ID != salon.FloorID {
					return nil, errors.New("El salón no pertenece a la planta seleccionada")
				}
			}
		}
		return salonID, nil
	}
	return nil, errors.New("El salón seleccionado no está disponible")
}
