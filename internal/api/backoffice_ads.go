package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"preactvillacarmen/internal/httpx"
	"preactvillacarmen/internal/lib/specialmenuimage"
)

var boAdHexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// boAdMenuRoute accepts the public menu page (coord id ads_cta_menu_routes_v1):
// `/menu/:id` with an optional slug, so buttons can link any active menu.
var boAdMenuRoute = regexp.MustCompile(`^/menu/[1-9][0-9]*(/[a-z0-9-]+)?$`)

var boAdPublicRoutes = []string{"/", "/contacto", "/eventos", "/menufindesemana", "/menudeldia", "/menusdegrupos", "/postres", "/vinos", "/cafes", "/bebidas", "/reservas", "/reservas.php", "/avisolegal", "/avisolegal.html", "/booking-policies", "/booking_policies.php", "/confirm", "/cancel", "/update-rice", "/protecciondatos", "/protecciondatos.html", "/menusanvalentin", "/regala"}

const (
	boAdMaxTextElements    = 5
	boAdMaxCTAs            = 5
	boAdMaxContentElements = boAdMaxTextElements*3 + 1
	boAdElementMinWidthPct = 10.0
	boAdElementMaxWidthPct = 100.0
	// Image heights: a thumbnail stays readable and a hero never explodes the card.
	boAdElementMinHeightPx = 40.0
	boAdElementMaxHeightPx = 1200.0
	// Element look bounds (ads_element_style_v1).
	boAdMinFontSizePx      = 8.0
	boAdMaxFontSizePx      = 120.0
	boAdMinFontWeight      = 100.0
	boAdMaxFontWeight      = 900.0
	boAdMinLetterSpacingPx = -5.0
	boAdMaxLetterSpacingPx = 20.0
	boAdMinLineHeight      = 0.8
	boAdMaxLineHeight      = 3.0
	boAdMaxRadiusPx        = 200.0
	boAdMinOffsetPx        = -2000.0
	boAdMaxOffsetPx        = 2000.0
	boAdMaxSteps           = 20
	// boAdMaxImageBytes is the single ads image budget: an upload within it is
	// stored untouched, and a larger one is compressed down to it (never below).
	boAdMaxImageBytes = 5 * 1024 * 1024
	boAdT2IModel      = "wavespeed-ai/z-image/turbo"
	boAdEnhanceModel  = "openai/gpt-image-2/edit"
)

// boAdElementSize is the operator-sized box of an element (coord id
// ads_element_size_v1): width as a percentage of the card content width and,
// for images, an explicit height in pixels. Absent fields keep the public
// template's own size, so legacy content renders untouched.
type boAdElementSize struct {
	Width  *float64 `json:"width,omitempty"`
	Height *float64 `json:"height,omitempty"`
}

// boAdElementStyle carries the customisable look of an element (coord id
// ads_element_style_v1): typography for texts, radius for images, opacity and
// the horizontal offset produced by dragging inside the canvas.
type boAdElementStyle struct {
	FontSize      *float64 `json:"font_size,omitempty"`
	FontWeight    *float64 `json:"font_weight,omitempty"`
	LetterSpacing *float64 `json:"letter_spacing,omitempty"`
	LineHeight    *float64 `json:"line_height,omitempty"`
	Color         string   `json:"color,omitempty"`
	Opacity       *float64 `json:"opacity,omitempty"`
	Radius        *float64 `json:"radius,omitempty"`
	OffsetX       *float64 `json:"offset_x,omitempty"`
	OffsetY       *float64 `json:"offset_y,omitempty"`
}

type boAdContentElement struct {
	ID    string            `json:"id"`
	Type  string            `json:"type"`
	Value string            `json:"value"`
	Align string            `json:"align,omitempty"`
	Size  *boAdElementSize  `json:"size,omitempty"`
	Style *boAdElementStyle `json:"style,omitempty"`
}

type boAdCTA struct {
	ID             string `json:"id"`
	Text           string `json:"text"`
	Color          string `json:"color"`
	NavigationMode string `json:"navigation_mode"`
	Route          string `json:"route,omitempty"`
	CustomURL      string `json:"custom_url,omitempty"`
	// Operator-sized pill width as a percentage of the card (ads_button_width_v1).
	Width *float64 `json:"width,omitempty"`
	// Coordination id: ads_button_slot_v1 - position of the button inside the
	// content flow (index before which it renders). Absent keeps the classic
	// actions row under the content.
	Slot *int `json:"slot,omitempty"`
}

// Coordination id: ads_layout_v1 - a "multiple" anuncio renders a wizard: a
// column of cards (one per step) where each card advances to its announcement.
type boAdStep struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	BackgroundMode  string `json:"background_mode"`
	BackgroundColor string `json:"background_color,omitempty"`
	BackgroundImage string `json:"background_image,omitempty"`
	// Coordination id: ads_step_detail_background_v1 - the opened announcement
	// of a step has its own background, independent from the card above.
	DetailBackgroundMode  string               `json:"detail_background_mode,omitempty"`
	DetailBackgroundColor string               `json:"detail_background_color,omitempty"`
	DetailBackgroundImage string               `json:"detail_background_image,omitempty"`
	SeeMore               bool                 `json:"see_more"`
	Buttons               []boAdCTA            `json:"buttons"`
	Content               []boAdContentElement `json:"content"`
}

type boAdLayout struct {
	Mode  string     `json:"mode"`
	Steps []boAdStep `json:"steps,omitempty"`
}

type boAdImageGenerationStatus string

const (
	boAdImageGenerationIdle    boAdImageGenerationStatus = "idle"
	boAdImageGenerationPending boAdImageGenerationStatus = "pending"
	boAdImageGenerationReady   boAdImageGenerationStatus = "ready"
	boAdImageGenerationFailed  boAdImageGenerationStatus = "failed"
)

type boAdScheduleRange struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

type boAd struct {
	ID                       int64                     `json:"id"`
	Name                     string                    `json:"name"`
	Active                   bool                      `json:"active"`
	Content                  []boAdContentElement      `json:"content"`
	CTAs                     []boAdCTA                 `json:"ctas"`
	Layout                   *boAdLayout               `json:"layout,omitempty"`
	ImageGenerationStatus    boAdImageGenerationStatus `json:"image_generation_status,omitempty"`
	ImageGenerationStartedAt string                    `json:"image_generation_started_at,omitempty"`
	StartsAt                 *string                   `json:"starts_at,omitempty"`
	EndsAt                   *string                   `json:"ends_at,omitempty"`
	CreatedAt                string                    `json:"created_at,omitempty"`
	UpdatedAt                string                    `json:"updated_at,omitempty"`
	BlockedRanges            []boAdScheduleRange       `json:"blocked_ranges,omitempty"`
}

type boAdInput struct {
	Name     string               `json:"name"`
	Active   bool                 `json:"active"`
	Content  []boAdContentElement `json:"content"`
	CTAs     []boAdCTA            `json:"ctas"`
	Layout   *boAdLayout          `json:"layout,omitempty"`
	StartsAt *string              `json:"starts_at,omitempty"`
	EndsAt   *string              `json:"ends_at,omitempty"`
}

func normalizeBOAdContent(input []boAdContentElement) ([]boAdContentElement, error) {
	out := make([]boAdContentElement, 0, len(input))
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, item := range input {
		item.ID = strings.TrimSpace(item.ID)
		item.Type = strings.ToLower(strings.TrimSpace(item.Type))
		item.Value = strings.TrimSpace(item.Value)
		item.Align = strings.ToLower(strings.TrimSpace(item.Align))
		if item.Align == "" {
			item.Align = "left"
		}
		if item.Align != "left" && item.Align != "center" && item.Align != "right" {
			return nil, errors.New("invalid content alignment")
		}
		if item.ID == "" {
			return nil, errors.New("content item id is required")
		}
		if seen[item.ID] {
			return nil, errors.New("duplicate content item id")
		}
		seen[item.ID] = true
		if item.Size != nil {
			size, err := normalizeBOAdElementSize(item.Type, item.Size)
			if err != nil {
				return nil, err
			}
			item.Size = size
		}
		if item.Style != nil {
			style, err := normalizeBOAdElementStyle(item.Type, item.Style)
			if err != nil {
				return nil, err
			}
			item.Style = style
		}
		switch item.Type {
		case "title", "subtitle", "text":
			counts[item.Type]++
			if counts[item.Type] > boAdMaxTextElements {
				return nil, fmt.Errorf("maximum %d %s elements", boAdMaxTextElements, item.Type)
			}
		case "image":
			counts[item.Type]++
			if counts[item.Type] > 1 {
				return nil, errors.New("maximum 1 image element")
			}
		default:
			return nil, fmt.Errorf("invalid content type %q", item.Type)
		}
		out = append(out, item)
	}
	return out, nil
}

// normalizeBOAdElementSize clamps the operator box to sane bounds. A nil field
// means "use the template default"; an empty box is dropped entirely so the
// stored JSON stays free of no-op objects.
func normalizeBOAdElementSize(elementType string, size *boAdElementSize) (*boAdElementSize, error) {
	clamp := func(v *float64, min, max float64) *float64 {
		if v == nil {
			return nil
		}
		out := *v
		if out < min {
			out = min
		}
		if out > max {
			out = max
		}
		return &out
	}
	width := clamp(size.Width, boAdElementMinWidthPct, boAdElementMaxWidthPct)
	height := clamp(size.Height, boAdElementMinHeightPx, boAdElementMaxHeightPx)
	if elementType != "image" {
		// Text blocks grow with their content: only the width is operator-sized.
		height = nil
	}
	if width == nil && height == nil {
		return nil, nil
	}
	return &boAdElementSize{Width: width, Height: height}, nil
}

// normalizeBOAdElementStyle clamps the operator look to readable bounds and
// drops whatever the element type cannot use, so stored JSON stays minimal and
// the public template keeps rendering untouched content identically.
func normalizeBOAdElementStyle(elementType string, style *boAdElementStyle) (*boAdElementStyle, error) {
	clamp := func(v *float64, min, max float64) *float64 {
		if v == nil {
			return nil
		}
		out := *v
		if out < min {
			out = min
		}
		if out > max {
			out = max
		}
		return &out
	}
	isText := elementType == "title" || elementType == "subtitle" || elementType == "text"
	isImage := elementType == "image"

	fontSize := clamp(style.FontSize, boAdMinFontSizePx, boAdMaxFontSizePx)
	fontWeight := clamp(style.FontWeight, boAdMinFontWeight, boAdMaxFontWeight)
	letterSpacing := clamp(style.LetterSpacing, boAdMinLetterSpacingPx, boAdMaxLetterSpacingPx)
	lineHeight := clamp(style.LineHeight, boAdMinLineHeight, boAdMaxLineHeight)
	opacity := clamp(style.Opacity, 0, 1)
	radius := clamp(style.Radius, 0, boAdMaxRadiusPx)
	offsetX := clamp(style.OffsetX, boAdMinOffsetPx, boAdMaxOffsetPx)
	offsetY := clamp(style.OffsetY, boAdMinOffsetPx, boAdMaxOffsetPx)

	style.Color = strings.TrimSpace(style.Color)
	if style.Color != "" && !boAdHexColor.MatchString(style.Color) && !strings.HasPrefix(style.Color, "rgb") {
		return nil, errors.New("invalid element color")
	}
	if !isText {
		fontSize, fontWeight, letterSpacing, lineHeight = nil, nil, nil, nil
	}
	if !isImage {
		radius = nil
	}
	// Offsets only make sense where the operator can drag: texts and images.
	if !isText && !isImage {
		offsetX, offsetY = nil, nil
	}
	if style.Color == "" && fontSize == nil && fontWeight == nil && letterSpacing == nil && lineHeight == nil && opacity == nil && radius == nil && offsetX == nil && offsetY == nil {
		return nil, nil
	}
	return &boAdElementStyle{
		FontSize: fontSize, FontWeight: fontWeight, LetterSpacing: letterSpacing, LineHeight: lineHeight,
		Color: style.Color, Opacity: opacity, Radius: radius, OffsetX: offsetX, OffsetY: offsetY,
	}, nil
}

// clampBOAdWidth keeps the pill between a readable sliver and the full card.
func clampBOAdWidth(v *float64) *float64 {
	if v == nil {
		return nil
	}
	out := *v
	if out < boAdElementMinWidthPct {
		out = boAdElementMinWidthPct
	}
	if out > boAdElementMaxWidthPct {
		out = boAdElementMaxWidthPct
	}
	return &out
}

func normalizeBOAdCTAs(input []boAdCTA) ([]boAdCTA, error) {
	if len(input) > boAdMaxCTAs {
		return nil, fmt.Errorf("maximum %d call to action buttons", boAdMaxCTAs)
	}
	out := make([]boAdCTA, 0, len(input))
	seen := map[string]bool{}
	for _, cta := range input {
		cta.ID = strings.TrimSpace(cta.ID)
		cta.Text = strings.TrimSpace(cta.Text)
		cta.Color = strings.TrimSpace(cta.Color)
		cta.NavigationMode = strings.ToLower(strings.TrimSpace(cta.NavigationMode))
		cta.Route = strings.TrimSpace(cta.Route)
		cta.CustomURL = strings.TrimSpace(cta.CustomURL)
		if cta.ID == "" || seen[cta.ID] {
			return nil, errors.New("invalid call to action id")
		}
		seen[cta.ID] = true
		if cta.Color == "" {
			cta.Color = "#436754"
		}
		if cta.NavigationMode != "route" && cta.NavigationMode != "custom" {
			return nil, errors.New("invalid call to action navigation mode")
		}
		if cta.NavigationMode == "route" {
			allowed := boAdMenuRoute.MatchString(cta.Route)
			for _, route := range boAdPublicRoutes {
				if cta.Route == route {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, errors.New("route call to action requires a supported page route")
			}
			cta.CustomURL = ""
		}
		cta.Width = clampBOAdWidth(cta.Width)

		if cta.NavigationMode == "custom" {
			u, err := url.ParseRequestURI(cta.CustomURL)
			if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, errors.New("custom call to action requires a valid http(s) URL")
			}
			cta.Route = ""
		}
		if cta.Slot != nil {
			// Bound by the largest possible content list: 3 text types x max
			// elements + 1 image. Anything past it means "after all content".
			slot := max(0, min(*cta.Slot, boAdMaxContentElements))
			cta.Slot = &slot
		}
		out = append(out, cta)
	}
	return out, nil
}

// normalizeBOAdBackground validates one background triple (card or detail).
func normalizeBOAdBackground(mode, color, image *string) error {
	*mode = strings.ToLower(strings.TrimSpace(*mode))
	*color = strings.TrimSpace(*color)
	*image = strings.TrimSpace(*image)
	switch *mode {
	case "":
		*mode = "transparent"
	case "transparent", "color", "image":
	default:
		return errors.New("invalid ad step background mode")
	}
	if *color != "" && !boAdHexColor.MatchString(*color) {
		return errors.New("invalid ad step background color")
	}
	if *image != "" {
		u, err := url.ParseRequestURI(*image)
		if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("invalid ad step background image")
		}
	}
	return nil
}

// normalizeBOAdLayout validates the wizard payload. Absent or "unico" layouts
// come back as nil so the DB keeps storing a plain announcement.
func normalizeBOAdLayout(input *boAdLayout) (*boAdLayout, error) {
	if input == nil {
		return nil, nil
	}
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	if input.Mode == "" {
		input.Mode = "unico"
	}
	if input.Mode != "unico" && input.Mode != "multiple" {
		return nil, errors.New("invalid ad layout mode")
	}
	if input.Mode != "multiple" {
		return nil, nil
	}
	if len(input.Steps) == 0 {
		return nil, errors.New("multiple layout requires at least one step")
	}
	if len(input.Steps) > boAdMaxSteps {
		return nil, fmt.Errorf("maximum %d steps", boAdMaxSteps)
	}
	steps := make([]boAdStep, 0, len(input.Steps))
	seen := map[string]bool{}
	for _, step := range input.Steps {
		step.ID = strings.TrimSpace(step.ID)
		step.Title = strings.TrimSpace(step.Title)
		step.Description = strings.TrimSpace(step.Description)
		if step.ID == "" || seen[step.ID] {
			return nil, errors.New("invalid ad step id")
		}
		seen[step.ID] = true
		if err := normalizeBOAdBackground(&step.BackgroundMode, &step.BackgroundColor, &step.BackgroundImage); err != nil {
			return nil, err
		}
		// Detail background is optional: empty mode means "same as the card".
		if step.DetailBackgroundMode != "" || step.DetailBackgroundColor != "" || step.DetailBackgroundImage != "" {
			if err := normalizeBOAdBackground(&step.DetailBackgroundMode, &step.DetailBackgroundColor, &step.DetailBackgroundImage); err != nil {
				return nil, err
			}
		}
		buttons, err := normalizeBOAdCTAs(step.Buttons)
		if err != nil {
			return nil, err
		}
		content, err := normalizeBOAdContent(step.Content)
		if err != nil {
			return nil, err
		}
		step.Buttons, step.Content = buttons, content
		steps = append(steps, step)
	}
	input.Steps = steps
	return input, nil
}

func boAdTextToImagePrompt(content []boAdContentElement) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		if item.Type == "image" || strings.TrimSpace(item.Value) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(item.Value))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Create a premium editorial restaurant advertising image inspired by this banner copy, without rendering any text inside the image. Mood should be elegant, photographic, inviting and suitable for a website popover. Banner copy in display order: " + strings.Join(parts, " | ")
}

func boAdTextToImageURL(baseURL string) string { return aiImageEditURLForModel(baseURL, boAdT2IModel) }
func boAdEnhanceURL(baseURL string) string     { return aiImageEditURLForModel(baseURL, boAdEnhanceModel) }

func (s *Server) readBOAd(ctx context.Context, restaurantID int, adID int64) (boAd, error) {
	var ad boAd
	var active int
	var startsAt, endsAt sql.NullTime
	var contentRaw, ctasRaw, layoutRaw []byte
	var statusRaw sql.NullString
	var startedAt, createdAt, updatedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT id, name, active, starts_at, ends_at, content_json, ctas_json, layout_json, image_generation_status, image_generation_started_at, created_at, updated_at FROM restaurant_ads WHERE id = ? AND restaurant_id = ? LIMIT 1`, adID, restaurantID).
		Scan(&ad.ID, &ad.Name, &active, &startsAt, &endsAt, &contentRaw, &ctasRaw, &layoutRaw, &statusRaw, &startedAt, &createdAt, &updatedAt)
	if err != nil {
		return ad, err
	}
	ad.Active = active != 0
	if startsAt.Valid {
		value := startsAt.Time.Format("2006-01-02")
		ad.StartsAt = &value
	}
	if endsAt.Valid {
		value := endsAt.Time.Format("2006-01-02")
		ad.EndsAt = &value
	}
	if statusRaw.Valid && strings.TrimSpace(statusRaw.String) != "" {
		ad.ImageGenerationStatus = boAdImageGenerationStatus(strings.TrimSpace(statusRaw.String))
	}
	if startedAt.Valid {
		ad.ImageGenerationStartedAt = startedAt.Time.UTC().Format(time.RFC3339)
	}
	if err := json.Unmarshal(contentRaw, &ad.Content); err != nil {
		return ad, err
	}
	if err := json.Unmarshal(ctasRaw, &ad.CTAs); err != nil {
		return ad, err
	}
	if len(layoutRaw) > 0 {
		if err := json.Unmarshal(layoutRaw, &ad.Layout); err != nil {
			return ad, err
		}
	}
	if createdAt.Valid {
		ad.CreatedAt = createdAt.Time.UTC().Format(time.RFC3339)
	}
	if updatedAt.Valid {
		ad.UpdatedAt = updatedAt.Time.UTC().Format(time.RFC3339)
	}
	return ad, nil
}

func validateBOAdInput(input boAdInput) (boAdInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = "Nuevo anuncio"
	}
	if len([]rune(input.Name)) > 160 {
		return input, errors.New("name is too long")
	}
	content, err := normalizeBOAdContent(input.Content)
	if err != nil {
		return input, err
	}
	ctas, err := normalizeBOAdCTAs(input.CTAs)
	if err != nil {
		return input, err
	}
	input.Content, input.CTAs = content, ctas
	layout, err := normalizeBOAdLayout(input.Layout)
	if err != nil {
		return input, err
	}
	input.Layout = layout
	if (input.StartsAt == nil) != (input.EndsAt == nil) {
		return input, errors.New("start and end dates must be provided together")
	}
	if input.StartsAt != nil {
		start, err := time.Parse("2006-01-02", strings.TrimSpace(*input.StartsAt))
		if err != nil {
			return input, errors.New("invalid start date")
		}
		end, err := time.Parse("2006-01-02", strings.TrimSpace(*input.EndsAt))
		if err != nil || end.Before(start) {
			return input, errors.New("invalid date range")
		}
		startText, endText := start.Format("2006-01-02"), end.Format("2006-01-02")
		input.StartsAt, input.EndsAt = &startText, &endText
	}
	return input, nil
}

func (s *Server) handleBOAdsList(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id FROM restaurant_ads WHERE restaurant_id = ? ORDER BY updated_at DESC, id DESC`, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading ads")
		return
	}
	defer rows.Close()
	ads := make([]boAd, 0)
	blockedRows, blockedErr := s.db.QueryContext(r.Context(), `SELECT id, name, starts_at, ends_at FROM restaurant_ads WHERE restaurant_id = ? AND active = 1 AND starts_at IS NOT NULL AND ends_at IS NOT NULL ORDER BY starts_at, id`, a.ActiveRestaurantID)
	if blockedErr != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "Error loading ad schedules")
		return
	}
	blockedRanges := make([]boAdScheduleRange, 0)
	for blockedRows.Next() {
		var item boAdScheduleRange
		var start, end time.Time
		if err := blockedRows.Scan(&item.ID, &item.Name, &start, &end); err != nil {
			blockedRows.Close()
			httpx.WriteError(w, 500, "Error loading ad schedules")
			return
		}
		item.StartsAt, item.EndsAt = start.Format("2006-01-02"), end.Format("2006-01-02")
		blockedRanges = append(blockedRanges, item)
	}
	blockedRows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			httpx.WriteError(w, 500, "Error loading ads")
			return
		}
		ad, err := s.readBOAd(r.Context(), a.ActiveRestaurantID, id)
		if err != nil {
			httpx.WriteError(w, 500, "Error loading ads")
			return
		}
		ad.BlockedRanges = blockedRanges
		ads = append(ads, ad)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "ads": ads})
}

// boAdValidationError marks input-validation failures from updateBOAd so the
// WebSocket path can answer with code "validation" without string matching.
type boAdValidationError struct{ err error }

func (e *boAdValidationError) Error() string { return e.err.Error() }
func (e *boAdValidationError) Unwrap() error { return e.err }

// updateBOAd validates and persists an ad (INSERT when adID<=0, UPDATE
// otherwise) and returns the freshly-read row. Shared by the REST handler and
// the ad_save WebSocket message so both paths enforce identical validation.
func (s *Server) updateBOAd(ctx context.Context, restaurantID int, adID int64, input boAdInput) (boAd, error) {
	normalized, err := validateBOAdInput(input)
	if err != nil {
		return boAd{}, &boAdValidationError{err}
	}
	contentRaw, _ := json.Marshal(normalized.Content)
	ctasRaw, _ := json.Marshal(normalized.CTAs)
	var layoutRaw []byte
	if normalized.Layout != nil {
		layoutRaw, _ = json.Marshal(normalized.Layout)
	}
	if adID <= 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO restaurant_ads (restaurant_id, name, active, starts_at, ends_at, content_json, ctas_json, layout_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, restaurantID, normalized.Name, boolToTinyint(normalized.Active), normalized.StartsAt, normalized.EndsAt, contentRaw, ctasRaw, layoutRaw)
		if err != nil {
			return boAd{}, err
		}
		id, _ := res.LastInsertId()
		return s.readBOAd(ctx, restaurantID, id)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE restaurant_ads SET name = ?, active = ?, starts_at = ?, ends_at = ?, content_json = ?, ctas_json = ?, layout_json = ? WHERE id = ? AND restaurant_id = ?`, normalized.Name, boolToTinyint(normalized.Active), normalized.StartsAt, normalized.EndsAt, contentRaw, ctasRaw, layoutRaw, adID, restaurantID); err != nil {
		return boAd{}, err
	}
	return s.readBOAd(ctx, restaurantID, adID)
}

func (s *Server) handleBOAdsCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	input := boAdInput{Name: "Nuevo anuncio", Content: []boAdContentElement{}, CTAs: []boAdCTA{}}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Invalid JSON body"})
			return
		}
	}
	ad, err := s.updateBOAd(r.Context(), a.ActiveRestaurantID, 0, input)
	if err != nil {
		var validationErr *boAdValidationError
		if errors.As(err, &validationErr) {
			httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": validationErr.Error()})
			return
		}
		httpx.WriteError(w, 500, "Error creating ad")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "ad": ad})
}

func (s *Server) handleBOAdsUpdate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	adID, err := parseChiPositiveInt64(r, "adId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid ad id"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var input boAdInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid JSON body"})
		return
	}
	ad, err := s.updateBOAd(r.Context(), a.ActiveRestaurantID, adID, input)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Ad not found"})
		return
	}
	if err != nil {
		var validationErr *boAdValidationError
		if errors.As(err, &validationErr) {
			httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": validationErr.Error()})
			return
		}
		httpx.WriteError(w, 500, "Error saving ad")
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "ad": ad})
}

func (s *Server) handleBOAdsDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	adID, err := parseChiPositiveInt64(r, "adId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid ad id"})
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM restaurant_ads WHERE id = ? AND restaurant_id = ?`, adID, a.ActiveRestaurantID)
	if err != nil {
		httpx.WriteError(w, 500, "Error deleting ad")
		return
	}
	n, _ := res.RowsAffected()
	httpx.WriteJSON(w, 200, map[string]any{"success": n > 0})
}

func (s *Server) ensureBOAd(ctx context.Context, restaurantID int, adID int64) (boAd, error) {
	return s.readBOAd(ctx, restaurantID, adID)
}

// setBOAdImageGenerationStatus persists the AI image-generation lifecycle state
// for an ad. Best-effort: failures are logged but do not mask the original
// error from the caller, since callers wrap this around their primary work.
func (s *Server) setBOAdImageGenerationStatus(ctx context.Context, restaurantID int, adID int64, status boAdImageGenerationStatus, setStartedAt bool) {
	startedClause := ""
	args := []any{string(status), adID, restaurantID}
	if setStartedAt {
		startedClause = ", image_generation_started_at = NOW()"
	}
	res, err := s.db.ExecContext(ctx, "UPDATE restaurant_ads SET image_generation_status = ?"+startedClause+" WHERE id = ? AND restaurant_id = ?", args...)
	if err != nil {
		slog.Default().Warn("failed to update ad image_generation_status", "ad_id", adID, "restaurant_id", restaurantID, "status", status, "err", err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// No row matched — either the ad was deleted mid-flight or never existed.
		slog.Default().Warn("no ad row updated for image_generation_status", "ad_id", adID, "restaurant_id", restaurantID, "status", status)
	}
}

// persistBOAdImageURL atomically writes the new image URL into the ad's
// content_json (creating an image element if the ad has none) and stamps
// updated_at. The image_generation_status transition (pending -> ready) is
// handled separately by setBOAdImageGenerationStatus; the two writes are
// independent so a status-update failure does not silently leave a stale
// URL on disk. The atomic part that matters here is: when the AI finishes
// successfully, the new URL is committed to the DB before the handler
// returns, so a mid-flight page reload shows the new image (or the pending
// skeleton) instead of the old one.
func (s *Server) persistBOAdImageURL(ctx context.Context, restaurantID int, adID int64, url string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var raw []byte
	if err := tx.QueryRowContext(ctx,
		`SELECT content_json FROM restaurant_ads WHERE id = ? AND restaurant_id = ? FOR UPDATE`,
		adID, restaurantID,
	).Scan(&raw); err != nil {
		return err
	}

	var content []boAdContentElement
	if err := json.Unmarshal(raw, &content); err != nil {
		return err
	}

	updated := false
	for i := range content {
		if content[i].Type == "image" {
			content[i].Value = url
			updated = true
			break
		}
	}
	if !updated {
		content = append(content, boAdContentElement{ID: "image-" + uuid.NewString(), Type: "image", Value: url})
	}

	patched, err := json.Marshal(content)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE restaurant_ads SET content_json = ?, updated_at = NOW() WHERE id = ? AND restaurant_id = ?`,
		patched, adID, restaurantID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func readBOAdMultipartImage(r *http.Request, maxInput int) ([]byte, string, string, error) {
	if err := r.ParseMultipartForm(int64(maxInput)); err != nil {
		return nil, "", "", err
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		return nil, "", "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(maxInput)+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(raw) == 0 || len(raw) > maxInput {
		return nil, "", "", errors.New("invalid image size")
	}
	return raw, header.Filename, header.Header.Get("Content-Type"), nil
}

// saveBOAdImage stores an ad image. Within the ads budget the original bytes are
// kept (no re-encode, no size loss); only a payload above the budget is
// compressed down to it, and never further than needed.
func (s *Server) saveBOAdImage(ctx context.Context, restaurantID int, adID int64, raw []byte, filename, contentType, suffix string) (string, error) {
	body := raw
	storeContentType := "image/webp"
	storeExt := ".webp"
	if len(raw) > boAdMaxImageBytes {
		normalized, err := specialmenuimage.NormalizeToWebPWithLimit(ctx, raw, filename, contentType, boAdMaxImageBytes)
		if err != nil {
			return "", err
		}
		body = normalized
	} else {
		detectedType, detectedExt, err := specialmenuimage.ImageContentTypeAndExt(raw, filename, contentType)
		if err != nil {
			return "", err
		}
		storeContentType, storeExt = detectedType, detectedExt
	}
	objectPath := path.Join(strconv.Itoa(restaurantID), "pictures", "ads", strconv.FormatInt(adID, 10), fmt.Sprintf("%s-%d%s", suffix, time.Now().UTC().UnixMilli(), storeExt))
	if err := s.bunnyPut(ctx, restaurantID, objectPath, body, storeContentType); err != nil {
		return "", err
	}
	return s.bunnyPullURL(ctx, restaurantID, objectPath), nil
}

func (s *Server) handleBOAdImageUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	adID, err := parseChiPositiveInt64(r, "adId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid ad id"})
		return
	}
	if _, err := s.ensureBOAd(r.Context(), a.ActiveRestaurantID, adID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Ad not found"})
		} else {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading ad")
		}
		return
	}
	if !s.bunnyConfigured(r.Context(), a.ActiveRestaurantID) {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Image storage not configured"})
		return
	}
	raw, filename, ct, err := readBOAdMultipartImage(r, specialmenuimage.MaxInputBytes)
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid image"})
		return
	}
	url, err := s.saveBOAdImage(r.Context(), a.ActiveRestaurantID, adID, raw, filename, ct, "upload")
	if err != nil {
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if err := s.persistBOAdImageURL(r.Context(), a.ActiveRestaurantID, adID, url); err != nil {
		slog.Default().Warn("failed to persist uploaded ad image url", "ad_id", adID, "restaurant_id", a.ActiveRestaurantID, "err", err.Error())
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "No se pudo guardar la imagen en el anuncio"})
		return
	}
	s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationReady, false)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "url": url})
}

func (s *Server) handleBOAdImageEnhance(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	adID, err := parseChiPositiveInt64(r, "adId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid ad id"})
		return
	}
	if _, err := s.ensureBOAd(r.Context(), a.ActiveRestaurantID, adID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Ad not found"})
		} else {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading ad")
		}
		return
	}
	provider := s.resolveAIImageProvider(r.Context(), a.ActiveRestaurantID)
	if strings.TrimSpace(provider.APIKey) == "" {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "WaveSpeed API key not configured"})
		return
	}
	if !s.bunnyConfigured(r.Context(), a.ActiveRestaurantID) {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Image storage not configured"})
		return
	}
	s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationPending, true)
	raw, filename, ct, err := readBOAdMultipartImage(r, specialmenuimage.MaxInputBytes)
	if err != nil {
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid image"})
		return
	}
	compressed, err := specialmenuimage.NormalizeToWebPWithLimit(r.Context(), raw, filename, ct, boAdMaxImageBytes)
	if err != nil {
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.openAIRequestTimeout())
	defer cancel()
	output, err := s.callComidaImageEdit(ctx, boAdEnhanceURL(provider.BaseURL), provider.APIKey, "Enhance this restaurant advertising image into a premium website campaign photo. Preserve the subject and composition, improve lighting, detail and polish, and do not add any text or logos.", compressed, "image/webp")
	if err != nil {
		code, detail := classifyBOAdAIError(err)
		s.broadcastBOAdImageFailed(r.Context(), a.ActiveRestaurantID, adID, code, detail)
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 502, map[string]any{"success": false, "message": aiFailureMessage("AI image enhancement failed", err)})
		return
	}
	url, err := s.saveBOAdImage(r.Context(), a.ActiveRestaurantID, adID, output, "enhanced", http.DetectContentType(output), "ai")
	if err != nil {
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": err.Error()})
		return
	}
	if err := s.persistBOAdImageURL(r.Context(), a.ActiveRestaurantID, adID, url); err != nil {
		slog.Default().Warn("failed to persist enhanced ad image url", "ad_id", adID, "restaurant_id", a.ActiveRestaurantID, "err", err.Error())
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "No se pudo guardar la imagen mejorada en el anuncio"})
		return
	}
	s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationReady, false)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "url": url})
}

func (s *Server) callBOAdTextToImage(ctx context.Context, endpoint, apiKey, prompt string) ([]byte, error) {
	body, _ := json.Marshal(map[string]any{"prompt": prompt, "enable_sync_mode": false, "enable_base64_output": false, "size": "1024*1024"})
	submit, err := s.waveSpeedDo(ctx, http.MethodPost, endpoint, apiKey, body)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(submit.Data.Status, "failed") {
		return nil, fmt.Errorf("wavespeed submit failed: %s", submit.Data.Error)
	}
	resultURL := strings.TrimSpace(submit.Data.URLs.Get)
	if resultURL == "" && strings.TrimSpace(submit.Data.ID) != "" {
		resultURL = s.waveSpeedResultFetchURL(submit.Data.ID)
	}
	if resultURL == "" {
		return nil, errors.New("wavespeed submit returned no result URL")
	}
	const maxIterations = 90 // ~3 min at 2s/poll, matches typical WaveSpeed timeout
	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		env, err := s.waveSpeedDo(ctx, http.MethodGet, resultURL, apiKey, nil)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(env.Data.Status)) {
		case "failed":
			return nil, fmt.Errorf("wavespeed generation failed: %s", env.Data.Error)
		case "completed":
			if len(env.Data.Outputs) == 0 {
				return nil, errors.New("wavespeed completed with no outputs")
			}
			out := strings.TrimSpace(env.Data.Outputs[0])
			if strings.HasPrefix(out, "http://") || strings.HasPrefix(out, "https://") {
				return s.downloadOpenAIImageURL(ctx, out)
			}
			if strings.HasPrefix(out, "data:") {
				if i := strings.Index(out, ","); i >= 0 {
					out = out[i+1:]
				}
			}
			return base64.StdEncoding.DecodeString(out)
		}
	}
	return nil, errors.New("wavespeed generation timed out")
}

func (s *Server) handleBOAdImageGenerate(w http.ResponseWriter, r *http.Request) {
	a, ok := boAuthFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, 401, "Unauthorized")
		return
	}
	adID, err := parseChiPositiveInt64(r, "adId")
	if err != nil {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Invalid ad id"})
		return
	}
	ad, err := s.ensureBOAd(r.Context(), a.ActiveRestaurantID, adID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, 404, map[string]any{"success": false, "message": "Ad not found"})
		} else {
			httpx.WriteError(w, http.StatusInternalServerError, "Error loading ad")
		}
		return
	}
	provider := s.resolveAIImageProvider(r.Context(), a.ActiveRestaurantID)
	if strings.TrimSpace(provider.APIKey) == "" {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "WaveSpeed API key not configured"})
		return
	}
	if !s.bunnyConfigured(r.Context(), a.ActiveRestaurantID) {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Image storage not configured"})
		return
	}
	prompt := boAdTextToImagePrompt(ad.Content)
	if strings.TrimSpace(prompt) == "" {
		httpx.WriteJSON(w, 400, map[string]any{"success": false, "message": "Write banner text before generating an image"})
		return
	}
	s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationPending, true)
	ctx, cancel := context.WithTimeout(r.Context(), s.openAIRequestTimeout())
	defer cancel()
	output, err := s.callBOAdTextToImage(ctx, boAdTextToImageURL(provider.BaseURL), provider.APIKey, prompt)
	if err != nil {
		code, detail := classifyBOAdAIError(err)
		s.broadcastBOAdImageFailed(r.Context(), a.ActiveRestaurantID, adID, code, detail)
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 502, map[string]any{"success": false, "message": aiFailureMessage("AI image generation failed", err)})
		return
	}
	url, err := s.saveBOAdImage(r.Context(), a.ActiveRestaurantID, adID, output, "generated", http.DetectContentType(output), "generated")
	if err != nil {
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": err.Error()})
		return
	}
	// Commit the generated URL into the ad's content_json server-side so the
	// image survives a reload even if the editor's follow-up persist is lost.
	// Upload/enhance already call persistBOAdImageURL; generate was the only
	// path returning the URL to the client without persisting it, leaving the
	// row as status=ready but image value empty after a reload.
	if err := s.persistBOAdImageURL(r.Context(), a.ActiveRestaurantID, adID, url); err != nil {
		slog.Default().Warn("failed to persist generated ad image url", "ad_id", adID, "restaurant_id", a.ActiveRestaurantID, "err", err.Error())
		s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationFailed, false)
		httpx.WriteJSON(w, 500, map[string]any{"success": false, "message": "No se pudo guardar la imagen generada en el anuncio"})
		return
	}
	s.setBOAdImageGenerationStatus(r.Context(), a.ActiveRestaurantID, adID, boAdImageGenerationReady, false)
	httpx.WriteJSON(w, 200, map[string]any{"success": true, "url": url})
}
