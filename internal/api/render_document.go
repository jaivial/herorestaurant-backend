package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Coordination id: render_document_v1
//
// One custom tool (render_document) turns model-written HTML into a PDF
// and/or a 4:3 image, stores them in the restaurant CDN and returns their
// URLs. Forky shows them with a forky-doc block; the WhatsApp bot (privileged
// chats only) also sends them to the chat.
//
// Rendering uses WeasyPrint (HTML/CSS -> PDF, no browser, no JavaScript) and
// Poppler's pdftoppm for the 4:3 PNG. It is sandboxed: every external
// resource is pre-fetched in Go through renderDocClient (https only, IP
// checked at connect time, SSRF guard) and inlined as data: URIs; WeasyPrint
// itself runs with a URL fetcher that refuses anything but data:, so the
// renderer never reaches the network.

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
	"Escribe HTML completo y autocontenido con CSS en <style>; usa fuentes del sistema o @font-face https, imágenes https públicas del restaurante o data:. NO se ejecuta JavaScript. Para maquetar en varias columnas usa display:grid o flex con anchos explícitos (no CSS columns). " +
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
	if _, err := exec.LookPath("weasyprint"); err != nil {
		return renderDocOutput{}, errors.New("el renderizador de documentos (WeasyPrint) no está disponible en el servidor")
	}
	if format != "pdf" {
		if _, err := exec.LookPath("pdftoppm"); err != nil {
			return renderDocOutput{}, errors.New("el conversor a imagen (pdftoppm) no está disponible en el servidor")
		}
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

// renderDocWeasyScript runs WeasyPrint with a fetcher that only resolves
// data: URIs: every network resource was already inlined by Go.
const renderDocWeasyScript = `import sys
from weasyprint import HTML
from weasyprint.urls import default_url_fetcher
def fetcher(url, timeout=10, ssl_context=None):
    if url.startswith("data:"):
        return default_url_fetcher(url, timeout, ssl_context)
    raise ValueError("render_document: recurso no permitido")
html = sys.stdin.buffer.read().decode("utf-8")
HTML(string=html, url_fetcher=fetcher, base_url="data:,").write_pdf(sys.argv[1])
`

// renderHTMLSandboxed renders the PDF with WeasyPrint and, for the image, a
// second PDF whose page is exactly width x width*3/4 rasterised by pdftoppm.
func renderHTMLSandboxed(ctx context.Context, html, format string, width int, landscape bool) (pdf, png []byte, err error) {
	runCtx, cancel := context.WithTimeout(ctx, renderDocTimeout)
	defer cancel()
	inlined, err := renderDocInlineResources(runCtx, html)
	if err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "render-doc-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)

	if format == "pdf" || format == "both" {
		size := "A4"
		if landscape {
			size = "A4 landscape"
		}
		// The HTML's own @page wins: this default comes first in the cascade.
		pdf, err = renderDocWeasy(runCtx, dir, "doc.pdf", renderDocWithPage(inlined, "size:"+size))
		if err != nil {
			return nil, nil, err
		}
	}
	if format == "image" || format == "both" {
		height := width * 3 / 4
		// One page sized in CSS px (96 dpi) = width x height; the HTML's
		// html/body at 100% fill it. A forced @page after the author's one
		// guarantees the 4:3 canvas.
		page := fmt.Sprintf("size:%dpx %dpx;margin:0", width, height)
		imgPDF, err := renderDocWeasy(runCtx, dir, "img.pdf", renderDocForcePage(inlined, page))
		if err != nil {
			return nil, nil, err
		}
		png, err = renderDocRaster(runCtx, dir, imgPDF, width, height)
		if err != nil {
			return nil, nil, err
		}
	}
	return pdf, png, nil
}

func renderDocWeasy(ctx context.Context, dir, name, html string) ([]byte, error) {
	out := filepath.Join(dir, name)
	cmd := exec.CommandContext(ctx, "python3", "-c", renderDocWeasyScript, out)
	cmd.Stdin = strings.NewReader(html)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin", "LANG=C.UTF-8", "XDG_CACHE_HOME=" + dir}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("weasyprint: %v %s", err, renderDocTail(stderr.String()))
	}
	return os.ReadFile(out)
}

func renderDocRaster(ctx context.Context, dir string, pdf []byte, width, height int) ([]byte, error) {
	in := filepath.Join(dir, "raster.pdf")
	if err := os.WriteFile(in, pdf, 0o600); err != nil {
		return nil, err
	}
	prefix := filepath.Join(dir, "raster")
	cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-singlefile", "-f", "1", "-l", "1",
		"-scale-to-x", fmt.Sprint(width), "-scale-to-y", fmt.Sprint(height), in, prefix)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftoppm: %v %s", err, renderDocTail(stderr.String()))
	}
	return os.ReadFile(prefix + ".png")
}

func renderDocTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[len(s)-300:]
	}
	return s
}

var renderDocHeadRe = regexp.MustCompile(`(?i)<head[^>]*>`)

// renderDocWithPage puts a default @page FIRST, so the author's own @page
// (later in the cascade) overrides it.
func renderDocWithPage(html, decl string) string {
	style := "<style>@page{" + decl + "}</style>"
	if loc := renderDocHeadRe.FindStringIndex(html); loc != nil {
		return html[:loc[1]] + style + html[loc[1]:]
	}
	return style + html
}

// renderDocForcePage appends an @page with !important LAST, so the 4:3
// canvas always wins over the author's @page.
func renderDocForcePage(html, decl string) string {
	style := "<style>@page{" + strings.ReplaceAll(decl, ";", " !important;") + " !important}</style>"
	if i := strings.LastIndex(strings.ToLower(html), "</body>"); i >= 0 {
		return html[:i] + style + html[i:]
	}
	return html + style
}

// renderDocURLRe finds external URLs in attributes and CSS url(...).
var renderDocURLRe = regexp.MustCompile(`(?i)(?:src|href|poster)\s*=\s*["']([^"']+)["']|url\(\s*["']?([^"')\s]+)["']?\s*\)|@import\s+["']([^"']+)["']`)

const renderDocMaxInlined = 24 << 20

// renderDocInlineResources downloads every https resource through the
// guarded client and replaces it with a data: URI. Anything that is not
// https or data: (http:, file:, relative...) is left in place and WeasyPrint's
// data-only fetcher refuses it.
func renderDocInlineResources(ctx context.Context, html string) (string, error) {
	urls := map[string]bool{}
	collect := func(text string) {
		for _, m := range renderDocURLRe.FindAllStringSubmatch(text, -1) {
			for _, u := range m[1:] {
				u = strings.TrimSpace(u)
				if strings.HasPrefix(strings.ToLower(u), "https://") {
					urls[u] = true
				}
			}
		}
	}
	collect(html)
	replacements := map[string]string{}
	total := 0
	// Stylesheets may reference fonts/images too: inline one level deep.
	for pass := 0; pass < 2; pass++ {
		pending := make([]string, 0, len(urls))
		for u := range urls {
			if _, done := replacements[u]; !done {
				pending = append(pending, u)
			}
		}
		sort.Strings(pending)
		for _, u := range pending {
			ctype, body, err := renderDocFetch(ctx, u)
			if err != nil {
				replacements[u] = "" // leave as-is; the data-only fetcher drops it
				continue
			}
			total += len(body)
			if total > renderDocMaxInlined {
				return "", errors.New("los recursos externos superan el máximo permitido")
			}
			if strings.Contains(ctype, "css") {
				collect(string(body))
			}
			replacements[u] = "data:" + ctype + ";base64," + base64.StdEncoding.EncodeToString(body)
		}
	}
	// Inline CSS resources inside fetched stylesheets, then the HTML itself.
	for u, data := range replacements {
		if data == "" || !strings.HasPrefix(data, "data:text/css") {
			continue
		}
		raw, _ := base64.StdEncoding.DecodeString(data[strings.Index(data, ",")+1:])
		css := string(raw)
		for v, d := range replacements {
			if d != "" && v != u && !strings.HasPrefix(d, "data:text/css") {
				css = strings.ReplaceAll(css, v, d)
			}
		}
		replacements[u] = "data:text/css;base64," + base64.StdEncoding.EncodeToString([]byte(css))
	}
	keys := make([]string, 0, len(replacements))
	for k := range replacements {
		keys = append(keys, k)
	}
	// Longest first so a URL that is a prefix of another never splits it.
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		if d := replacements[k]; d != "" {
			html = strings.ReplaceAll(html, k, d)
		}
	}
	return html, nil
}

// renderDocBlockedIP reports addresses the renderer must never reach.
func renderDocBlockedIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast()
}

// renderDocURLAllowed is the cheap pre-check (scheme + host); the binding
// check is the dialer of renderDocClient.
func renderDocURLAllowed(raw string) bool {
	if strings.HasPrefix(raw, "data:") || raw == "about:blank" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && renderDocBlockedIP(ip) {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h != "localhost" && !strings.HasSuffix(h, ".localhost")
}

// renderDocClient fetches page resources: https only, no redirects to other
// schemes, and every connection's real IP is checked by the dialer.
var renderDocClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
			Control: func(network, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				if renderDocBlockedIP(net.ParseIP(host)) {
					return errors.New("render_document: destino interno bloqueado")
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		MaxIdleConns:          4,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !renderDocURLAllowed(req.URL.String()) {
			return errors.New("redirección bloqueada")
		}
		return nil
	},
}

const renderDocMaxResource = 8 << 20

func renderDocFetch(ctx context.Context, raw string) (string, []byte, error) {
	if !renderDocURLAllowed(raw) {
		return "", nil, errors.New("url no permitida")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", nil, err
	}
	res, err := renderDocClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return "", nil, fmt.Errorf("http %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, renderDocMaxResource+1))
	if err != nil || len(body) > renderDocMaxResource {
		return "", nil, errors.New("recurso ilegible o demasiado grande")
	}
	ctype, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if ctype == "" {
		ctype = http.DetectContentType(body)
		ctype, _, _ = mime.ParseMediaType(ctype)
	}
	return ctype, body, nil
}

// renderDocFilename turns a title into a safe object/file name.
func renderDocFilename(title string) string {
	slug := slugifyCategoryName(title)
	if len(slug) > 60 {
		slug = slug[:60]
	}
	return slug
}
