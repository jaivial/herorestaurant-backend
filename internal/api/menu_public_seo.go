package api

// Coordination id: menu_public_seo_v1 (backoffice -> MySQL -> public API ->
// prerendered canonical URL, head metadata and visible H1).

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"regexp"
	"strings"
)

var menuSEOSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const menuSEOSelect = `COALESCE(seo_slug, ''), COALESCE(seo_title, ''), COALESCE(seo_title_english, ''), COALESCE(seo_description, ''), COALESCE(seo_description_english, ''), COALESCE(seo_h1, ''), COALESCE(seo_h1_english, ''), COALESCE(seo_intro, ''), COALESCE(seo_intro_english, ''), COALESCE(seo_faq, ''), COALESCE(seo_faq_english, '')`

func scanMenuSEO(scanner interface{ Scan(...any) error }, seo *publicMenuSEO) error {
	return scanner.Scan(menuSEOScanTargets(seo)...)
}

func menuSEOScanTargets(seo *publicMenuSEO, prefix ...any) []any {
	return append(prefix, &seo.SEOSlug, &seo.SEOTitle, &seo.SEOTitleEnglish, &seo.SEODescription, &seo.SEODescriptionEnglish, &seo.SEOH1, &seo.SEOH1English, &seo.SEOIntro, &seo.SEOIntroEnglish, &seo.SEOFAQ, &seo.SEOFAQEnglish)
}

func (s *Server) loadPublicMenuSEO(ctx context.Context, restaurantID int, menuID int64) publicMenuSEO {
	var seo publicMenuSEO
	err := scanMenuSEO(s.db.QueryRowContext(ctx, `SELECT `+menuSEOSelect+` FROM menus WHERE id = ? AND restaurant_id = ?`, menuID, restaurantID), &seo)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("menu_public_seo_load_failed restaurant_id=%d menu_id=%d error=%v", restaurantID, menuID, err)
	}
	return seo
}

func (s *Server) loadPublicMenuSEOMap(ctx context.Context, restaurantID int) map[int64]publicMenuSEO {
	out := make(map[int64]publicMenuSEO)
	rows, err := s.db.QueryContext(ctx, `SELECT id, `+menuSEOSelect+` FROM menus WHERE restaurant_id = ?`, restaurantID)
	if err != nil {
		log.Printf("menu_public_seo_list_load_failed restaurant_id=%d error=%v", restaurantID, err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var seo publicMenuSEO
		if err := rows.Scan(menuSEOScanTargets(&seo, &id)...); err != nil {
			log.Printf("menu_public_seo_list_scan_failed restaurant_id=%d error=%v", restaurantID, err)
			continue
		}
		out[id] = seo
	}
	return out
}

// publicMenuSEO is embedded so every public menu representation exposes the
// same flat JSON contract without duplicating the field declarations.
type publicMenuSEO struct {
	SEOSlug               string `json:"seo_slug,omitempty"`
	SEOTitle              string `json:"seo_title,omitempty"`
	SEOTitleEnglish       string `json:"seo_title_english,omitempty"`
	SEODescription        string `json:"seo_description,omitempty"`
	SEODescriptionEnglish string `json:"seo_description_english,omitempty"`
	SEOH1                 string `json:"seo_h1,omitempty"`
	SEOH1English          string `json:"seo_h1_english,omitempty"`
	// Coordination id: menu_seo_content_v1
	SEOIntro        string `json:"seo_intro,omitempty"`
	SEOIntroEnglish string `json:"seo_intro_english,omitempty"`
	SEOFAQ          string `json:"seo_faq,omitempty"`
	SEOFAQEnglish   string `json:"seo_faq_english,omitempty"`
}

func normalizeMenuSEOSlug(value string) (string, error) {
	slug := strings.ToLower(strings.TrimSpace(value))
	if slug == "" {
		return "", nil
	}
	if len(slug) > 190 || !menuSEOSlugPattern.MatchString(slug) {
		return "", errors.New("El slug debe usar minusculas, numeros y guiones, sin espacios ni otros simbolos")
	}
	return slug, nil
}

func normalizeMenuSEOText(value string, max int, label string) (string, error) {
	text := strings.TrimSpace(value)
	if len([]rune(text)) > max {
		return "", errors.New(label + " supera la longitud maxima permitida")
	}
	return text, nil
}
