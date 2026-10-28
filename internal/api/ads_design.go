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
	"time"
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
	// Schedule of a step (its card), YYYY-MM-DD, both or none: a card with
	// dates is only shown between them (inside the dates of the ad); without
	// dates it follows the ad.
	StartsAt *string `json:"starts_at,omitempty"`
	EndsAt   *string `json:"ends_at,omitempty"`
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
	d.Main.ID, d.Main.Title, d.Main.StartsAt, d.Main.EndsAt = "", "", nil, nil
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
		if err := normalizeAdStepSchedule(&st); err != nil {
			return nil, err
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

// normalizeAdStepSchedule checks the dates of a step: both or none, valid
// YYYY-MM-DD, the end not before the start.
func normalizeAdStepSchedule(st *boAdDesignPage) error {
	trim := func(v *string) *string {
		if v == nil || strings.TrimSpace(*v) == "" {
			return nil
		}
		t := strings.TrimSpace(*v)
		return &t
	}
	st.StartsAt, st.EndsAt = trim(st.StartsAt), trim(st.EndsAt)
	if (st.StartsAt == nil) != (st.EndsAt == nil) {
		return fmt.Errorf("la tarjeta «%s» necesita fecha de inicio y de fin (o ninguna)", firstNonEmpty(st.Title, st.ID))
	}
	if st.StartsAt == nil {
		return nil
	}
	start, err1 := time.Parse("2006-01-02", *st.StartsAt)
	end, err2 := time.Parse("2006-01-02", *st.EndsAt)
	if err1 != nil || err2 != nil || end.Before(start) {
		return fmt.Errorf("fechas no válidas en la tarjeta «%s»", firstNonEmpty(st.Title, st.ID))
	}
	return nil
}

// adStepVisibleOn: a step without dates follows the ad; with dates it is
// shown only between them. An empty date shows every step (previews).
func adStepVisibleOn(st boAdDesignPage, isoDate string) bool {
	if isoDate == "" || st.StartsAt == nil || st.EndsAt == nil {
		return true
	}
	return isoDate >= *st.StartsAt && isoDate <= *st.EndsAt
}

// adDesignShowsOn reports whether an HTML ad has anything to show that day: a
// multiple ad whose cards are all out of their dates is not shown at all.
func adDesignShowsOn(d *boAdDesign, isoDate string) bool {
	if d == nil || d.Kind != "multiple" {
		return true
	}
	for _, st := range d.Steps {
		if adStepVisibleOn(st, isoDate) {
			return true
		}
	}
	return false
}

// adHiddenCardsCSS hides, on the cards page, the cards whose step is out of
// its dates that day (the grid closes the gap on its own).
func adHiddenCardsCSS(d *boAdDesign, isoDate string) string {
	if d == nil || d.Kind != "multiple" {
		return ""
	}
	var b strings.Builder
	for _, st := range d.Steps {
		if !adStepVisibleOn(st, isoDate) {
			b.WriteString(`[data-ad-step="`)
			b.WriteString(st.ID) // validated by adDesignStepIDRe: [A-Za-z0-9_-]
			b.WriteString(`"]{display:none!important}`)
		}
	}
	return b.String()
}

// adTodayMadrid is the day the website lives in.
func adTodayMadrid() string {
	return time.Now().In(boMadridTZ).Format("2006-01-02")
}

// publicAdDesign is what the public ad list carries: kind, chrome and the
// steps shown that day (id + title), never the pages (each one is rendered on
// its own URL). An empty date keeps every step (previews).
func publicAdDesign(d *boAdDesign, isoDate string) *boAdDesign {
	if d == nil {
		return nil
	}
	out := &boAdDesign{Version: d.Version, Kind: d.Kind, Container: d.Container}
	for _, st := range d.Steps {
		if adStepVisibleOn(st, isoDate) {
			out.Steps = append(out.Steps, boAdDesignPage{ID: st.ID, Title: st.Title})
		}
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

// adDeviceClasses: the device a page is shown on is set on <html> as
// data-vp (mobile < 768px, tablet < 1024px, desktop) with the named group
// "group/vp", so a design can lay out per device with
// group-data-[vp=desktop]/vp: classes. The window of an ad is about as wide
// on a tablet as on a desktop, so the width of the page alone cannot tell
// them apart.
const adDeviceGroupClass = "group/vp"

// adDeviceDetectJS sets data-vp from the screen when the embedding page did
// not say it (?vp=): the top window when it is the same site, the screen
// otherwise.
const adDeviceDetectJS = `(function(){var h=document.documentElement;if(h.getAttribute("data-vp"))return;var w=0;try{w=window.top.innerWidth}catch(e){}if(!w)w=screen.width;h.setAttribute("data-vp",w<768?"mobile":w<1024?"tablet":"desktop")})();`

// adDeviceParam validates the ?vp= of a render request.
func adDeviceParam(raw string) string {
	switch v := strings.TrimSpace(raw); v {
	case "mobile", "tablet", "desktop":
		return v
	}
	return ""
}

// renderAdDesignHTML is the public document of one page of an HTML ad.
func renderAdDesignHTML(page boAdDesignPage, adID int64, step, src, title, nonce, extraCSS, device string) string {
	js := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}
	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"es\" class=\"" + adDeviceGroupClass + "\"")
	if device != "" {
		b.WriteString(" data-vp=\"" + device + "\"")
	}
	b.WriteString("><head><meta charset=\"utf-8\">")
	b.WriteString("<script nonce=\"" + nonce + "\">" + adDeviceDetectJS + "</script>")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	b.WriteString("<base target=\"_top\"><title>")
	b.WriteString(html.EscapeString(firstNonEmpty(strings.TrimSpace(title), "Anuncio")))
	// The page never scrolls: the embedding page sizes the iframe to the
	// height posted below. A scrollbar would narrow the page, its images would
	// shrink with it and the reported height would keep the scrollbar forever.
	b.WriteString("</title><style>html,body{margin:0;padding:0;background:transparent;overflow:hidden}[data-ad-step]{cursor:pointer}")
	// The stored CSS comes from the engine (Tailwind output): it never holds
	// a closing style tag, but the page must not be breakable through it.
	b.WriteString(strings.ReplaceAll(page.CSS, "</", "<\\/"))
	b.WriteString(extraCSS)
	b.WriteString("</style></head><body>")
	b.WriteString(page.HTML)
	b.WriteString("<script nonce=\"")
	b.WriteString(nonce)
	b.WriteString("\">")
	b.WriteString(fmt.Sprintf(adDesignBridgeJS, js(adID), js(step), js(src)))
	b.WriteString("</script></body></html>")
	return b.String()
}
