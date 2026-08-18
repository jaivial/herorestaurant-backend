package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// translationSystemPrompt is intentionally simple per product spec.
const translationSystemPrompt = "You are translating a restaurant menu. Translate the user message to English. Return ONLY the translated text. Never add markdown, quotes, notes, or explanations."

// translationLang is the only target language supported for now.
const translationLang = "en"

// Entity type identifiers used in dish_translations.entity_type.
const (
	entityComidaItems   = "comida_items"
	entityPostres       = "POSTRES"
	entityVinos         = "VINOS"
	entityMenus         = "menus"
	entitySections      = "group_menu_sections_v2"
	entitySectionDishes = "group_menu_section_dishes_v2"
	entityDishCatalog   = "menu_dishes_catalog"
	// DIA and FINDE use their table name as entity_type.
)

// minimaxMessagesResponse mirrors the Anthropic-compatible messages API shape
// that MiniMax exposes at {base}/v1/messages.
type minimaxMessagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func (s *Server) translationsEnabled(ctx context.Context, restaurantID int) bool {
	// Root of truth is now the per-restaurant config; the global env key is the
	// legacy fallback.
	if restaurantID > 0 {
		return s.hasMiniMaxConfig(ctx, restaurantID)
	}
	return strings.TrimSpace(s.cfg.MiniMaxAPIKey) != ""
}

func (s *Server) minimaxTranslateConcurrency() int {
	n := s.cfg.MiniMaxTranslateConcurrency
	if n <= 0 {
		return 4
	}
	return n
}

// translateToEnglish sends a single field of text to MiniMax and returns the
// English translation. It never logs the API key or the source/target text.
func (s *Server) translateToEnglish(ctx context.Context, restaurantID int, text string) (string, error) {
	apiKey := s.resolveMiniMaxKey(ctx, restaurantID)
	if apiKey == "" {
		return "", errors.New("minimax api key not configured")
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", nil
	}

	reqBody := map[string]any{
		"model":      s.resolveMiniMaxModel(ctx, restaurantID, ""),
		"max_tokens": 1024,
		"system":     translationSystemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": trimmed},
		},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(s.cfg.MiniMaxBaseURL, "/") + "/v1/messages"
	timeout := s.cfg.MiniMaxTranslateTimeout
	if timeout <= 0 {
		timeout = 20_000_000_000 // 20s
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	cli := &http.Client{Timeout: timeout}
	resp, err := cli.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("minimax translate http %d", resp.StatusCode)
	}

	var parsed minimaxMessagesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("minimax translate error: %s", parsed.Error.Type)
	}

	var sb strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "", errors.New("minimax translate empty response")
	}
	return out, nil
}

// translationField is a single (name, text) pair to translate.
type translationField struct {
	Name string
	Text string
}

// arrayFieldName builds a stable per-index field name for array fields.
func arrayFieldName(base string, idx int) string {
	return base + "." + strconv.Itoa(idx)
}

// flattenArrayFields expands a string slice into per-index translation fields.
func flattenArrayFields(base string, items []string) []translationField {
	out := make([]translationField, 0, len(items))
	for i, it := range items {
		out = append(out, translationField{Name: arrayFieldName(base, i), Text: it})
	}
	return out
}

// buildEnglishArray reconstructs an English array aligned to a Spanish array of
// length n. Returns nil if no element has a translation.
func buildEnglishArray(m map[string]string, base string, n int) []string {
	if len(m) == 0 || n <= 0 {
		return nil
	}
	out := make([]string, n)
	any := false
	for i := 0; i < n; i++ {
		if v := translationOr(m, arrayFieldName(base, i)); v != "" {
			out[i] = v
			any = true
		}
	}
	if !any {
		return nil
	}
	return out
}

// loadTranslations returns a nested map: entityID -> field -> translated text
// for the given entity type and language, for the provided entity IDs.
func (s *Server) loadTranslations(ctx context.Context, restaurantID int, entityType string, ids []int64, lang string) (map[int64]map[string]string, error) {
	out := make(map[int64]map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	if lang == "" {
		lang = translationLang
	}

	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+3)
	args = append(args, restaurantID, entityType, lang)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}

	query := `
		SELECT entity_id, field_name, translated_text
		FROM dish_translations
		WHERE restaurant_id = ? AND entity_type = ? AND lang = ?
		  AND entity_id IN (` + strings.Join(placeholders, ",") + `)`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			entityID int64
			field    string
			text     string
		)
		if err := rows.Scan(&entityID, &field, &text); err != nil {
			return out, err
		}
		if out[entityID] == nil {
			out[entityID] = make(map[string]string, 4)
		}
		out[entityID][field] = text
	}
	return out, rows.Err()
}

// loadEntityTranslations is a convenience wrapper for a single entity ID.
func (s *Server) loadEntityTranslations(ctx context.Context, restaurantID int, entityType string, id int64, lang string) map[string]string {
	all, err := s.loadTranslations(ctx, restaurantID, entityType, []int64{id}, lang)
	if err != nil {
		return map[string]string{}
	}
	if m := all[id]; m != nil {
		return m
	}
	return map[string]string{}
}

func (s *Server) upsertTranslation(ctx context.Context, restaurantID int, entityType string, entityID int64, field, sourceHash, translated string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO dish_translations
			(restaurant_id, entity_type, entity_id, field_name, lang, source_hash, translated_text)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			source_hash = VALUES(source_hash),
			translated_text = VALUES(translated_text)
	`, restaurantID, entityType, entityID, field, translationLang, sourceHash, translated)
	return err
}

// translateEntityFields translates the given text fields to English concurrently
// and upserts them into dish_translations. It skips empty fields and fields
// whose source_hash already matches (unchanged text). It never fails the caller:
// translation errors are logged and swallowed so the primary write still
// succeeds. It returns the map of field -> translated text that were produced
// (or already up to date), so callers can enrich their response immediately.
func (s *Server) translateEntityFields(ctx context.Context, restaurantID int, entityType string, entityID int64, fields []translationField) map[string]string {
	result := make(map[string]string, len(fields))
	if !s.translationsEnabled(ctx, restaurantID) || entityID <= 0 {
		return result
	}

	// Load existing translations to skip unchanged fields.
	existing := s.loadExistingTranslationHashes(ctx, restaurantID, entityType, entityID)

	type job struct {
		field translationField
	}
	toRun := make([]job, 0, len(fields))
	for _, f := range fields {
		text := strings.TrimSpace(f.Text)
		if text == "" {
			continue
		}
		h := hashText(text)
		if prev, ok := existing[f.Name]; ok && prev.hash == h {
			// Unchanged: reuse existing translation.
			result[f.Name] = prev.text
			continue
		}
		toRun = append(toRun, job{field: translationField{Name: f.Name, Text: text}})
	}
	if len(toRun) == 0 {
		return result
	}

	sem := make(chan struct{}, s.minimaxTranslateConcurrency())
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for _, jb := range toRun {
		wg.Add(1)
		go func(f translationField) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			translated, err := s.translateToEnglish(ctx, restaurantID, f.Text)
			if err != nil {
				log.Printf("[translations] entity=%s id=%d field=%s status=error err=%v", entityType, entityID, f.Name, err)
				return
			}
			if translated == "" {
				return
			}
			if err := s.upsertTranslation(ctx, restaurantID, entityType, entityID, f.Name, hashText(f.Text), translated); err != nil {
				log.Printf("[translations] entity=%s id=%d field=%s status=dberror err=%v", entityType, entityID, f.Name, err)
				return
			}
			log.Printf("[translations] entity=%s id=%d field=%s status=ok", entityType, entityID, f.Name)
			mu.Lock()
			result[f.Name] = translated
			mu.Unlock()
		}(jb.field)
	}
	wg.Wait()
	return result
}

// ---- menus / sections / dishes (group menus v2) ----

func (s *Server) translateMenuBasics(ctx context.Context, restaurantID int, menuID int64, title string, subtitle, comments []string) {
	if menuID <= 0 {
		return
	}
	fields := []translationField{{Name: "menu_title", Text: title}}
	fields = append(fields, flattenArrayFields("menu_subtitle", subtitle)...)
	fields = append(fields, flattenArrayFields("comments", comments)...)
	s.translateEntityFields(ctx, restaurantID, entityMenus, menuID, fields)
}

func (s *Server) translateMenuConventionalArrays(ctx context.Context, restaurantID int, menuID int64, entrantes []string, principalesTitle string, principalesItems []string, postre []string) {
	if menuID <= 0 {
		return
	}
	fields := []translationField{{Name: "principales_title", Text: principalesTitle}}
	fields = append(fields, flattenArrayFields("entrantes", entrantes)...)
	fields = append(fields, flattenArrayFields("principales", principalesItems)...)
	fields = append(fields, flattenArrayFields("postre", postre)...)
	s.translateEntityFields(ctx, restaurantID, entityMenus, menuID, fields)
}

func (s *Server) translateSection(ctx context.Context, restaurantID int, sectionID int64, title string, annotations []string) {
	if sectionID <= 0 {
		return
	}
	fields := []translationField{{Name: "title", Text: title}}
	fields = append(fields, flattenArrayFields("annotations", annotations)...)
	s.translateEntityFields(ctx, restaurantID, entitySections, sectionID, fields)
}

func (s *Server) translateSectionDish(ctx context.Context, restaurantID int, dishID int64, title, description string) {
	if dishID <= 0 {
		return
	}
	s.translateEntityFields(ctx, restaurantID, entitySectionDishes, dishID, []translationField{
		{Name: "title", Text: title},
		{Name: "description", Text: description},
	})
}

// enrichPublicMenus bulk-loads translations for a slice of public menu items
// (including their sections and dishes) and applies the English fields.
func (s *Server) enrichPublicMenus(ctx context.Context, restaurantID int, menus []publicMenuItem) {
	if len(menus) == 0 {
		return
	}
	menuIDs := make([]int64, 0, len(menus))
	sectionIDs := make([]int64, 0, 32)
	dishIDs := make([]int64, 0, 128)
	for i := range menus {
		menuIDs = append(menuIDs, menus[i].ID)
		for si := range menus[i].Sections {
			sectionIDs = append(sectionIDs, menus[i].Sections[si].ID)
			for di := range menus[i].Sections[si].Dishes {
				if id := menus[i].Sections[si].Dishes[di].ID; id > 0 {
					dishIDs = append(dishIDs, id)
				}
			}
		}
	}

	menuTr, _ := s.loadTranslations(ctx, restaurantID, entityMenus, menuIDs, translationLang)
	secTr, _ := s.loadTranslations(ctx, restaurantID, entitySections, sectionIDs, translationLang)
	dishTr, _ := s.loadTranslations(ctx, restaurantID, entitySectionDishes, dishIDs, translationLang)

	for i := range menus {
		m := &menus[i]
		if mt := menuTr[m.ID]; mt != nil {
			m.MenuTitleEnglish = translationOr(mt, "menu_title")
			m.MenuSubtitleEnglish = buildEnglishArray(mt, "menu_subtitle", len(m.MenuSubtitle))
			m.Settings.CommentsEnglish = buildEnglishArray(mt, "comments", len(m.Settings.Comments))
		}
		// Enrich fallback sections (ID=0, used by closed_conventional / a_la_carte)
		s.enrichFallbackSections(m, menuTr)
		for si := range m.Sections {
			sec := &m.Sections[si]
			if st := secTr[sec.ID]; st != nil {
				sec.TitleEnglish = translationOr(st, "title")
				sec.AnnotationsEnglish = buildEnglishArray(st, "annotations", len(sec.Annotations))
			}
			for di := range sec.Dishes {
				d := &sec.Dishes[di]
				if dt := dishTr[d.ID]; dt != nil {
					d.TitleEnglish = translationOr(dt, "title")
					d.DescriptionEnglish = translationOr(dt, "description")
				}
			}
		}
	}
}

func (s *Server) enrichPublicHomeMenus(ctx context.Context, restaurantID int, menus []publicMenuItemHome) {
	ids := make([]int64, len(menus))
	for i := range menus {
		ids[i] = menus[i].ID
	}
	all, err := s.loadTranslations(ctx, restaurantID, entityMenus, ids, translationLang)
	if err != nil {
		return
	}
	applyPublicHomeMenuTranslations(menus, all)
}

func applyPublicHomeMenuTranslations(menus []publicMenuItemHome, all map[int64]map[string]string) {
	for i := range menus {
		tr := all[menus[i].ID]
		menus[i].MenuTitleEnglish = translationOr(tr, "menu_title")
		menus[i].MenuSubtitleEnglish = buildEnglishArray(tr, "menu_subtitle", len(menus[i].MenuSubtitle))
	}
}

// enrichPublicMenu enriches a single public menu item.
func (s *Server) enrichPublicMenu(ctx context.Context, restaurantID int, m *publicMenuItem) {
	if m == nil {
		return
	}
	one := []publicMenuItem{*m}
	s.enrichPublicMenus(ctx, restaurantID, one)
	*m = one[0]
}

// enrichFallbackSections populates _english fields on fallback sections (ID=0)
// by reading entrantes.X / principales.X / postre.X translations from menuTr.
func (s *Server) enrichFallbackSections(m *publicMenuItem, menuTr map[int64]map[string]string) {
	if m == nil || len(m.Sections) == 0 || m.Sections[0].ID != 0 {
		return
	}
	mt := menuTr[m.ID]
	if mt == nil {
		return
	}
	for si := range m.Sections {
		sec := &m.Sections[si]
		var prefix string
		switch sec.Kind {
		case "entrantes":
			prefix = "entrantes"
		case "principales":
			prefix = "principales"
			sec.TitleEnglish = translationOr(mt, "principales_title")
		case "postres":
			prefix = "postre"
		default:
			continue
		}
		for di := range sec.Dishes {
			d := &sec.Dishes[di]
			if en := translationOr(mt, fmt.Sprintf("%s.%d", prefix, di)); en != "" {
				d.TitleEnglish = en
			}
		}
	}
}

func decodePrincipalesItemsJSON(raw string) []string {
	decoded := decodeJSONOrFallback(raw, map[string]any{})
	m, _ := decoded.(map[string]any)
	if m != nil {
		if items, ok := m["items"]; ok {
			return anySliceToStringList(items)
		}
	}
	return nil
}

func decodePrincipalesTitleJSON(raw string) string {
	decoded := decodeJSONOrFallback(raw, map[string]any{})
	m, _ := decoded.(map[string]any)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(anyToString(m["titulo_principales"]))
}

type translationHash struct {
	hash string
	text string
}

func (s *Server) loadExistingTranslationHashes(ctx context.Context, restaurantID int, entityType string, entityID int64) map[string]translationHash {
	out := make(map[string]translationHash, 4)
	rows, err := s.db.QueryContext(ctx, `
		SELECT field_name, source_hash, translated_text
		FROM dish_translations
		WHERE restaurant_id = ? AND entity_type = ? AND entity_id = ? AND lang = ?
	`, restaurantID, entityType, entityID, translationLang)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var field, h, text string
		if err := rows.Scan(&field, &h, &text); err != nil {
			return out
		}
		out[field] = translationHash{hash: h, text: text}
	}
	return out
}

// deleteEntityTranslations removes all translations for an entity (used on delete).
func (s *Server) deleteEntityTranslations(ctx context.Context, entityType string, restaurantID int, entityID int64) {
	_, _ = s.db.ExecContext(ctx, `
		DELETE FROM dish_translations
		WHERE restaurant_id = ? AND entity_type = ? AND entity_id = ?
	`, restaurantID, entityType, entityID)
}

// firstNonEmptyTranslation returns the English value if present and non-empty.
func translationOr(m map[string]string, field string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[field]; ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// ensure sql import is used even if some helpers are trimmed later.
var _ = sql.ErrNoRows

// ---- comida_items enrichment ----

func comidaItemTranslationFields(item comidaItemResponse) []translationField {
	return []translationField{
		{Name: "nombre", Text: item.Nombre},
		{Name: "descripcion", Text: item.Descripcion},
		{Name: "titulo", Text: item.Titulo},
		{Name: "tipo", Text: item.Tipo},
		{Name: "categoria", Text: item.Categoria},
	}
}

func applyComidaItemTranslations(m map[string]string, item *comidaItemResponse) {
	if item == nil || len(m) == 0 {
		return
	}
	item.NombreEnglish = translationOr(m, "nombre")
	item.DescripcionEnglish = translationOr(m, "descripcion")
	item.TituloEnglish = translationOr(m, "titulo")
	item.TipoEnglish = translationOr(m, "tipo")
	item.CategoriaEnglish = translationOr(m, "categoria")
}

// translateComidaItem translates changed fields and applies them to the item.
func (s *Server) translateComidaItem(ctx context.Context, restaurantID int, item *comidaItemResponse) {
	if item == nil || item.Num <= 0 {
		return
	}
	m := s.translateEntityFields(ctx, restaurantID, entityComidaItems, int64(item.Num), comidaItemTranslationFields(*item))
	applyComidaItemTranslations(m, item)
}

// enrichComidaItem loads stored translations and applies them (read path).
func (s *Server) enrichComidaItem(ctx context.Context, restaurantID int, item *comidaItemResponse) {
	if item == nil || item.Num <= 0 {
		return
	}
	m := s.loadEntityTranslations(ctx, restaurantID, entityComidaItems, int64(item.Num), translationLang)
	applyComidaItemTranslations(m, item)
}

// enrichComidaItems bulk-loads translations for a slice of items (read path).
func (s *Server) enrichComidaItems(ctx context.Context, restaurantID int, items []comidaItemResponse) {
	if len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, int64(it.Num))
	}
	all, err := s.loadTranslations(ctx, restaurantID, entityComidaItems, ids, translationLang)
	if err != nil {
		return
	}
	for i := range items {
		applyComidaItemTranslations(all[int64(items[i].Num)], &items[i])
	}
}

// ---- VINOS enrichment ----

func vinoTranslationFields(v comidaVinoResponse) []translationField {
	return []translationField{
		{Name: "nombre", Text: v.Nombre},
		{Name: "descripcion", Text: v.Descripcion},
		{Name: "bodega", Text: v.Bodega},
		{Name: "denominacion_origen", Text: v.DenominacionOrigen},
		{Name: "tipo", Text: v.Tipo},
	}
}

func applyVinoTranslations(m map[string]string, v *comidaVinoResponse) {
	if v == nil || len(m) == 0 {
		return
	}
	v.NombreEnglish = translationOr(m, "nombre")
	v.DescripcionEnglish = translationOr(m, "descripcion")
	v.BodegaEnglish = translationOr(m, "bodega")
	v.DenominacionOrigenEnglish = translationOr(m, "denominacion_origen")
	v.TipoEnglish = translationOr(m, "tipo")
}

func (s *Server) translateVino(ctx context.Context, restaurantID int, v *comidaVinoResponse) {
	if v == nil || v.Num <= 0 {
		return
	}
	m := s.translateEntityFields(ctx, restaurantID, entityVinos, int64(v.Num), vinoTranslationFields(*v))
	applyVinoTranslations(m, v)
}

func (s *Server) enrichVino(ctx context.Context, restaurantID int, v *comidaVinoResponse) {
	if v == nil || v.Num <= 0 {
		return
	}
	m := s.loadEntityTranslations(ctx, restaurantID, entityVinos, int64(v.Num), translationLang)
	applyVinoTranslations(m, v)
}

func (s *Server) enrichVinos(ctx context.Context, restaurantID int, items []comidaVinoResponse) {
	if len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, int64(it.Num))
	}
	all, err := s.loadTranslations(ctx, restaurantID, entityVinos, ids, translationLang)
	if err != nil {
		return
	}
	for i := range items {
		applyVinoTranslations(all[int64(items[i].Num)], &items[i])
	}
}

// ---- POSTRES enrichment ----

func (s *Server) translatePostre(ctx context.Context, restaurantID int, item *comidaItemResponse, postre *comidaPostreResponse) {
	if postre == nil || postre.Num <= 0 {
		return
	}
	m := s.translateEntityFields(ctx, restaurantID, entityPostres, int64(postre.Num), []translationField{
		{Name: "descripcion", Text: postre.Descripcion},
	})
	if en := translationOr(m, "descripcion"); en != "" {
		postre.DescripcionEnglish = en
		if item != nil {
			item.DescripcionEnglish = en
			item.NombreEnglish = en
		}
	}
}

func (s *Server) enrichPostre(ctx context.Context, restaurantID int, item *comidaItemResponse, postre *comidaPostreResponse) {
	if postre == nil || postre.Num <= 0 {
		return
	}
	m := s.loadEntityTranslations(ctx, restaurantID, entityPostres, int64(postre.Num), translationLang)
	if en := translationOr(m, "descripcion"); en != "" {
		postre.DescripcionEnglish = en
		if item != nil {
			item.DescripcionEnglish = en
			item.NombreEnglish = en
		}
	}
}

func (s *Server) enrichPostresList(ctx context.Context, restaurantID int, items []comidaItemResponse, postres []comidaPostreResponse) {
	if len(postres) == 0 {
		return
	}
	ids := make([]int64, 0, len(postres))
	for _, p := range postres {
		ids = append(ids, int64(p.Num))
	}
	all, err := s.loadTranslations(ctx, restaurantID, entityPostres, ids, translationLang)
	if err != nil {
		return
	}
	for i := range postres {
		if en := translationOr(all[int64(postres[i].Num)], "descripcion"); en != "" {
			postres[i].DescripcionEnglish = en
		}
	}
	for i := range items {
		if en := translationOr(all[int64(items[i].Num)], "descripcion"); en != "" {
			items[i].DescripcionEnglish = en
			items[i].NombreEnglish = en
		}
	}
}
