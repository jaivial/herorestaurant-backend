package api

import (
	"fmt"
	"regexp"
	"strings"
)

// campaignTheme holds the visual tokens applied when the shared markdown body
// is rendered as an email. WhatsApp ignores them (plain text channel).
type campaignTheme struct {
	Background string `json:"background"`
	Surface    string `json:"surface"`
	Text       string `json:"text"`
	Accent     string `json:"accent"`
	FontFamily string `json:"fontFamily"`
	MaxWidth   int    `json:"maxWidth"`
	Align      string `json:"align"`
}

// campaignBaseTheme mirrors the transactional email template already used by
// restaurant 1 (booking confirmations), so campaigns look native by default.
var campaignBaseTheme = campaignTheme{
	Background: "#f4f4f4",
	Surface:    "#ffffff",
	Text:       "#333333",
	Accent:     "#097969",
	FontFamily: "Arial, Helvetica, sans-serif",
	MaxWidth:   600,
	Align:      "left",
}

func normalizeCampaignTheme(t campaignTheme) campaignTheme {
	out := t
	out.Background = firstNonEmpty(hexColorOrEmpty(t.Background), campaignBaseTheme.Background)
	out.Surface = firstNonEmpty(hexColorOrEmpty(t.Surface), campaignBaseTheme.Surface)
	out.Text = firstNonEmpty(hexColorOrEmpty(t.Text), campaignBaseTheme.Text)
	out.Accent = firstNonEmpty(hexColorOrEmpty(t.Accent), campaignBaseTheme.Accent)
	out.FontFamily = firstNonEmpty(strings.TrimSpace(t.FontFamily), campaignBaseTheme.FontFamily)
	if out.MaxWidth < 320 || out.MaxWidth > 900 {
		out.MaxWidth = 600
	}
	if out.Align != "center" && out.Align != "right" {
		out.Align = "left"
	}
	return out
}

var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

func hexColorOrEmpty(v string) string {
	v = strings.TrimSpace(v)
	if hexColorRe.MatchString(v) {
		return v
	}
	return ""
}

var (
	mdImageRe  = regexp.MustCompile(`!\[([^\]]*)\]\(([^)\s]+)\)`)
	mdLinkRe   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	mdBoldRe   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalicRe = regexp.MustCompile(`(^|[^*])\*([^*]+)\*`)
	mdCodeRe   = regexp.MustCompile("`([^`]+)`")
)

// renderCampaignInline turns the inline markdown subset into email HTML.
func renderCampaignInline(line string, theme campaignTheme) string {
	out := htmlEscape(line)
	out = mdImageRe.ReplaceAllStringFunc(out, func(m string) string {
		parts := mdImageRe.FindStringSubmatch(m)
		return fmt.Sprintf(`<img src="%s" alt="%s" style="max-width:100%%;height:auto;border-radius:10px;display:block;margin:12px 0" />`, parts[2], parts[1])
	})
	out = mdLinkRe.ReplaceAllString(out, fmt.Sprintf(`<a href="$2" style="color:%s;text-decoration:underline">$1</a>`, theme.Accent))
	out = mdBoldRe.ReplaceAllString(out, "<strong>$1</strong>")
	out = mdItalicRe.ReplaceAllString(out, "$1<em>$2</em>")
	out = mdCodeRe.ReplaceAllString(out, `<code>$1</code>`)
	return out
}

// renderCampaignEmailHTML renders the markdown body into a table-free but
// email-safe HTML document using the campaign theme.
func renderCampaignEmailHTML(markdown string, theme campaignTheme, brandName, logoURL string) string {
	theme = normalizeCampaignTheme(theme)
	var b strings.Builder
	inList := false
	closeList := func() {
		if inList {
			b.WriteString("</ul>")
			inList = false
		}
	}
	for _, raw := range strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			closeList()
			continue
		}
		switch {
		case strings.HasPrefix(line, "### "):
			closeList()
			fmt.Fprintf(&b, `<h3 style="margin:18px 0 8px;font-size:18px;color:%s">%s</h3>`, theme.Text, renderCampaignInline(line[4:], theme))
		case strings.HasPrefix(line, "## "):
			closeList()
			fmt.Fprintf(&b, `<h2 style="margin:20px 0 10px;font-size:22px;color:%s">%s</h2>`, theme.Text, renderCampaignInline(line[3:], theme))
		case strings.HasPrefix(line, "# "):
			closeList()
			fmt.Fprintf(&b, `<h1 style="margin:0 0 14px;font-size:27px;color:%s">%s</h1>`, theme.Accent, renderCampaignInline(line[2:], theme))
		case strings.HasPrefix(line, "> "):
			closeList()
			fmt.Fprintf(&b, `<blockquote style="margin:14px 0;padding:10px 14px;border-left:4px solid %s;background:%s11">%s</blockquote>`, theme.Accent, theme.Accent, renderCampaignInline(line[2:], theme))
		case line == "---":
			closeList()
			fmt.Fprintf(&b, `<hr style="border:none;border-top:1px solid %s33;margin:20px 0" />`, theme.Text)
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			if !inList {
				b.WriteString(`<ul style="margin:10px 0;padding-left:20px">`)
				inList = true
			}
			fmt.Fprintf(&b, `<li style="margin:4px 0">%s</li>`, renderCampaignInline(line[2:], theme))
		default:
			closeList()
			fmt.Fprintf(&b, `<p style="margin:10px 0;line-height:1.6">%s</p>`, renderCampaignInline(line, theme))
		}
	}
	closeList()

	return campaignEmailShell(theme, brandName, logoURL, b.String())
}

// campaignEmailBodyPlaceholder marks where the rendered markdown goes when the
// shell is handed to the editor for live preview.
const campaignEmailBodyPlaceholder = "{{CAMPAIGN_BODY}}"

// campaignEmailShell reproduces the transactional booking email layout: accent
// header band with the logo, 600px white card, automatic-message footer. Both
// the sent email and the editor preview use this exact markup.
func campaignEmailShell(theme campaignTheme, brandName, logoURL, bodyHTML string) string {
	theme = normalizeCampaignTheme(theme)
	header := ""
	if strings.TrimSpace(logoURL) != "" {
		header = fmt.Sprintf(`<tr>
<td style="padding:30px 20px;text-align:center;background-color:%s;">
<img src="%s" alt="%s" style="max-width:200px;height:auto;">
</td>
</tr>
`, theme.Accent, logoURL, htmlEscape(brandName))
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>%s</title>
</head>
<body style="margin:0;padding:0;font-family:%s;line-height:1.6;background-color:%s;">
<table role="presentation" width="100%%" cellspacing="0" cellpadding="0" style="max-width:%dpx;margin:0 auto;background-color:%s;border-radius:8px;overflow:hidden;box-shadow:0 2px 4px rgba(0,0,0,0.1);">
%s<tr>
<td style="padding:30px 20px;color:%s;text-align:%s;">
%s
<hr style="border:none;border-top:1px solid #eee;margin:30px 0;">
<p style="font-size:12px;color:#666;text-align:center;">Este es un email automatico, por favor no responda a este mensaje.<br>&copy; %s. Todos los derechos reservados.</p>
</td>
</tr>
</table>
</body>
</html>`,
		htmlEscape(brandName),
		theme.FontFamily,
		theme.Background,
		theme.MaxWidth,
		theme.Surface,
		header,
		theme.Text,
		theme.Align,
		bodyHTML,
		htmlEscape(brandName),
	)
}

// splitCampaignLeadImage returns the first markdown image URL and the body with
// that image removed, so WhatsApp can send it as media plus caption.
func splitCampaignLeadImage(markdown string) (string, string) {
	match := mdImageRe.FindStringSubmatchIndex(markdown)
	if match == nil {
		return "", markdown
	}
	url := markdown[match[4]:match[5]]
	if !strings.HasPrefix(url, "https://") {
		return "", markdown
	}
	return url, markdown[:match[0]] + markdown[match[1]:]
}

// renderCampaignWhatsAppText converts the same markdown into WhatsApp markup.
// Images degrade to their CDN URL so the client still previews them.
func renderCampaignWhatsAppText(markdown string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "---" {
			out = append(out, "———")
			continue
		}
		line = mdImageRe.ReplaceAllString(line, "$2")
		line = mdLinkRe.ReplaceAllString(line, "$1: $2")
		line = strings.TrimPrefix(line, "> ")
		for _, prefix := range []string{"### ", "## ", "# "} {
			if strings.HasPrefix(line, prefix) {
				line = "*" + strings.TrimPrefix(line, prefix) + "*"
				break
			}
		}
		if strings.HasPrefix(line, "* ") {
			line = "- " + strings.TrimPrefix(line, "* ")
		}
		line = mdBoldRe.ReplaceAllString(line, "*$1*")
		line = mdItalicRe.ReplaceAllString(line, "$1_$2_")
		line = mdCodeRe.ReplaceAllString(line, "```$1```")
		out = append(out, line)
	}
	text := strings.Join(out, "\n")
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(text)
}
