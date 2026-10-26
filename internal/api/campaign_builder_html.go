package api

import (
	"regexp"
	"strings"
)

// Coordination id: campaign_html_builder_v1
//
// The backoffice HTML builder compiles the campaign email itself (Tailwind
// classes inlined into style attributes plus responsive media queries) and
// stores it in campaigns.email_html. The only per-recipient piece is the
// opt-out link: the builder writes this placeholder as its href and the sender
// fills it here, so every recipient gets its own /baja-publicidad link.
const campaignBuilderUnsubscribePlaceholder = "{{unsubscribe_url}}"

var campaignBuilderBodyCloseRe = regexp.MustCompile(`(?i)</body\s*>`)

// renderCampaignBuilderEmail returns the email to deliver from a compiled
// builder document. When the design dropped the placeholder, the standard
// opt-out footer is appended so no campaign ever leaves without a way out.
func renderCampaignBuilderEmail(emailHTML, unsubscribeURL string, theme campaignTheme) string {
	if strings.Contains(emailHTML, campaignBuilderUnsubscribePlaceholder) {
		return strings.ReplaceAll(emailHTML, campaignBuilderUnsubscribePlaceholder, htmlEscape(strings.TrimSpace(unsubscribeURL)))
	}
	footer := campaignUnsubscribeFooterHTML(unsubscribeURL, theme)
	if footer == "" {
		return emailHTML
	}
	if loc := campaignBuilderBodyCloseRe.FindStringIndex(emailHTML); loc != nil {
		return emailHTML[:loc[0]] + footer + emailHTML[loc[0]:]
	}
	return emailHTML + footer
}
