package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Coordination id: render_document_v1
//
// One custom tool (render_document) turns model-written HTML into a PDF
// and/or a 4:3 image, stores them in the restaurant CDN and returns their
// URLs. Forky shows them with a forky-doc block; the WhatsApp bot (privileged
// chats only) also sends them to the chat. Rendering is sandboxed: no
// JavaScript, and the page may only load https resources on public hosts
// (images, fonts, CSS) — never internal addresses (SSRF guard).

const (
	renderDocMaxHTML  = 400 << 10
	renderDocTimeout  = 45 * time.Second
	renderDocWidthMin = 400
	renderDocWidthMax = 2400
)

type renderDocInput struct {
	HTML        string `json:"html"`
	Title       string `json:"title"`
	Format      string `json:"format"`      // pdf | image | both
	Width       int    `json:"width"`       // image width, px (height = width*3/4)
	Orientation string `json:"orientation"` // pdf: portrait | landscape
	Caption     string `json:"caption"`
}

type renderDocOutput struct {
	Title    string `json:"title"`
	PDFURL   string `json:"pdf_url,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

func renderDocumentSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
		"html":{"type":"string","description":"Documento HTML completo y autocontenido (estilos en <style>; imágenes https públicas o data:). Sin JavaScript: no se ejecuta."},
		"title":{"type":"string","description":"Título/nombre del archivo"},
		"format":{"type":"string","enum":["pdf","image","both"],"description":"pdf, image (PNG 4:3) o both. Por defecto pdf."},
		"width":{"type":"integer","description":"Ancho de la imagen en px (alto = ancho·3/4). Por defecto 1600."},
		"orientation":{"type":"string","enum":["portrait","landscape"],"description":"Solo PDF: orientación A4 si el HTML no define @page. Por defecto portrait."},
		"caption":{"type":"string","description":"Solo WhatsApp: texto que acompaña al envío"}
	},"required":["html"]}`)
}

const renderDocumentDescription = "Genera un documento a partir de HTML que escribes tú (carta, menú, cartel, informe, presupuesto, ficha) y lo convierte en PDF y/o imagen PNG en proporción 4:3. " +
	"Escribe HTML completo y autocontenido con CSS en <style>; usa fuentes del sistema o @font-face https, imágenes https públicas del restaurante o data:. NO se ejecuta JavaScript. " +
	"Para la imagen 4:3 diseña el contenido para un lienzo de 1600×1200 (o el width que pidas): usa html,body{width:100%;height:100%;margin:0}. Para PDF usa @page {size:A4; margin:...} si necesitas otro tamaño. " +
	"Devuelve pdf_url e image_url en el CDN del restaurante."

func init() {
	assistantToolRegistry = append(assistantToolRegistry, assistantTool{
		Name:           "render_document",
		Description:    renderDocumentDescription + " En el chat muéstralos con un bloque forky-doc (kind pdf o image) usando esas URLs tal cual.",
		Schema:         renderDocumentSchema(),
		Section:        assistantSessionSection,
		BackofficeOnly: true,
		Handler: func(s *Server, ctx context.Context, rid int, input json.RawMessage) (string, error) {
			out, err := s.renderDocument(ctx, rid, input)
			if err != nil {
				return "", err
			}
			return botJSON(out), nil
		},
	})
}

// botRenderDocumentDef is the WhatsApp twin: same renderer, plus delivery to
// the chat. Registered only in the privileged control catalog.
func botRenderDocumentDef() botToolDef {
	return botToolDef{
		Name:        "render_document",
		Description: renderDocumentDescription + " En WhatsApp además ENVÍA a este chat el PDF (documento) y/o la imagen.",
		InputSchema: renderDocumentSchema(),
	}
}

func (s *Server) botToolRenderDocument(ctx context.Context, rid int, msg botWebhookMessage, input json.RawMessage) (string, error) {
	if s.botStoredTenantConfig(ctx, rid).DisableAttachments {
		return botJSON(map[string]any{"error": "los adjuntos están desactivados para este restaurante"}), nil
	}
	out, err := s.renderDocument(ctx, rid, input)
	if err != nil {
		return botJSON(map[string]any{"error": err.Error()}), nil
	}
	var in renderDocInput
	_ = json.Unmarshal(input, &in)
	gw, ok := s.botGatewayFor(ctx, rid)
	if !ok {
		return botJSON(map[string]any{"error": "whatsapp no configurado", "pdf_url": out.PDFURL, "image_url": out.ImageURL}), nil
	}
	caption := strings.TrimSpace(in.Caption)
	sent := []string{}
	if out.ImageURL != "" {
		if err := gw.SendMedia(ctx, msg.Sender, waMedia{Kind: "image", URL: out.ImageURL, Caption: caption}); err != nil {
			return botJSON(map[string]any{"error": "whatsapp no aceptó la imagen: " + err.Error(), "image_url": out.ImageURL, "pdf_url": out.PDFURL}), nil
		}
		sent = append(sent, "image")
		caption = "" // the caption travels once
	}
	if out.PDFURL != "" {
		if err := gw.SendMedia(ctx, msg.Sender, waMedia{Kind: "document", URL: out.PDFURL, Filename: renderDocFilename(out.Title) + ".pdf", Caption: caption}); err != nil {
			return botJSON(map[string]any{"error": "whatsapp no aceptó el PDF: " + err.Error(), "image_url": out.ImageURL, "pdf_url": out.PDFURL, "sent": sent}), nil
		}
		sent = append(sent, "pdf")
	}
	s.botRecordConversationMessage(ctx, rid, msg.Sender, "assistant", "["+out.Title+" ("+strings.Join(sent, "+")+")]", "render_document", "agent")
	botCheckpointCtx(ctx, "render_document_v1", "restaurant_id=%d sent=%s", rid, strings.Join(sent, ","))
	return botJSON(map[string]any{"sent": sent, "title": out.Title, "pdf_url": out.PDFURL, "image_url": out.ImageURL}), nil
}

// renderDocument validates, renders and uploads. Shared by Forky and the bot.
func (s *Server) renderDocument(ctx context.Context, rid int, raw json.RawMessage) (renderDocOutput, error) {
	var in renderDocInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return renderDocOutput{}, errors.New("argumentos inválidos")
	}
	html := strings.TrimSpace(in.HTML)
	if html == "" {
		return renderDocOutput{}, errors.New("html es obligatorio")
	}
	if len(html) > renderDocMaxHTML {
		return renderDocOutput{}, fmt.Errorf("el html supera %d KB", renderDocMaxHTML>>10)
	}
	format := strings.ToLower(strings.TrimSpace(in.Format))
	if format == "" {
		format = "pdf"
	}
	if format != "pdf" && format != "image" && format != "both" {
		return renderDocOutput{}, errors.New("format debe ser pdf, image o both")
	}
	width := in.Width
	if width == 0 {
		width = 1600
	}
	if width < renderDocWidthMin || width > renderDocWidthMax {
		return renderDocOutput{}, fmt.Errorf("width debe estar entre %d y %d", renderDocWidthMin, renderDocWidthMax)
	}
	if chromeExecPath() == "" {
		return renderDocOutput{}, errors.New("el renderizador (Chromium) no está disponible en el servidor")
	}
	if !s.bunnyConfigured(ctx, rid) {
		return renderDocOutput{}, errors.New("no hay almacenamiento (BunnyCDN) configurado")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "Documento"
	}
	started := time.Now()
	pdf, png, err := renderHTMLSandboxed(ctx, html, format, width, strings.EqualFold(in.Orientation, "landscape"))
	if err != nil {
		log.Printf("render_document_v1 restaurant=%d format=%s failed err=%v", rid, format, err)
		return renderDocOutput{}, errors.New("no se pudo renderizar el documento: " + err.Error())
	}
	base := fmt.Sprintf("%d/documentos/%s-%d", rid, renderDocFilename(title), time.Now().UnixNano())
	out := renderDocOutput{Title: title}
	if pdf != nil {
		if err := s.bunnyPut(ctx, rid, base+".pdf", pdf, "application/pdf"); err != nil {
			return renderDocOutput{}, errors.New("no se pudo subir el PDF al CDN")
		}
		out.PDFURL = s.bunnyPullURL(ctx, rid, base+".pdf")
	}
	if png != nil {
		if err := s.bunnyPut(ctx, rid, base+".png", png, "image/png"); err != nil {
			return renderDocOutput{}, errors.New("no se pudo subir la imagen al CDN")
		}
		out.ImageURL = s.bunnyPullURL(ctx, rid, base+".png")
		out.Width, out.Height = width, width*3/4
	}
	log.Printf("render_document_v1 restaurant=%d format=%s html_bytes=%d pdf_bytes=%d png_bytes=%d ms=%d", rid, format, len(html), len(pdf), len(png), time.Since(started).Milliseconds())
	return out, nil
}

// renderHTMLSandboxed renders with JavaScript disabled and every request
// intercepted: data:/about: pass, https only to public hosts, everything else
// (file:, http:, private/loopback/link-local IPs) is blocked.
func renderHTMLSandboxed(ctx context.Context, html, format string, width int, landscape bool) (pdf, png []byte, err error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromeExecPath()),
		chromedp.NoSandbox,
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("font-render-hinting", "none"),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.WithoutCancel(ctx), opts...)
	defer cancelAlloc()
	taskCtx, cancelTask := chromedp.NewContext(allocCtx)
	defer cancelTask()
	runCtx, cancel := context.WithTimeout(taskCtx, renderDocTimeout)
	defer cancel()

	chromedp.ListenTarget(runCtx, func(ev any) {
		e, ok := ev.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		go func() {
			c := chromedp.FromContext(runCtx)
			if c == nil || c.Target == nil {
				return
			}
			execCtx := cdp.WithExecutor(runCtx, c.Target)
			if renderDocURLAllowed(e.Request.URL) {
				_ = fetch.ContinueRequest(e.RequestID).Do(execCtx)
				return
			}
			_ = fetch.FailRequest(e.RequestID, network.ErrorReasonBlockedByClient).Do(execCtx)
		}()
	})

	height := width * 3 / 4
	const waitAssets = `new Promise((resolve)=>{
		const done=()=>resolve(true);
		const imgs=Array.from(document.images).filter(i=>!i.complete);
		let pending=imgs.length;
		const fonts=document.fonts?document.fonts.ready:Promise.resolve();
		fonts.then(()=>{ if(pending===0) done(); });
		imgs.forEach(i=>{const f=()=>{if(--pending<=0) fonts.then(done);}; i.addEventListener('load',f); i.addEventListener('error',f);});
		setTimeout(done,5000);
	})`
	actions := []chromedp.Action{
		fetch.Enable().WithPatterns([]*fetch.RequestPattern{{URLPattern: "*"}}),
		emulation.SetScriptExecutionDisabled(true),
		emulation.SetDeviceMetricsOverride(int64(width), int64(height), 1, false),
		chromedp.Navigate("data:text/html;charset=utf-8;base64," + base64.StdEncoding.EncodeToString([]byte(html))),
		// Evaluate runs over the protocol, not as page script, so it still
		// works with page scripts disabled.
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, _, e := runtime.Evaluate(waitAssets).WithAwaitPromise(true).Do(ctx)
			return e
		}),
	}
	if format == "image" || format == "both" {
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			var e error
			png, e = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).
				WithClip(&page.Viewport{X: 0, Y: 0, Width: float64(width), Height: float64(height), Scale: 1}).
				WithCaptureBeyondViewport(false).Do(ctx)
			return e
		}))
	}
	if format == "pdf" || format == "both" {
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			var e error
			pdf, _, e = page.PrintToPDF().WithPrintBackground(true).WithPreferCSSPageSize(true).
				WithLandscape(landscape).WithPaperWidth(8.27).WithPaperHeight(11.69).Do(ctx)
			return e
		}))
	}
	if err := chromedp.Run(runCtx, actions...); err != nil {
		return nil, nil, err
	}
	return pdf, png, nil
}

// renderDocURLAllowed is the SSRF guard of the renderer.
func renderDocURLAllowed(raw string) bool {
	if strings.HasPrefix(raw, "data:") || raw == "about:blank" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return false
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return false
		}
	}
	return true
}

// renderDocFilename turns a title into a safe object/file name.
func renderDocFilename(title string) string {
	slug := slugifyCategoryName(title)
	if len(slug) > 60 {
		slug = slug[:60]
	}
	return slug
}
