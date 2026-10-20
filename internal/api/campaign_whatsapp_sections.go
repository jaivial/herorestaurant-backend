package api

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// campaignButtonLineRe matches a markdown line that is only a link (optionally
// prefixed by an emoji and/or wrapped in bold): the way the editor expresses a
// call-to-action button.
var campaignButtonLineRe = regexp.MustCompile(`^[^\w\[]*\[([^\]]+)\]\((https?://[^)\s]+)\)[^\w]*$`)

// campaignWASection is one WhatsApp message group of a campaign: real images,
// the text caption and one call-to-action button per standalone link line.
type campaignWASection struct {
	Images  []string
	Text    string
	Buttons []string // "label|url"
	Title   string
}

// splitCampaignWASections splits the markdown at its headings. Images become
// media messages and standalone link lines become buttons. ok is false when the
// campaign has no standalone link line, so legacy campaigns keep the old path.
func splitCampaignWASections(markdown string) (sections []campaignWASection, ok bool) {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	var cur []string
	flush := func() {
		sec := campaignWASection{}
		var text []string
		for _, raw := range cur {
			line := strings.TrimSpace(mdIconRe.ReplaceAllString(raw, ""))
			if m := campaignButtonLineRe.FindStringSubmatch(line); m != nil {
				sec.Buttons = append(sec.Buttons, strings.TrimSpace(m[1])+"|"+m[2])
				ok = true
				continue
			}
			if im := mdImageRe.FindStringSubmatch(line); im != nil && strings.HasPrefix(im[2], "https://") && strings.TrimSpace(mdImageRe.ReplaceAllString(line, "")) == "" {
				sec.Images = append(sec.Images, im[2])
				continue
			}
			text = append(text, raw)
		}
		sec.Text = strings.TrimSpace(strings.Join(text, "\n"))
		for _, t := range text {
			t = strings.TrimSpace(t)
			if strings.HasPrefix(t, "#") {
				sec.Title = strings.TrimSpace(strings.TrimLeft(t, "# "))
				break
			}
		}
		if sec.Text != "" || len(sec.Images) > 0 || len(sec.Buttons) > 0 {
			sections = append(sections, sec)
		}
		cur = nil
	}
	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if (strings.HasPrefix(t, "## ") || strings.HasPrefix(t, "# ")) && len(cur) > 0 {
			flush()
		}
		cur = append(cur, raw)
	}
	flush()
	return sections, ok
}

// deliverCampaignWhatsAppSections sends each section as real image(s) plus a
// button message with one button per link, then the website / opt-out buttons.
func (s *Server) deliverCampaignWhatsAppSections(ctx context.Context, restaurantID int, gw WhatsAppGateway, coordID, num, brandName string, sections []campaignWASection, finalChoices []string) error {
	pause := func() {
		select {
		case <-ctx.Done():
		case <-time.After(900 * time.Millisecond):
		}
	}
	textOf := func(sec campaignWASection) string {
		t := renderCampaignWhatsAppBody(sec.Text, "", "", false)
		return strings.TrimSpace(t)
	}
	for i, sec := range sections {
		if i > 0 {
			pause()
		}
		text := textOf(sec)
		if i == 0 && strings.TrimSpace(brandName) != "" {
			text = strings.TrimSpace("*" + strings.TrimSpace(brandName) + "*\n\n" + text)
		}
		sentCaption := false
		for j, img := range sec.Images {
			caption := ""
			if j == len(sec.Images)-1 && len(sec.Buttons) == 0 {
				caption = text
				sentCaption = true
			}
			if err := gw.SendMedia(ctx, normalizeWhatsAppNumber(num), waMedia{Kind: "image", URL: img, Caption: caption, Filename: "campana.webp"}); err != nil {
				if err2 := s.sendCampaignWhatsAppText(ctx, restaurantID, gw, coordID, num, strings.TrimSpace(caption+"\n\n"+img)); err2 != nil {
					return err2
				}
				if caption != "" {
					sentCaption = true
				}
			}
		}
		if len(sec.Buttons) > 0 {
			body := text
			if len(sec.Images) > 0 {
				// Caption cannot carry buttons: the text goes with the buttons.
				body = text
			}
			if body == "" {
				body = firstNonEmpty(sec.Title, strings.TrimSpace(brandName), "Más información")
			}
			if err := gw.SendMenu(ctx, num, body, sec.Buttons); err != nil {
				fallback := body
				for _, b := range sec.Buttons {
					label, url, _ := strings.Cut(b, "|")
					fallback += "\n\n👉 " + label + ": " + url
				}
				if err2 := s.sendCampaignWhatsAppText(ctx, restaurantID, gw, coordID, num, fallback); err2 != nil {
					return err2
				}
			}
			continue
		}
		if !sentCaption && text != "" {
			if err := s.sendCampaignWhatsAppText(ctx, restaurantID, gw, coordID, num, text); err != nil {
				return err
			}
		}
	}
	if len(finalChoices) > 0 {
		pause()
		body := firstNonEmpty(strings.TrimSpace(brandName), "Más información")
		if err := gw.SendMenu(ctx, num, body, finalChoices); err != nil {
			fallback := body
			for _, b := range finalChoices {
				label, url, _ := strings.Cut(b, "|")
				fallback += "\n\n" + label + ": " + url
			}
			return s.sendCampaignWhatsAppText(ctx, restaurantID, gw, coordID, num, fallback)
		}
	}
	return nil
}

// whatsappMenuImageSender is implemented by gateways whose interactive message
// can carry a header image, so image, text and buttons travel as ONE message.
type whatsappMenuImageSender interface {
	SendMenuImage(ctx context.Context, to, text, imageURL string, choices []string) error
}

// campaignUnifiedWhatsApp builds the single-message form of a campaign: the
// text without button lines, the first image and every button in order.
func campaignUnifiedWhatsApp(markdown, brandName string) (text, image string, buttons []string) {
	sections, _ := splitCampaignWASections(markdown)
	var parts []string
	for _, sec := range sections {
		if image == "" && len(sec.Images) > 0 {
			image = sec.Images[0]
		}
		buttons = append(buttons, sec.Buttons...)
		if t := strings.TrimSpace(renderCampaignWhatsAppBody(sec.Text, "", "", false)); t != "" {
			parts = append(parts, t)
		}
	}
	text = strings.Join(parts, "\n\n")
	if b := strings.TrimSpace(brandName); b != "" {
		text = "*" + b + "*\n" + text
	}
	return strings.TrimSpace(text), image, buttons
}

// campaignWAMaxCTA is the real provider limit: Evolution rejects an interactive
// message with more than 2 URL buttons ("at most 2 CTA buttons are allowed").
const campaignWAMaxCTA = 2

// campaignPickCTAs returns the buttons that get a real interactive button (the
// pre-booking link first, then menu links, then the rest in order) and the
// remaining ones, which travel as "label: url" lines in the same message.
func campaignPickCTAs(buttons []string) (cta, rest []string) {
	rank := func(b string) int {
		_, u, _ := strings.Cut(b, "|")
		switch {
		case strings.Contains(u, "date="):
			return 0
		case strings.Contains(u, "/menu/"):
			return 1
		}
		return 2
	}
	picked := map[int]bool{}
	for r := 0; r <= 2 && len(cta) < campaignWAMaxCTA; r++ {
		for i, b := range buttons {
			if len(cta) < campaignWAMaxCTA && !picked[i] && rank(b) == r {
				cta = append(cta, b)
				picked[i] = true
			}
		}
	}
	for i, b := range buttons {
		if !picked[i] {
			rest = append(rest, b)
		}
	}
	return cta, rest
}

// deliverCampaignWhatsAppUnified sends ONE interactive message holding the whole
// campaign: header image, text, up to 2 CTA buttons and the remaining links as
// lines (the opt-out link included).
func (s *Server) deliverCampaignWhatsAppUnified(ctx context.Context, restaurantID int, gw WhatsAppGateway, coordID, num, brandName, markdown string, extraChoices []string) error {
	text, image, buttons := campaignUnifiedWhatsApp(markdown, brandName)
	cta, rest := campaignPickCTAs(buttons)
	rest = append(rest, extraChoices...)
	for _, b := range rest {
		label, url, _ := strings.Cut(b, "|")
		text += "\n\n" + strings.TrimSpace(label) + ": " + strings.TrimSpace(url)
	}
	if len(cta) > 0 {
		if sender, ok := gw.(whatsappMenuImageSender); ok && image != "" {
			if err := sender.SendMenuImage(ctx, num, text, image, cta); err == nil {
				return nil
			}
		}
		if err := gw.SendMenu(ctx, num, text, cta); err == nil {
			return nil
		}
	}
	plain := text
	for _, b := range cta {
		label, url, _ := strings.Cut(b, "|")
		plain += "\n\n" + strings.TrimSpace(label) + ": " + strings.TrimSpace(url)
	}
	if image != "" {
		if err := gw.SendMedia(ctx, normalizeWhatsAppNumber(num), waMedia{Kind: "image", URL: image, Caption: plain, Filename: "campana.webp"}); err == nil {
			return nil
		}
	}
	return s.sendCampaignWhatsAppText(ctx, restaurantID, gw, coordID, num, plain)
}
