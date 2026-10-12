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
	if canvas.Background.Type == "" {
		canvas.Background.Type = "color"
	}
	if canvas.Background.Type != "color" && canvas.Background.Type != "image" {
		return errors.New("canvas.background.type must be color or image")
	}
	if canvas.Background.Type == "color" && canvas.Background.Value != "" && !adCanvasSafeColor(canvas.Background.Value) {
		return errors.New("canvas.background.value is not a safe color")
	}
	if canvas.Background.Type == "image" {
		if !adCanvasSafeURL(canvas.Background.Value) || strings.HasPrefix(strings.TrimSpace(canvas.Background.Value), "/") {
			return errors.New("canvas.background.value is not a safe image url")
		}
	}
	if len(canvas.Elements) > adCanvasMaxElements {
		return fmt.Errorf("canvas supports at most %d elements", adCanvasMaxElements)
	}
	seen := map[string]bool{}
	for i := range canvas.Elements {
		el := &canvas.Elements[i]
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
