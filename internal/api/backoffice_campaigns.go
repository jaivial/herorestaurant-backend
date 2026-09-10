package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
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
	// Operator-facing pacing bounds, expressed in messages per minute. The
	// ceilings are the limits the legacy scripts ran with in production:
	// email (Titan SMTP) 300/hour => 5 per minute, WhatsApp 12/hour, which a
	// per-minute knob can only express as its floor (1) plus the hard 300s
	// spacing enforced in campaignChannelPause.
	campaignMinPerMinute            = 1
	campaignMaxEmailPerMinute       = 5
	campaignMaxWhatsAppPerMinute    = 1
	campaignDefaultEmailPerMinute   = 5
	campaignDefaultWhatsAppPerMinut = 1

	// send_whatsapp_campaign.php: $delayBetweenSends = 300 (12/hour, 300/day).
	campaignWhatsAppMinPause = 300 * time.Second
)

// campaignBookingsAudienceQuery is the audience source: the newest booking per
// contact inside the lookback window.
const campaignBookingsAudienceQuery = `
	SELECT MAX(id), customer_name, COALESCE(contact_email, ''), COALESCE(contact_phone, '')
	FROM bookings
	WHERE restaurant_id = ? AND reservation_date >= DATE_SUB(CURDATE(), INTERVAL ? DAY)
	GROUP BY customer_name, contact_email, contact_phone
	ORDER BY MAX(id) DESC`

// campaignSuppressionQuery is every do-not-contact target of a restaurant plus
// the legacy invalid_emails / invalid_phones / no_marketing rows. The legacy
// tables do not share one collation, so every branch is cast to the same one.
const campaignSuppressionQuery = `
	SELECT channel, target FROM (
		SELECT channel, CONVERT(target USING utf8mb4) COLLATE utf8mb4_unicode_ci AS target
		FROM campaign_suppressions WHERE restaurant_id = ?
		UNION ALL
		SELECT 'email', CONVERT(LOWER(TRIM(email)) USING utf8mb4) COLLATE utf8mb4_unicode_ci
		FROM invalid_emails WHERE TRIM(COALESCE(email, '')) <> ''
		UNION ALL
		SELECT 'whatsapp', CONVERT(TRIM(phone) USING utf8mb4) COLLATE utf8mb4_unicode_ci
		FROM invalid_phones WHERE TRIM(COALESCE(phone, '')) <> ''
		UNION ALL
		SELECT 'whatsapp', CONVERT(TRIM(contact_phone) USING utf8mb4) COLLATE utf8mb4_unicode_ci
		FROM no_marketing WHERE TRIM(COALESCE(contact_phone, '')) <> ''
		UNION ALL
		SELECT 'email', CONVERT(LOWER(TRIM(contact_email)) USING utf8mb4) COLLATE utf8mb4_unicode_ci
		FROM no_marketing WHERE TRIM(COALESCE(contact_email, '')) <> ''
	) suppressed`

// campaignChannelPause turns a per-minute rate into the delay between sends,
// clamped through clampCampaignRate. The whatsapp channel keeps a hard minimum
// spacing of 300s whatever the configured rate: the legacy
// send_whatsapp_campaign.php only allowed 12 messages per hour and 300s between
// sends, which no per-minute value can express.
func campaignChannelPause(channel string, perMinute int) time.Duration {
	fallback := campaignDefaultEmailPerMinute
	ceiling := campaignMaxEmailPerMinute
	if channel == "whatsapp" {
		fallback = campaignDefaultWhatsAppPerMinut
		ceiling = campaignMaxWhatsAppPerMinute
	}
	pause := time.Duration(float64(time.Minute) / float64(clampCampaignRate(perMinute, fallback, ceiling)))
	if channel == "whatsapp" && pause < campaignWhatsAppMinPause {
		return campaignWhatsAppMinPause
	}
	return pause
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
		return in, errors.New("El nombre de la campaña es obligatorio")
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
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error cargando campañas"})
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
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campaña no encontrada"})
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
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error creando campaña"})
		return
	}
	id, _ := res.LastInsertId()
	slog.Default().Info("campaign.created", "coord_id", coordID, "campaign_id", id, "restaurant_id", a.ActiveRestaurantID)
	c, err := s.loadBOCampaign(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error leyendo campaña"})
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
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error guardando campaña"})
		return
	}
	c, err := s.loadBOCampaign(r.Context(), a.ActiveRestaurantID, id)
	if err != nil {
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campaña no encontrada"})
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
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "Error borrando campaña"})
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

// campaignTemplateReferenceRestaurant is the restaurant whose transactional
// email design seeds every campaign preview.
const campaignTemplateReferenceRestaurant = 1

// handleBOCampaignTemplate returns the default email template: the reference
// restaurant's look plus the active restaurant's brand and logo.
func (s *Server) handleBOCampaignTemplate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	reference, _ := s.loadRestaurantBranding(r.Context(), campaignTemplateReferenceRestaurant)
	branding := reference
	if a.ActiveRestaurantID != campaignTemplateReferenceRestaurant {
		if own, err := s.loadRestaurantBranding(r.Context(), a.ActiveRestaurantID); err == nil {
			branding = own
		}
	}
	// Optional theme overrides let the editor refresh the shell when the user
	// tweaks colors, without ever building the markup on the client.
	q := r.URL.Query()
	width, _ := strconv.Atoi(q.Get("max_width"))
	theme := normalizeCampaignTheme(campaignTheme{
		Background: q.Get("background"),
		Surface:    q.Get("surface"),
		Text:       q.Get("text"),
		Accent:     q.Get("accent"),
		FontFamily: q.Get("font_family"),
		MaxWidth:   width,
		Align:      q.Get("align"),
	})
	brandName := firstNonEmpty(branding.BrandName, reference.BrandName, "Restaurante")
	logoURL := firstNonEmpty(branding.LogoURL, reference.LogoURL)
	// The restaurant own public site is the last-resort website: even without a
	// website saved in ConfigContacto the CTA and the opt-out button render.
	baseURL := s.campaignRestaurantBaseURL(r.Context(), a.ActiveRestaurantID)
	websiteURL := firstNonEmpty(strings.TrimSpace(branding.Website), strings.TrimSpace(reference.Website), baseURL)
	// The previews must show the opt-out button the recipient gets, so the shell
	// is built with a placeholder link (an unknown booking id inserts nothing).
	unsubscribeURL := campaignUnsubscribePreviewURL(campaignAbsoluteBase(websiteURL))
	httpx.WriteJSON(w, 200, map[string]any{
		"success":         true,
		"theme":           theme,
		"brand_name":      brandName,
		"logo_url":        logoURL,
		"website":         websiteURL,
		"unsubscribe_url": unsubscribeURL,
		// The editor renders the markdown locally and drops it in this exact
		// shell, so the preview matches the delivered email byte for byte.
		"shell":            campaignEmailShellWithUnsubscribe(theme, brandName, logoURL, campaignEmailBodyPlaceholder, websiteURL, unsubscribeURL),
		"body_placeholder": campaignEmailBodyPlaceholder,
	})
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
	// Same base as the editor shell: the restaurant own public site is the
	// last-resort website, and the opt-out link always rides on that base.
	siteURL := firstNonEmpty(strings.TrimSpace(branding.Website), s.campaignRestaurantBaseURL(r.Context(), a.ActiveRestaurantID))
	unsubscribeURL := campaignUnsubscribePreviewURL(siteURL)
	httpx.WriteJSON(w, 200, map[string]any{
		"success":  true,
		"html":     renderCampaignEmailHTMLWithUnsubscribe(in.BodyMarkdown, in.Theme, branding.BrandName, branding.LogoURL, siteURL, unsubscribeURL),
		"whatsapp": renderCampaignWhatsAppTextWithUnsubscribe(in.BodyMarkdown, branding.BrandName, siteURL, unsubscribeURL),
	})
}

// campaignSuppressionFilter is the do-not-contact list of a restaurant, keyed
// the way campaignAudience compares targets: emails lower-cased, phones through
// normalizeWhatsAppNumber. campaign_suppressions is the source of truth; the
// legacy invalid_emails / invalid_phones / no_marketing tables are still
// honoured so an old bounce or opt-out can never be re-contacted.
type campaignSuppressionFilter struct {
	emails map[string]bool
	phones map[string]bool
}

func (f campaignSuppressionFilter) blocked(channel, target string) bool {
	if channel == "whatsapp" {
		return f.phones[normalizeWhatsAppNumber(target)]
	}
	return f.emails[strings.ToLower(strings.TrimSpace(target))]
}

// loadCampaignSuppressionFilter reads every suppressed target of the restaurant
// plus the legacy bounce/opt-out rows (they carry no restaurant_id, exactly as
// the legacy send scripts used them globally).
func (s *Server) loadCampaignSuppressionFilter(ctx context.Context, restaurantID int) (campaignSuppressionFilter, error) {
	f := campaignSuppressionFilter{emails: map[string]bool{}, phones: map[string]bool{}}
	rows, err := s.db.QueryContext(ctx, campaignSuppressionQuery, restaurantID)
	if err != nil {
		return f, err
	}
	defer rows.Close()
	for rows.Next() {
		var channel, target string
		if err := rows.Scan(&channel, &target); err != nil {
			continue
		}
		if channel == "whatsapp" {
			if num := normalizeWhatsAppNumber(target); num != "" {
				f.phones[num] = true
			}
			continue
		}
		if mail := strings.ToLower(strings.TrimSpace(target)); mail != "" {
			f.emails[mail] = true
		}
	}
	return f, rows.Err()
}

// campaignAudience resolves the recipient list for the campaign settings. Both
// the bookings and the manual audience go through the suppression filter, so a
// hand-pasted list cannot bypass an unsubscribe.
func (s *Server) campaignAudience(ctx context.Context, restaurantID int, c boCampaign) ([]campaignTarget, error) {
	wantsEmail := campaignHasChannel(c.Channels, "email")
	wantsWhatsApp := campaignHasChannel(c.Channels, "whatsapp")
	suppressed, err := s.loadCampaignSuppressionFilter(ctx, restaurantID)
	if err != nil {
		return nil, err
	}
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
		if suppressed.blocked(channel, target) {
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
	rows, err := s.db.QueryContext(ctx, campaignBookingsAudienceQuery, restaurantID, c.AudienceDays)
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
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campaña no encontrada"})
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
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campaña no encontrada"})
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

// campaignNonPublicDomains are the dev-only hosts a restaurant_domains row can
// hold on a local run: an opt-out link on them can never be opened from a real
// phone, so they must not produce a footer.
var campaignNonPublicDomains = map[string]bool{
	"localhost": true,
	"127.0.0.1": true,
	"0.0.0.0":   true,
	"::1":       true,
}

// campaignPublicBaseURL turns a restaurant_domains row into the https base URL
// used to build per-recipient opt-out links. Empty, loopback or single-label
// hosts return "": the caller then omits the footer instead of rendering a
// link that cannot be opened.
func campaignPublicBaseURL(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if i := strings.Index(domain, "://"); i >= 0 {
		domain = domain[i+3:]
	}
	if i := strings.IndexAny(domain, "/?#"); i >= 0 {
		domain = domain[:i]
	}
	if i := strings.LastIndex(domain, ":"); i >= 0 {
		domain = domain[:i]
	}
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" || campaignNonPublicDomains[domain] || !strings.Contains(domain, ".") {
		return ""
	}
	// Loopback / LAN addresses are equally unreachable from a recipient phone.
	if ip := net.ParseIP(domain); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return ""
	}
	return "https://" + domain
}

// campaignRestaurantBaseURLCache memoizes the resolved public base URL per
// restaurant: deliverCampaignTo runs once per recipient, so restaurant_domains
// must be read once per send, not once per message.
var campaignRestaurantBaseURLCache sync.Map // restaurant id -> campaignDomainCacheEntry

// campaignDomainCacheTTL keeps a restaurant that publishes its domain later from
// waiting for a backend restart to get working CTAs.
const campaignDomainCacheTTL = 10 * time.Minute

type campaignDomainCacheEntry struct {
	baseURL   string
	expiresAt time.Time
}

// campaignRestaurantBaseURL resolves the public https base URL of the
// restaurant: the target of both the "Visita nuestra web" button and the
// per-recipient opt-out link. Domains are walked by priority (primary first) and
// the first host a recipient can actually open wins, so a dev-only row such as
// localhost (often the primary on a local install) can no longer blank out the
// campaign CTAs. Without a usable domain the shared booking fallback is used, so
// the buttons are never silently dropped.
func (s *Server) campaignRestaurantBaseURL(ctx context.Context, restaurantID int) string {
	if v, ok := campaignRestaurantBaseURLCache.Load(restaurantID); ok {
		if e, ok := v.(campaignDomainCacheEntry); ok && time.Now().Before(e.expiresAt) {
			return e.baseURL
		}
	}
	baseURL := ""
	rows, err := s.db.QueryContext(ctx,
		`SELECT domain FROM restaurant_domains WHERE restaurant_id = ? ORDER BY is_primary DESC, id ASC`,
		restaurantID,
	)
	if err == nil {
		for rows.Next() {
			var domain string
			if scanErr := rows.Scan(&domain); scanErr != nil {
				break
			}
			if candidate := campaignPublicBaseURL(domain); candidate != "" {
				baseURL = candidate
				break
			}
		}
		_ = rows.Close()
	}
	if baseURL == "" {
		baseURL = strings.TrimRight(publicBaseURLFromContext(ctx, s, restaurantID), "/")
	}
	campaignRestaurantBaseURLCache.Store(restaurantID, campaignDomainCacheEntry{baseURL: baseURL, expiresAt: time.Now().Add(campaignDomainCacheTTL)})
	return baseURL
}

// sendCampaignWhatsAppText is the campaign text path: tracked send plus outbox
// retry so a provider hiccup never loses the message. The text it is handed is
// already final: the website button path passes the bare render, the fallback
// passes appendCampaignWebsiteLine's output (the "Visita nuestra web: url"
// line). A missing gateway reproduces sendWhatsAppMessage's error.
func (s *Server) sendCampaignWhatsAppText(ctx context.Context, restaurantID int, gw WhatsAppGateway, coordID, num, text string) error {
	if gw == nil {
		return errors.New("whatsapp no configurado")
	}
	if err := s.sendWhatsAppTextTracked(ctx, restaurantID, gw, num, text, "backoffice_member_message"); err != nil {
		_ = s.enqueueWhatsAppDelivery(ctx, restaurantID, "campaign", fmt.Sprintf("%s|%s", coordID, num), num, whatsappOutboxPayload{Text: text}, err)
		return err
	}
	return nil
}

// deliverCampaignTo sends the campaign to a single target on its channel.
func (s *Server) deliverCampaignTo(ctx context.Context, restaurantID int, c boCampaign, target campaignTarget) error {
	// Per-recipient opt-out link: booking id + channel identify the recipient on
	// the public landing page (/baja-publicidad). The base URL is per restaurant
	// and memoized.
	// Website comes from the restaurant configuration; when none is saved the
	// restaurant own public domain is used, so both CTAs are always rendered.
	branding, _ := s.loadRestaurantBranding(ctx, restaurantID)
	siteURL := firstNonEmpty(strings.TrimSpace(branding.Website), s.campaignRestaurantBaseURL(ctx, restaurantID))
	// The opt-out link is published on the restaurant own website (the public
	// Preact app serves /baja-publicidad).
	unsubBase := campaignAbsoluteBase(siteURL)
	unsubscribeURL := campaignUnsubscribeURL(unsubBase, target.BookingID, target.Channel, target.Target)
	// Observational point: proves both CTAs were attached to the delivered message.
	slog.Default().Info("campaign.delivery.links", "coord_id", c.CoordID, "channel", target.Channel,
		"booking_id", target.BookingID, "website_button", strings.TrimSpace(siteURL) != "", "optout_button", unsubscribeURL != "")
	if target.Channel == "whatsapp" {
		num := normalizeWhatsAppNumber(target.Target)
		if num == "" {
			return errors.New("telefono invalido")
		}
		// The restaurant website travels as a native WhatsApp button
		// ("label|url" -> call-to-action URL button on Evolution, choices as-is
		// on UAZAPI) instead of a plain-text line, so the text/caption keeps
		// only header + body + opt-out link. Without a gateway configured the
		// button is impossible and the link stays in the text as before.
		gw, gwOK := s.botGatewayFor(ctx, restaurantID)
		// Website and opt-out travel as interactive buttons of the message, so
		// the text keeps only the brand header, the body and nothing else.
		choices := campaignWhatsAppChoices(siteURL, unsubscribeURL)
		// Text every fallback reuses: same message with both links back as
		// plain-text lines, so no link is ever lost.
		fallback := appendCampaignUnsubscribeLine(
			appendCampaignWebsiteLine(renderCampaignWhatsAppBody(c.BodyMarkdown, branding.BrandName, "", false), siteURL),
			unsubscribeURL)
		// A markdown image becomes a real WhatsApp media message with the rest
		// of the body as caption; extra images stay as URLs inside the text.
		// Evolution cannot mix media and buttons in one call, so the button
		// message follows the image.
		if imageURL, rest := splitCampaignLeadImage(c.BodyMarkdown); imageURL != "" && gwOK {
			caption := renderCampaignWhatsAppBody(rest, branding.BrandName, "", false)
			if err := gw.SendMedia(ctx, num, waMedia{Kind: "image", URL: imageURL, Caption: caption, Filename: "campana.webp"}); err == nil {
				if len(choices) == 0 {
					return nil
				}
				// The image caption already carries the message, so the button
				// message keeps a short header instead of repeating the whole
				// caption the recipient just read.
				buttonBody := strings.TrimSpace(branding.BrandName)
				if buttonBody != "" {
					if err := gw.SendMenu(ctx, num, buttonBody, choices); err == nil {
						return nil
					}
				}
				return s.sendCampaignWhatsAppText(ctx, restaurantID, gw, c.CoordID, num,
					appendCampaignUnsubscribeLine(appendCampaignWebsiteLine(caption, siteURL), unsubscribeURL))
			}
		}
		text := renderCampaignWhatsAppBody(c.BodyMarkdown, branding.BrandName, "", false)
		if len(choices) > 0 {
			if gwOK {
				if err := gw.SendMenu(ctx, num, text, choices); err == nil {
					return nil
				}
			}
			return s.sendCampaignWhatsAppText(ctx, restaurantID, gw, c.CoordID, num, fallback)
		}
		return s.sendCampaignWhatsAppText(ctx, restaurantID, gw, c.CoordID, num, text)
	}
	cfg, err := s.loadEmailProviderConfig(ctx, restaurantID)
	if err != nil {
		return fmt.Errorf("config de email: %w", err)
	}
	if cfg.ID == 0 || !cfg.IsActive {
		return errors.New("email no configurado")
	}
	fromName := firstNonEmpty(branding.EmailFromName, branding.BrandName, "Restaurante")
	fromAddr := resolveEmailFromAddr(branding, cfg)
	subject := firstNonEmpty(c.Subject, c.Name)
	html := renderCampaignEmailHTMLWithUnsubscribe(c.BodyMarkdown, c.Theme, branding.BrandName, branding.LogoURL, siteURL, unsubscribeURL)
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
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campaña no encontrada"})
		return
	}
	if c.Status == "sending" {
		httpx.WriteJSON(w, 409, map[string]any{"success": false, "message": "La campaña ya se esta enviando"})
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
				time.Sleep(campaignChannelPause("whatsapp", c.WhatsAppPerMin))
			} else {
				time.Sleep(campaignChannelPause("email", c.EmailPerMinute))
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
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Campaña no encontrada"})
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "coord_id": c.CoordID, "status": c.Status, "stats": c.Stats})
}

// handleBOCampaignUnsubscribed lists, paginated, every booking that opted out
// of marketing, with the booking details the operator needs to recognise it.
// Read only: rows are added by the recipient through the public landing page,
// never here.
func (s *Server) handleBOCampaignUnsubscribed(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	const campaignUnsubscribedPageSize = 10
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * campaignUnsubscribedPageSize

	var total int
	if err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM campaign_suppressions
		WHERE restaurant_id = ? AND booking_id > 0
	`, a.ActiveRestaurantID).Scan(&total); err != nil {
		slog.Default().Warn("campaign.unsubscribed.count_failed", "coord_id", campaignUnsubscribedCoordID,
			"restaurant_id", a.ActiveRestaurantID, "err", err.Error())
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo listar las bajas")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT s.booking_id,
		       COALESCE(NULLIF(TRIM(b.customer_name), ''), '')        AS customer_name,
		       COALESCE(NULLIF(TRIM(s.target), ''), '')               AS target,
		       s.channel,
		       COALESCE(NULLIF(TRIM(s.reason), ''), '')               AS reason,
		       s.created_at,
		       COALESCE(b.reservation_date, DATE(s.created_at))       AS reservation_date,
		       COALESCE(b.party_size, 0)                              AS party_size
		FROM campaign_suppressions s
		LEFT JOIN bookings b ON b.id = s.booking_id AND b.restaurant_id = s.restaurant_id
		WHERE s.restaurant_id = ? AND s.booking_id > 0
		ORDER BY s.created_at DESC, s.id DESC
		LIMIT ? OFFSET ?
	`, a.ActiveRestaurantID, campaignUnsubscribedPageSize, offset)
	if err != nil {
		slog.Default().Warn("campaign.unsubscribed.list_failed", "coord_id", campaignUnsubscribedCoordID,
			"restaurant_id", a.ActiveRestaurantID, "err", err.Error())
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo listar las bajas")
		return
	}
	defer rows.Close()

	type campaignUnsubscribedRow struct {
		BookingID       int64  `json:"booking_id"`
		CustomerName    string `json:"customer_name"`
		Contact         string `json:"contact"`
		Channel         string `json:"channel"`
		Reason          string `json:"reason"`
		Since           string `json:"since"`
		ReservationDate string `json:"reservation_date"`
		PartySize       int    `json:"party_size"`
	}
	items := make([]campaignUnsubscribedRow, 0)
	for rows.Next() {
		var row campaignUnsubscribedRow
		var (
			unsubAt    time.Time
			reservedAt sql.NullTime
			partySize  sql.NullInt64
		)
		if err := rows.Scan(&row.BookingID, &row.CustomerName, &row.Contact, &row.Channel, &row.Reason,
			&unsubAt, &reservedAt, &partySize); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "No se pudo listar las bajas")
			return
		}
		row.Since = unsubAt.Format("02/01/2006")
		if reservedAt.Valid {
			row.ReservationDate = reservedAt.Time.Format("02/01/2006")
		}
		row.PartySize = int(partySize.Int64)
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo listar las bajas")
		return
	}

	totalPages := (total + campaignUnsubscribedPageSize - 1) / campaignUnsubscribedPageSize
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"items":       items,
		"page":        page,
		"page_size":   campaignUnsubscribedPageSize,
		"total":       total,
		"total_pages": totalPages,
	})
}
