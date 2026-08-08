package api

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"preactvillacarmen/internal/config"
	"preactvillacarmen/internal/integrations"
)

// bookingWidgetJS is the vanilla booking widget, injected into every generated
// site as a same-origin runtime script (CSP script-src 'self').
// ponytail: one embedded IIFE for all restaurants; move to instatic/scripts/ if
// widget iteration outpaces Go rebuilds.
//
//go:embed booking_widget.iife.js
var bookingWidgetJS string

// ---------------------------------------------------------------------------
// Instatic instance manager: on-demand Bun+instatic process per restaurant.
//
// Serving is 100% static (see instatic_proxy.go) — instances exist only for
// editing/publishing. Each EnsureRunning stamps lastActivity; the supervisor
// idle-reaps instances quiet longer than instaticIdleTTL, so steady-state
// process count is zero.
//
// ponytail: minimal supervisor — spawn on demand, idle-reap. No container
// orchestration. If concurrent editors grow past ~8, move to one Docker
// container per instance (compose per instance).
// ---------------------------------------------------------------------------

const (
	instaticHealthTimeout = 5 * time.Second
	instaticHealthEvery   = 30 * time.Second
	instaticBootTimeout   = 40 * time.Second
	instaticHttpTimeout   = 25 * time.Second
	instaticIdleTTL       = 15 * time.Minute
)

type instaticManager struct {
	db  *sql.DB
	cfg config.Config

	mu sync.Mutex
	// proc holds the running *exec.Cmd per restaurant (nil if not running).
	proc map[int]*exec.Cmd
	// ready is the HTTP base URL for a restaurant's running instance.
	ready map[int]string
	// lastActivity records the last EnsureRunning (edit/seed/publish) time per
	// restaurant; the supervisor reaps instances idle past instaticIdleTTL.
	lastActivity map[int]time.Time
	// portByRestaurant assigns the instance port once, stable across restarts
	// so reverse-proxy mappings survive.
	portByRestaurant map[int]int

	stop chan struct{}
}

func newInstaticManager(db *sql.DB, cfg config.Config) *instaticManager {
	m := &instaticManager{
		db:               db,
		cfg:              cfg,
		proc:             map[int]*exec.Cmd{},
		ready:            map[int]string{},
		lastActivity:     map[int]time.Time{},
		portByRestaurant: map[int]int{},
		stop:             make(chan struct{}),
	}
	// Restore port assignment from DB so a restart keeps the same port.
	m.loadPortAssignments()
	return m
}

// loadPortAssignments reads existing instatic_instances rows so port mapping
// survives process restarts.
func (m *instaticManager) loadPortAssignments() {
	rows, err := m.db.Query(`SELECT restaurant_id, port FROM instatic_instances`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var rid, port int
		if err := rows.Scan(&rid, &port); err == nil {
			m.portByRestaurant[rid] = port
		}
	}
}

// portFor returns the stable port for a restaurant, allocating the next free
// one (starting at cfg.InstaticBasePort) if none is assigned yet.
func (m *instaticManager) portFor(restaurantID int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.portByRestaurant[restaurantID]; ok {
		return p
	}
	used := map[int]bool{}
	for _, p := range m.portByRestaurant {
		used[p] = true
	}
	p := m.cfg.InstaticBasePort
	for used[p] && p < m.cfg.InstaticBasePort+m.cfg.InstaticMaxInstances {
		p++
	}
	m.portByRestaurant[restaurantID] = p
	return p
}

// instanceDataDir returns the on-disk directory for a restaurant's instance.
func (m *instaticManager) instanceDataDir(restaurantID int) string {
	return filepath.Join(m.cfg.InstaticBaseDir, fmt.Sprintf("restaurant-%d", restaurantID))
}

// EnsureRunning starts (or verifies) the instatic instance for a restaurant,
// bootstrapping setup + login on first start.
func (m *instaticManager) EnsureRunning(ctx context.Context, restaurantID int) (string, error) {
	m.mu.Lock()
	m.lastActivity[restaurantID] = time.Now() // keep alive: reaper spares recent activity
	base, ok := m.ready[restaurantID]
	cmd, tracked := m.proc[restaurantID]
	// A tracked proc that has exited (crash, external kill) is not "spawning" —
	// drop it so we restart instead of polling a dead port until timeout.
	spawning := tracked && cmd.Process != nil && cmd.ProcessState == nil
	if tracked && !spawning {
		delete(m.proc, restaurantID)
		delete(m.ready, restaurantID)
	}
	m.mu.Unlock()
	if ok && m.health(ctx, base) {
		return base, nil
	}

	if !spawning {
		if err := m.start(ctx, restaurantID); err != nil {
			return "", err
		}
	}

	return m.waitReady(ctx, restaurantID)
}

// start launches the bun process for a restaurant.
func (m *instaticManager) start(ctx context.Context, restaurantID int) error {
	port := m.portFor(restaurantID)
	dir := m.instanceDataDir(restaurantID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("instatic mkdir %s: %w", dir, err)
	}
	sqlitePath := filepath.Join(dir, "instatic.db")
	uploadsDir := filepath.Join(dir, "uploads")

	sub := m.subdomainFor(restaurantID)
	origin := fmt.Sprintf("https://%s", sub)

	// Detached from the request ctx: the reaper/Stop own the lifetime (a request-
	// scoped ctx would kill the instance on HTTP response, racing the next start's
	// port bind). No `--watch` — the watch wrapper re-runs Bun.serve() on reload
	// (EADDRINUSE) and leaves an orphan child on Kill (single process is killable).
	cmd := exec.Command("bun", "server/index.ts")
	cmd.Dir = m.cfg.InstaticServerDir
	cmd.Env = append(os.Environ(),
		"PORT="+fmt.Sprintf("%d", port),
		"DATABASE_URL=sqlite:"+sqlitePath,
		"UPLOADS_DIR="+uploadsDir,
		"INSTATIC_SECRET_KEY="+strings.TrimSpace(os.Getenv("INSTATIC_SECRET_KEY")),
		"PUBLIC_ORIGIN="+origin,
		"BUN_ENV=production",
	)
	logf, err := os.OpenFile(filepath.Join(dir, "instatic.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("instatic start: %w", err)
	}

	m.mu.Lock()
	m.proc[restaurantID] = cmd
	m.mu.Unlock()

	// Record the instance row (upsert).
	_, _ = m.db.Exec(`
		INSERT INTO instatic_instances (restaurant_id, port, status, data_dir, sqlite_path, uploads_dir, public_origin)
		VALUES (?, ?, 'running', ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			port = VALUES(port), status = 'running', data_dir = VALUES(data_dir),
			sqlite_path = VALUES(sqlite_path), uploads_dir = VALUES(uploads_dir),
			public_origin = VALUES(public_origin), last_health_at = NOW()
	`, restaurantID, port, dir, sqlitePath, uploadsDir, origin)
	return nil
}

// waitReady polls the instance health endpoint until it responds, then ensures
// setup + login so subsequent admin calls authenticate.
func (m *instaticManager) waitReady(ctx context.Context, restaurantID int) (string, error) {
	port := m.portFor(restaurantID)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	deadline := time.Now().Add(instaticBootTimeout)
	for time.Now().Before(deadline) {
		if m.health(ctx, base) {
			m.mu.Lock()
			m.ready[restaurantID] = base
			m.mu.Unlock()
			_ = m.ensureBootstrapped(ctx, restaurantID, base)
			_, _ = m.db.Exec(`UPDATE instatic_instances SET status='running', last_health_at=NOW() WHERE restaurant_id=?`, restaurantID)
			return base, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("instatic instance for restaurant %d did not become ready on %s", restaurantID, base)
}

// health pings the instance's health endpoint (instatic serves /health).
func (m *instaticManager) health(ctx context.Context, base string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ensureBootstrapped runs setup + login + step-up disable on a fresh instance.
func (m *instaticManager) ensureBootstrapped(ctx context.Context, restaurantID int, base string) error {
	// Already bootstrapped (session token present)?
	var token string
	err := m.db.QueryRow(`SELECT instatic_session_token FROM instatic_instances WHERE restaurant_id=?`, restaurantID).Scan(&token)
	if err == nil && token != "" {
		return nil
	}

	// Setup status.
	status := map[string]any{}
	if err := m.call(ctx, base, http.MethodGet, "/admin/api/cms/setup/status", nil, &status); err != nil {
		return err
	}
	if needs, _ := status["needsSetup"].(bool); needs {
		body := map[string]any{
			"siteName":    fmt.Sprintf("Restaurante %d", restaurantID),
			"email":       m.cfg.InstaticSeedAdminEmail,
			"password":    m.cfg.InstaticSeedAdminPassword,
			"displayName": "Website Admin",
		}
		if err := m.call(ctx, base, http.MethodPost, "/admin/api/cms/setup", body, nil); err != nil {
			return fmt.Errorf("instatic setup: %w", err)
		}
	}

	// Login -> capture session cookie.
	resp, err := m.do(ctx, base, http.MethodPost, "/admin/api/cms/login", map[string]any{
		"email":    m.cfg.InstaticSeedAdminEmail,
		"password": m.cfg.InstaticSeedAdminPassword,
	})
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if len(resp.Cookies()) == 0 {
		return fmt.Errorf("instatic login returned no session cookie")
	}
	var sessionCookie string
	for _, c := range resp.Cookies() {
		if c.Name == "instatic_admin_session" {
			sessionCookie = c.Value
			break
		}
	}
	if sessionCookie == "" {
		return fmt.Errorf("instatic login: missing instatic_admin_session cookie")
	}

	// Disable step-up so headless publish works. SQLite is instance-owned.
	_ = m.disableStepUp(ctx, restaurantID)

	_, _ = m.db.Exec(`UPDATE instatic_instances SET instatic_session_token=? WHERE restaurant_id=?`, sessionCookie, restaurantID)
	return nil
}

// sessionToken returns the stored instatic admin session cookie value for a
// restaurant (empty if not bootstrapped).
func (m *instaticManager) sessionToken(restaurantID int) string {
	var token string
	_ = m.db.QueryRow(`SELECT instatic_session_token FROM instatic_instances WHERE restaurant_id=?`, restaurantID).Scan(&token)
	return token
}

// disableStepUp flips the owner's step_up_auth_mode to 'disabled' via sqlite3.
func (m *instaticManager) disableStepUp(ctx context.Context, restaurantID int) error {
	dir := m.instanceDataDir(restaurantID)
	sqlitePath := filepath.Join(dir, "instatic.db")
	cmd := exec.CommandContext(ctx, "bun", "-e",
		`import {Database} from 'bun:sqlite'; const db=new Database(process.argv[1]); db.run("update users set step_up_auth_mode='disabled'"); db.close();`,
		sqlitePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("disable step-up: %v: %s", err, string(out))
	}
	return nil
}

// subdomainFor returns the stable public subdomain for a restaurant.
func (m *instaticManager) subdomainFor(restaurantID int) string {
	baseURL := m.appBaseURL()
	var slug string
	_ = m.db.QueryRow(`SELECT slug FROM restaurants WHERE id=?`, restaurantID).Scan(&slug)
	if slug == "" {
		slug = fmt.Sprintf("restaurant-%d", restaurantID)
	}
	return fmt.Sprintf("%s.%s", slug, baseURL)
}

// appBaseURL reads the app base URL from app_settings (falls back to the
// default if missing/unreadable).
func (m *instaticManager) appBaseURL() string {
	var v string
	if err := m.db.QueryRow(`SELECT v FROM app_settings WHERE k='app_base_url'`).Scan(&v); err == nil && v != "" {
		return v
	}
	return "backoffice-dev.menustudioai.com"
}

// instaticRequest performs an authenticated admin call against a restaurant's
// instance (after EnsureRunning).
func (m *instaticManager) instaticRequest(ctx context.Context, restaurantID int, method, path string, body any, out any) error {
	base, err := m.EnsureRunning(ctx, restaurantID)
	if err != nil {
		return err
	}
	return m.call(ctx, base, method, path, body, out)
}

// call performs an authenticated JSON request against an instance.
func (m *instaticManager) call(ctx context.Context, base, method, path string, body, out any) error {
	resp, err := m.do(ctx, base, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("instatic %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

// do performs a JSON request against an instance, adding the stored session
// cookie when present.
func (m *instaticManager) do(ctx context.Context, base, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Attach session cookie if we know it.
	var token string
	var rid int
	// The manager doesn't know the restaurant id from base; look it up.
	_ = m.db.QueryRow(`SELECT restaurant_id, instatic_session_token FROM instatic_instances WHERE public_origin=?`, base).Scan(&rid, &token)
	if rid == 0 {
		for rid2, b := range m.readySnapshot() {
			if b == base {
				rid = rid2
				break
			}
		}
	}
	if rid != 0 && token == "" {
		_ = m.db.QueryRow(`SELECT instatic_session_token FROM instatic_instances WHERE restaurant_id=?`, rid).Scan(&token)
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "instatic_admin_session", Value: token})
	}

	client := &http.Client{Timeout: instaticHttpTimeout}
	return client.Do(req)
}

func (m *instaticManager) readySnapshot() map[int]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[int]string{}
	for k, v := range m.ready {
		out[k] = v
	}
	return out
}

// Stop terminates a restaurant's instance (and marks it stopped).
func (m *instaticManager) Stop(restaurantID int) {
	m.mu.Lock()
	cmd, ok := m.proc[restaurantID]
	delete(m.proc, restaurantID)
	delete(m.ready, restaurantID)
	delete(m.lastActivity, restaurantID)
	m.mu.Unlock()
	if ok && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	_, _ = m.db.Exec(`UPDATE instatic_instances SET status='stopped' WHERE restaurant_id=?`, restaurantID)
}

// StartSupervisor runs the periodic idle-reaper loop in the background.
func (m *instaticManager) StartSupervisor() {
	go func() {
		t := time.NewTicker(instaticHealthEvery)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				m.sweep()
			}
		}
	}()
}

// StopSupervisor halts the reaper loop.
func (m *instaticManager) StopSupervisor() {
	close(m.stop)
}

// sweep reaps instances that are idle past instaticIdleTTL or whose process has
// died. Nothing is auto-restarted — EnsureRunning respawns on the next
// edit/seed/publish. This keeps steady-state process count at zero.
func (m *instaticManager) sweep() {
	now := time.Now()
	m.mu.Lock()
	var reap []int
	for id, cmd := range m.proc {
		dead := cmd.Process == nil || cmd.ProcessState != nil
		last, ok := m.lastActivity[id]
		idle := !ok || now.Sub(last) > instaticIdleTTL
		if dead || idle {
			reap = append(reap, id)
		}
	}
	m.mu.Unlock()
	for _, id := range reap {
		m.Stop(id)
	}
}

// RegisterInstaticRoutes wires the instatic manager's HTTP endpoints.
func (m *instaticManager) RegisterInstaticRoutes(r chi.Router) {
	r.Route("/instatic", func(r chi.Router) {
		r.Get("/status/{restaurantId}", m.handleStatus)
		r.Post("/ensure/{restaurantId}", m.handleEnsure)
		r.Post("/seed/{restaurantId}", m.handleSeed)
		r.Post("/publish/{restaurantId}", m.handlePublish)
		r.Post("/provision-domain/{restaurantId}", m.handleProvisionDomain)
	})
}

func (m *instaticManager) handleStatus(w http.ResponseWriter, r *http.Request) {
	restaurantID := chiURLInt(r, "restaurantId")
	m.mu.Lock()
	base, ready := m.ready[restaurantID]
	cmd, hasProc := m.proc[restaurantID]
	m.mu.Unlock()
	status := "stopped"
	if hasProc && cmd.Process != nil && cmd.ProcessState == nil {
		status = "running"
	}
	writeJSON(w, map[string]any{
		"success":       true,
		"status":        status,
		"base":          base,
		"ready":         ready,
		"restaurant_id": restaurantID,
		"port":          m.portFor(restaurantID),
	})
}

func (m *instaticManager) handleEnsure(w http.ResponseWriter, r *http.Request) {
	restaurantID := chiURLInt(r, "restaurantId")
	base, err := m.EnsureRunning(r.Context(), restaurantID)
	if err != nil {
		httpxWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"success": true, "base": base})
}

func (m *instaticManager) handleSeed(w http.ResponseWriter, r *http.Request) {
	restaurantID := chiURLInt(r, "restaurantId")
	var templateID string
	_ = m.db.QueryRowContext(r.Context(), `SELECT template_id FROM restaurant_websites WHERE restaurant_id=? LIMIT 1`, restaurantID).Scan(&templateID)
	if strings.TrimSpace(templateID) == "" {
		templateID = "villa-carmen"
	}
	if err := m.seedSite(r.Context(), restaurantID, templateID); err != nil {
		httpxWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"success": true, "template": templateID})
}

func (m *instaticManager) handlePublish(w http.ResponseWriter, r *http.Request) {
	restaurantID := chiURLInt(r, "restaurantId")
	base, err := m.EnsureRunning(r.Context(), restaurantID)
	if err != nil {
		httpxWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var out any
	if err := m.call(r.Context(), base, http.MethodPost, "/admin/api/cms/publish", map[string]any{}, &out); err != nil {
		httpxWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"success": true, "result": out})
}

func (m *instaticManager) handleProvisionDomain(w http.ResponseWriter, r *http.Request) {
	restaurantID := chiURLInt(r, "restaurantId")
	sub, err := m.ProvisionSubdomain(r.Context(), restaurantID)
	if err != nil {
		httpxWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"success": true, "subdomain": sub})
}

// ---------------------------------------------------------------------------
// Seeding: gather restaurant data from MySQL, run the instatic seeder script.
// ---------------------------------------------------------------------------

// seedSite provisions the restaurant's content into its instatic instance by
// invoking the bun restaurant-seed script. Go owns the data extraction; the
// script owns instatic's schema (valid page trees via createNode/insertNode).
func (m *instaticManager) seedSite(ctx context.Context, restaurantID int, template string) error {
	base, err := m.EnsureRunning(ctx, restaurantID)
	if err != nil {
		return err
	}

	// Session cookie for this restaurant's instance.
	var cookie string
	if err := m.db.QueryRow(`SELECT instatic_session_token FROM instatic_instances WHERE restaurant_id=?`, restaurantID).Scan(&cookie); err != nil {
		return fmt.Errorf("seed: no session for restaurant %d: %w", restaurantID, err)
	}

	payload, err := m.buildRestaurantPayload(ctx, restaurantID, template)
	if err != nil {
		return err
	}

	dataFile := filepath.Join(m.instanceDataDir(restaurantID), "seed-payload.json")
	if err := writeJSONFile(dataFile, payload); err != nil {
		return fmt.Errorf("seed: write payload: %w", err)
	}

	cmd := exec.CommandContext(ctx, "bun", "run", "scripts/restaurant-seed.ts",
		"--base", base,
		"--cookie", cookie,
		"--data", dataFile,
	)
	cmd.Dir = m.cfg.InstaticServerDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("seed script failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// buildRestaurantPayload extracts restaurant info + menu + hours from MySQL
// into the JSON shape the instatic seeder expects.
func (m *instaticManager) buildRestaurantPayload(ctx context.Context, restaurantID int, template string) (map[string]any, error) {
	payload := map[string]any{}
	payload["template"] = template
	payload["restaurantId"] = restaurantID
	payload["widgetScript"] = bookingWidgetJS

	var name, address, phone, email string
	_ = m.db.QueryRowContext(ctx, `SELECT name FROM restaurants WHERE id=?`, restaurantID).Scan(&name)
	_ = m.db.QueryRowContext(ctx, `SELECT direccion, telefono, email FROM restaurant_info WHERE restaurant_id=?`, restaurantID).Scan(&address, &phone, &email)
	payload["name"] = name
	payload["address"] = address
	payload["phone"] = phone
	payload["email"] = email

	// Branding: brand name overrides the plain restaurant name; logo + colors.
	var brandName, logo, brandPrimary, brandAccent sql.NullString
	_ = m.db.QueryRowContext(ctx, `SELECT brand_name, logo_url, primary_color, accent_color FROM restaurant_branding WHERE restaurant_id=?`, restaurantID).
		Scan(&brandName, &logo, &brandPrimary, &brandAccent)
	if brandName.Valid && brandName.String != "" {
		payload["name"] = brandName.String
	}
	payload["logo"] = nullStringValue(logo)
	payload["primaryColor"] = nullStringValue(brandPrimary)
	payload["accentColor"] = nullStringValue(brandAccent)

	// Widget colors (booking form theming) from widget_settings.
	var wc struct{ p, s, b, sf, t, mu, font sql.NullString }
	_ = m.db.QueryRowContext(ctx, `SELECT primary_color, success_color, border_color, surface_color, text_color, muted_color, font_stack FROM widget_settings WHERE restaurant_id=?`, restaurantID).
		Scan(&wc.p, &wc.s, &wc.b, &wc.sf, &wc.t, &wc.mu, &wc.font)
	payload["widgetColors"] = map[string]any{
		"primary": nullStringValue(wc.p), "success": nullStringValue(wc.s), "border": nullStringValue(wc.b),
		"surface": nullStringValue(wc.sf), "text": nullStringValue(wc.t), "muted": nullStringValue(wc.mu),
		"font": nullStringValue(wc.font),
	}

	// Menu from `comida_items` (the live editable menu): rich rows grouped by
	// categoria, in DB order. name/price/desc/allergens/supplement.
	sections := m.comidaSections(ctx, restaurantID)
	if len(sections) == 0 {
		sections = m.legacyMenuSections(ctx, restaurantID) // fallback: old `menus` table
	}
	payload["menuSections"] = sections

	// Gallery images (menu_slider_images.image_path, in position order).
	var gallery []string
	if grows, gerr := m.db.QueryContext(ctx, `SELECT image_path FROM menu_slider_images WHERE restaurant_id=? ORDER BY position, id`, restaurantID); gerr == nil {
		defer grows.Close()
		for grows.Next() {
			var p string
			if grows.Scan(&p) == nil && p != "" {
				gallery = append(gallery, p)
			}
		}
	}
	payload["gallery"] = gallery

	// Wines (VINOS, active) grouped for a cellar section.
	var wines []map[string]any
	if wrows, werr := m.db.QueryContext(ctx, `SELECT nombre, precio, tipo, bodega, descripcion FROM VINOS WHERE restaurant_id=? AND active=1 ORDER BY tipo, nombre`, restaurantID); werr == nil {
		defer wrows.Close()
		for wrows.Next() {
			var nombre, tipo, bodega, desc sql.NullString
			var precio sql.NullFloat64
			if wrows.Scan(&nombre, &precio, &tipo, &bodega, &desc) != nil {
				continue
			}
			wines = append(wines, map[string]any{
				"name": nullStringValue(nombre), "type": nullStringValue(tipo),
				"winery": nullStringValue(bodega), "desc": nullStringValue(desc),
				"price": priceStr(precio),
			})
		}
	}
	payload["wines"] = wines

	// Opening hours from `openinghours`: hoursarray = JSON array of HH:MM slots.
	// Derive the open→close range from the earliest and latest slot per day.
	type hoursRow struct {
		Hours string
	}
	var hours []map[string]any
	hourRows, err := m.db.QueryContext(ctx, `SELECT hoursarray FROM openinghours WHERE restaurant_id=? ORDER BY dateselected DESC LIMIT 7`, restaurantID)
	if err == nil {
		defer hourRows.Close()
		for hourRows.Next() {
			var hr hoursRow
			if err := hourRows.Scan(&hr.Hours); err != nil {
				continue
			}
			var slots []string
			if json.Unmarshal([]byte(hr.Hours), &slots) != nil || len(slots) == 0 {
				continue
			}
			hours = append(hours, map[string]any{"day": "", "hours": slots[0] + " – " + slots[len(slots)-1]})
		}
	}
	payload["hours"] = hours

	return payload, nil
}

// comidaSections builds rich menu sections from the live comida_items table,
// grouped by categoria in DB order (preserves first-seen category order).
func (m *instaticManager) comidaSections(ctx context.Context, restaurantID int) []map[string]any {
	rows, err := m.db.QueryContext(ctx, `SELECT nombre, precio, suplemento, descripcion, alergenos_json, categoria FROM comida_items WHERE restaurant_id=? AND active=1 ORDER BY category_id, id`, restaurantID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	order := []string{}
	byCat := map[string][]any{}
	for rows.Next() {
		var nombre, desc, alerg, cat sql.NullString
		var precio sql.NullFloat64
		var supl sql.NullFloat64
		if rows.Scan(&nombre, &precio, &supl, &desc, &alerg, &cat) != nil {
			continue
		}
		title := nullStringValue(cat)
		if title == "" {
			title = "Carta"
		}
		if _, ok := byCat[title]; !ok {
			order = append(order, title)
		}
		var allergens []string
		if alerg.Valid {
			_ = json.Unmarshal([]byte(alerg.String), &allergens) // best-effort
		}
		item := map[string]any{
			"name": nullStringValue(nombre), "price": priceStr(precio),
			"desc": nullStringValue(desc), "allergens": allergens,
		}
		if supl.Valid && supl.Float64 > 0 {
			item["supplement"] = priceStr(supl)
		}
		byCat[title] = append(byCat[title], item)
	}
	sections := make([]map[string]any, 0, len(order))
	for _, title := range order {
		sections = append(sections, map[string]any{"title": title, "items": byCat[title]})
	}
	return sections
}

// legacyMenuSections is the old `menus`-table parser, kept only as a fallback
// for restaurants that never migrated to comida_items.
// ponytail: dead once all restaurants use comida_items — delete then.
func (m *instaticManager) legacyMenuSections(ctx context.Context, restaurantID int) []map[string]any {
	var sections []map[string]any
	rows, err := m.db.QueryContext(ctx, `SELECT menu_title, entrantes, principales, postre FROM menus WHERE restaurant_id=? AND active=1 ORDER BY id`, restaurantID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var title, entrantes, principales, postre string
		if rows.Scan(&title, &entrantes, &principales, &postre) != nil || title == "" {
			continue
		}
		items := []any{}
		var ent []string
		if json.Unmarshal([]byte(entrantes), &ent) == nil {
			for _, it := range ent {
				items = append(items, map[string]any{"name": it})
			}
		}
		for _, raw := range []string{principales, postre} {
			var obj struct {
				Items []string `json:"items"`
			}
			if json.Unmarshal([]byte(raw), &obj) == nil && len(obj.Items) > 0 {
				for _, it := range obj.Items {
					items = append(items, map[string]any{"name": it})
				}
				continue
			}
			var arr []string
			if json.Unmarshal([]byte(raw), &arr) == nil {
				for _, it := range arr {
					items = append(items, map[string]any{"name": it})
				}
			}
		}
		sections = append(sections, map[string]any{"title": title, "items": items})
	}
	return sections
}

// priceStr renders a nullable decimal price as a compact string ("18" / "18.5"),
// empty when null/zero.
func priceStr(v sql.NullFloat64) string {
	if !v.Valid || v.Float64 == 0 {
		return ""
	}
	return strconv.FormatFloat(v.Float64, 'f', -1, 64)
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// ---------------------------------------------------------------------------
// Cloudflare DNS provisioning for restaurant subdomains + custom domains.
// ---------------------------------------------------------------------------

// HostPublicIP is the IP subdomains point at. Overridable via env for test
// hosts. This box's public IP is 65.109.100.94 (Hetzner).
func (m *instaticManager) hostPublicIP() string {
	if v := os.Getenv("INSTATIC_HOST_PUBLIC_IP"); v != "" {
		return v
	}
	return "65.109.100.94"
}

func (m *instaticManager) cfClient() *integrations.CloudflareClient {
	return integrations.NewCloudflareClient(m.cfg.CloudflareAPIEmail, m.cfg.CloudflareAPIKey, m.cfg.CloudflareAPIToken, m.cfg.CloudflareAccountID)
}

// cfDNSClient returns a CF client for DNS/zone writes using the global API key
// (the registrar token lacks DNS:Edit). Used for zone + record provisioning.
func (m *instaticManager) cfDNSClient() *integrations.CloudflareClient {
	return integrations.NewCloudflareClient(m.cfg.CloudflareAPIEmail, m.cfg.CloudflareAPIKey, "", m.cfg.CloudflareAccountID)
}

// cfZoneFor resolves the Cloudflare zone that owns a hostname. It finds the
// longest existing zone that is a suffix of the hostname — so
// `backoffice-dev.menustudioai.com` resolves to the `menustudioai.com` zone.
func (m *instaticManager) cfZoneFor(ctx context.Context, cf *integrations.CloudflareClient, hostname string) (integrations.Zone, error) {
	// Try exact zone first, then progressively strip labels.
	labels := strings.Split(hostname, ".")
	for i := 0; i < len(labels)-1; i++ {
		candidate := strings.Join(labels[i:], ".")
		zones, err := cf.ListZones(ctx, candidate)
		if err == nil {
			for _, z := range zones {
				if strings.EqualFold(z.Name, candidate) {
					return z, nil
				}
			}
		}
	}
	// Fall back to creating the zone for the base (apex only works).
	apex := strings.Join(labels[len(labels)-2:], ".")
	return cf.EnsureZone(ctx, apex)
}

// ProvisionSubdomain creates the DNS A record for a restaurant's subdomain
// (e.g. villacarmen.backoffice-dev.menustudioai.com → host IP, proxied so
// Cloudflare terminates TLS). Returns the subdomain FQDN. Idempotent.
func (m *instaticManager) ProvisionSubdomain(ctx context.Context, restaurantID int) (string, error) {
	cf := m.cfClient()
	base := m.appBaseURL()
	sub := m.subdomainFor(restaurantID) // slug.base

	zone, err := m.cfZoneFor(ctx, cf, base)
	if err != nil {
		return "", fmt.Errorf("resolve zone for %s: %w", base, err)
	}
	if _, err := cf.EnsureDNSRecord(ctx, zone.ID, "A", sub, m.hostPublicIP(), true); err != nil {
		return "", fmt.Errorf("ensure A record %s: %w", sub, err)
	}
	// Record the domain mapping.
	_, _ = m.db.Exec(`
		INSERT INTO site_builder_domain_mappings (id, site_id, domain, is_primary, status, ssl_status)
		SELECT UUID(), id, ?, 1, 'active', 'active' FROM site_builder_sites WHERE restaurant_id = ? AND subdomain = ?
		ON DUPLICATE KEY UPDATE status='active', ssl_status='active'
	`, sub, restaurantID, strings.SplitN(sub, ".", 2)[0])
	_, _ = m.db.Exec(`
		INSERT INTO restaurant_domains (restaurant_id, domain, is_primary, registration_status, cf_zone_id)
		VALUES (?, ?, 1, 'provisioned', ?)
		ON DUPLICATE KEY UPDATE registration_status='provisioned', cf_zone_id=?
	`, restaurantID, sub, zone.ID, zone.ID)
	return sub, nil
}

// ---------------------------------------------------------------------------
// Small local helpers (avoid importing httpx/chi helpers that may not exist).
// ---------------------------------------------------------------------------

func chiURLInt(r *http.Request, key string) int {
	v := chi.URLParam(r, key)
	var n int
	fmt.Sscanf(v, "%d", &n)
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpxWriteError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"success": false, "message": msg})
}
