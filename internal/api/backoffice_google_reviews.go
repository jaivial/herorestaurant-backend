package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/vault"
)

// Coordination id: google_reviews_ai_v1 - backoffice endpoints (root only)
// under /admin/config/google-reviews: connection (OAuth + location), reply
// settings, manual sync and the per-review draft / publish / skip actions.

const googleReviewsConfigPath = "/app/config?content=resenas"

type googleReviewsStatusDTO struct {
	Configured          bool       `json:"configured"`
	Connected           bool       `json:"connected"`
	Email               string     `json:"email"`
	Location            string     `json:"location"`
	LocationTitle       string     `json:"locationTitle"`
	AutoPublishMinStars int        `json:"autoPublishMinStars"`
	Formality           string     `json:"formality"`
	Signature           string     `json:"signature"`
	ComplaintContact    string     `json:"complaintContact"`
	SyncEnabled         bool       `json:"syncEnabled"`
	SyncHour            int        `json:"syncHour"`
	LastSyncAt          *time.Time `json:"lastSyncAt"`
	LastSyncError       string     `json:"lastSyncError"`
	RedirectURI         string     `json:"redirectUri"`
}

func boJSONError(w http.ResponseWriter, status int, msg string) {
	httpx.WriteJSON(w, status, map[string]any{"success": false, "message": msg})
}

// googleRedirectURI is the OAuth callback as the browser sees it (through the
// backoffice proxy). GOOGLE_OAUTH_REDIRECT_URL wins; otherwise it is derived
// from the forwarded host.
func (s *Server) googleRedirectURI(r *http.Request) string {
	if u := strings.TrimSpace(s.cfg.GoogleOAuthRedirectURL); u != "" {
		return u
	}
	host := firstNonEmpty(r.Header.Get("X-Forwarded-Host"), r.Host)
	proto := firstNonEmpty(r.Header.Get("X-Forwarded-Proto"), "https")
	return proto + "://" + host + "/api/admin/config/google-reviews/oauth/callback"
}

// googleOAuthState signs "restaurant:user:expiry" so the callback can only
// bind the Google account to the restaurant and user that started the flow.
func (s *Server) googleOAuthState(restaurantID, userID int) string {
	payload := fmt.Sprintf("%d:%d:%d", restaurantID, userID, time.Now().Add(15*time.Minute).Unix())
	mac := hmac.New(sha256.New, []byte("google_reviews_ai_v1:"+s.cfg.VaultToken))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) googleOAuthStateValid(state string, restaurantID, userID int) bool {
	p, sig, ok := strings.Cut(state, ".")
	if !ok {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte("google_reviews_ai_v1:"+s.cfg.VaultToken))
	mac.Write(payload)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, mac.Sum(nil)) {
		return false
	}
	parts := strings.Split(string(payload), ":")
	if len(parts) != 3 {
		return false
	}
	exp, _ := strconv.ParseInt(parts[2], 10, 64)
	return parts[0] == strconv.Itoa(restaurantID) && parts[1] == strconv.Itoa(userID) && time.Now().Unix() < exp
}

func (s *Server) handleBOGoogleReviewsStatus(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	cfg, err := s.loadGoogleBusiness(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		boJSONError(w, http.StatusInternalServerError, "Error cargando la conexión con Google")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "status": googleReviewsStatusDTO{
		Configured: s.googleOAuthConfigured(), Connected: cfg.connected(), Email: cfg.Email,
		Location: cfg.Location, LocationTitle: cfg.LocationTitle, AutoPublishMinStars: cfg.AutoPublishMinStars,
		Formality: cfg.Formality, Signature: cfg.Signature, ComplaintContact: cfg.ComplaintContact,
		SyncEnabled: cfg.SyncEnabled, SyncHour: s.cfg.GoogleReviewsSyncHour, LastSyncAt: cfg.LastSyncAt,
		LastSyncError: cfg.LastSyncError, RedirectURI: s.googleRedirectURI(r),
	}})
}

func (s *Server) handleBOGoogleReviewsSettings(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in struct {
		AutoPublishMinStars int    `json:"autoPublishMinStars"`
		Formality           string `json:"formality"`
		Signature           string `json:"signature"`
		ComplaintContact    string `json:"complaintContact"`
		SyncEnabled         bool   `json:"syncEnabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		boJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if in.AutoPublishMinStars < 0 || in.AutoPublishMinStars > 5 {
		in.AutoPublishMinStars = 0
	}
	if in.Formality != "usted" {
		in.Formality = "tu"
	}
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO restaurant_google_business
		(restaurant_id, auto_publish_min_stars, formality, signature, complaint_contact, sync_enabled) VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE auto_publish_min_stars = VALUES(auto_publish_min_stars), formality = VALUES(formality),
		signature = VALUES(signature), complaint_contact = VALUES(complaint_contact), sync_enabled = VALUES(sync_enabled)`,
		a.ActiveRestaurantID, in.AutoPublishMinStars, in.Formality, truncateRunes(strings.TrimSpace(in.Signature), 160),
		truncateRunes(strings.TrimSpace(in.ComplaintContact), 255), in.SyncEnabled); err != nil {
		boJSONError(w, http.StatusInternalServerError, "Error guardando los ajustes")
		return
	}
	s.handleBOGoogleReviewsStatus(w, r)
}

func (s *Server) handleBOGoogleReviewsOAuthStart(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if !s.googleOAuthConfigured() {
		boJSONError(w, http.StatusServiceUnavailable, "Google no está configurado en el servidor (GOOGLE_CLIENT_ID/SECRET)")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true,
		"url": s.googleConsentURL(s.googleRedirectURI(r), s.googleOAuthState(a.ActiveRestaurantID, a.User.ID))})
}

// handleBOGoogleReviewsOAuthCallback is the browser redirect from Google: it
// exchanges the code, stores the encrypted refresh token, picks the location
// when there is only one and returns to the backoffice tab.
func (s *Server) handleBOGoogleReviewsOAuthCallback(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	back := func(result string) {
		http.Redirect(w, r, googleReviewsConfigPath+"&google="+url.QueryEscape(result), http.StatusFound)
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		back("error:" + e)
		return
	}
	if !s.googleOAuthStateValid(q.Get("state"), a.ActiveRestaurantID, a.User.ID) {
		back("error:state")
		return
	}
	tok, err := s.googleExchangeCode(r.Context(), q.Get("code"), s.googleRedirectURI(r))
	if err != nil || tok.RefreshToken == "" {
		detail := "missing_refresh_token"
		if err != nil {
			detail = err.Error()
		}
		boLogGoogleReviews("oauth_exchange_failed", a.ActiveRestaurantID, detail)
		back("error:exchange")
		return
	}
	enc, err := vault.Encrypt(s.cfg.VaultToken, tok.RefreshToken)
	if err != nil {
		back("error:vault")
		return
	}
	account, location, title := "", "", ""
	if locs, lerr := googleListLocations(r.Context(), tok.AccessToken); lerr == nil && len(locs) == 1 {
		account, location, title = locs[0].Account, locs[0].Name, locs[0].Title
	} else if lerr != nil {
		boLogGoogleReviews("locations_failed", a.ActiveRestaurantID, lerr.Error())
	}
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO restaurant_google_business
		(restaurant_id, google_email, refresh_token_encrypted, account_name, location_name, location_title, connected_at, last_sync_error)
		VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(), '')
		ON DUPLICATE KEY UPDATE google_email = VALUES(google_email), refresh_token_encrypted = VALUES(refresh_token_encrypted),
		account_name = VALUES(account_name), location_name = VALUES(location_name), location_title = VALUES(location_title),
		connected_at = VALUES(connected_at), last_sync_error = ''`,
		a.ActiveRestaurantID, googleIDTokenEmail(tok.IDToken), enc, account, location, title); err != nil {
		back("error:db")
		return
	}
	boLogGoogleReviews("connected", a.ActiveRestaurantID, "location_auto="+strconv.FormatBool(location != ""))
	back("connected")
}

func (s *Server) handleBOGoogleReviewsLocations(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	cfg, err := s.loadGoogleBusiness(r.Context(), a.ActiveRestaurantID)
	if err != nil || !cfg.connected() {
		boJSONError(w, http.StatusBadRequest, "Conecta primero una cuenta de Google")
		return
	}
	token, err := s.googleAccessToken(r.Context(), cfg.RefreshToken)
	if err != nil {
		boJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	locs, err := googleListLocations(r.Context(), token)
	if err != nil {
		boJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "locations": locs})
}

func (s *Server) handleBOGoogleReviewsSetLocation(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	var in googleLocation
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !strings.HasPrefix(in.Account, "accounts/") || !strings.HasPrefix(in.Name, "locations/") {
		boJSONError(w, http.StatusBadRequest, "Ficha no válida")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE restaurant_google_business SET account_name = ?, location_name = ?, location_title = ?
		WHERE restaurant_id = ? AND refresh_token_encrypted IS NOT NULL`, in.Account, in.Name, truncateRunes(in.Title, 255), a.ActiveRestaurantID); err != nil {
		boJSONError(w, http.StatusInternalServerError, "Error guardando la ficha")
		return
	}
	s.handleBOGoogleReviewsStatus(w, r)
}

func (s *Server) handleBOGoogleReviewsDisconnect(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	if cfg, err := s.loadGoogleBusiness(r.Context(), a.ActiveRestaurantID); err == nil && cfg.connected() {
		googleRevoke(r.Context(), cfg.RefreshToken)
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE restaurant_google_business SET refresh_token_encrypted = NULL, google_email = '',
		account_name = '', location_name = '', location_title = '' WHERE restaurant_id = ?`, a.ActiveRestaurantID); err != nil {
		boJSONError(w, http.StatusInternalServerError, "Error desconectando")
		return
	}
	boLogGoogleReviews("disconnected", a.ActiveRestaurantID, "")
	s.handleBOGoogleReviewsStatus(w, r)
}

func (s *Server) handleBOGoogleReviewsSync(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	sum, err := s.googleReviewsSync(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error(), "summary": sum})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "summary": sum})
}

func (s *Server) handleBOGoogleReviewsList(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	where := "status IN ('pending','draft')"
	switch r.URL.Query().Get("status") {
	case "published":
		where = "status = 'published'"
	case "skipped":
		where = "status = 'skipped'"
	case "all":
		where = "1 = 1"
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id FROM google_reviews WHERE restaurant_id = ? AND `+where+`
		ORDER BY review_created_at DESC LIMIT 100`, a.ActiveRestaurantID)
	if err != nil {
		boJSONError(w, http.StatusInternalServerError, "Error cargando reseñas")
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	out := make([]googleReviewRow, 0, len(ids))
	for _, id := range ids {
		if rv, err := s.loadGoogleReview(r.Context(), a.ActiveRestaurantID, id); err == nil {
			out = append(out, rv)
		}
	}
	counts := map[string]int{}
	if crow, err := s.db.QueryContext(r.Context(), `SELECT status, COUNT(*) FROM google_reviews WHERE restaurant_id = ? GROUP BY status`, a.ActiveRestaurantID); err == nil {
		for crow.Next() {
			var st string
			var n int
			if crow.Scan(&st, &n) == nil {
				counts[st] = n
			}
		}
		_ = crow.Close()
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "reviews": out, "counts": counts})
}

// handleBOGoogleReviewAction runs one action on a review: draft (regenerate
// with an optional hint), publish (the text sent, edits included) or skip.
func (s *Server) handleBOGoogleReviewAction(w http.ResponseWriter, r *http.Request) {
	a, _ := boAuthFromContext(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		boJSONError(w, http.StatusBadRequest, "Reseña no válida")
		return
	}
	var in struct {
		Text        string `json:"text"`
		Instruction string `json:"instruction"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	cfg, err := s.loadGoogleBusiness(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		boJSONError(w, http.StatusInternalServerError, "Error cargando la conexión con Google")
		return
	}
	if _, err := s.loadGoogleReview(r.Context(), a.ActiveRestaurantID, id); err != nil {
		boJSONError(w, http.StatusNotFound, "Reseña no encontrada")
		return
	}
	switch chi.URLParam(r, "action") {
	case "draft":
		if _, err := s.googleReviewDraft(r.Context(), cfg, id, in.Instruction); err != nil {
			boJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "publish":
		token, err := s.googleReviewsAccessToken(r.Context(), cfg)
		if err == nil {
			err = s.googleReviewPublish(r.Context(), cfg, token, id, in.Text)
		}
		if err != nil {
			boJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "skip":
		if _, err := s.db.ExecContext(r.Context(), `UPDATE google_reviews SET status = 'skipped' WHERE id = ? AND restaurant_id = ? AND status <> 'published'`, id, a.ActiveRestaurantID); err != nil {
			boJSONError(w, http.StatusInternalServerError, "Error descartando")
			return
		}
	default:
		boJSONError(w, http.StatusNotFound, "Acción no válida")
		return
	}
	rv, _ := s.loadGoogleReview(r.Context(), a.ActiveRestaurantID, id)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "review": rv})
}

func boLogGoogleReviews(event string, restaurantID int, detail string) {
	slog.Default().Info("google_reviews."+event, "coord_id", "google_reviews_ai_v1", "restaurant_id", restaurantID, "detail", truncateRunes(detail, 300))
}
