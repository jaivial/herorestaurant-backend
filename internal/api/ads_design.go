package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Coordination id: ads_html_editor_v1
//
// HTML ads: the backoffice ad editor is the same Tailwind HTML builder as the
// campaign editor (ui/htmlBuilder), so one design adapts to every screen size
// on its own instead of three absolutely positioned canvases.
//
// An ad is "single" (one page: the ad) or "multiple" (a two-step wizard: the
// main page shows every offer as a card, and each card opens its own detail
// page with a way back). A card is any element of the main page carrying
// data-ad-step="<step id>". Every page is stored twice over: the design HTML
// the builder edits and the CSS its Tailwind classes compile to, so the public
// render is a plain document with no compile step.
//
// The HTML is never trusted from the client: on save every page goes through
// the backoffice design engine (/bo-tools/campaign-design, action ad_compile),
// which sanitizes it with the builder's own rules and compiles the CSS. The
// public render adds a strict CSP on top.

const (
	adDesignVersion           = 1
	adDesignMaxSteps          = 20
	adDesignMaxPageHTML       = 200 * 1024
	adDesignMaxPageCSS        = 400 * 1024
	adDesignMaxTitle          = 120
	adDesignDefaultWidth      = 560
	adDesignDefaultWidthMulti = 760
	adDesignMinWidth          = 280
	adDesignMaxWidth          = 1200
	adDesignMaxRadius         = 48
	// adDesignMaxPayload bounds the save request of an HTML ad (pages + CSS).
	adDesignMaxPayload = 4 << 20
)

type boAdDesignPage struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
	HTML  string `json:"html"`
	CSS   string `json:"css,omitempty"`
}

// boAdDesignContainer is the popover chrome: its maximum width, the overlay
// that dims the website, the corner radius and the sheet background.
type boAdDesignContainer struct {
	MaxWidth   int    `json:"max_width,omitempty"`
	Overlay    string `json:"overlay,omitempty"`
	Radius     *int   `json:"radius,omitempty"`
	Background string `json:"background,omitempty"`
}

type boAdDesign struct {
	Version   int                 `json:"version"`
	Kind      string              `json:"kind"`
	Container boAdDesignContainer `json:"container"`
	Main      boAdDesignPage      `json:"main"`
	Steps     []boAdDesignPage    `json:"steps,omitempty"`
}

var adDesignStepIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// normalizeBOAdDesign checks the shape of a design and fills the defaults.
// The HTML itself is sanitized later by the engine (compileBOAdDesign).
func normalizeBOAdDesign(in *boAdDesign) (*boAdDesign, error) {
	if in == nil {
		return nil, nil
	}
	d := *in
	d.Version = adDesignVersion
	d.Kind = strings.TrimSpace(d.Kind)
	switch d.Kind {
	case "single":
		d.Steps = nil
	case "multiple":
	default:
		return nil, errors.New("el tipo de anuncio debe ser individual o múltiple")
	}
	if len(d.Steps) > adDesignMaxSteps {
		return nil, fmt.Errorf("un anuncio múltiple admite como mucho %d pasos", adDesignMaxSteps)
	}
	c := d.Container
	if c.MaxWidth == 0 {
		c.MaxWidth = adDesignDefaultWidth
		if d.Kind == "multiple" {
			c.MaxWidth = adDesignDefaultWidthMulti
		}
	}
	if c.MaxWidth < adDesignMinWidth || c.MaxWidth > adDesignMaxWidth {
		return nil, fmt.Errorf("el ancho máximo debe estar entre %d y %d px", adDesignMinWidth, adDesignMaxWidth)
	}
	c.Overlay, c.Background = strings.TrimSpace(c.Overlay), strings.TrimSpace(c.Background)
	if c.Overlay != "" && !adCanvasSafeColor(c.Overlay) {
		return nil, errors.New("color de fondo oscurecido no válido")
	}
	if c.Background != "" && !adCanvasSafeColor(c.Background) {
		return nil, errors.New("color de fondo del anuncio no válido")
	}
	if c.Radius != nil && (*c.Radius < 0 || *c.Radius > adDesignMaxRadius) {
		return nil, fmt.Errorf("las esquinas deben estar entre 0 y %d px", adDesignMaxRadius)
	}
	d.Container = c
	if strings.TrimSpace(d.Main.HTML) == "" {
		return nil, errors.New("el anuncio no tiene diseño")
	}
	d.Main.ID, d.Main.Title = "", ""
	seen := map[string]bool{}
	steps := make([]boAdDesignPage, 0, len(d.Steps))
	for _, st := range d.Steps {
		st.ID = strings.TrimSpace(st.ID)
		if !adDesignStepIDRe.MatchString(st.ID) || seen[st.ID] {
			return nil, errors.New("identificador de paso no válido o repetido")
		}
		seen[st.ID] = true
		st.Title = strings.TrimSpace(st.Title)
		if utf8.RuneCountInString(st.Title) > adDesignMaxTitle {
			return nil, errors.New("el título de un paso es demasiado largo")
		}
		if strings.TrimSpace(st.HTML) == "" {
			return nil, fmt.Errorf("el paso «%s» no tiene diseño", firstNonEmpty(st.Title, st.ID))
		}
		steps = append(steps, st)
	}
	d.Steps = steps
	if d.Kind == "multiple" && len(d.Steps) == 0 {
		return nil, errors.New("un anuncio múltiple necesita al menos una tarjeta con su página")
	}
	for _, p := range append([]boAdDesignPage{d.Main}, d.Steps...) {
		if len(p.HTML) > adDesignMaxPageHTML {
			return nil, errors.New("el diseño de una página es demasiado grande")
		}
	}
	return &d, nil
}

// adDesignCardRe finds the step a card opens (data-ad-step="<id>").
var adDesignCardRe = regexp.MustCompile(`data-ad-step\s*=\s*"([^"]*)"`)

// compileBOAdDesign sends every page to the backoffice design engine, which
// sanitizes the HTML with the builder's rules and compiles its Tailwind CSS
// (base reset included), and stores both in the design.
func (s *Server) compileBOAdDesign(ctx context.Context, d *boAdDesign) error {
	pages := make([]campaignDesignPage, 0, 1+len(d.Steps))
	pages = append(pages, campaignDesignPage{HTML: d.Main.HTML})
	for _, st := range d.Steps {
		pages = append(pages, campaignDesignPage{HTML: st.HTML})
	}
	res, err := s.campaignDesignCall(ctx, campaignDesignRequest{Action: "ad_compile", Pages: pages})
	if err != nil {
		return err
	}
	if len(res.Pages) != len(pages) {
		return errors.New("el motor de diseño devolvió un número de páginas inesperado")
	}
	for i, p := range res.Pages {
		if strings.TrimSpace(p.HTML) == "" {
			return campaignDesignRefusal{msg: "una página del anuncio quedó vacía al limpiar su HTML"}
		}
		if len(p.HTML) > adDesignMaxPageHTML || len(p.CSS) > adDesignMaxPageCSS {
			return campaignDesignRefusal{msg: "el diseño de una página es demasiado grande"}
		}
		if i == 0 {
			d.Main.HTML, d.Main.CSS = p.HTML, p.CSS
			continue
		}
		d.Steps[i-1].HTML, d.Steps[i-1].CSS = p.HTML, p.CSS
	}
	if d.Kind == "multiple" {
		// Every card opens a page the ad has (checked on the sanitized HTML,
		// whose attributes are always double-quoted): a card without its page
		// would open nothing on the website.
		ids := map[string]bool{}
		for _, st := range d.Steps {
			ids[st.ID] = true
		}
		for _, m := range adDesignCardRe.FindAllStringSubmatch(d.Main.HTML, -1) {
			if id := html.UnescapeString(m[1]); !ids[id] {
				return campaignDesignRefusal{msg: fmt.Sprintf("la tarjeta «%s» no tiene página de detalle", id)}
			}
		}
	}
	return nil
}

// adDesignPage returns the page a render request asks for: the main page, or
// the detail page of one step of a multiple ad.
func adDesignPage(d *boAdDesign, step string) (boAdDesignPage, bool) {
	if d == nil {
		return boAdDesignPage{}, false
	}
	step = strings.TrimSpace(step)
	if step == "" {
		return d.Main, true
	}
	for _, st := range d.Steps {
		if st.ID == step {
			return st, true
		}
	}
	return boAdDesignPage{}, false
}

// publicAdDesign is what the public ad list carries: kind, chrome and the
// steps (id + title), never the pages (each one is rendered on its own URL).
func publicAdDesign(d *boAdDesign) *boAdDesign {
	if d == nil {
		return nil
	}
	out := &boAdDesign{Version: d.Version, Kind: d.Kind, Container: d.Container}
	for _, st := range d.Steps {
		out.Steps = append(out.Steps, boAdDesignPage{ID: st.ID, Title: st.Title})
	}
	return out
}

func adRenderNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}

// adDesignCSP locks the rendered page down: no network fetch but images and
// fonts over https, only the inline styles of the page and its one nonce'd
// script (height and step messages).
func adDesignCSP(nonce string) string {
	return "default-src 'none'; img-src https: data:; style-src 'unsafe-inline'; font-src https: data:; script-src 'nonce-" + nonce + "'; base-uri 'none'; form-action 'none'"
}

// adDesignBridgeJS tells the embedding page (the website popover or the
// external plugin) the height of the page and which card was clicked, and tags
// outbound links with the source of the embed (utm_source, as the canvas
// render did). {type:"vc-ad-measure"} from the parent asks for the height
// again (the first report can land before the parent listens).
const adDesignBridgeJS = `(function(){var ad=%s,step=%s,src=%s;
function post(m){m.type="vc-ad";m.ad=ad;m.step=step;try{parent.postMessage(m,"*")}catch(e){}}
var last=0;function height(){var b=document.body,h=Math.ceil(Math.max(b.scrollHeight,b.offsetHeight,document.documentElement.scrollHeight));if(h!==last){last=h;post({event:"height",height:h})}}
window.addEventListener("load",height);if(window.ResizeObserver){new ResizeObserver(height).observe(document.body)}height();
window.addEventListener("message",function(e){var d=e.data;if(d&&d.type==="vc-ad-measure"){last=0;height()}});
document.addEventListener("click",function(e){var t=e.target;if(!t||!t.closest)return;var card=t.closest("[data-ad-step]");
if(card){e.preventDefault();post({event:"step",target:card.getAttribute("data-ad-step")});return}
var a=t.closest("a[href]");if(!a)return;var href=a.getAttribute("href")||"";
if(/^https?:/i.test(href)){try{var u=new URL(href);if(src&&!u.searchParams.has("utm_source")){u.searchParams.set("utm_source",src);a.setAttribute("href",u.toString())}if(u.host!==location.host){a.setAttribute("target","_blank");a.setAttribute("rel","noopener")}}catch(err){}}});
})();`

// renderAdDesignHTML is the public document of one page of an HTML ad.
func renderAdDesignHTML(page boAdDesignPage, adID int64, step, src, title, nonce string) string {
	js := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"es\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	b.WriteString("<base target=\"_top\"><title>")
	b.WriteString(html.EscapeString(firstNonEmpty(strings.TrimSpace(title), "Anuncio")))
	b.WriteString("</title><style>html,body{margin:0;padding:0;background:transparent}[data-ad-step]{cursor:pointer}")
	// The stored CSS comes from the engine (Tailwind output): it never holds
	// a closing style tag, but the page must not be breakable through it.
	b.WriteString(strings.ReplaceAll(page.CSS, "</", "<\\/"))
	b.WriteString("</style></head><body>")
	b.WriteString(page.HTML)
	b.WriteString("<script nonce=\"")
	b.WriteString(nonce)
	b.WriteString("\">")
	b.WriteString(fmt.Sprintf(adDesignBridgeJS, js(adID), js(step), js(src)))
	b.WriteString("</script></body></html>")
	return b.String()
}
