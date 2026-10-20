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
			line := strings.TrimSpace(raw)
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
