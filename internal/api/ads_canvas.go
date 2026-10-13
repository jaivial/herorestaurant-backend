package api

// Coordination id: ads_canvas_v1
// Free-form ("Figma-like") ad canvas: a fixed-size stage with absolutely
// positioned elements. The same document drives the backoffice editor, the
// public 1-1 render endpoint (iframe), and the external-site JS plugin, so
// what the merchant designs is exactly what visitors see.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	adCanvasVersion       = 1
	adCanvasMinDimension  = 100
	adCanvasMaxDimension  = 4096
	adCanvasMaxElements   = 200
	adCanvasMaxTextLength = 2000
	adCanvasMaxURLLength  = 2048
	adCanvasMaxRawBytes   = 256 * 1024
)

type boAdCanvasBackground struct {
	Type  string `json:"type"`  // "color" | "image"
	Value string `json:"value"` // css color or image url
}

type boAdCanvasElement struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"` // "text" | "image" | "button" | "shape"
	X        float64         `json:"x"`
	Y        float64         `json:"y"`
	W        float64         `json:"w"`
	H        float64         `json:"h"`
	Rotation float64         `json:"rotation,omitempty"`
	Z        int             `json:"z"`
	Props    json.RawMessage `json:"props"`
}

type boAdCanvasTextProps struct {
	Text       string  `json:"text"`
	FontSize   float64 `json:"fontSize"`
	FontFamily string  `json:"fontFamily,omitempty"`
	Color      string  `json:"color"`
	Align      string  `json:"align"` // left|center|right
	Bold       bool    `json:"bold,omitempty"`
	Italic     bool    `json:"italic,omitempty"`
	LineHeight float64 `json:"lineHeight,omitempty"`
}

type boAdCanvasImageProps struct {
	Src    string  `json:"src"`
	Fit    string  `json:"fit"` // cover|contain
	Radius float64 `json:"radius,omitempty"`
	Alt    string  `json:"alt,omitempty"`
}

type boAdCanvasButtonProps struct {
	Text     string  `json:"text"`
	Href     string  `json:"href"`
	Bg       string  `json:"bg"`
	Color    string  `json:"color"`
	Radius   float64 `json:"radius,omitempty"`
	FontSize float64 `json:"fontSize"`
	Bold     bool    `json:"bold,omitempty"`
	NewTab   bool    `json:"newTab,omitempty"`
}

type boAdCanvasShapeProps struct {
	Shape       string  `json:"shape"` // rect|ellipse
	Fill        string  `json:"fill"`
	Stroke      string  `json:"stroke,omitempty"`
	StrokeWidth float64 `json:"strokeWidth,omitempty"`
	Radius      float64 `json:"radius,omitempty"`
	Opacity     float64 `json:"opacity,omitempty"`
}

type boAdCanvas struct {
	Version    int                  `json:"version"`
	Width      int                  `json:"width"`
	Height     int                  `json:"height"`
	Background boAdCanvasBackground `json:"background"`
	Elements   []boAdCanvasElement  `json:"elements"`
	// Coordination id: ads_canvas_breakpoints_v1 - responsive variants keyed by
	// "tablet" | "mobile". A variant with its own width/height/background/
	// elements renders instead of the base doc at that breakpoint; a missing
	// field inherits the base value, so a variant can start as a pure copy and
	// diverge per field.
	Variants map[string]*boAdCanvasVariant `json:"variants,omitempty"`
	// Container styles the public popover applies around the canvas iframe
	// (maxWidth px, overlay css color, modal radius px). Zero value = default.
	Container *boAdCanvasContainer `json:"container,omitempty"`
}

// boAdCanvasVariant is a per-breakpoint override of the base canvas document.
type boAdCanvasVariant struct {
	Width      int                   `json:"width"`
	Height     int                   `json:"height"`
	Background *boAdCanvasBackground `json:"background,omitempty"` // nil = inherit base
	Elements   []boAdCanvasElement   `json:"elements,omitempty"`   // nil = inherit base
}

// boAdCanvasContainer styles the ad popover chrome shared by every breakpoint.
type boAdCanvasContainer struct {
	MaxWidth int     `json:"maxWidth,omitempty"` // popover max width px (0 = default 560)
	Overlay  string  `json:"overlay,omitempty"`  // overlay css color ("" = default)
	Radius   float64 `json:"radius,omitempty"`   // modal radius px (0 = default 24)
}

// adCanvasView is the resolved document for one breakpoint: base fields with
// the variant overrides applied. It is what every renderer paints.
type adCanvasView struct {
	Width      int
	Height     int
	Background boAdCanvasBackground
	Elements   []boAdCanvasElement
}

// Responsive breakpoints (coord id ads_canvas_breakpoints_v1), mirrored by the
// preact popover (publicAds.ts) and the backoffice studio (AdVisualStudio.tsx).
const (
	adCanvasMobileBreakpoint = 768
	adCanvasTabletBreakpoint = 1024
)

// adCanvasVariantForWidth maps a viewport/container width to a variant name.
func adCanvasVariantForWidth(vw int) string {
	if vw > 0 && vw < adCanvasMobileBreakpoint {
		return "mobile"
	}
	if vw > 0 && vw < adCanvasTabletBreakpoint {
		return "tablet"
	}
	return "desktop"
}

// adCanvasViewFor resolves the document for a variant name ("desktop" or a
// missing variant falls back to the base doc, so old canvases keep rendering).
func adCanvasViewFor(c *boAdCanvas, variant string) adCanvasView {
	view := adCanvasView{Width: c.Width, Height: c.Height, Background: c.Background, Elements: c.Elements}
	if variant == "" || variant == "desktop" {
		return view
	}
	v := c.Variants[variant]
	if v == nil {
		return view
	}
	if v.Width > 0 {
		view.Width = v.Width
	}
	if v.Height > 0 {
		view.Height = v.Height
	}
	if v.Background != nil {
		view.Background = *v.Background
	}
	if v.Elements != nil {
		view.Elements = v.Elements
	}
	return view
}

// adCanvasSafeURL allows http(s), mailto, tel and root-relative links only.
func adCanvasSafeURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > adCanvasMaxURLLength {
		return false
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto", "tel":
		return true
	}
	return false
}

func adCanvasSafeColor(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 64 {
		return false
	}
	// Accept #rgb/#rgba/#rrggbb/#rrggbbaa and simple rgb()/rgba()/hsl()/hsla() and named colors.
	if strings.HasPrefix(raw, "#") {
		for _, c := range raw[1:] {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
		return len(raw) >= 4 && len(raw) <= 9
	}
	lower := strings.ToLower(raw)
	for _, prefix := range []string{"rgb(", "rgba(", "hsl(", "hsla("} {
		if strings.HasPrefix(lower, prefix) && strings.HasSuffix(lower, ")") {
			return true
		}
	}
	for _, c := range lower {
		if !(c >= 'a' && c <= 'z') {
			return false
		}
	}
	return len(lower) >= 3
}

func validateBOAdCanvas(canvas *boAdCanvas) error {
	if canvas == nil {
		return nil
	}
	if canvas.Version != adCanvasVersion {
		return fmt.Errorf("canvas.version must be %d", adCanvasVersion)
	}
	if canvas.Width < adCanvasMinDimension || canvas.Width > adCanvasMaxDimension || canvas.Height < adCanvasMinDimension || canvas.Height > adCanvasMaxDimension {
		return fmt.Errorf("canvas dimensions must be between %d and %d", adCanvasMinDimension, adCanvasMaxDimension)
	}
	if err := validateAdCanvasBackground(&canvas.Background, "canvas.background"); err != nil {
		return err
	}
	if len(canvas.Elements) > adCanvasMaxElements {
		return fmt.Errorf("canvas supports at most %d elements", adCanvasMaxElements)
	}
	if err := validateAdCanvasElements(canvas.Elements); err != nil {
		return err
	}
	for name, variant := range canvas.Variants {
		if name != "tablet" && name != "mobile" {
			return fmt.Errorf("canvas.variants: unknown breakpoint %q (tablet|mobile)", name)
		}
		if variant == nil {
			return fmt.Errorf("canvas.variants.%s: empty variant", name)
		}
		if variant.Width != 0 || variant.Height != 0 {
			if variant.Width < adCanvasMinDimension || variant.Width > adCanvasMaxDimension || variant.Height < adCanvasMinDimension || variant.Height > adCanvasMaxDimension {
				return fmt.Errorf("canvas.variants.%s dimensions must be between %d and %d", name, adCanvasMinDimension, adCanvasMaxDimension)
			}
		}
		if variant.Background != nil {
			if err := validateAdCanvasBackground(variant.Background, "canvas.variants."+name+".background"); err != nil {
				return err
			}
		}
		if len(variant.Elements) > adCanvasMaxElements {
			return fmt.Errorf("canvas.variants.%s supports at most %d elements", name, adCanvasMaxElements)
		}
		if err := validateAdCanvasElements(variant.Elements); err != nil {
			return fmt.Errorf("canvas.variants.%s: %w", name, err)
		}
	}
	if canvas.Container != nil {
		c := canvas.Container
		if c.MaxWidth != 0 && (c.MaxWidth < 240 || c.MaxWidth > 2000) {
			return errors.New("canvas.container.maxWidth must be between 240 and 2000")
		}
		if c.Overlay != "" && !adCanvasSafeColor(c.Overlay) {
			return errors.New("canvas.container.overlay is not a safe color")
		}
		if c.Radius < 0 || c.Radius > 96 {
			return errors.New("canvas.container.radius must be between 0 and 96")
		}
	}
	return nil
}

func validateAdCanvasBackground(bg *boAdCanvasBackground, where string) error {
	if bg.Type == "" {
		bg.Type = "color"
	}
	if bg.Type != "color" && bg.Type != "image" {
		return fmt.Errorf("%s.type must be color or image", where)
	}
	if bg.Type == "color" && bg.Value != "" && !adCanvasSafeColor(bg.Value) {
		return fmt.Errorf("%s.value is not a safe color", where)
	}
	if bg.Type == "image" {
		if !adCanvasSafeURL(bg.Value) || strings.HasPrefix(strings.TrimSpace(bg.Value), "/") {
			return fmt.Errorf("%s.value is not a safe image url", where)
		}
	}
	return nil
}

func validateAdCanvasElements(elements []boAdCanvasElement) error {
	seen := map[string]bool{}
	for i := range elements {
		el := &elements[i]
		el.ID = strings.TrimSpace(el.ID)
		if el.ID == "" || len(el.ID) > 64 {
			return fmt.Errorf("element %d: id is required (max 64 chars)", i)
		}
		if seen[el.ID] {
			return fmt.Errorf("element %s: duplicated id", el.ID)
		}
		seen[el.ID] = true
		if el.W <= 0 || el.H <= 0 || el.W > adCanvasMaxDimension*2 || el.H > adCanvasMaxDimension*2 {
			return fmt.Errorf("element %s: invalid size", el.ID)
		}
		if len(el.Props) > 32*1024 {
			return fmt.Errorf("element %s: props too large", el.ID)
		}
		switch el.Type {
		case "text":
			var p boAdCanvasTextProps
			if err := json.Unmarshal(el.Props, &p); err != nil {
				return fmt.Errorf("element %s: invalid text props", el.ID)
			}
			if len(p.Text) > adCanvasMaxTextLength {
				return fmt.Errorf("element %s: text too long", el.ID)
			}
			if p.FontSize < 4 || p.FontSize > 400 {
				return fmt.Errorf("element %s: fontSize out of range", el.ID)
			}
			if p.Color != "" && !adCanvasSafeColor(p.Color) {
				return fmt.Errorf("element %s: invalid color", el.ID)
			}
			if p.Align != "left" && p.Align != "center" && p.Align != "right" {
				return fmt.Errorf("element %s: align must be left, center or right", el.ID)
			}
			if len(p.FontFamily) > 120 {
				return fmt.Errorf("element %s: fontFamily too long", el.ID)
			}
		case "image":
			var p boAdCanvasImageProps
			if err := json.Unmarshal(el.Props, &p); err != nil {
				return fmt.Errorf("element %s: invalid image props", el.ID)
			}
			src := strings.TrimSpace(p.Src)
			if !adCanvasSafeURL(src) || strings.HasPrefix(src, "/") {
				return fmt.Errorf("element %s: image src must be an absolute http(s) url", el.ID)
			}
			if p.Fit != "cover" && p.Fit != "contain" {
				return fmt.Errorf("element %s: fit must be cover or contain", el.ID)
			}
			if len(p.Alt) > 200 {
				return fmt.Errorf("element %s: alt too long", el.ID)
			}
		case "button":
			var p boAdCanvasButtonProps
			if err := json.Unmarshal(el.Props, &p); err != nil {
				return fmt.Errorf("element %s: invalid button props", el.ID)
			}
			if strings.TrimSpace(p.Text) == "" || len(p.Text) > 120 {
				return fmt.Errorf("element %s: button text required (max 120)", el.ID)
			}
			if !adCanvasSafeURL(p.Href) {
				return fmt.Errorf("element %s: button href is not a safe url", el.ID)
			}
			if p.Bg != "" && !adCanvasSafeColor(p.Bg) {
				return fmt.Errorf("element %s: invalid bg color", el.ID)
			}
			if p.Color != "" && !adCanvasSafeColor(p.Color) {
				return fmt.Errorf("element %s: invalid text color", el.ID)
			}
			if p.FontSize < 4 || p.FontSize > 200 {
				return fmt.Errorf("element %s: fontSize out of range", el.ID)
			}
		case "shape":
			var p boAdCanvasShapeProps
			if err := json.Unmarshal(el.Props, &p); err != nil {
				return fmt.Errorf("element %s: invalid shape props", el.ID)
			}
			if p.Shape != "rect" && p.Shape != "ellipse" {
				return fmt.Errorf("element %s: shape must be rect or ellipse", el.ID)
			}
			if p.Fill != "" && !adCanvasSafeColor(p.Fill) {
				return fmt.Errorf("element %s: invalid fill color", el.ID)
			}
			if p.Stroke != "" && !adCanvasSafeColor(p.Stroke) {
				return fmt.Errorf("element %s: invalid stroke color", el.ID)
			}
		default:
			return fmt.Errorf("element %s: unknown type %q", el.ID, el.Type)
		}
	}
	return nil
}
