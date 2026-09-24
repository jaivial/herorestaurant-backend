package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/httpx"
)

// =============================================================================
// Stripe Connect fees (root-managed).
//
// Destination charges with on_behalf_of: Stripe bills its processing fee to
// the PLATFORM account, not to the restaurant. So the application fee taken
// from every charge = Stripe base (passed through) + platform commission. With
// platform commission 0 the restaurant still bears Stripe's base, and the
// platform breaks even on standard cards.
//
// Global values live in platform_settings (fallback: env). A restaurant may
// override the platform commission (0 allowed) in restaurant_stripe_fee_overrides.
// Coordination id: stripe_connect_fees_v1
// =============================================================================

const (
	settingPlatformFeePercent   = "stripe_connect.platform_fee_percent"
	settingStripeBasePercent    = "stripe_connect.stripe_base_percent"
	settingStripeBaseFixedCents = "stripe_connect.stripe_base_fixed_cents"

	// Stripe EEA standard card pricing used when nothing is configured.
	defaultStripeBasePercent    = 1.25
	defaultStripeBaseFixedCents = 25
	maxFeePercent               = 30
)

type connectFeeSettings struct {
	PlatformFeePercent   float64 `json:"platform_fee_percent"`
	StripeBasePercent    float64 `json:"stripe_base_percent"`
	StripeBaseFixedCents int64   `json:"stripe_base_fixed_cents"`
}

// connectFee is what applies to one restaurant.
type connectFee struct {
	connectFeeSettings
	Override     bool    `json:"override"`
	TotalPercent float64 `json:"total_percent"`
}

func (s *Server) loadConnectFeeSettings(ctx context.Context) connectFeeSettings {
	out := connectFeeSettings{
		PlatformFeePercent:   s.cfg.StripePlatformFeePercent,
		StripeBasePercent:    defaultStripeBasePercent,
		StripeBaseFixedCents: defaultStripeBaseFixedCents,
	}
	rows, err := s.db.QueryContext(ctx, `SELECT setting_key, setting_value FROM platform_settings WHERE setting_key IN (?, ?, ?)`,
		settingPlatformFeePercent, settingStripeBasePercent, settingStripeBaseFixedCents)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) != nil {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			continue
		}
		switch k {
		case settingPlatformFeePercent:
			out.PlatformFeePercent = f
		case settingStripeBasePercent:
			out.StripeBasePercent = f
		case settingStripeBaseFixedCents:
			out.StripeBaseFixedCents = int64(f)
		}
	}
	return out
}

func (s *Server) connectFeeFor(ctx context.Context, restaurantID int) connectFee {
	fee := connectFee{connectFeeSettings: s.loadConnectFeeSettings(ctx)}
	var override float64
	if err := s.db.QueryRowContext(ctx, `SELECT platform_fee_percent FROM restaurant_stripe_fee_overrides WHERE restaurant_id = ?`, restaurantID).Scan(&override); err == nil {
		fee.PlatformFeePercent, fee.Override = override, true
	} else if !errors.Is(err, sql.ErrNoRows) {
		log.Printf("[stripe_connect_fees_v1] restaurant=%d override read: %v", restaurantID, err)
	}
	fee.TotalPercent = round2(fee.StripeBasePercent + fee.PlatformFeePercent)
	return fee
}

// applicationFeeCents is what the platform keeps from a charge of amount
// cents. Managed Risk direct charges: Stripe takes its own fee from the
// restaurant's account, so the application fee is ONLY the platform
// commission (0% -> no application fee). Never reaches the full amount.
// Coordination id: stripe_connect_managed_risk_v1
func (f connectFee) applicationFeeCents(amount int64) int64 {
	fee := int64(math.Round(float64(amount) * f.PlatformFeePercent / 100))
	if fee >= amount {
		fee = amount - 1
	}
	if fee < 0 {
		fee = 0
	}
	return fee
}

func validPercent(v float64) bool { return v >= 0 && v <= maxFeePercent && !math.IsNaN(v) }

// ---------- root endpoints (Plataforma > Stripe Connect) ----------

type platformConnectAccount struct {
	RestaurantID   int      `json:"restaurant_id"`
	Name           string   `json:"name"`
	Slug           string   `json:"slug"`
	Connected      bool     `json:"connected"`
	Demo           bool     `json:"demo"`
	Status         string   `json:"status"`
	ChargesEnabled bool     `json:"charges_enabled"`
	PayoutsEnabled bool     `json:"payouts_enabled"`
	FeeOverride    *float64 `json:"fee_override"`
	EffectiveFee   float64  `json:"effective_fee_percent"`
	TotalPercent   float64  `json:"total_percent"`
	ConnectedAt    string   `json:"connected_at"`
}

func (s *Server) handlePlatformStripeConnectList(w http.ResponseWriter, r *http.Request) {
	settings := s.loadConnectFeeSettings(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT r.id, r.name, r.slug,
			c.restaurant_id IS NOT NULL, COALESCE(c.demo, 0), COALESCE(c.status, 'not_connected'),
			COALESCE(c.charges_enabled, 0), COALESCE(c.payouts_enabled, 0),
			o.platform_fee_percent,
			COALESCE(DATE_FORMAT(c.created_at, '%Y-%m-%dT%H:%i:%sZ'), '')
		FROM restaurants r
		LEFT JOIN restaurant_stripe_connect c ON c.restaurant_id = r.id
		LEFT JOIN restaurant_stripe_fee_overrides o ON o.restaurant_id = r.id
		ORDER BY (c.restaurant_id IS NULL), r.name`)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo cuentas conectadas")
		return
	}
	defer rows.Close()
	out := []platformConnectAccount{}
	for rows.Next() {
		var (
			a        platformConnectAccount
			override sql.NullFloat64
		)
		if err := rows.Scan(&a.RestaurantID, &a.Name, &a.Slug, &a.Connected, &a.Demo, &a.Status, &a.ChargesEnabled, &a.PayoutsEnabled, &override, &a.ConnectedAt); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "Error leyendo cuentas conectadas")
			return
		}
		a.EffectiveFee = settings.PlatformFeePercent
		if override.Valid {
			v := override.Float64
			a.FeeOverride, a.EffectiveFee = &v, v
		}
		a.TotalPercent = round2(settings.StripeBasePercent + a.EffectiveFee)
		out = append(out, a)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "settings": settings, "accounts": out})
}

func (s *Server) handlePlatformStripeConnectSettings(w http.ResponseWriter, r *http.Request) {
	var in connectFeeSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos no válidos")
		return
	}
	if !validPercent(in.PlatformFeePercent) || !validPercent(in.StripeBasePercent) || in.StripeBaseFixedCents < 0 || in.StripeBaseFixedCents > 500 {
		httpx.WriteError(w, http.StatusBadRequest, "Comisiones fuera de rango (0–30 %, fijo 0–5 €)")
		return
	}
	for k, v := range map[string]string{
		settingPlatformFeePercent:   strconv.FormatFloat(round2(in.PlatformFeePercent), 'f', 2, 64),
		settingStripeBasePercent:    strconv.FormatFloat(round2(in.StripeBasePercent), 'f', 2, 64),
		settingStripeBaseFixedCents: strconv.FormatInt(in.StripeBaseFixedCents, 10),
	} {
		if _, err := s.db.ExecContext(r.Context(), `INSERT INTO platform_settings (setting_key, setting_value) VALUES (?, ?) ON DUPLICATE KEY UPDATE setting_value = VALUES(setting_value)`, k, v); err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "No se pudo guardar")
			return
		}
	}
	a, _ := boAuthFromContext(r.Context())
	log.Printf("[stripe_connect_fees_v1] global updated by user=%d platform=%.2f base=%.2f+%d", a.User.ID, in.PlatformFeePercent, in.StripeBasePercent, in.StripeBaseFixedCents)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "settings": s.loadConnectFeeSettings(r.Context())})
}

// PUT body {"fee_percent": number|null}; null removes the override.
func (s *Server) handlePlatformStripeConnectRestaurantFee(w http.ResponseWriter, r *http.Request) {
	rid, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || rid <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Restaurante no válido")
		return
	}
	var in struct {
		FeePercent *float64 `json:"fee_percent"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "Datos no válidos")
		return
	}
	a, _ := boAuthFromContext(r.Context())
	if in.FeePercent == nil {
		_, err = s.db.ExecContext(r.Context(), `DELETE FROM restaurant_stripe_fee_overrides WHERE restaurant_id = ?`, rid)
	} else if !validPercent(*in.FeePercent) {
		httpx.WriteError(w, http.StatusBadRequest, "La comisión debe estar entre 0 y 30 %")
		return
	} else {
		_, err = s.db.ExecContext(r.Context(), `
			INSERT INTO restaurant_stripe_fee_overrides (restaurant_id, platform_fee_percent, updated_by) VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE platform_fee_percent = VALUES(platform_fee_percent), updated_by = VALUES(updated_by)`,
			rid, round2(*in.FeePercent), a.User.ID)
	}
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "No se pudo guardar")
		return
	}
	fee := s.connectFeeFor(r.Context(), rid)
	log.Printf("[stripe_connect_fees_v1] restaurant=%d override=%v platform=%.2f by user=%d", rid, fee.Override, fee.PlatformFeePercent, a.User.ID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "fee": fee})
}
