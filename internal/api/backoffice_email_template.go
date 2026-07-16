package api

import (
	"strings"
)

// App-branded email identity. These emails are sent by the "Restaurant
// Backoffice" application itself (invitations, password resets), not by the
// restaurant, so they carry the app brand instead of the restaurant branding.
const backofficeEmailFromName = "Restaurant Backoffice"
const backofficeEmailBrand = "Restaurant Backoffice"
const backofficeEmailLogoURL = "https://herorestaurantmedia.b-cdn.net/icon/backoffice-logo-light.png"

// Dark palette. Colors are pinned so the message renders dark even in email
// clients that would otherwise apply a light background.
const (
	boEmailBg       = "#0b0d11" // page background
	boEmailSurface  = "#161a21" // card surface
	boEmailHeaderBg = "#1d222b" // header band
	boEmailBorder   = "#2a2f3a" // hairlines
	boEmailText     = "#f2f4f8" // primary text
	boEmailMuted    = "#9aa2b1" // secondary text
	boEmailAccent   = "#38b28f" // CTA / links
	boEmailAccentHi = "#45d1a6" // accent highlight
	boEmailOnAccnt  = "#06231b" // text on accent button
)

const backofficeEmailTemplate = `<!DOCTYPE html>
<html lang="es" style="color-scheme:dark;supported-color-schemes:dark;">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta name="color-scheme" content="dark">
<meta name="supported-color-schemes" content="dark">
<title>{{TITLE}}</title>
<style>
  :root { color-scheme: dark; supported-color-schemes: dark; }
  body { margin:0; padding:0; width:100% !important; }
  a { text-decoration:none; }
  .bo-bg { background-color:{{BG}} !important; }
  .bo-card { background-color:{{SURFACE}} !important; border-color:{{BORDER}} !important; }
  .bo-head { background-color:{{HEADER_BG}} !important; }
  .bo-text { color:{{TEXT}} !important; }
  .bo-muted { color:{{MUTED}} !important; }
  .bo-accent { color:{{ACCENT}} !important; }
  .bo-btn a { color:{{ON_ACCENT}} !important; }
  /* Force the dark palette even when the client is in light mode. */
  @media (prefers-color-scheme: light) {
    body, .bo-bg { background-color:{{BG}} !important; }
    .bo-card { background-color:{{SURFACE}} !important; border-color:{{BORDER}} !important; }
    .bo-head { background-color:{{HEADER_BG}} !important; }
    .bo-text { color:{{TEXT}} !important; }
    .bo-muted { color:{{MUTED}} !important; }
    .bo-accent { color:{{ACCENT}} !important; }
  }
  /* Gmail / Outlook mobile dark-mode override hooks. */
  [data-ogsc] .bo-bg, u + .body .bo-bg { background-color:{{BG}} !important; }
  [data-ogsc] .bo-card, [data-ogsb] .bo-card { background-color:{{SURFACE}} !important; border-color:{{BORDER}} !important; }
  [data-ogsc] .bo-head, [data-ogsb] .bo-head { background-color:{{HEADER_BG}} !important; }
  [data-ogsc] .bo-text { color:{{TEXT}} !important; }
  [data-ogsc] .bo-muted { color:{{MUTED}} !important; }
  [data-ogsc] .bo-accent { color:{{ACCENT}} !important; }
  @media only screen and (max-width:600px) {
    .bo-pad { padding-left:24px !important; padding-right:24px !important; }
  }
</style>
</head>
<body class="body bo-bg" style="margin:0;padding:0;background-color:{{BG}};font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;">{{PREHEADER}}</div>
<table role="presentation" class="bo-bg" width="100%" cellspacing="0" cellpadding="0" border="0" style="background-color:{{BG}};margin:0;padding:0;">
<tr><td align="center" style="padding:40px 16px;">

<table role="presentation" class="bo-card" width="100%" cellspacing="0" cellpadding="0" border="0" style="max-width:480px;width:100%;background-color:{{SURFACE}};border:1px solid {{BORDER}};border-radius:18px;overflow:hidden;">

  <!-- Header: app logo + wordmark -->
  <tr>
  <td class="bo-head" align="center" style="background-color:{{HEADER_BG}};padding:34px 32px 26px 32px;border-bottom:1px solid {{BORDER}};">
    <img src="{{LOGO}}" width="72" height="72" alt="{{BRAND}}" style="display:block;width:72px;height:72px;margin:0 auto 16px auto;border:0;outline:none;">
    <div class="bo-text" style="font-size:18px;font-weight:700;letter-spacing:0.02em;color:{{TEXT}};">{{BRAND}}</div>
    <div class="bo-muted" style="font-size:12px;font-weight:500;letter-spacing:0.16em;text-transform:uppercase;color:{{MUTED}};margin-top:5px;">Panel de gestion</div>
  </td>
  </tr>

  <!-- Accent divider -->
  <tr><td style="height:4px;line-height:4px;font-size:0;background-color:{{ACCENT}};background-image:linear-gradient(90deg,{{ACCENT}},{{ACCENT_HI}});">&nbsp;</td></tr>

  <!-- Body -->
  <tr>
  <td class="bo-pad" style="padding:36px 40px 8px 40px;">
    <h1 class="bo-text" style="margin:0 0 14px 0;font-size:23px;line-height:1.3;font-weight:700;color:{{TEXT}};text-align:center;">{{TITLE}}</h1>
    <div class="bo-muted" style="font-size:15px;line-height:1.65;color:{{MUTED}};text-align:center;">{{INTRO}}</div>
  </td>
  </tr>

  <!-- CTA -->
  <tr>
  <td class="bo-pad" align="center" style="padding:28px 40px 6px 40px;">
    <table role="presentation" cellspacing="0" cellpadding="0" border="0" class="bo-btn"><tr>
    <td align="center" bgcolor="{{ACCENT}}" style="border-radius:12px;background-color:{{ACCENT}};background-image:linear-gradient(90deg,{{ACCENT}},{{ACCENT_HI}});box-shadow:0 6px 18px rgba(56,178,143,0.28);">
    <a href="{{CTA_URL}}" style="display:inline-block;padding:15px 40px;font-size:15px;font-weight:700;letter-spacing:0.01em;color:{{ON_ACCENT}};text-decoration:none;border-radius:12px;">{{CTA_LABEL}}</a>
    </td>
    </tr></table>
  </td>
  </tr>

  <!-- Fallback link -->
  <tr>
  <td class="bo-pad" style="padding:18px 40px 4px 40px;">
    <div class="bo-muted" style="font-size:12px;line-height:1.6;color:{{MUTED}};text-align:center;">O copia y pega este enlace en tu navegador:</div>
    <div style="font-size:12px;line-height:1.6;text-align:center;word-break:break-all;margin-top:6px;"><a class="bo-accent" href="{{CTA_URL}}" style="color:{{ACCENT}};text-decoration:underline;">{{CTA_URL}}</a></div>
  </td>
  </tr>

  <!-- Footer -->
  <tr>
  <td class="bo-pad" style="padding:26px 40px 32px 40px;">
    <hr style="border:none;border-top:1px solid {{BORDER}};margin:0 0 18px 0;">
    <div class="bo-muted" style="font-size:12px;line-height:1.6;color:{{MUTED}};text-align:center;">{{FOOTER}}</div>
    <div class="bo-muted" style="font-size:11px;line-height:1.6;color:{{MUTED}};text-align:center;margin-top:10px;">Enviado por {{BRAND}} &middot; Email automatico, por favor no respondas a este mensaje.</div>
  </td>
  </tr>

</table>

</td></tr>
</table>
</body>
</html>`

// renderBackofficeEmailShell builds the shared dark, app-branded HTML wrapper.
// introHTML is trusted, pre-escaped HTML for the body copy.
func renderBackofficeEmailShell(title, preheader, introHTML, ctaLabel, ctaURL, footerNote string) string {
	safeCTAURL := htmlEscape(ctaURL)
	r := strings.NewReplacer(
		"{{TITLE}}", htmlEscape(title),
		"{{PREHEADER}}", htmlEscape(preheader),
		"{{INTRO}}", introHTML,
		"{{CTA_LABEL}}", htmlEscape(ctaLabel),
		"{{CTA_URL}}", safeCTAURL,
		"{{FOOTER}}", htmlEscape(footerNote),
		"{{BRAND}}", htmlEscape(backofficeEmailBrand),
		"{{LOGO}}", htmlEscape(backofficeEmailLogoURL),
		"{{BG}}", boEmailBg,
		"{{SURFACE}}", boEmailSurface,
		"{{HEADER_BG}}", boEmailHeaderBg,
		"{{BORDER}}", boEmailBorder,
		"{{TEXT}}", boEmailText,
		"{{MUTED}}", boEmailMuted,
		"{{ACCENT}}", boEmailAccent,
		"{{ACCENT_HI}}", boEmailAccentHi,
		"{{ON_ACCENT}}", boEmailOnAccnt,
	)
	return r.Replace(backofficeEmailTemplate)
}

// buildBackofficeInvitationEmailHTML renders the member invitation email.
func buildBackofficeInvitationEmailHTML(restaurantName, invitationURL string) string {
	brand := htmlEscape(strings.TrimSpace(restaurantName))
	intro := "Te han invitado a acceder al backoffice de <strong class=\"bo-text\" style=\"color:" + boEmailText + ";\">" + brand + "</strong>. Completa tu alta para configurar tu acceso y empezar a gestionar el restaurante."
	return renderBackofficeEmailShell(
		"Invitacion de acceso",
		"Completa tu alta en "+backofficeEmailBrand,
		intro,
		"Completar mi alta",
		invitationURL,
		"Si no esperabas esta invitacion, puedes ignorar este mensaje de forma segura.",
	)
}

// buildBackofficeInvoiceEmailHTML renders the invoice email using the same
// shared, app-branded shell as the member invitation / password-reset emails,
// with an inline-styled invoice summary (rendered per the selected template)
// embedded as the body. The full-fidelity invoice is attached as a PDF.
// customMessage is optional free text (plain, will be escaped); when provided it
// is shown above the invoice summary.
func buildBackofficeInvoiceEmailHTML(data invoiceRenderData, invoiceURL, customMessage string) string {
	brand := strings.TrimSpace(data.Issuer.Name)
	customer := htmlEscape(strings.TrimSpace(data.Customer.Name))
	number := strings.TrimSpace(data.Number)
	title := "Factura"
	preheader := "Tu factura de " + brand
	if number != "" {
		title = "Factura " + number
		preheader = "Tu factura " + number + " de " + brand
	}

	strong := func(v string) string {
		return "<strong class=\"bo-text\" style=\"color:" + boEmailText + ";\">" + v + "</strong>"
	}

	var intro strings.Builder
	if strings.TrimSpace(customMessage) != "" {
		intro.WriteString("<div style=\"margin-bottom:18px;\">")
		intro.WriteString(strings.ReplaceAll(htmlEscape(strings.TrimSpace(customMessage)), "\n", "<br>"))
		intro.WriteString("</div>")
	} else {
		var factura string
		if number != "" {
			factura = "tu factura " + strong(htmlEscape(number))
		} else {
			factura = "tu factura"
		}
		intro.WriteString("<div style=\"margin-bottom:18px;\">Hola " + strong(customer) +
			", aqu\u00ed tienes " + factura + " emitida por " + strong(htmlEscape(brand)) + ".</div>")
	}

	// Render the per-template invoice summary fragment.
	if summary, err := renderInvoiceEmailFragment(data); err == nil {
		intro.WriteString(summary)
	}

	return renderBackofficeEmailShell(
		title,
		preheader,
		intro.String(),
		"Ver factura",
		invoiceURL,
		"Si tienes cualquier consulta sobre esta factura, contacta con "+brand+".",
	)
}

// buildBackofficePasswordResetEmailHTML renders the password-reset email.
func buildBackofficePasswordResetEmailHTML(restaurantName, resetURL string) string {
	brand := htmlEscape(strings.TrimSpace(restaurantName))
	intro := "Has solicitado restablecer tu contrasena de acceso al backoffice de <strong class=\"bo-text\" style=\"color:" + boEmailText + ";\">" + brand + "</strong>. Usa el boton para elegir una nueva contrasena."
	return renderBackofficeEmailShell(
		"Restablecer contrasena",
		"Restablece tu contrasena de "+backofficeEmailBrand,
		intro,
		"Restablecer contrasena",
		resetURL,
		"Si no solicitaste este cambio, puedes ignorar este mensaje; tu contrasena seguira siendo la misma.",
	)
}
