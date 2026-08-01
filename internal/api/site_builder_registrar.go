package api

// Cloudflare Registrar integration: domain availability search + purchase.
//
// Routes (session + ajustes gated):
//   GET  /admin/site-builder/domains/search?q=<domain>  → availability
//   POST /admin/site-builder/domains/register           → buy domain
//
// The Cloudflare API's availability lives on the single-domain GET endpoint.
// Registration requires the account's Registrar to be enabled and the token
// to carry `registrar` scopes (verified separately).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func (s *Server) handleRegistrarSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		httpxWriteError(w, http.StatusBadRequest, "Missing q")
		return
	}
	cf := s.instatic.cfClient()
	dom, err := cf.CheckDomain(r.Context(), strings.ToLower(q))
	if err != nil {
		httpxWriteError(w, http.StatusBadGateway, "registrar check failed: "+err.Error())
		return
	}
	log.Printf("[registrar] CheckDomain %s -> name=%s registrable=%v tier=%s reason=%s", q, dom.Name, dom.Registrable, dom.Tier, dom.Reason)
	// CF returns `registrable` from domain-check; unavailable → registrable:false
	// with a reason (e.g. extension_not_supported / domain unavailable).
	writeJSON(w, map[string]any{
		"success": true,
		"domain": map[string]any{
			"name":          dom.Name,
			"supported_tld": dom.SupportedTLD(),
			"can_register":  dom.Registrable,
			"reason":        dom.Reason,
			"price":         dom.Pricing.RegistrationCost,
			"currency":      dom.Pricing.Currency,
		},
	})
}

type registerRequest struct {
	Domain string `json:"domain"`
}

func (s *Server) handleRegistrarRegister(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpxWriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	// Anti-abuse: require the register security token (REGISTER_API_TOKEN).
	// This is a money-spending endpoint — only callers with the token can buy.
	if s.cfg.RegisterAPIToken == "" {
		httpxWriteError(w, http.StatusServiceUnavailable, "register token no configurado (REGISTER_API_TOKEN)")
		return
	}
	provided := strings.TrimSpace(r.Header.Get("X-Register-Token"))
	if provided == "" || provided != s.cfg.RegisterAPIToken {
		httpxWriteError(w, http.StatusForbidden, "token de registro inválido")
		return
	}
	var req registerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.Domain) == "" {
		httpxWriteError(w, http.StatusBadRequest, "Missing domain")
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))

	cf := s.instatic.cfClient()
	dom, err := cf.CheckDomain(r.Context(), domain)
	if err != nil {
		httpxWriteError(w, http.StatusBadGateway, "registrar check failed: "+err.Error())
		return
	}
	if !dom.SupportedTLD() {
		httpxWriteError(w, http.StatusConflict, "TLD no soportado por Cloudflare Registrar: "+domain)
		return
	}
	if !dom.Registrable {
		httpxWriteError(w, http.StatusConflict, "dominio no disponible para registrar: "+domain+" ("+dom.Reason+")")
		return
	}

	// Register via Cloudflare Registrar (/registrar/registrations).
	// contacts.registrant is OPTIONAL — if omitted, CF uses the account's
	// default address book entry. We omit it so the account's configured
	// registrant applies (less to get wrong). If the account has no default,
	// CF returns a clear validation error and we surface it.
	regBody := fmt.Sprintf(`{"domain_name": %q, "years": 1, "privacy_mode": "redaction"}`, domain)
	raw, err := cf.RegisterDomain(r.Context(), regBody)
	if err != nil {
		httpxWriteError(w, http.StatusBadGateway, "registro falló: "+err.Error())
		return
	}
	var resp struct {
		Result struct {
			ID     string `json:"id"`
			Domain string `json:"domain"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &resp)

	// Record the domain.
	_, _ = s.db.Exec(`
		INSERT INTO restaurant_domains (restaurant_id, domain, is_primary, registration_status)
		VALUES (?, ?, 0, 'registered')
		ON DUPLICATE KEY UPDATE registration_status='registered'
	`, a.ActiveRestaurantID, domain)

	// Provision zone + DNS (proxied → CF TLS) so the site is immediately live.
	s.provisionDomainSite(r.Context(), a.ActiveRestaurantID, domain)

	writeJSON(w, map[string]any{"success": true, "domain": domain, "registration_id": resp.Result.ID, "registered": true})
}

// handleRegistrarProvision creates the CF zone + DNS for an already-registered
// domain WITHOUT purchasing anything. Gated by the same register token.
func (s *Server) handleRegistrarProvision(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpxWriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if s.cfg.RegisterAPIToken == "" {
		httpxWriteError(w, http.StatusServiceUnavailable, "register token no configurado (REGISTER_API_TOKEN)")
		return
	}
	provided := strings.TrimSpace(r.Header.Get("X-Register-Token"))
	if provided == "" || provided != s.cfg.RegisterAPIToken {
		httpxWriteError(w, http.StatusForbidden, "token de registro inválido")
		return
	}
	var req registerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.Domain) == "" {
		httpxWriteError(w, http.StatusBadRequest, "Missing domain")
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	s.provisionDomainSite(r.Context(), a.ActiveRestaurantID, domain)
	writeJSON(w, map[string]any{"success": true, "domain": domain, "provisioned": true})
}

// provisionDomainSite creates the CF zone + A records for a purchased domain
// and points it at this host (proxied). Shared by the registrar + Stripe paths.
// Uses the DNS-capable client (global key) — the registrar token lacks DNS edit.
func (s *Server) provisionDomainSite(ctx context.Context, restaurantID int, domain string) {
	cf := s.instatic.cfDNSClient()
	subCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if zone, err := cf.EnsureZone(subCtx, domain); err == nil {
		if _, err := cf.EnsureDNSRecord(subCtx, zone.ID, "A", domain, s.instatic.hostPublicIP(), true); err == nil {
			_, _ = cf.EnsureDNSRecord(subCtx, zone.ID, "A", "*."+domain, s.instatic.hostPublicIP(), true)
			_, _ = s.db.Exec(`UPDATE restaurant_domains SET cf_zone_id=?, registration_status='active' WHERE restaurant_id=? AND domain=?`, zone.ID, restaurantID, domain)
			_, _ = s.db.Exec(`
				INSERT INTO site_builder_domain_mappings (id, site_id, domain, is_primary, status, ssl_status)
				SELECT UUID(), id, ?, 1, 'active', 'active' FROM site_builder_sites WHERE restaurant_id=?
				ON DUPLICATE KEY UPDATE status='active', ssl_status='active'
			`, domain, restaurantID)
		}
	}
}

