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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"preactvillacarmen/internal/sqldb"
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
	// SiteID optionally selects which of the restaurant's sites the domain
	// maps to (required when the restaurant has more than one site).
	SiteID string `json:"site_id"`
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
	// padding_domain_identity_v1: the shared normalizePremiumDomain identity
	// (was lower+trim only; the claim helper now refuses anything else).
	domain := normalizePremiumDomain(req.Domain)
	if domain == "" {
		httpxWriteError(w, http.StatusBadRequest, "Invalid domain")
		return
	}
	// domain_reservation_v1: claim the row BEFORE any purchase.
	claimed, claimErr := s.claimOwnedDomain(r.Context(), a.ActiveRestaurantID, domain)
	if errors.Is(claimErr, errDomainUnavailable) {
		httpxWriteError(w, http.StatusConflict, "dominio no disponible para registrar: "+domain)
		return
	}
	if claimErr != nil {
		httpxWriteError(w, http.StatusInternalServerError, "no se pudo validar el dominio")
		return
	}
	// Resolve the target site BEFORE any purchase (site_mapping_selection_v1).
	if _, siteErr := s.resolveMappingSite(r.Context(), a.ActiveRestaurantID, req.SiteID); siteErr != nil {
		httpxWriteError(w, http.StatusBadRequest, siteErr.Error())
		return
	}
	switch claimed.RegistrationStatus {
	case "registered", "active":
		// Same-owner retry after a completed purchase: never buy twice.
		writeJSON(w, map[string]any{"success": true, "domain": domain, "registered": true, "already_registered": true})
		return
	case "registering":
		httpxWriteError(w, http.StatusConflict, "registro de "+domain+" ya en curso")
		return
	case "register_uncertain":
		httpxWriteError(w, http.StatusConflict, "registro de "+domain+" pendiente de conciliación manual con el registrador")
		return
	}

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
	// Move the OWN row pending|paid|failed -> registering atomically: a
	// concurrent same-owner request loses here and does not purchase.
	res, err := s.db.ExecContext(r.Context(), nativeSQL(`UPDATE restaurant_domains SET registration_status='registering' WHERE restaurant_id=? AND domain=? AND registration_status IN ('pending','paid','failed')`, `UPDATE restaurant_domains SET registration_status='registering' WHERE restaurant_id=? AND app.norm_ascii(domain) = app.norm_ascii(?) AND registration_status IN ('pending','paid','failed')`), a.ActiveRestaurantID, domain)
	if err != nil {
		httpxWriteError(w, http.StatusInternalServerError, "no se pudo reservar el dominio")
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		httpxWriteError(w, http.StatusConflict, "registro de "+domain+" ya en curso o en un estado que no permite comprarlo")
		return
	}
	regBody := fmt.Sprintf(`{"domain_name": %q, "years": 1, "privacy_mode": "redaction"}`, domain)
	raw, err := cf.RegisterDomain(r.Context(), regBody)
	if err != nil {
		// The provider may or may not have registered it: never retry
		// automatically (no double buy); manual reconciliation.
		if _, uerr := s.db.ExecContext(r.Context(), nativeSQL(`UPDATE restaurant_domains SET registration_status='register_uncertain' WHERE restaurant_id=? AND domain=? AND registration_status='registering'`, `UPDATE restaurant_domains SET registration_status='register_uncertain' WHERE restaurant_id=? AND app.norm_ascii(domain) = app.norm_ascii(?) AND registration_status='registering'`), a.ActiveRestaurantID, domain); uerr != nil {
			log.Printf("[domain_reservation_v1] %s restaurant=%d mark uncertain failed (row stays registering): %v", domain, a.ActiveRestaurantID, uerr)
		}
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

	// Record the domain on the OWN claimed row (registering -> registered).
	// A failed write leaves 'registering' (retries refused; manual recovery),
	// never a second purchase.
	if _, err := s.db.ExecContext(r.Context(), nativeSQL(`UPDATE restaurant_domains SET registration_status='registered' WHERE restaurant_id=? AND domain=? AND registration_status='registering'`, `UPDATE restaurant_domains SET registration_status='registered' WHERE restaurant_id=? AND app.norm_ascii(domain) = app.norm_ascii(?) AND registration_status='registering'`), a.ActiveRestaurantID, domain); err != nil {
		log.Printf("[domain_reservation_v1] %s restaurant=%d registered at provider but DB update failed: %v", domain, a.ActiveRestaurantID, err)
	}

	// Provision zone + DNS (proxied → CF TLS) so the site is immediately live.
	mapping := s.provisionDomainSite(r.Context(), a.ActiveRestaurantID, domain, req.SiteID)

	writeJSON(w, map[string]any{"success": true, "domain": domain, "registration_id": resp.Result.ID, "registered": true, "site_mapping": mapping})
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
	// padding_domain_identity_v1: the shared normalizePremiumDomain identity
	// (was lower+trim only; the claim helper now refuses anything else).
	domain := normalizePremiumDomain(req.Domain)
	if domain == "" {
		httpxWriteError(w, http.StatusBadRequest, "Invalid domain")
		return
	}
	if _, siteErr := s.resolveMappingSite(r.Context(), a.ActiveRestaurantID, req.SiteID); siteErr != nil {
		httpxWriteError(w, http.StatusBadRequest, siteErr.Error())
		return
	}
	mapping := s.provisionDomainSite(r.Context(), a.ActiveRestaurantID, domain, req.SiteID)
	writeJSON(w, map[string]any{"success": true, "domain": domain, "provisioned": true, "site_mapping": mapping})
}

// provisionDomainSite creates the CF zone + A records for a purchased domain
// and points it at this host (proxied). Shared by the registrar + Stripe paths.
// Uses the DNS-capable client (global key) — the registrar token lacks DNS edit.
func (s *Server) provisionDomainSite(ctx context.Context, restaurantID int, domain, siteID string) string {
	// domain_owner_isolation_v1: never point another restaurant's domain at
	// this restaurant (row owner check; an absent row keeps the source path).
	if foreign, err := domainOwnedByOther(ctx, s.db, domain, restaurantID, false); err != nil || foreign {
		log.Printf("[domain_owner_isolation_v1] provision skipped domain=%s restaurant=%d foreign=%v err=%v", domain, restaurantID, foreign, err)
		return "skipped_not_owned"
	}
	mapping := "skipped_dns"
	cf := s.instatic.cfDNSClient()
	subCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if zone, err := cf.EnsureZone(subCtx, domain); err == nil {
		if _, err := cf.EnsureDNSRecord(subCtx, zone.ID, "A", domain, s.instatic.hostPublicIP(), true); err == nil {
			_, _ = cf.EnsureDNSRecord(subCtx, zone.ID, "A", "*."+domain, s.instatic.hostPublicIP(), true)
			_, _ = s.db.Exec(nativeSQL(`UPDATE restaurant_domains SET cf_zone_id=?, registration_status='active' WHERE restaurant_id=? AND domain=?`, `UPDATE restaurant_domains SET cf_zone_id=?, registration_status='active' WHERE restaurant_id=? AND app.norm_ascii(domain) = app.norm_ascii(?)`), zone.ID, restaurantID, domain)
			mapping = s.mapDomainToSite(ctx, restaurantID, domain, siteID)
		}
	}
	return mapping
}

// mapDomainToSite records the domain mapping for an explicit site, never an
// arbitrary one (site_mapping_selection_v1): an explicit site_id must belong
// to the restaurant; without one, exactly one site is mapped, several sites
// fail closed ("site_id_required"). id = MySQL UUID() / Go v1 UUID on PG.
// resolveMappingSite returns the site a domain maps to: the requested site
// (must belong to the restaurant), else the restaurant's only site, else ""
// when it has none (source: no mapping). Several sites without a selection
// is an error, raised BEFORE any purchase or payment.
func (s *Server) resolveMappingSite(ctx context.Context, restaurantID int, siteID string) (string, error) {
	if siteID != "" {
		var one int
		if err := s.db.QueryRowContext(ctx, nativeSQL(`SELECT 1 FROM site_builder_sites WHERE id = ? AND restaurant_id = ?`, `SELECT 1 FROM site_builder_sites WHERE app.norm_ascii(id) = app.norm_ascii(?) AND restaurant_id = ?`), siteID, restaurantID).Scan(&one); err != nil {
			return "", errors.New("site_id no pertenece al restaurante")
		}
		return siteID, nil
	}
	rows, err := s.db.QueryContext(ctx, portableSQL(`SELECT id FROM site_builder_sites WHERE restaurant_id = ? LIMIT 2`), restaurantID)
	if err != nil {
		return "", errors.New("no se pudieron leer los sitios")
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	switch len(ids) {
	case 0:
		return "", nil
	case 1:
		return ids[0], nil
	}
	return "", errors.New("el restaurante tiene varios sitios: site_id requerido")
}

func (s *Server) mapDomainToSite(ctx context.Context, restaurantID int, domain, siteID string) string {
	resolved, err := s.resolveMappingSite(ctx, restaurantID, siteID)
	if err != nil {
		log.Printf("[site_mapping_selection_v1] restaurant=%d domain=%s: %v; mapping not created", restaurantID, domain, err)
		return "site_id_required"
	}
	if resolved == "" {
		return "skipped_no_site"
	}
	siteID = resolved
	mapArgs := []any{siteID, domain}
	if sqlDialect == sqldb.Postgres {
		u, ue := sqlUUIDv1()
		if ue != nil {
			log.Printf("[sql_uuid_v1_pg_v1] provision mapping uuid restaurant=%d: %v", restaurantID, ue)
			return "skipped_error"
		}
		mapArgs = append([]any{u}, mapArgs...)
	}
	if _, err := s.db.ExecContext(ctx, nativeSQL(`
		INSERT INTO site_builder_domain_mappings (id, site_id, domain, is_primary, status, ssl_status)
		VALUES (UUID(), ?, ?, 1, 'active', 'active')
		ON DUPLICATE KEY UPDATE status = IF(site_id = VALUES(site_id), 'active', status), ssl_status = IF(site_id = VALUES(site_id), 'active', ssl_status)
	`, `
		INSERT INTO site_builder_domain_mappings (id, site_id, domain, is_primary, status, ssl_status)
		VALUES (CAST(? AS text), ?, ?, 1, 'active', 'active')
		ON CONFLICT (app.norm_ascii(domain)) DO UPDATE SET status='active', ssl_status='active' WHERE site_builder_domain_mappings.site_id = EXCLUDED.site_id
	`), mapArgs...); err != nil {
		return "skipped_error"
	}
	var mappedSite string
	if err := s.db.QueryRowContext(ctx, nativeSQL(`SELECT site_id FROM site_builder_domain_mappings WHERE domain = ?`, `SELECT site_id FROM site_builder_domain_mappings WHERE app.norm_ascii(domain) = app.norm_ascii(?)`), domain).Scan(&mappedSite); err != nil || mappedSite != siteID {
		return "skipped_mapped_to_other_site"
	}
	return "mapped"
}
