package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

// Campaigns: one markdown body broadcast as email (SMTP of the restaurant) and
// as WhatsApp (Evolution gateway of the restaurant). Observation points share
// the campaign coord_id ("camp-<uuid>") across frontend and backend logs.

const (
	campaignMaxImageBytes = 400 * 1024
	campaignMaxRecipients = 5000
	// Operator-facing pacing bounds, expressed in messages per minute.
	campaignMinPerMinute            = 1
	campaignMaxEmailPerMinute       = 600
	campaignMaxWhatsAppPerMinute    = 120
	campaignDefaultEmailPerMinute   = 60
	campaignDefaultWhatsAppPerMinut = 12
)

// campaignChannelPause turns a per-minute rate into the delay between sends.
func campaignChannelPause(perMinute int) time.Duration {
	if perMinute < campaignMinPerMinute {
		perMinute = campaignMinPerMinute
	}
	return time.Duration(float64(time.Minute) / float64(perMinute))
}

type boCampaign struct {
	ID               int64         `json:"id"`
	CoordID          string        `json:"coord_id"`
	Name             string        `json:"name"`
	Subject          string        `json:"subject"`
	BodyMarkdown     string        `json:"body_markdown"`
	Theme            campaignTheme `json:"theme"`
	Channels         []string      `json:"channels"`
	Audience         string        `json:"audience"`
	AudienceDays     int           `json:"audience_days"`
	ManualRecipients []string      `json:"manual_recipients"`
	EmailPerMinute   int           `json:"email_per_minute"`
	WhatsAppPerMin   int           `json:"whatsapp_per_minute"`
	Status           string        `json:"status"`
	SentAt           string        `json:"sent_at,omitempty"`
	CreatedAt        string        `json:"created_at,omitempty"`
	UpdatedAt        string        `json:"updated_at,omitempty"`
	Stats            campaignStats `json:"stats"`
}

type campaignStats struct {
	Total   int `json:"total"`
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
	Pending int `json:"pending"`
}

type boCampaignInput struct {
	Name             string        `json:"name"`
	Subject          string        `json:"subject"`
	BodyMarkdown     string        `json:"body_markdown"`
	Theme            campaignTheme `json:"theme"`
	Channels         []string      `json:"channels"`
	Audience         string        `json:"audience"`
	AudienceDays     int           `json:"audience_days"`
	ManualRecipients []string      `json:"manual_recipients"`
	EmailPerMinute   int           `json:"email_per_minute"`
	WhatsAppPerMin   int           `json:"whatsapp_per_minute"`
}

type campaignTarget struct {
	Channel   string `json:"channel"`
	Target    string `json:"target"`
	Name      string `json:"name"`
	BookingID int64  `json:"booking_id,omitempty"`
}

func clampCampaignRate(value, fallback, max int) int {
	if value <= 0 {
		return fallback
	}
	if value < campaignMinPerMinute {
		return campaignMinPerMinute
	}
	if value > max {
		return max
	}
	return value
}

func campaignHasChannel(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func normalizeCampaignChannels(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if (v != "email" && v != "whatsapp") || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) == 0 {
		out = []string{"email"}
	}
	return out
}

func normalizeCampaignInput(in boCampaignInput) (boCampaignInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Name == "" {
		return in, errors.New("El nombre de la campana es obligatorio")
	}
	if len(in.Name) > 180 {
		in.Name = in.Name[:180]
	}
	if len(in.Subject) > 200 {
		in.Subject = in.Subject[:200]
	}
	in.Channels = normalizeCampaignChannels(in.Channels)
	in.Theme = normalizeCampaignTheme(in.Theme)
	in.Audience = strings.ToLower(strings.TrimSpace(in.Audience))
	if in.Audience != "manual" && in.Audience != "bookings" {
		in.Audience = "bookings"
	}
	if in.AudienceDays <= 0 || in.AudienceDays > 3650 {
		in.AudienceDays = 365
	}
	manual := []string{}
	for _, raw := range in.ManualRecipients {
		v := strings.TrimSpace(raw)
		if v != "" {
			manual = append(manual, v)
		}
	}
	in.ManualRecipients = manual
	in.EmailPerMinute = clampCampaignRate(in.EmailPerMinute, campaignDefaultEmailPerMinute, campaignMaxEmailPerMinute)
	in.WhatsAppPerMin = clampCampaignRate(in.WhatsAppPerMin, campaignDefaultWhatsAppPerMinut, campaignMaxWhatsAppPerMinute)
	return in, nil
}

func scanBOCampaign(scan func(dest ...any) error) (boCampaign, error) {
	var (
		c         boCampaign
		themeRaw  sql.NullString
		channels  string
		manualRaw sql.NullString
		sentAt    sql.NullString
		createdAt sql.NullString
		updatedAt sql.NullString
	)
	if err := scan(&c.ID, &c.CoordID, &c.Name, &c.Subject, &c.BodyMarkdown, &themeRaw, &channels, &c.Audience, &c.AudienceDays, &manualRaw, &c.EmailPerMinute, &c.WhatsAppPerMin, &c.Status, &sentAt, &createdAt, &updatedAt); err != nil {
		return boCampaign{}, err
	}
	_ = json.Unmarshal([]byte(themeRaw.String), &c.Theme)
	c.Theme = normalizeCampaignTheme(c.Theme)
	c.Channels = normalizeCampaignChannels(strings.Split(channels, ","))
	c.ManualRecipients = []string{}
	_ = json.Unmarshal([]byte(manualRaw.String), &c.ManualRecipients)
	if c.ManualRecipients == nil {
		c.ManualRecipients = []string{}
	}
	c.SentAt = sentAt.String
	c.CreatedAt = createdAt.String
	c.UpdatedAt = updatedAt.String
	return c, nil
}

const boCampaignColumns = `id, coord_id, name, subject, body_markdown, theme_json, channels, audience, audience_days, manual_recipients, email_per_minute, whatsapp_per_minute, status,
	DATE_FORMAT(sent_at, '%Y-%m-%d %H:%i'), DATE_FORMAT(created_at, '%Y-%m-%d %H:%i'), DATE_FORMAT(updated_at, '%Y-%m-%d %H:%i')`

func (s *Server) loadBOCampaign(ctx context.Context, restaurantID int, id int64) (boCampaign, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+boCampaignColumns+` FROM campaigns WHERE restaurant_id = ? AND id = ? LIMIT 1`, restaurantID, id)
	c, err := scanBOCampaign(row.Scan)
	if err != nil {
		return boCampaign{}, err
	}
	c.Stats, _ = s.campaignStats(ctx, c.ID)
	return c, nil
}

func (s *Server) campaignStats(ctx context.Context, campaignID int64) (campaignStats, error) {
	var st campaignStats
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(status = 'sent'), 0),
		       COALESCE(SUM(status = 'failed'), 0),
		       COALESCE(SUM(status = 'pending'), 0)
		FROM campaign_recipients WHERE campaign_id = ?
	`, campaignID).Scan(&st.Total, &st.Sent, &st.Failed, &st.Pending)
	return st, err
}

func (s *Server) handleBOCampaignsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT `+boCampaignColumns+` FROM campaigns WHERE restaurant_id = ? ORDER BY updated_at DESC`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error cargando campanas"})
		return
	}
	defer rows.Close()
	out := []boCampaign{}
	for rows.Next() {
		c, err := scanBOCampaign(rows.Scan)
		if err != nil {
			continue
		}
		c.Stats, _ = s.campaignStats(r.Context(), c.ID)
		out = append(out, c)
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "campaigns": out})
}

func (s *Server) handleBOCampaignGet(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := parseChiPositiveInt64(r, "campaignId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Id invalido"})
		return
	}
	c, err := s.loadBOCampaign(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campana no encontrada"})
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "campaign": c})
}

func (s *Server) handleBOCampaignCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	var in boCampaignInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}
	in, err := normalizeCampaignInput(in)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": err.Error()})
		return
	}
	theme, _ := json.Marshal(in.Theme)
	manual, _ := json.Marshal(in.ManualRecipients)
	coordID := "camp-" + uuid.NewString()
	res, err := s.db.ExecContext(r.Context(), `
		INSERT INTO campaigns (restaurant_id, coord_id, name, subject, body_markdown, theme_json, channels, audience, audience_days, manual_recipients, email_per_minute, whatsapp_per_minute, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'draft')
	`, a.ActiveRestaurantID, coordID, in.Name, in.Subject, in.BodyMarkdown, string(theme), strings.Join(in.Channels, ","), in.Audience, in.AudienceDays, string(manual), in.EmailPerMinute, in.WhatsAppPerMin)
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error creando campana"})
		return
	}
	id, _ := res.LastInsertId()
	slog.Default().Info("campaign.created", "coord_id", coordID, "campaign_id", id, "restaurant_id", a.ActiveRestaurantID)
	c, err := s.loadBOCampaign(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error leyendo campana"})
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "campaign": c})
}

func (s *Server) handleBOCampaignUpdate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := parseChiPositiveInt64(r, "campaignId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Id invalido"})
		return
	}
	var in boCampaignInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}
	in, err = normalizeCampaignInput(in)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": err.Error()})
		return
	}
	theme, _ := json.Marshal(in.Theme)
	manual, _ := json.Marshal(in.ManualRecipients)
	if _, err := s.db.ExecContext(r.Context(), `
		UPDATE campaigns SET name = ?, subject = ?, body_markdown = ?, theme_json = ?, channels = ?, audience = ?, audience_days = ?, manual_recipients = ?,
			email_per_minute = ?, whatsapp_per_minute = ?
		WHERE restaurant_id = ? AND id = ?
	`, in.Name, in.Subject, in.BodyMarkdown, string(theme), strings.Join(in.Channels, ","), in.Audience, in.AudienceDays, string(manual), in.EmailPerMinute, in.WhatsAppPerMin, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error guardando campana"})
		return
	}
	c, err := s.loadBOCampaign(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campana no encontrada"})
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "campaign": c})
}

func (s *Server) handleBOCampaignDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := parseChiPositiveInt64(r, "campaignId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Id invalido"})
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM campaigns WHERE restaurant_id = ? AND id = ?`, a.ActiveRestaurantID, id); err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error borrando campana"})
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `DELETE FROM campaign_recipients WHERE restaurant_id = ? AND campaign_id = ?`, a.ActiveRestaurantID, id)
	httpx.WriteJSON(w, 200, map[string]any{"success": true})
}

// handleBOCampaignImageUpload stores an editor image in BunnyCDN and returns
// the pull URL so the markdown never carries blobs or base64.
func (s *Server) handleBOCampaignImageUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := parseChiPositiveInt64(r, "campaignId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Id invalido"})
		return
	}
	if !s.bunnyConfigured(r.Context(), a.ActiveRestaurantID) {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Almacenamiento de imagenes no configurado"})
		return
	}
	raw, filename, ct, err := readBOAdMultipartImage(r, specialmenuimage.MaxInputBytes)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Imagen invalida"})
		return
	}
	normalized, err := specialmenuimage.NormalizeToWebPWithLimit(r.Context(), raw, filename, ct, campaignMaxImageBytes)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "No se pudo procesar la imagen"})
		return
	}
	objectPath := path.Join(strconv.Itoa(a.ActiveRestaurantID), "pictures", "campaigns", strconv.FormatInt(id, 10), fmt.Sprintf("md-%d.webp", time.Now().UTC().UnixMilli()))
	if err := s.bunnyPut(r.Context(), a.ActiveRestaurantID, objectPath, normalized, "image/webp"); err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "No se pudo subir la imagen"})
		return
	}
	url := s.bunnyPullURL(r.Context(), a.ActiveRestaurantID, objectPath)
	slog.Default().Info("campaign.image.uploaded", "campaign_id", id, "restaurant_id", a.ActiveRestaurantID, "url", url)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "url": url})
}

// handleBOCampaignPreview renders both channel outputs without sending.
func (s *Server) handleBOCampaignPreview(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	var in boCampaignInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}
	branding, _ := s.loadRestaurantBranding(r.Context(), a.ActiveRestaurantID)
	httpx.WriteJSON(w, 200, map[string]any{
		"success":  true,
		"html":     renderCampaignEmailHTML(in.BodyMarkdown, in.Theme, branding.BrandName, branding.LogoURL),
		"whatsapp": renderCampaignWhatsAppText(in.BodyMarkdown),
	})
}

// campaignAudience resolves the recipient list for the campaign settings.
func (s *Server) campaignAudience(ctx context.Context, restaurantID int, c boCampaign) ([]campaignTarget, error) {
	wantsEmail := campaignHasChannel(c.Channels, "email")
	wantsWhatsApp := campaignHasChannel(c.Channels, "whatsapp")
	seen := map[string]bool{}
	out := []campaignTarget{}
	add := func(channel, target, name string, bookingID int64) {
		target = strings.TrimSpace(target)
		if target == "" || len(out) >= campaignMaxRecipients {
			return
		}
		if channel == "whatsapp" {
			target = normalizeWhatsAppNumber(target)
		} else if !strings.Contains(target, "@") {
			return
		}
		if target == "" {
			return
		}
		key := channel + "|" + strings.ToLower(target)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, campaignTarget{Channel: channel, Target: target, Name: strings.TrimSpace(name), BookingID: bookingID})
	}

	if c.Audience == "manual" {
		for _, raw := range c.ManualRecipients {
			if strings.Contains(raw, "@") {
				if wantsEmail {
					add("email", raw, "", 0)
				}
				continue
			}
			if wantsWhatsApp {
				add("whatsapp", raw, "", 0)
			}
		}
		return out, nil
	}

	// The newest booking per contact is the traceability anchor stored with
	// each recipient row (campaign id + booking id + channel).
	rows, err := s.db.QueryContext(ctx, `
		SELECT MAX(id), customer_name, COALESCE(contact_email, ''), COALESCE(contact_phone, '')
		FROM bookings
		WHERE restaurant_id = ? AND reservation_date >= DATE_SUB(CURDATE(), INTERVAL ? DAY)
		GROUP BY customer_name, contact_email, contact_phone
		ORDER BY MAX(id) DESC
	`, restaurantID, c.AudienceDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var bookingID int64
		var name, email, phone string
		if err := rows.Scan(&bookingID, &name, &email, &phone); err != nil {
			continue
		}
		if wantsEmail {
			add("email", email, name, bookingID)
		}
		if wantsWhatsApp {
			add("whatsapp", phone, name, bookingID)
		}
	}
	return out, rows.Err()
}

func (s *Server) handleBOCampaignAudience(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := parseChiPositiveInt64(r, "campaignId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Id invalido"})
		return
	}
	c, err := s.loadBOCampaign(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campana no encontrada"})
		return
	}
	targets, err := s.campaignAudience(r.Context(), a.ActiveRestaurantID, c)
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error calculando destinatarios"})
		return
	}
	emails, whats := 0, 0
	for _, t := range targets {
		if t.Channel == "email" {
			emails++
		} else {
			whats++
		}
	}
	sample := targets
	if len(sample) > 20 {
		sample = sample[:20]
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "coord_id": c.CoordID, "total": len(targets), "emails": emails, "whatsapp": whats, "sample": sample})
}

// handleBOCampaignTest sends one message to the operator-provided target.
func (s *Server) handleBOCampaignTest(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := parseChiPositiveInt64(r, "campaignId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Id invalido"})
		return
	}
	var body struct {
		Channel string `json:"channel"`
		Target  string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "JSON invalido"})
		return
	}
	c, err := s.loadBOCampaign(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campana no encontrada"})
		return
	}
	channel := strings.ToLower(strings.TrimSpace(body.Channel))
	if channel != "whatsapp" {
		channel = "email"
	}
	if err := s.deliverCampaignTo(r.Context(), a.ActiveRestaurantID, c, campaignTarget{Channel: channel, Target: body.Target}); err != nil {
		httpx.WriteJSON(w, 200, map[string]any{"success": false, "message": err.Error()})
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "coord_id": c.CoordID})
}

// deliverCampaignTo sends the campaign to a single target on its channel.
func (s *Server) deliverCampaignTo(ctx context.Context, restaurantID int, c boCampaign, target campaignTarget) error {
	if target.Channel == "whatsapp" {
		num := normalizeWhatsAppNumber(target.Target)
		if num == "" {
			return errors.New("telefono invalido")
		}
		// A markdown image becomes a real WhatsApp media message with the rest
		// of the body as caption; extra images stay as URLs inside the text.
		if imageURL, rest := splitCampaignLeadImage(c.BodyMarkdown); imageURL != "" {
			if gw, ok := s.botGatewayFor(ctx, restaurantID); ok {
				caption := renderCampaignWhatsAppText(rest)
				if err := gw.SendMedia(ctx, num, waMedia{Kind: "image", URL: imageURL, Caption: caption, Filename: "campana.webp"}); err == nil {
					return nil
				}
			}
		}
		text := renderCampaignWhatsAppText(c.BodyMarkdown)
		if err := s.sendWhatsAppMessage(ctx, restaurantID, num, text); err != nil {
			// Queue for retry so a provider hiccup never loses the message.
			_ = s.enqueueWhatsAppDelivery(ctx, restaurantID, "campaign", fmt.Sprintf("%s|%s", c.CoordID, num), num, whatsappOutboxPayload{Text: text}, err)
			return err
		}
		return nil
	}
	cfg, err := s.loadEmailProviderConfig(ctx, restaurantID)
	if err != nil {
		return fmt.Errorf("config de email: %w", err)
	}
	if cfg.ID == 0 || !cfg.IsActive {
		return errors.New("email no configurado")
	}
	branding, _ := s.loadRestaurantBranding(ctx, restaurantID)
	fromName := firstNonEmpty(branding.EmailFromName, branding.BrandName, "Restaurante")
	fromAddr := resolveEmailFromAddr(branding, cfg)
	subject := firstNonEmpty(c.Subject, c.Name)
	html := renderCampaignEmailHTML(c.BodyMarkdown, c.Theme, branding.BrandName, branding.LogoURL)
	return sendViaConfig(ctx, cfg, fromName, fromAddr, target.Target, subject, html)
}

// handleBOCampaignSend materializes the audience then drains it in background.
func (s *Server) handleBOCampaignSend(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := parseChiPositiveInt64(r, "campaignId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Id invalido"})
		return
	}
	c, err := s.loadBOCampaign(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campana no encontrada"})
		return
	}
	if c.Status == "sending" {
		httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": "La campana ya se esta enviando"})
		return
	}
	targets, err := s.campaignAudience(r.Context(), a.ActiveRestaurantID, c)
	if err != nil || len(targets) == 0 {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "No hay destinatarios"})
		return
	}
	for _, t := range targets {
		_, _ = s.db.ExecContext(r.Context(), `
			INSERT IGNORE INTO campaign_recipients (campaign_id, restaurant_id, channel, target, name, booking_id, status)
			VALUES (?, ?, ?, ?, ?, ?, 'pending')
		`, c.ID, a.ActiveRestaurantID, t.Channel, t.Target, t.Name, nullIfZeroInt64(t.BookingID))
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE campaigns SET status = 'sending' WHERE id = ?`, c.ID)
	slog.Default().Info("campaign.send.started", "coord_id", c.CoordID, "campaign_id", c.ID, "restaurant_id", a.ActiveRestaurantID, "targets", len(targets))
	go s.runCampaignSend(context.WithoutCancel(r.Context()), a.ActiveRestaurantID, c.ID)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "coord_id": c.CoordID, "queued": len(targets)})
}

// runCampaignSend walks the pending rows one by one, throttled so neither the
// SMTP server nor the WhatsApp gateway sees a burst.
func (s *Server) runCampaignSend(ctx context.Context, restaurantID int, campaignID int64) {
	c, err := s.loadBOCampaign(ctx, restaurantID, campaignID)
	if err != nil {
		return
	}
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT id, channel, target, name, COALESCE(booking_id, 0) FROM campaign_recipients WHERE campaign_id = ? AND status = 'pending' ORDER BY id LIMIT 50`, campaignID)
		if err != nil {
			break
		}
		type pending struct {
			id int64
			t  campaignTarget
		}
		batch := []pending{}
		for rows.Next() {
			var p pending
			if err := rows.Scan(&p.id, &p.t.Channel, &p.t.Target, &p.t.Name, &p.t.BookingID); err == nil {
				batch = append(batch, p)
			}
		}
		rows.Close()
		if len(batch) == 0 {
			break
		}
		for _, p := range batch {
			sendErr := s.deliverCampaignTo(ctx, restaurantID, c, p.t)
			if sendErr != nil {
				_, _ = s.db.ExecContext(ctx, `UPDATE campaign_recipients SET status = 'failed', error = ? WHERE id = ?`, truncate(sendErr.Error(), 500), p.id)
				slog.Default().Warn("campaign.delivery.failed", "coord_id", c.CoordID, "campaign_id", campaignID, "channel", p.t.Channel, "booking_id", p.t.BookingID, "err", sendErr.Error())
			} else {
				_, _ = s.db.ExecContext(ctx, `UPDATE campaign_recipients SET status = 'sent', error = NULL, sent_at = NOW() WHERE id = ?`, p.id)
				slog.Default().Info("campaign.delivery.sent", "coord_id", c.CoordID, "campaign_id", campaignID, "channel", p.t.Channel, "booking_id", p.t.BookingID)
			}
			if p.t.Channel == "whatsapp" {
				time.Sleep(campaignChannelPause(c.WhatsAppPerMin))
			} else {
				time.Sleep(campaignChannelPause(c.EmailPerMinute))
			}
		}
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE campaigns SET status = 'sent', sent_at = NOW() WHERE id = ?`, campaignID)
	st, _ := s.campaignStats(ctx, campaignID)
	slog.Default().Info("campaign.send.finished", "coord_id", c.CoordID, "campaign_id", campaignID, "sent", st.Sent, "failed", st.Failed)
}

func nullIfZeroInt64(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

// handleBOCampaignRecipients exposes the delivery ledger: which booking id was
// reached, on which channel, when, and with which error if it failed.
func (s *Server) handleBOCampaignRecipients(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := parseChiPositiveInt64(r, "campaignId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Id invalido"})
		return
	}
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	query := `SELECT id, channel, target, name, COALESCE(booking_id, 0), status, COALESCE(error, ''), COALESCE(DATE_FORMAT(sent_at, '%Y-%m-%d %H:%i'), '')
		FROM campaign_recipients WHERE restaurant_id = ? AND campaign_id = ?`
	args := []any{a.ActiveRestaurantID, id}
	if status == "sent" || status == "failed" || status == "pending" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC LIMIT 500`
	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error cargando destinatarios"})
		return
	}
	defer rows.Close()
	type recipientRow struct {
		ID        int64  `json:"id"`
		Channel   string `json:"channel"`
		Target    string `json:"target"`
		Name      string `json:"name"`
		BookingID int64  `json:"booking_id"`
		Status    string `json:"status"`
		Error     string `json:"error"`
		SentAt    string `json:"sent_at"`
	}
	out := []recipientRow{}
	for rows.Next() {
		var row recipientRow
		if err := rows.Scan(&row.ID, &row.Channel, &row.Target, &row.Name, &row.BookingID, &row.Status, &row.Error, &row.SentAt); err == nil {
			out = append(out, row)
		}
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "recipients": out})
}

func (s *Server) handleBOCampaignStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	id, err := parseChiPositiveInt64(r, "campaignId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Id invalido"})
		return
	}
	c, err := s.loadBOCampaign(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campana no encontrada"})
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "coord_id": c.CoordID, "status": c.Status, "stats": c.Stats})
}
