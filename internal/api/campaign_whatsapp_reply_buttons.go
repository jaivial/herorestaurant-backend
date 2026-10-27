package api

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// Coordination id: campaign_whatsapp_reply_buttons_v1
//
// WhatsApp on Evolution cannot mix URL and reply buttons in one message
// ("Reply buttons cannot be mixed with other button types") and takes at most
// two URL buttons, so a campaign used to arrive with two link buttons plus its
// opt-out as a raw link line in the text. A campaign that declares its own
// buttons now goes out, on Evolution, as ONE message (image, text and up to
// three reply buttons): its two main links, picked like campaignPickCTAs, and
// "Darme de baja". No link is left in the text.
//
// A tap comes back to the webhook with the button id and is answered here,
// before the bot: a link button gets its link as a one-tap URL button, the
// opt-out button puts the contact on campaign_suppressions with the same rule
// as the bot's unsubscribe_marketing tool. Other gateways keep the URL buttons.

const (
	campaignReplyButtonPrefix = "vcc:"
	campaignReplyOptOutAction = "baja"
	// campaignOptOutReplyCopy is the opt-out button. Same copy as
	// CAMPAIGN_WHATSAPP_OPTOUT_REPLY_COPY in the backoffice preview.
	campaignOptOutReplyCopy = "Darme de baja"
	// campaignButtonMaxLabel: WhatsApp truncates button labels past 20 characters.
	campaignButtonMaxLabel = 20
)

// campaignReplyButtonID is the id of one campaign reply button: "vcc:<campaign>:<action>",
// where action is the index of the link among the picked buttons or "baja".
func campaignReplyButtonID(campaignID int64, action string) string {
	return fmt.Sprintf("%s%d:%s", campaignReplyButtonPrefix, campaignID, action)
}

func parseCampaignReplyButtonID(id string) (campaignID int64, action string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(id), campaignReplyButtonPrefix)
	if !found {
		return 0, "", false
	}
	idPart, action, found := strings.Cut(rest, ":")
	n, err := strconv.ParseInt(idPart, 10, 64)
	if !found || err != nil || n <= 0 || strings.TrimSpace(action) == "" {
		return 0, "", false
	}
	return n, strings.TrimSpace(action), true
}

// campaignButtonLabel is a label as WhatsApp shows it on a button: at most
// campaignButtonMaxLabel characters, cut with an ellipsis.
func campaignButtonLabel(label string) string {
	r := []rune(strings.TrimSpace(label))
	if len(r) <= campaignButtonMaxLabel {
		return string(r)
	}
	return strings.TrimSpace(string(r[:campaignButtonMaxLabel-1])) + "…"
}

// campaignReplyChoices turns the picked link buttons ("label|url") into reply
// choices ("label|vcc:<campaign>:<i>") and closes them with the opt-out one.
func campaignReplyChoices(campaignID int64, cta []string) []string {
	out := make([]string, 0, len(cta)+1)
	for i, b := range cta {
		label, _, _ := strings.Cut(b, "|")
		out = append(out, campaignButtonLabel(label)+"|"+campaignReplyButtonID(campaignID, strconv.Itoa(i)))
	}
	return append(out, campaignOptOutReplyCopy+"|"+campaignReplyButtonID(campaignID, campaignReplyOptOutAction))
}

// campaignSendReplyButtons sends the unified campaign message with reply
// buttons. Reports whether it went out; the caller keeps its URL-button path
// as the fallback.
func (s *Server) campaignSendReplyButtons(ctx context.Context, gw WhatsAppGateway, coordID string, campaignID int64, num, text, image string, cta []string) bool {
	if _, isEvolution := gw.(*evolutionGateway); !isEvolution || campaignID <= 0 {
		return false
	}
	choices := campaignReplyChoices(campaignID, cta)
	var err error
	if sender, ok := gw.(whatsappMenuImageSender); ok && image != "" {
		err = sender.SendMenuImage(ctx, num, text, image, choices)
	} else {
		err = gw.SendMenu(ctx, num, text, choices)
	}
	if err != nil {
		slog.Default().Warn("campaign.whatsapp.reply_buttons_failed", "coord_id", coordID, "campaign_id", campaignID, "err", err.Error())
		return false
	}
	slog.Default().Info("campaign.whatsapp.reply_buttons", "coord_id", coordID, "campaign_id", campaignID, "buttons", len(choices), "image", image != "")
	return true
}

// campaignHandleReplyButton answers a tapped campaign button. False when the
// message is not a campaign button (or its link no longer exists), so it
// follows the normal bot path and the label is answered as a typed message.
func (s *Server) campaignHandleReplyButton(ctx context.Context, restaurantID int, in waInbound) bool {
	campaignID, action, ok := parseCampaignReplyButtonID(in.ButtonID)
	if !ok {
		return false
	}
	// The webhook's gateway only parses; replies go through the tenant's own.
	gw, gwOK := s.botGatewayFor(ctx, restaurantID)
	if !gwOK || gw == nil {
		return false
	}
	num := normalizeWhatsAppNumber(in.Sender)
	if num == "" {
		return false
	}
	// Resolve the link first: a button that no longer maps to a link falls
	// back to the bot untouched (and not marked as seen, or the bot would drop it).
	label, url := "", ""
	if action != campaignReplyOptOutAction {
		idx, err := strconv.Atoi(action)
		if err != nil || idx < 0 {
			return false
		}
		c, err := s.loadBOCampaign(ctx, restaurantID, campaignID)
		if err != nil {
			return false
		}
		_, _, buttons := campaignUnifiedWhatsApp(c.BodyMarkdown, "")
		cta, _ := campaignPickCTAs(buttons)
		if idx >= len(cta) {
			return false
		}
		label, url, _ = strings.Cut(cta[idx], "|")
		label, url = firstNonEmpty(strings.TrimSpace(label), "Abrir enlace"), strings.TrimSpace(url)
	}
	if s.botSeenBefore(in.Sender, in.MessageID) {
		return true // the provider retried the webhook: already answered
	}
	s.botRecordConversationMessage(ctx, restaurantID, num, "user", firstNonEmpty(strings.TrimSpace(in.Text), label, campaignOptOutReplyCopy), "", "campaign_button")

	if action == campaignReplyOptOutAction {
		branding, _ := s.loadRestaurantBranding(ctx, restaurantID)
		from := ""
		if brand := strings.TrimSpace(branding.BrandName); brand != "" {
			from = " de " + brand
		}
		text := "Hecho: no volverás a recibir más promociones" + from + ". Las confirmaciones y recordatorios de tus reservas te seguirán llegando."
		stored := s.suppressMarketingContact(ctx, restaurantID, in.Sender, "whatsapp_button")
		if stored == 0 {
			text = "No hemos podido registrar la baja en este momento. Escríbenos «darme de baja» y lo hacemos nosotros."
		}
		if err := s.sendWhatsAppTextTracked(ctx, restaurantID, gw, num, text, "campaign_optout"); err != nil {
			slog.Default().Warn("campaign.whatsapp.optout_reply_failed", "campaign_id", campaignID, "restaurant_id", restaurantID, "err", err.Error())
		}
		slog.Default().Info("campaign.whatsapp.optout", "campaign_id", campaignID, "restaurant_id", restaurantID, "stored", stored)
		return true
	}

	body := label + "\nToca el botón para abrirlo."
	if err := s.sendWhatsAppMenuTracked(ctx, restaurantID, gw, num, body, []string{campaignButtonLabel(label) + "|" + url}, "campaign_button"); err != nil {
		_ = s.sendWhatsAppTextTracked(ctx, restaurantID, gw, num, label+": "+url, "campaign_button")
	}
	slog.Default().Info("campaign.whatsapp.button_link", "campaign_id", campaignID, "restaurant_id", restaurantID, "action", action)
	return true
}
