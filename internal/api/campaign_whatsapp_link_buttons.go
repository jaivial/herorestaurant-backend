package api

import (
	"context"
	"log/slog"
	"strings"
)

// Coordination id: campaign_whatsapp_link_buttons_v1
//
// evo-weai (>= 0.2.11) delivers an interactive message with more than two URL
// buttons (WEAI_MAX_CTA_BUTTONS, default 10). A campaign that declares its own
// buttons goes out there as ONE message: image, text, up to
// campaignWAMaxLinkButtons link buttons (picked like campaignPickCTAs) and
// "Darme de baja" as a link to the recipient's opt-out page, so every button
// opens its link with one tap. A gateway that refuses the set (stock Evolution
// caps URL buttons at 2) falls back to the reply buttons of
// campaign_whatsapp_reply_buttons_v1.

// campaignWAMaxLinkButtons is how many campaign links become buttons, besides
// the opt-out one.
const campaignWAMaxLinkButtons = 3

// campaignLinkChoices turns the campaign links and the opt-out choice
// ("label|url") into URL button choices, opt-out last.
func campaignLinkChoices(buttons, optOut []string) []string {
	links, _ := campaignPickButtons(buttons, campaignWAMaxLinkButtons)
	out := make([]string, 0, len(links)+1)
	for _, b := range links {
		label, url, _ := strings.Cut(b, "|")
		out = append(out, campaignButtonLabel(label)+"|"+strings.TrimSpace(url))
	}
	for _, b := range optOut {
		if _, url, _ := strings.Cut(b, "|"); strings.TrimSpace(url) != "" {
			out = append(out, campaignOptOutReplyCopy+"|"+strings.TrimSpace(url))
		}
	}
	return out
}

// campaignSendLinkButtons sends the unified campaign message with URL buttons
// only. Reports whether it went out; the caller keeps the reply-button path as
// the fallback.
func (s *Server) campaignSendLinkButtons(ctx context.Context, gw WhatsAppGateway, coordID string, campaignID int64, num, text, image string, buttons, optOut []string) bool {
	if _, isEvolution := gw.(*evolutionGateway); !isEvolution || len(buttons) == 0 {
		return false
	}
	choices := campaignLinkChoices(buttons, optOut)
	var err error
	if sender, ok := gw.(whatsappMenuImageSender); ok && image != "" {
		err = sender.SendMenuImage(ctx, num, text, image, choices)
	} else {
		err = gw.SendMenu(ctx, num, text, choices)
	}
	if err != nil {
		slog.Default().Warn("campaign.whatsapp.link_buttons_failed", "coord_id", coordID, "campaign_id", campaignID, "err", err.Error())
		return false
	}
	slog.Default().Info("campaign.whatsapp.link_buttons", "coord_id", coordID, "campaign_id", campaignID, "buttons", len(choices), "image", image != "")
	return true
}
