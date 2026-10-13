package api

// Coordination id: ads_canvas_v1
// Public 1-1 render of an ad canvas as a self-contained HTML page. This is
// the single render source for the backoffice editor preview (iframe), the
// Preact home (iframe), and the external-site JS plugin (iframe), so the
// design renders identically everywhere.

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"preactvillacarmen/internal/httpx"
)

func adCanvasCSSColor(raw, fallback string) string {
	if adCanvasSafeColor(raw) {
		return strings.TrimSpace(raw)
	}
	return fallback
}

func adCanvasNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// handlePublicAdRender serves GET /public/ads/{adId}/render.
// Query params: restaurant_id (optional when host resolves tenant),
// src (optional free-form value for outbound-link attribution),
// variant ("desktop" | "tablet" | "mobile", optional) and vw (viewport width
// hint, optional) — coord id ads_canvas_breakpoints_v1: with vw the breakpoint
// is derived like the public popover does, so the iframe renders the same
// responsive document a real visitor at that width would see.
func (s *Server) handlePublicAdRender(w http.ResponseWriter, r *http.Request) {
	adID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("adId")), 10, 64)
	if err != nil || adID <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "Invalid ad id")
		return
	}
	restaurantID := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("restaurant_id")); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id <= 0 {
			httpx.WriteError(w, http.StatusBadRequest, "Invalid restaurant_id")
			return
		}
		restaurantID = id
	} else if id, ok := s.resolveRestaurantID(r); ok {
		restaurantID = id
	}
	if restaurantID <= 0 {
		httpx.WriteError(w, http.StatusNotFound, "Unknown restaurant")
		return
	}
	ad, err := s.readBOAd(r.Context(), restaurantID, adID)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, "Ad not found")
		return
	}
	canvas := ad.Canvas
	if stepID := strings.TrimSpace(r.URL.Query().Get("step")); stepID != "" {
		canvas = nil
		if ad.Layout != nil {
			for _, step := range ad.Layout.Steps {
				if step.ID == stepID {
					canvas = step.Canvas
					break
				}
			}
		}
	}
	if canvas == nil {
		httpx.WriteError(w, http.StatusNotFound, "Ad has no canvas")
		return
	}

	src := strings.TrimSpace(r.URL.Query().Get("src"))
	if len(src) > 64 {
		src = src[:64]
	}
	variant := strings.TrimSpace(r.URL.Query().Get("variant"))
	if variant == "" {
		if rawVW := strings.TrimSpace(r.URL.Query().Get("vw")); rawVW != "" {
			if vw, err := strconv.Atoi(rawVW); err == nil {
				variant = adCanvasVariantForWidth(vw)
			}
		}
	}
	if variant != "mobile" && variant != "tablet" && variant != "desktop" {
		variant = "desktop"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, renderAdCanvasHTML(adCanvasViewFor(canvas, variant), src))
}

// renderAdCanvasHTML builds the self-contained HTML page for a resolved
// breakpoint view. Every dynamic value is escaped or validated before
// reaching the output.
func renderAdCanvasHTML(view adCanvasView, src string) string {
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	b.WriteString("<title>Anuncio</title><style>")
	b.WriteString("html,body{margin:0;padding:0;height:100%;overflow:hidden;background:transparent;}")
	b.WriteString("#vc-ad-viewport{position:absolute;inset:0;display:flex;align-items:center;justify-content:center;}")
	b.WriteString("#vc-ad-stage{position:relative;flex:none;transform-origin:center center;}")
	b.WriteString(".vc-ad-el{position:absolute;box-sizing:border-box;}")
	b.WriteString(".vc-ad-text{overflow:hidden;white-space:pre-wrap;word-break:break-word;display:flex;flex-direction:column;justify-content:center;}")
	b.WriteString(".vc-ad-img{width:100%;height:100%;display:block;}")
	b.WriteString(".vc-ad-btn{display:flex;align-items:center;justify-content:center;width:100%;height:100%;text-decoration:none;cursor:pointer;}")
	b.WriteString("</style></head><body>")
	b.WriteString("<div id=\"vc-ad-viewport\"><div id=\"vc-ad-stage\" style=\"width:")
	b.WriteString(strconv.Itoa(view.Width))
	b.WriteString("px;height:")
	b.WriteString(strconv.Itoa(view.Height))
	b.WriteString("px;")

	bgType := view.Background.Type
	bgValue := view.Background.Value
	if bgType == "image" && adCanvasSafeURL(bgValue) {
		b.WriteString("background-image:url(")
		b.WriteString(html.EscapeString(bgValue))
		b.WriteString(");background-size:cover;background-position:center;")
	} else {
		b.WriteString("background:")
		b.WriteString(adCanvasCSSColor(bgValue, "#ffffff"))
		b.WriteString(";")
	}
	b.WriteString("\">")

	elements := append([]boAdCanvasElement(nil), view.Elements...)
	for i := 0; i < len(elements); i++ {
		for j := i + 1; j < len(elements); j++ {
			if elements[j].Z < elements[i].Z {
				elements[i], elements[j] = elements[j], elements[i]
			}
		}
	}

	for _, el := range elements {
		style := "left:" + adCanvasNumber(el.X) + "px;top:" + adCanvasNumber(el.Y) + "px;width:" + adCanvasNumber(el.W) + "px;height:" + adCanvasNumber(el.H) + "px;"
		if el.Rotation != 0 {
			style += "transform:rotate(" + adCanvasNumber(el.Rotation) + "deg);"
		}
		switch el.Type {
		case "text":
			var p boAdCanvasTextProps
			if err := json.Unmarshal(el.Props, &p); err != nil {
				continue
			}
			style += "font-size:" + adCanvasNumber(p.FontSize) + "px;color:" + adCanvasCSSColor(p.Color, "#111111") + ";text-align:" + p.Align + ";"
			if p.Bold {
				style += "font-weight:700;"
			}
			if p.Italic {
				style += "font-style:italic;"
			}
			if p.LineHeight > 0 {
				style += "line-height:" + adCanvasNumber(p.LineHeight) + ";"
			}
			if strings.TrimSpace(p.FontFamily) != "" {
				style += "font-family:" + html.EscapeString(p.FontFamily) + ";"
			}
			b.WriteString("<div class=\"vc-ad-el vc-ad-text\" style=\"")
			b.WriteString(style)
			b.WriteString("\">")
			b.WriteString(html.EscapeString(p.Text))
			b.WriteString("</div>")
		case "image":
			var p boAdCanvasImageProps
			if err := json.Unmarshal(el.Props, &p); err != nil {
				continue
			}
			srcURL := strings.TrimSpace(p.Src)
			if !adCanvasSafeURL(srcURL) {
				continue
			}
			fit := p.Fit
			if fit != "contain" {
				fit = "cover"
			}
			b.WriteString("<div class=\"vc-ad-el\" style=\"")
			b.WriteString(style)
			b.WriteString("\"><img class=\"vc-ad-img\" src=\"")
			b.WriteString(html.EscapeString(srcURL))
			b.WriteString("\" alt=\"")
			b.WriteString(html.EscapeString(p.Alt))
			b.WriteString("\" style=\"object-fit:")
			b.WriteString(fit)
			b.WriteString(";border-radius:")
			b.WriteString(adCanvasNumber(p.Radius))
			b.WriteString("px;\"></div>")
		case "button":
			var p boAdCanvasButtonProps
			if err := json.Unmarshal(el.Props, &p); err != nil {
				continue
			}
			href := strings.TrimSpace(p.Href)
			if !adCanvasSafeURL(href) {
				continue
			}
			if src != "" && (strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://")) {
				sep := "?"
				if strings.Contains(href, "?") {
					sep = "&"
				}
				href = href + sep + "utm_source=" + src
			}
			style += "font-size:" + adCanvasNumber(p.FontSize) + "px;"
			b.WriteString("<div class=\"vc-ad-el\" style=\"")
			b.WriteString(style)
			b.WriteString("\"><a class=\"vc-ad-btn\" href=\"")
			b.WriteString(html.EscapeString(href))
			b.WriteString("\"")
			if p.NewTab {
				b.WriteString(" target=\"_blank\" rel=\"noopener noreferrer\"")
			}
			b.WriteString(" style=\"background:")
			b.WriteString(adCanvasCSSColor(p.Bg, "#111111"))
			b.WriteString(";color:")
			b.WriteString(adCanvasCSSColor(p.Color, "#ffffff"))
			b.WriteString(";border-radius:")
			b.WriteString(adCanvasNumber(p.Radius))
			b.WriteString("px;")
			if p.Bold {
				b.WriteString("font-weight:700;")
			}
			b.WriteString("\">")
			b.WriteString(html.EscapeString(p.Text))
			b.WriteString("</a></div>")
		case "shape":
			var p boAdCanvasShapeProps
			if err := json.Unmarshal(el.Props, &p); err != nil {
				continue
			}
			style += "background:" + adCanvasCSSColor(p.Fill, "transparent") + ";"
			if p.Shape == "ellipse" {
				style += "border-radius:50%;"
			} else if p.Radius > 0 {
				style += "border-radius:" + adCanvasNumber(p.Radius) + "px;"
			}
			if p.Stroke != "" && p.StrokeWidth > 0 {
				style += "border:" + adCanvasNumber(p.StrokeWidth) + "px solid " + adCanvasCSSColor(p.Stroke, "#000000") + ";"
			}
			if p.Opacity > 0 && p.Opacity < 1 {
				style += "opacity:" + adCanvasNumber(p.Opacity) + ";"
			}
			b.WriteString("<div class=\"vc-ad-el\" style=\"")
			b.WriteString(style)
			b.WriteString("\"></div>")
		}
	}
	b.WriteString("</div></div>")
	b.WriteString("<script>(function(){var stage=document.getElementById(\"vc-ad-stage\");")
	b.WriteString("var w=")
	b.WriteString(strconv.Itoa(view.Width))
	b.WriteString(",h=")
	b.WriteString(strconv.Itoa(view.Height))
	b.WriteString(";function fit(){var vw=window.innerWidth,vh=window.innerHeight;var sc=Math.min(vw/w,vh/h);stage.style.transform=\"scale(\"+sc+\")\";}window.addEventListener(\"resize\",fit);fit();})();</script>")
	b.WriteString("</body></html>")
	return b.String()
}
