// Command backfill-translations translates existing dish/menu text to English
// and stores it in the dish_translations table. Resume-safe: rows whose
// source_hash already matches are skipped. Never prints source text or the key.
//
//	cd backend
//	VAULT_TOKEN=... go run ./cmd/backfill-translations --restaurant-id=1 --dry-run
//	VAULT_TOKEN=... go run ./cmd/backfill-translations --restaurant-id=1
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"

	"preactvillacarmen/internal/vault"
)

const systemPrompt = "You are translating a restaurant menu. Translate the user message to English. Return ONLY the translated text. Never add markdown, quotes, notes, or explanations."

func main() {
	_ = godotenv.Load()

	restaurantID := flag.Int("restaurant-id", 1, "restaurant id to backfill")
	dryRun := flag.Bool("dry-run", false, "do not call MiniMax or write; just report counts")
	flag.Parse()

	host := envDefault("DB_HOST", "127.0.0.1")
	port := envDefault("DB_PORT", "3306")
	user := envDefault("DB_USER", "villacarmen")
	password := envDefault("DB_PASSWORD", "villacarmen")
	dbName := envDefault("DB_NAME", "villacarmen")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&collation=utf8mb4_unicode_ci",
		user, password, host, port, dbName)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		fmt.Printf("open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		fmt.Printf("ping db: %v\n", err)
		os.Exit(1)
	}

	tr := &translator{
		db:      db,
		baseURL: strings.TrimRight(envDefault("MINIMAX_BASE_URL", "https://api.minimax.io/anthropic"), "/"),
		timeout: 20 * time.Second,
	}
	if !*dryRun && os.Getenv("VAULT_TOKEN") == "" && strings.TrimSpace(os.Getenv("MINIMAX_API_KEY")) == "" {
		fmt.Println("VAULT_TOKEN (or MINIMAX_API_KEY fallback) is required unless --dry-run")
		os.Exit(1)
	}

	total := 0
	total += backfillCatalog(ctx, db, tr, *restaurantID, *dryRun)
	total += backfillSimple(ctx, db, tr, *restaurantID, *dryRun, "POSTRES", "NUM", "POSTRES", map[string]string{"descripcion": "DESCRIPCION"})
	total += backfillSimple(ctx, db, tr, *restaurantID, *dryRun, "VINOS", "num", "VINOS", map[string]string{
		"nombre": "nombre", "descripcion": "descripcion", "bodega": "bodega", "denominacion_origen": "denominacion_origen", "tipo": "tipo",
	})
	total += backfillMenuTable(ctx, db, tr, *restaurantID, *dryRun, "DIA")
	total += backfillMenuTable(ctx, db, tr, *restaurantID, *dryRun, "FINDE")
	total += backfillGroupMenus(ctx, db, tr, *restaurantID, *dryRun)
	total += backfillGroupMenuSections(ctx, db, tr, *restaurantID, *dryRun)
	total += backfillGroupMenuDishes(ctx, db, tr, *restaurantID, *dryRun)

	fmt.Printf("done. fields processed: %d (dry-run=%v)\n", total, *dryRun)
}

// ---- entity backfills ----

func backfillCatalog(ctx context.Context, db *sql.DB, tr *translator, restaurantID int, dry bool) int {
	rows, err := db.QueryContext(ctx, `
		SELECT id, COALESCE(nombre,''), COALESCE(descripcion,''), COALESCE(titulo,''), COALESCE(tipo,''), COALESCE(categoria,'')
		FROM comida_items WHERE restaurant_id = ?`, restaurantID)
	if err != nil {
		fmt.Printf("comida_items query: %v\n", err)
		return 0
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var nombre, desc, titulo, tipo, categoria string
		if err := rows.Scan(&id, &nombre, &desc, &titulo, &tipo, &categoria); err != nil {
			continue
		}
		fields := map[string]string{
			"nombre": nombre, "descripcion": desc, "titulo": titulo, "tipo": tipo, "categoria": categoria,
		}
		count += processEntity(ctx, db, tr, restaurantID, "comida_items", id, fields, dry)
	}
	return count
}

func backfillSimple(ctx context.Context, db *sql.DB, tr *translator, restaurantID int, dry bool, table, idCol, entityType string, cols map[string]string) int {
	sel := idCol
	for _, c := range cols {
		sel += ", COALESCE(" + c + ",'')"
	}
	rows, err := db.QueryContext(ctx, "SELECT "+sel+" FROM "+table+" WHERE restaurant_id = ?", restaurantID)
	if err != nil {
		fmt.Printf("%s query: %v\n", table, err)
		return 0
	}
	defer rows.Close()

	fieldNames := make([]string, 0, len(cols))
	for name := range cols {
		fieldNames = append(fieldNames, name)
	}

	count := 0
	for rows.Next() {
		dest := make([]any, 0, len(fieldNames)+1)
		var id int64
		dest = append(dest, &id)
		vals := make([]string, len(fieldNames))
		for i := range fieldNames {
			dest = append(dest, &vals[i])
		}
		if err := rows.Scan(dest...); err != nil {
			continue
		}
		fields := make(map[string]string, len(fieldNames))
		for i, name := range fieldNames {
			fields[name] = vals[i]
		}
		count += processEntity(ctx, db, tr, restaurantID, entityType, id, fields, dry)
	}
	return count
}

func backfillMenuTable(ctx context.Context, db *sql.DB, tr *translator, restaurantID int, dry bool, table string) int {
	rows, err := db.QueryContext(ctx, "SELECT NUM, COALESCE(DESCRIPCION,''), COALESCE(TIPO,'') FROM "+table+" WHERE restaurant_id = ?", restaurantID)
	if err != nil {
		fmt.Printf("%s query: %v\n", table, err)
		return 0
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var desc, tipo string
		if err := rows.Scan(&id, &desc, &tipo); err != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(tipo), "PRECIO") {
			continue
		}
		count += processEntity(ctx, db, tr, restaurantID, table, id, map[string]string{"descripcion": desc}, dry)
	}
	return count
}

func backfillGroupMenus(ctx context.Context, db *sql.DB, tr *translator, restaurantID int, dry bool) int {
	rows, err := db.QueryContext(ctx, `
		SELECT id, COALESCE(menu_title,''), COALESCE(menu_subtitle,'[]'), COALESCE(comments,'[]'),
		       COALESCE(entrantes,'[]'), COALESCE(principales,'{}'), COALESCE(postre,'[]')
		FROM menus WHERE restaurant_id = ?`, restaurantID)
	if err != nil {
		fmt.Printf("menus query: %v\n", err)
		return 0
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var title string
		var subtitleRaw, commentsRaw, entrantesRaw, principalesRaw, postreRaw string
		if err := rows.Scan(&id, &title, &subtitleRaw, &commentsRaw, &entrantesRaw, &principalesRaw, &postreRaw); err != nil {
			continue
		}
		fields := map[string]string{"menu_title": title}
		for i, s := range parseJSONArray(subtitleRaw) {
			fields[fmt.Sprintf("menu_subtitle.%d", i)] = s
		}
		for i, s := range parseJSONArray(commentsRaw) {
			fields[fmt.Sprintf("comments.%d", i)] = s
		}
		for i, s := range parseJSONArray(entrantesRaw) {
			fields[fmt.Sprintf("entrantes.%d", i)] = s
		}
		if title := parsePrincipalesTitle(principalesRaw); title != "" {
			fields["principales_title"] = title
		}
		for i, s := range parsePrincipalesItems(principalesRaw) {
			fields[fmt.Sprintf("principales.%d", i)] = s
		}
		for i, s := range parseJSONArray(postreRaw) {
			fields[fmt.Sprintf("postre.%d", i)] = s
		}
		count += processEntity(ctx, db, tr, restaurantID, "menus", id, fields, dry)
	}
	return count
}

func backfillGroupMenuSections(ctx context.Context, db *sql.DB, tr *translator, restaurantID int, dry bool) int {
	rows, err := db.QueryContext(ctx, `
		SELECT id, COALESCE(title,''), COALESCE(annotations_json,'[]')
		FROM group_menu_sections_v2 WHERE restaurant_id = ?`, restaurantID)
	if err != nil {
		fmt.Printf("group_menu_sections_v2 query: %v\n", err)
		return 0
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var title string
		var annotationsRaw string
		if err := rows.Scan(&id, &title, &annotationsRaw); err != nil {
			continue
		}
		fields := map[string]string{"title": title}
		for i, s := range parseJSONArray(annotationsRaw) {
			fields[fmt.Sprintf("annotations.%d", i)] = s
		}
		count += processEntity(ctx, db, tr, restaurantID, "group_menu_sections_v2", id, fields, dry)
	}
	return count
}

func backfillGroupMenuDishes(ctx context.Context, db *sql.DB, tr *translator, restaurantID int, dry bool) int {
	rows, err := db.QueryContext(ctx, `
		SELECT id, COALESCE(title_snapshot,''), COALESCE(description_snapshot,'')
		FROM group_menu_section_dishes_v2 WHERE restaurant_id = ? AND active = 1`, restaurantID)
	if err != nil {
		fmt.Printf("group_menu_section_dishes_v2 query: %v\n", err)
		return 0
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var title, description string
		if err := rows.Scan(&id, &title, &description); err != nil {
			continue
		}
		count += processEntity(ctx, db, tr, restaurantID, "group_menu_section_dishes_v2", id, map[string]string{
			"title": title, "description": description,
		}, dry)
	}
	return count
}

func parseJSONArray(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, s := range arr {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parsePrincipalesItems(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	items, _ := m["items"]
	arr, _ := items.([]any)
	out := make([]string, 0, len(arr))
	for _, a := range arr {
		s := strings.TrimSpace(fmt.Sprint(a))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func parsePrincipalesTitle(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	title, ok := m["titulo_principales"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(title))
}

// processEntity translates each non-empty, changed field and upserts it.
func processEntity(ctx context.Context, db *sql.DB, tr *translator, restaurantID int, entityType string, entityID int64, fields map[string]string, dry bool) int {
	existing := loadHashes(ctx, db, restaurantID, entityType, entityID)
	n := 0
	for name, raw := range fields {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		h := hashText(text)
		if prev, ok := existing[name]; ok && prev == h {
			continue
		}
		if dry {
			fmt.Printf("[dry] %s#%d %s\n", entityType, entityID, name)
			n++
			continue
		}
		translated, err := tr.translate(ctx, restaurantID, text)
		if err != nil {
			fmt.Printf("[err] %s#%d %s: %v\n", entityType, entityID, name, err)
			continue
		}
		if translated == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO dish_translations (restaurant_id, entity_type, entity_id, field_name, lang, source_hash, translated_text)
			VALUES (?, ?, ?, ?, 'en', ?, ?)
			ON DUPLICATE KEY UPDATE source_hash = VALUES(source_hash), translated_text = VALUES(translated_text)
		`, restaurantID, entityType, entityID, name, h, translated); err != nil {
			fmt.Printf("[db] %s#%d %s: %v\n", entityType, entityID, name, err)
			continue
		}
		fmt.Printf("[ok] %s#%d %s\n", entityType, entityID, name)
		n++
	}
	return n
}

func loadHashes(ctx context.Context, db *sql.DB, restaurantID int, entityType string, entityID int64) map[string]string {
	out := map[string]string{}
	rows, err := db.QueryContext(ctx, `
		SELECT field_name, source_hash FROM dish_translations
		WHERE restaurant_id = ? AND entity_type = ? AND entity_id = ? AND lang = 'en'`,
		restaurantID, entityType, entityID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var f, h string
		if err := rows.Scan(&f, &h); err == nil {
			out[f] = h
		}
	}
	return out
}

// ---- MiniMax client ----

type translator struct {
	db      *sql.DB
	baseURL string
	timeout time.Duration
}

// translate resolves the per-restaurant MiniMax key from the DB (encrypted at
// rest, decrypted with VAULT_TOKEN) and falls back to the legacy env vars.
func (t *translator) translate(ctx context.Context, restaurantID int, text string) (string, error) {
	apiKey := strings.TrimSpace(os.Getenv("MINIMAX_API_KEY"))
	model := envDefault("MINIMAX_MODEL", "MiniMax-M3")

	if restaurantID > 0 && os.Getenv("VAULT_TOKEN") != "" {
		var encrypted sql.NullString
		var dbModel sql.NullString
		err := t.db.QueryRowContext(ctx,
			`SELECT api_key_encrypted, model FROM restaurant_minimax_config WHERE restaurant_id = ?`,
			restaurantID,
		).Scan(&encrypted, &dbModel)
		if err == nil && encrypted.Valid && encrypted.String != "" {
			if plain, derr := vault.Decrypt(os.Getenv("VAULT_TOKEN"), encrypted.String); derr == nil {
				if plain = strings.TrimSpace(plain); plain != "" {
					apiKey = plain
				}
			} else {
				fmt.Printf("minimax decrypt error restaurant=%d: %v (falling back to env)\n", restaurantID, derr)
			}
		}
		if err == nil && dbModel.Valid && strings.TrimSpace(dbModel.String) != "" {
			model = strings.TrimSpace(dbModel.String)
		}
	}

	if apiKey == "" {
		return "", errors.New("minimax api key not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"system":     systemPrompt,
		"messages":   []map[string]any{{"role": "user", "content": text}},
	})
	reqCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := (&http.Client{Timeout: t.timeout}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, b := range parsed.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "", errors.New("empty response")
	}
	return out, nil
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func envDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
