package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"preactvillacarmen/internal/vault"
)

// Coordination id: google_reviews_ai_v1
//
// Daily Google review assistant. Per restaurant: refresh the OAuth access
// token, import the newest reviews of the chosen Business Profile location,
// draft a reply for every unanswered one with the restaurant AI model, publish
// on its own the drafts at or above the configured star threshold (recent
// reviews only) and leave the rest as drafts for the owner to approve in the
// backoffice. The management WhatsApp group gets a one-line summary.

const (
	googleReviewsDraftsPerSync   = 15
	googleReviewsAutoPublishDays = 14
	googleReviewsDraftTimeout    = 45 * time.Second
	googleReviewsMaxReplyRunes   = 3500
	googleReviewsSyncTimeout     = 15 * time.Minute
	googleReviewsDraftAttempts   = 3
)

type googleBusinessConfig struct {
	RestaurantID        int
	Email               string
	RefreshToken        string
	Account             string
	Location            string
	LocationTitle       string
	AutoPublishMinStars int
	Formality           string
	Signature           string
	ComplaintContact    string
	SyncEnabled         bool
	LastSyncAt          *time.Time
	LastSyncError       string
}

func (c googleBusinessConfig) connected() bool { return c.RefreshToken != "" }

type googleReviewRow struct {
	ID          int64      `json:"id"`
	Reviewer    string     `json:"reviewer"`
	Stars       int        `json:"stars"`
	Comment     string     `json:"comment"`
	CreatedAt   *time.Time `json:"createdAt"`
	Reply       string     `json:"reply"`
	Status      string     `json:"status"`
	Error       string     `json:"error"`
	PublishedAt *time.Time `json:"publishedAt"`
}

type googleReviewsSummary struct {
	Imported  int `json:"imported"`
	New       int `json:"new"`
	Drafted   int `json:"drafted"`
	Published int `json:"published"`
	Pending   int `json:"pending"`
}

// googleReviewsRunning keeps one sync per restaurant at a time (button + loop).
var googleReviewsRunning sync.Map

func (s *Server) loadGoogleBusiness(ctx context.Context, restaurantID int) (googleBusinessConfig, error) {
	cfg := googleBusinessConfig{RestaurantID: restaurantID, AutoPublishMinStars: 0, Formality: "tu", SyncEnabled: true}
	var enc sql.NullString
	var last sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT google_email, refresh_token_encrypted, account_name, location_name, location_title,
		auto_publish_min_stars, formality, signature, complaint_contact, sync_enabled, last_sync_at, last_sync_error
		FROM restaurant_google_business WHERE restaurant_id = ?`, restaurantID).
		Scan(&cfg.Email, &enc, &cfg.Account, &cfg.Location, &cfg.LocationTitle, &cfg.AutoPublishMinStars, &cfg.Formality,
			&cfg.Signature, &cfg.ComplaintContact, &cfg.SyncEnabled, &last, &cfg.LastSyncError)
	if errors.Is(err, sql.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if last.Valid {
		cfg.LastSyncAt = &last.Time
	}
	if enc.Valid && enc.String != "" {
		plain, derr := vault.Decrypt(s.cfg.VaultToken, enc.String)
		if derr != nil {
			slog.Default().Warn("google_reviews.token_decrypt_failed", "restaurant_id", restaurantID, "err", derr.Error())
		} else {
			cfg.RefreshToken = plain
		}
	}
	return cfg, nil
}

// googleReviewsAccessToken returns a fresh access token for a connected
// restaurant with a chosen location.
func (s *Server) googleReviewsAccessToken(ctx context.Context, cfg googleBusinessConfig) (string, error) {
	if !s.googleOAuthConfigured() {
		return "", errors.New("Google no está configurado en el servidor (GOOGLE_CLIENT_ID/SECRET)")
	}
	if !cfg.connected() {
		return "", errors.New("Conecta primero una cuenta de Google")
	}
	if cfg.Location == "" {
		return "", errors.New("Elige primero la ficha de Google Business")
	}
	return s.googleAccessToken(ctx, cfg.RefreshToken)
}

func (s *Server) googleReviewsSync(ctx context.Context, restaurantID int) (googleReviewsSummary, error) {
	var sum googleReviewsSummary
	if _, busy := googleReviewsRunning.LoadOrStore(restaurantID, true); busy {
		return sum, errors.New("Ya hay una revisión en curso")
	}
	defer googleReviewsRunning.Delete(restaurantID)
	cfg, err := s.loadGoogleBusiness(ctx, restaurantID)
	if err != nil {
		return sum, err
	}
	sum, err = s.googleReviewsSyncRun(ctx, cfg)
	errText := ""
	if err != nil {
		errText = truncateRunes(err.Error(), 480)
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE restaurant_google_business SET last_sync_at = UTC_TIMESTAMP(), last_sync_error = ? WHERE restaurant_id = ?`, errText, restaurantID)
	slog.Default().Info("google_reviews.sync", "coord_id", "google_reviews_ai_v1", "restaurant_id", restaurantID,
		"imported", sum.Imported, "new", sum.New, "drafted", sum.Drafted, "published", sum.Published, "pending", sum.Pending, "err", errText)
	if err == nil && (sum.New > 0 || sum.Published > 0) {
		s.googleReviewsNotify(ctx, restaurantID, sum)
	}
	return sum, err
}

func (s *Server) googleReviewsSyncRun(ctx context.Context, cfg googleBusinessConfig) (googleReviewsSummary, error) {
	var sum googleReviewsSummary
	token, err := s.googleReviewsAccessToken(ctx, cfg)
	if err != nil {
		return sum, err
	}
	pages := 2
	if cfg.LastSyncAt == nil {
		pages = 10 // first import: the latest 500 reviews
	}
	reviews, err := googleListReviews(ctx, token, cfg.Account, cfg.Location, pages)
	if err != nil {
		return sum, err
	}
	for _, rv := range reviews {
		isNew, err := s.googleReviewUpsert(ctx, cfg.RestaurantID, rv)
		if err != nil {
			return sum, err
		}
		sum.Imported++
		if isNew {
			sum.New++
		}
	}
	drafted, err := s.googleReviewsDraftPending(ctx, cfg, googleReviewsDraftsPerSync)
	sum.Drafted = drafted
	if err != nil {
		return sum, err
	}
	if cfg.AutoPublishMinStars > 0 {
		rows, err := s.db.QueryContext(ctx, `SELECT id, reply FROM google_reviews WHERE restaurant_id = ? AND status = 'draft'
			AND error = '' AND star_rating >= ? AND review_created_at >= UTC_TIMESTAMP() - INTERVAL ? DAY`,
			cfg.RestaurantID, cfg.AutoPublishMinStars, googleReviewsAutoPublishDays)
		if err != nil {
			return sum, err
		}
		type pick struct {
			id    int64
			reply string
		}
		var picks []pick
		for rows.Next() {
			var p pick
			if rows.Scan(&p.id, &p.reply) == nil {
				picks = append(picks, p)
			}
		}
		_ = rows.Close()
		for _, p := range picks {
			if s.googleReviewPublish(ctx, cfg, token, p.id, p.reply) == nil {
				sum.Published++
			}
		}
	}
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM google_reviews WHERE restaurant_id = ? AND status IN ('pending','draft')`, cfg.RestaurantID).Scan(&sum.Pending)
	return sum, nil
}

// googleReviewUpsert stores one review. A reply already present on Google
// marks it published; a reply removed on Google sends it back to pending.
func (s *Server) googleReviewUpsert(ctx context.Context, restaurantID int, rv googleReview) (bool, error) {
	googleReply := ""
	if rv.ReviewReply != nil {
		googleReply = strings.TrimSpace(rv.ReviewReply.Comment)
	}
	status := "pending"
	if googleReply != "" {
		status = "published"
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO google_reviews
		(restaurant_id, review_name, reviewer_name, star_rating, comment, review_created_at, review_updated_at, reply, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)
		ON DUPLICATE KEY UPDATE reviewer_name = VALUES(reviewer_name), star_rating = VALUES(star_rating),
		  comment = VALUES(comment), review_updated_at = VALUES(review_updated_at),
		  reply = IF(VALUES(status) = 'published', VALUES(reply), IF(status = 'published', NULL, reply)),
		  status = IF(VALUES(status) = 'published', 'published', IF(status = 'published', 'pending', status))`,
		restaurantID, rv.Name, strings.TrimSpace(rv.Reviewer.DisplayName), googleStarRatings[rv.StarRating],
		strings.TrimSpace(rv.Comment), googleParseTime(rv.CreateTime), googleParseTime(rv.UpdateTime), googleReply, status)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	// "New" means new AND unanswered: the first import brings hundreds of
	// reviews that already carry an owner reply.
	return n == 1 && googleReply == "", nil
}

func googleParseTime(v string) any {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return nil
	}
	return t.UTC()
}

// googleReviewsDraftPending writes a draft for the newest pending reviews.
func (s *Server) googleReviewsDraftPending(ctx context.Context, cfg googleBusinessConfig, limit int) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM google_reviews WHERE restaurant_id = ? AND status = 'pending'
		ORDER BY review_created_at DESC LIMIT ?`, cfg.RestaurantID, limit)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	drafted := 0
	for _, id := range ids {
		if _, err := s.googleReviewDraft(ctx, cfg, id, ""); err != nil {
			return drafted, err
		}
		drafted++
	}
	return drafted, nil
}

func (s *Server) loadGoogleReview(ctx context.Context, restaurantID int, id int64) (googleReviewRow, error) {
	return scanGoogleReview(s.db.QueryRowContext(ctx, `SELECT `+googleReviewColumns+` FROM google_reviews WHERE id = ? AND restaurant_id = ?`, id, restaurantID).Scan)
}

const googleReviewColumns = `id, reviewer_name, star_rating, comment, review_created_at, reply, status, error, published_at`

// scanGoogleReview reads one row selected with googleReviewColumns.
func scanGoogleReview(scan func(dest ...any) error) (googleReviewRow, error) {
	var r googleReviewRow
	var comment, reply sql.NullString
	var created, published sql.NullTime
	err := scan(&r.ID, &r.Reviewer, &r.Stars, &comment, &created, &reply, &r.Status, &r.Error, &published)
	r.Comment, r.Reply = comment.String, reply.String
	if created.Valid {
		r.CreatedAt = &created.Time
	}
	if published.Valid {
		r.PublishedAt = &published.Time
	}
	return r, err
}

// googleReviewDraft asks the restaurant AI model for a reply and stores it as
// a draft. instruction is an optional owner hint ("más corto", "más formal").
func (s *Server) googleReviewDraft(ctx context.Context, cfg googleBusinessConfig, id int64, instruction string) (string, error) {
	rv, err := s.loadGoogleReview(ctx, cfg.RestaurantID, id)
	if err != nil {
		return "", err
	}
	_, ref := s.campaignAIModels(ctx, cfg.RestaurantID)
	if ref == "" {
		return "", errors.New("No hay ningún modelo de IA configurado para este restaurante")
	}
	branding, _ := s.loadRestaurantBranding(ctx, cfg.RestaurantID)
	brand := firstNonEmpty(strings.TrimSpace(branding.BrandName), "el restaurante")
	signature := firstNonEmpty(strings.TrimSpace(cfg.Signature), "El equipo de "+brand)
	treatment := "tutea al cliente (tú)"
	if cfg.Formality == "usted" {
		treatment = "trata al cliente de usted"
	}
	contact := "invítale a escribir o llamar al restaurante para hablarlo en privado"
	if c := strings.TrimSpace(cfg.ComplaintContact); c != "" {
		contact = "invítale a contactar en privado en " + c
	}
	system := "Eres quien responde, en nombre de " + brand + ", a las reseñas de Google del restaurante. Escribe SOLO el texto de la respuesta, sin comillas ni explicaciones.\n" +
		"Reglas:\n" +
		"- Responde en el mismo idioma en que está escrita la reseña (si no hay texto, en español). Si el texto viene con una traducción de Google, usa el idioma original.\n" +
		"- " + treatment + " cuando escribas en español. Usa el nombre de pila del cliente solo si suena natural.\n" +
		"- Menciona algo concreto de lo que cuenta (un plato, la celebración, el servicio). Nada de frases de plantilla ni de repetir siempre el mismo inicio.\n" +
		"- 4-5 estrellas: agradece de forma cercana y breve (2-4 frases) e invita a volver.\n" +
		"- 3 estrellas: agradece, reconoce con tacto la pega concreta y explica que se tendrá en cuenta.\n" +
		"- 1-2 estrellas: pide disculpas por la experiencia sin discutir ni contradecir al cliente, sin reconocer culpas legales ni dar detalles internos, y " + contact + ".\n" +
		"- Si solo hay estrellas y ningún texto, una o dos frases de agradecimiento.\n" +
		"- Nunca inventes hechos, nombres de empleados, platos ni datos que no estén en la reseña. Nunca ofrezcas descuentos, invitaciones, reembolsos ni compensaciones. Nunca incluyas datos personales ni enlaces.\n" +
		"- Como mucho un emoji y solo en reseñas positivas. Máximo 900 caracteres.\n" +
		"- Termina con la firma en una línea aparte: " + signature
	payload := map[string]any{"estrellas": rv.Stars, "cliente": rv.Reviewer, "reseña": rv.Comment}
	if h := strings.TrimSpace(instruction); h != "" {
		payload["indicación_del_propietario"] = truncateRunes(h, 300)
		if strings.TrimSpace(rv.Reply) != "" {
			payload["borrador_anterior"] = rv.Reply
		}
	}
	raw, _ := json.Marshal(payload)
	coordID := fmt.Sprintf("google_reviews_ai_v1:%d:%d", cfg.RestaurantID, id)
	// MiniMax sometimes bleeds tokens of other languages or scripts into a
	// sentence ("Gracias porailable a la paella", CJK fragments). Every draft
	// gets a proofreading pass; glyphs that cannot belong to the reply trigger
	// a fresh attempt, and a draft still suspicious after the last one keeps an
	// error so it is never published on its own (google_reviews_quality_v1).
	text, suspicious := "", true
	for attempt := 1; attempt <= googleReviewsDraftAttempts && suspicious; attempt++ {
		draft, err := s.googleReviewsLLMText(ctx, cfg.RestaurantID, ref, coordID, system, string(raw))
		if err != nil {
			return "", err
		}
		proof, _ := json.Marshal(map[string]string{"reseña": rv.Comment, "respuesta": draft})
		text, err = s.googleReviewsLLMText(ctx, cfg.RestaurantID, ref, coordID, googleReviewsProofreadPrompt, string(proof))
		if err != nil || text == "" {
			text = draft
		}
		suspicious = googleReplySuspicious(text, rv.Comment)
		slog.Default().Info("google_reviews.draft", "coord_id", "google_reviews_quality_v1", "restaurant_id", cfg.RestaurantID, "review_id", id, "attempt", attempt, "suspicious", suspicious)
	}
	note := ""
	if suspicious {
		text = assistantCleanseReply(text)
		note = "Revisa el texto antes de publicarlo: la IA devolvió caracteres extraños."
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE google_reviews SET reply = ?, status = 'draft', error = ? WHERE id = ? AND restaurant_id = ? AND status <> 'published'`,
		text, note, id, cfg.RestaurantID); err != nil {
		return "", err
	}
	return text, nil
}

const googleReviewsProofreadPrompt = "Eres corrector de textos. Te paso la respuesta que un restaurante va a publicar a una reseña de Google. " +
	"Corrige SOLO: palabras cortadas, pegadas o deformadas, palabras sueltas de otro idioma o de otro alfabeto que no encajen en la frase, y erratas. " +
	"Mantén el idioma de la respuesta, el sentido, el tono, los saltos de línea y la firma final. No añadas ni quites frases. " +
	"Devuelve SOLO el texto corregido, sin comillas ni explicaciones."

// googleReviewsLLMText runs one call with the restaurant model and returns its
// text (usage recorded under the google_reviews feature).
func (s *Server) googleReviewsLLMText(ctx context.Context, restaurantID int, ref, coordID, system, user string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, googleReviewsDraftTimeout)
	defer cancel()
	resp, err := s.botCallModelRef(withBotCallTimeout(callCtx, googleReviewsDraftTimeout), restaurantID, ref, coordID, system,
		[]botMessage{{Role: "user", Content: []botBlock{{Type: "text", Text: user}}}}, nil, 1200)
	if err != nil {
		return "", fmt.Errorf("IA: %w", err)
	}
	provider, model := botSplitModelRef(ref)
	s.recordAIUsage(ctx, restaurantID, aiFeatureGoogleReviews, provider, model,
		helperAnthropicUsage(resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheReadInputTokens, resp.Usage.CacheCreationInputTokens), false)
	text := ""
	for _, b := range resp.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	text = truncateRunes(strings.Trim(strings.TrimSpace(text), "\"“”"), googleReviewsMaxReplyRunes)
	if text == "" {
		return "", errors.New("La IA no devolvió ninguna respuesta")
	}
	return text, nil
}

// googleReplySuspicious reports MiniMax glitch glyphs (CJK, filler symbols)
// in the reply that the review itself does not contain. A review written in a
// CJK script is answered in that script, so it is never checked.
func googleReplySuspicious(reply, review string) bool {
	for _, r := range review {
		if assistantMinimaxGlitch(r) {
			return false
		}
	}
	for _, r := range reply {
		if assistantMinimaxGlitch(r) && !strings.ContainsRune(review, r) {
			return true
		}
	}
	return false
}

// googleReviewPublish sends the reply to Google. On failure the review stays
// a draft with the Google error, so it can be retried from the backoffice.
func (s *Server) googleReviewPublish(ctx context.Context, cfg googleBusinessConfig, accessToken string, id int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("La respuesta está vacía")
	}
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT review_name FROM google_reviews WHERE id = ? AND restaurant_id = ?`, id, cfg.RestaurantID).Scan(&name); err != nil {
		return err
	}
	if err := googlePutReply(ctx, accessToken, name, text); err != nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE google_reviews SET reply = ?, status = 'draft', error = ? WHERE id = ? AND restaurant_id = ?`, text, truncateRunes(err.Error(), 480), id, cfg.RestaurantID)
		slog.Default().Warn("google_reviews.publish_failed", "coord_id", "google_reviews_ai_v1", "restaurant_id", cfg.RestaurantID, "review_id", id, "err", err.Error())
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE google_reviews SET reply = ?, status = 'published', error = '', published_at = UTC_TIMESTAMP() WHERE id = ? AND restaurant_id = ?`, text, id, cfg.RestaurantID)
	slog.Default().Info("google_reviews.published", "coord_id", "google_reviews_ai_v1", "restaurant_id", cfg.RestaurantID, "review_id", id)
	return err
}

func (s *Server) googleReviewsNotify(ctx context.Context, restaurantID int, sum googleReviewsSummary) {
	gw, ok := s.botGatewayFor(ctx, restaurantID)
	if !ok {
		return
	}
	jid := s.botManagementGroupJID(ctx, restaurantID, s.loadBotTenantConfig(ctx, restaurantID))
	if jid == "" {
		return
	}
	text := fmt.Sprintf("⭐ Reseñas de Google\nNuevas: %d · Publicadas automáticamente: %d · Pendientes de revisar: %d\nRevísalas en Backoffice → Configuración → Reseñas Google.",
		sum.New, sum.Published, sum.Pending)
	if err := gw.SendText(ctx, jid, text); err != nil {
		slog.Default().Warn("google_reviews.notify_failed", "restaurant_id", restaurantID, "err", err.Error())
	}
}

// runGoogleReviewsLoop syncs once a day, at GOOGLE_REVIEWS_SYNC_HOUR (Madrid),
// every connected restaurant with the daily review enabled.
func (s *Server) runGoogleReviewsLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Now().In(boMadridTZ).Hour() != s.cfg.GoogleReviewsSyncHour || !s.googleOAuthConfigured() {
				continue
			}
			rows, err := s.db.QueryContext(ctx, `SELECT restaurant_id FROM restaurant_google_business
				WHERE sync_enabled = 1 AND refresh_token_encrypted IS NOT NULL AND location_name <> ''
				AND (last_sync_at IS NULL OR last_sync_at < UTC_TIMESTAMP() - INTERVAL 20 HOUR)`)
			if err != nil {
				continue
			}
			var ids []int
			for rows.Next() {
				var id int
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			_ = rows.Close()
			for _, id := range ids {
				syncCtx, cancel := context.WithTimeout(ctx, googleReviewsSyncTimeout)
				_, _ = s.googleReviewsSync(syncCtx, id)
				cancel()
			}
		}
	}
}
