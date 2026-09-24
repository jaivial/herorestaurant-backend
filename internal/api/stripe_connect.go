package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/integrations"
	"preactvillacarmen/internal/vault"
)

// =============================================================================
// Stripe Connect: one platform Stripe account, one connected (Express)
// account per restaurant. Restaurants never see keys or webhooks: they finish
// Stripe-hosted onboarding (identity + IBAN) and payouts go to their bank.
//
// Secrets: platform key + Connect webhook secret only in env. The tenant's
// connected account id is sealed with BACKOFFICE_VAULT_KEY bound to the
// restaurant id; an HMAC of it (account_hash) lets webhooks find the tenant.
// Coordination id: stripe_connect_multitenant_v1
// =============================================================================

const (
	connectStatusPending    = "pending"    // created, onboarding not finished
	connectStatusRestricted = "restricted" // submitted but Stripe needs more info
	connectStatusActive     = "active"     // charges + payouts enabled
	connectDemoAccountID    = "acct_demo"
)

type connectAccountRow struct {
	RestaurantID     int
	AccountID        string
	Status           string
	ChargesEnabled   bool
	PayoutsEnabled   bool
	DetailsSubmitted bool
	Demo             bool
}

func connectAAD(restaurantID int) string {
	return fmt.Sprintf("restaurant:%d:stripe_connect", restaurantID)
}

func (s *Server) connectAccountHash(accountID string) string {
	mac := hmac.New(sha256.New, []byte("stripe_connect_lookup:"+s.cfg.BackofficeVaultKey))
	mac.Write([]byte(accountID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) platformStripe() (*integrations.StripeClient, error) {
	if s.cfg.StripePlatformSecretKey == "" {
		return nil, errors.New("Stripe de la plataforma no configurado")
	}
	return integrations.NewStripeClient(s.cfg.StripePlatformSecretKey, s.cfg.StripeConnectWebhookSecret), nil
}

func (s *Server) loadConnectAccount(ctx context.Context, restaurantID int) (*connectAccountRow, error) {
	var (
		enc                    string
		row                    connectAccountRow
		charges, payouts, dsub int
		demo                   int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT account_encrypted, status, charges_enabled, payouts_enabled, details_submitted, demo
		FROM restaurant_stripe_connect WHERE restaurant_id = ?`, restaurantID).
		Scan(&enc, &row.Status, &charges, &payouts, &dsub, &demo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.cfg.BackofficeVaultKey == "" {
		return nil, errors.New("BACKOFFICE_VAULT_KEY no configurado")
	}
	plain, err := vault.DecryptBound(s.cfg.BackofficeVaultKey, connectAAD(restaurantID), enc)
	if err != nil {
		log.Printf("[stripe_connect_multitenant_v1] restaurant=%d connect decrypt failed", restaurantID)
		return nil, errors.New("No se pudo leer la cuenta de cobros")
	}
	var payload struct {
		AccountID string `json:"account_id"`
	}
	if json.Unmarshal([]byte(plain), &payload) != nil || payload.AccountID == "" {
		return nil, errors.New("Cuenta de cobros corrupta")
	}
	row.RestaurantID, row.AccountID = restaurantID, payload.AccountID
	row.ChargesEnabled, row.PayoutsEnabled, row.DetailsSubmitted, row.Demo = charges != 0, payouts != 0, dsub != 0, demo != 0
	return &row, nil
}

func (s *Server) saveConnectAccount(ctx context.Context, row connectAccountRow) error {
	if s.cfg.BackofficeVaultKey == "" {
		return errors.New("BACKOFFICE_VAULT_KEY no configurado")
	}
	payload, _ := json.Marshal(map[string]string{"account_id": row.AccountID})
	enc, err := vault.EncryptBound(s.cfg.BackofficeVaultKey, connectAAD(row.RestaurantID), string(payload))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO restaurant_stripe_connect
			(restaurant_id, account_encrypted, account_hash, status, charges_enabled, payouts_enabled, details_submitted, demo)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE account_encrypted = VALUES(account_encrypted), account_hash = VALUES(account_hash),
			status = VALUES(status), charges_enabled = VALUES(charges_enabled), payouts_enabled = VALUES(payouts_enabled),
			details_submitted = VALUES(details_submitted), demo = VALUES(demo)`,
		row.RestaurantID, enc, s.connectAccountHash(row.AccountID), row.Status,
		boolToTinyint(row.ChargesEnabled), boolToTinyint(row.PayoutsEnabled), boolToTinyint(row.DetailsSubmitted), boolToTinyint(row.Demo))
	return err
}

func connectStatusFor(a *integrations.ConnectAccount) string {
	switch {
	case a.ChargesEnabled && a.PayoutsEnabled:
		return connectStatusActive
	case a.DetailsSubmitted:
		return connectStatusRestricted
	default:
		return connectStatusPending
	}
}

// refreshConnectAccount pulls the live onboarding state from Stripe.
func (s *Server) refreshConnectAccount(ctx context.Context, row *connectAccountRow) (*integrations.ConnectAccount, error) {
	if row.Demo {
		return nil, nil
	}
	cli, err := s.platformStripe()
	if err != nil {
		return nil, err
	}
	acct, err := cli.RetrieveAccount(ctx, row.AccountID)
	if err != nil {
		return nil, err
	}
	row.Status, row.ChargesEnabled, row.PayoutsEnabled, row.DetailsSubmitted = connectStatusFor(acct), acct.ChargesEnabled, acct.PayoutsEnabled, acct.DetailsSubmitted
	return acct, s.saveConnectAccount(ctx, *row)
}

// connectReady: the tenant can take online payments (live active or demo).
func (s *Server) connectReady(ctx context.Context, restaurantID int) (*connectAccountRow, bool) {
	row, err := s.loadConnectAccount(ctx, restaurantID)
	if err != nil || row == nil {
		return row, false
	}
	return row, row.Demo || row.Status == connectStatusActive
}

type connectStatusDTO struct {
	Connected        bool     `json:"connected"`
	Demo             bool     `json:"demo"`
	Status           string   `json:"status"`
	ChargesEnabled   bool     `json:"charges_enabled"`
	PayoutsEnabled   bool     `json:"payouts_enabled"`
	DetailsSubmitted bool     `json:"details_submitted"`
	CurrentlyDue     []string `json:"currently_due"`
	BankLast4        string   `json:"bank_last4"`
	PlatformReady    bool     `json:"platform_ready"`
	FeePercent       float64  `json:"fee_percent"`
}

func (s *Server) connectDTO(row *connectAccountRow, acct *integrations.ConnectAccount) connectStatusDTO {
	out := connectStatusDTO{PlatformReady: s.cfg.StripePlatformSecretKey != "", FeePercent: s.cfg.StripePlatformFeePercent, CurrentlyDue: []string{}}
	if row == nil {
		out.Status = "not_connected"
		return out
	}
	out.Connected, out.Demo, out.Status = true, row.Demo, row.Status
	out.ChargesEnabled, out.PayoutsEnabled, out.DetailsSubmitted = row.ChargesEnabled, row.PayoutsEnabled, row.DetailsSubmitted
	if acct != nil {
		out.CurrentlyDue = acct.Requirements.CurrentlyDue
		if len(acct.ExternalAccounts.Data) > 0 {
			out.BankLast4 = acct.ExternalAccounts.Data[0].Last4
		}
	}
	return out
}

// User-facing Stripe failures answer 424: the CDN edge rewrites 502/503 bodies,
// which would hide the actionable message from the backoffice and the site.
func (s *Server) handleBOStripeConnectStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	row, err := s.loadConnectAccount(r.Context(), a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}
	var acct *integrations.ConnectAccount
	if row != nil && !row.Demo {
		if acct, err = s.refreshConnectAccount(r.Context(), row); err != nil {
			log.Printf("[stripe_connect_multitenant_v1] restaurant=%d refresh failed: %v", a.ActiveRestaurantID, err)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "connect": s.connectDTO(row, acct)})
}

// handleBOStripeConnectOnboard creates the tenant's connected account (once)
// and returns a fresh hosted onboarding link. demo=true skips Stripe.
func (s *Server) handleBOStripeConnectOnboard(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	var req struct {
		Demo bool `json:"demo"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req)
	ctx, rid := r.Context(), a.ActiveRestaurantID

	row, err := s.loadConnectAccount(ctx, rid)
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if req.Demo {
		if row != nil && !row.Demo {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Este restaurante ya tiene una cuenta real conectada"})
			return
		}
		demo := connectAccountRow{RestaurantID: rid, AccountID: connectDemoAccountID, Status: connectStatusActive, ChargesEnabled: true, PayoutsEnabled: true, DetailsSubmitted: true, Demo: true}
		if err := s.saveConnectAccount(ctx, demo); err != nil {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "No se pudo activar el modo demo"})
			return
		}
		logCheckpoint(r, "stripe_connect_demo_enabled", "restaurant", fmt.Sprint(rid))
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "connect": s.connectDTO(&demo, nil)})
		return
	}

	cli, err := s.platformStripe()
	if err != nil {
		httpx.WriteJSON(w, http.StatusFailedDependency, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if row == nil || row.Demo {
		branding, _ := s.loadRestaurantBranding(ctx, rid)
		info, _ := s.loadRestaurantInfo(ctx, rid)
		acct, err := cli.CreateExpressAccount(ctx, "ES", strings.TrimSpace(info.Email), branding.BrandName, branding.Website,
			map[string]string{"restaurant_id": fmt.Sprint(rid), "coordination_id": "stripe_connect_multitenant_v1"})
		if err != nil {
			log.Printf("[stripe_connect_multitenant_v1] restaurant=%d create account failed: %v", rid, err)
			msg, code := "Stripe no pudo crear la cuenta de cobros", "STRIPE_CONNECT_CREATE_FAILED"
			if strings.Contains(err.Error(), "signed up for Connect") {
				msg, code = "La plataforma todavía no tiene Stripe Connect activado. El administrador debe activarlo en dashboard.stripe.com/connect.", "STRIPE_CONNECT_NOT_ENABLED"
			}
			httpx.WriteJSON(w, http.StatusFailedDependency, map[string]any{"success": false, "message": msg, "error_code": code})
			return
		}
		row = &connectAccountRow{RestaurantID: rid, AccountID: acct.ID, Status: connectStatusFor(acct)}
		if err := s.saveConnectAccount(ctx, *row); err != nil {
			httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": "No se pudo guardar la cuenta de cobros"})
			return
		}
		logCheckpoint(r, "stripe_connect_account_created", "restaurant", fmt.Sprint(rid))
	}
	base := s.backofficePublicBaseURL()
	if base == "" {
		httpx.WriteJSON(w, http.StatusFailedDependency, map[string]any{"success": false, "message": "BACKOFFICE_PUBLIC_BASE_URL no configurado"})
		return
	}
	link, err := cli.CreateAccountLink(ctx, row.AccountID, base+"/app/config?content=stripe&onboarding=refresh", base+"/app/config?content=stripe&onboarding=return")
	if err != nil {
		log.Printf("[stripe_connect_multitenant_v1] restaurant=%d account link failed: %v", rid, err)
		httpx.WriteJSON(w, http.StatusFailedDependency, map[string]any{"success": false, "message": "Stripe no pudo abrir el alta"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "onboarding_url": link})
}

// handleBOStripeConnectDashboard opens the tenant's Express dashboard (payouts, IBAN).
func (s *Server) handleBOStripeConnectDashboard(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	row, err := s.loadConnectAccount(r.Context(), a.ActiveRestaurantID)
	if err != nil || row == nil || row.Demo || !row.DetailsSubmitted {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Completa primero el alta de cobros"})
		return
	}
	cli, err := s.platformStripe()
	if err != nil {
		httpx.WriteJSON(w, http.StatusFailedDependency, map[string]any{"success": false, "message": err.Error()})
		return
	}
	link, err := cli.CreateLoginLink(r.Context(), row.AccountID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusFailedDependency, map[string]any{"success": false, "message": "Stripe no pudo abrir el panel"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "dashboard_url": link})
}

// handleBOStripeConnectDisconnect removes a demo link (live accounts are kept
// so pending payouts are never orphaned; they are closed from Stripe).
func (s *Server) handleBOStripeConnectDisconnect(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]any{"success": false, "message": "Unauthorized"})
		return
	}
	row, err := s.loadConnectAccount(r.Context(), a.ActiveRestaurantID)
	if err != nil || row == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	if !row.Demo {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "message": "Una cuenta real solo se puede cerrar desde Stripe"})
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `DELETE FROM restaurant_stripe_connect WHERE restaurant_id = ?`, a.ActiveRestaurantID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}

// backofficePublicBaseURL comes from env only: the request Host is the
// backend's internal address behind the SSR proxy and is client-controlled.
func (s *Server) backofficePublicBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("BACKOFFICE_PUBLIC_BASE_URL")), "/")
}
