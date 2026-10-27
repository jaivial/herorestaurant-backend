package api

import (
	"testing"
)

// Coordination id: wa_bot_group_mention_v1
//
// A photo sent to the bot in a group carries its mention INSIDE the media
// wrapper: the provider (evo-weai, crates/wa-client/src/fork.rs) writes the
// context of an imageMessage as imageMessage.contextInfo. The group gate
// reads the mention from there, so a photo addressed to the bot is served
// instead of being dropped as "not mentioned".
//
// The payload mirrors the one kraken received on 2026-10-27 19:26 in the
// privileged group 120363430262328476@g.us ("Bot Alqueria"): a captionless
// imageMessage whose context names the bot.
func TestEvo_ParseInbound_GroupImageMentionIsRecognised(t *testing.T) {
	body := []byte(`{"event":"messages.upsert","instance":"nv-1-1791470960846499754","data":{
		"key":{"remoteJid":"120363430262328476@g.us","id":"3A94B10C545367CCDC4E","fromMe":false,
			"participant":"242554100576489@lid","participantJidAlt":"34692747052@s.whatsapp.net"},
		"pushName":"Chef",
		"messageType":"imageMessage",
		"message":{"imageMessage":{"mimetype":"image/jpeg","caption":"",
			"contextInfo":{"mentionedJid":["34600111222@s.whatsapp.net"],"participant":"242554100576489@lid"}}}
	}}`)
	g := &evolutionGateway{ownJIDs: []string{"34600111222@s.whatsapp.net"}}
	in, ok := g.ParseInboundMessage(body)
	if !ok {
		t.Fatal("a photo addressed to the bot was dropped by the mention gate")
	}
	if !in.Mentioned {
		t.Error("a photo addressed to the bot was not marked as mentioned")
	}
	if in.ParticipantJID != "34692747052@s.whatsapp.net" {
		t.Errorf("participant=%q", in.ParticipantJID)
	}
}

// The caption of a photo is the text of the message: in a group it is the
// request ("mira esta carta"), and it is also what a mention written in the
// caption looks like.
func TestEvo_ParseInbound_GroupImageCaptionIsTheText(t *testing.T) {
	body := []byte(`{"event":"messages.upsert","instance":"nv-1-1791470960846499754","data":{
		"key":{"remoteJid":"120363430262328476@g.us","id":"3AF0574FDE71BA0F6042","fromMe":false,
			"participant":"242554100576489@lid","participantJidAlt":"34692747052@s.whatsapp.net"},
		"pushName":"Chef",
		"messageType":"imageMessage",
		"message":{"imageMessage":{"mimetype":"image/jpeg","caption":"@34600111222 mira esta carta",
			"contextInfo":{"mentionedJid":["34600111222@s.whatsapp.net"]}}}
	}}`)
	g := &evolutionGateway{ownJIDs: []string{"34600111222@s.whatsapp.net"}}
	in, ok := g.ParseInboundMessage(body)
	if !ok {
		t.Fatal("a captioned photo addressed to the bot was dropped by the mention gate")
	}
	if in.Text != "mira esta carta" {
		t.Errorf("text=%q, want the caption without the mention", in.Text)
	}
	if in.Caption != "@34600111222 mira esta carta" {
		t.Errorf("caption=%q", in.Caption)
	}
}

// A document carries its mention in documentMessage.contextInfo, like a
// photo does.
func TestEvo_ParseInbound_GroupDocumentMentionIsRecognised(t *testing.T) {
	body := []byte(`{"event":"messages.upsert","instance":"nv-1-1791470960846499754","data":{
		"key":{"remoteJid":"120363430262328476@g.us","id":"DOC1","fromMe":false,
			"participant":"242554100576489@lid","participantJidAlt":"34692747052@s.whatsapp.net"},
		"pushName":"Chef",
		"messageType":"documentMessage",
		"message":{"documentMessage":{"fileName":"carta.pdf","caption":"",
			"contextInfo":{"mentionedJid":["34600111222@s.whatsapp.net"]}}}
	}}`)
	g := &evolutionGateway{ownJIDs: []string{"34600111222@s.whatsapp.net"}}
	in, ok := g.ParseInboundMessage(body)
	if !ok {
		t.Fatal("a document addressed to the bot was dropped by the mention gate")
	}
	if !in.Mentioned {
		t.Error("a document addressed to the bot was not marked as mentioned")
	}
}

// A reply to a bot message addresses the bot without a mention chip: the
// quoted participant travels in the context of the media wrapper too.
func TestEvo_ParseInbound_GroupImageReplyToTheBotIsRecognised(t *testing.T) {
	body := []byte(`{"event":"messages.upsert","instance":"nv-1-1791470960846499754","data":{
		"key":{"remoteJid":"120363430262328476@g.us","id":"IMG2","fromMe":false,
			"participant":"242554100576489@lid","participantJidAlt":"34692747052@s.whatsapp.net"},
		"pushName":"Chef",
		"messageType":"imageMessage",
		"message":{"imageMessage":{"mimetype":"image/jpeg","caption":"","contextInfo":{"participant":"34600111222@s.whatsapp.net"}}}
	}}`)
	g := &evolutionGateway{ownJIDs: []string{"34600111222@s.whatsapp.net"}}
	in, ok := g.ParseInboundMessage(body)
	if !ok {
		t.Fatal("a photo replying to the bot was dropped by the mention gate")
	}
	if !in.Mentioned {
		t.Error("a photo replying to the bot was not marked as mentioned")
	}
}

// The text path is unchanged: the mention of a text message still comes from
// the context of the extended text message.
func TestEvo_ParseInbound_GroupTextMentionIsRecognised(t *testing.T) {
	body := []byte(`{"event":"messages.upsert","instance":"nv-1-1791470960846499754","data":{
		"key":{"remoteJid":"120363430262328476@g.us","id":"3EB06D336D95F1DBF69373","fromMe":false,
			"participant":"242554100576489@lid","participantJidAlt":"34692747052@s.whatsapp.net"},
		"pushName":"Chef",
		"messageType":"extendedTextMessage",
		"message":{"extendedTextMessage":{"text":"hola","contextInfo":{"mentionedJid":["34600111222@s.whatsapp.net"]}}}
	}}`)
	g := &evolutionGateway{ownJIDs: []string{"34600111222@s.whatsapp.net"}}
	in, ok := g.ParseInboundMessage(body)
	if !ok {
		t.Fatal("a text addressed to the bot was dropped by the mention gate")
	}
	if !in.Mentioned {
		t.Error("a text addressed to the bot was not marked as mentioned")
	}
}

// A photo that does NOT address the bot stays out: the gate still requires
// an explicit mention.
func TestEvo_ParseInbound_GroupImageWithoutMentionStaysOut(t *testing.T) {
	body := []byte(`{"event":"messages.upsert","instance":"nv-1-1791470960846499754","data":{
		"key":{"remoteJid":"120363430262328476@g.us","id":"IMG3","fromMe":false,
			"participant":"242554100576489@lid","participantJidAlt":"34692747052@s.whatsapp.net"},
		"pushName":"Chef",
		"messageType":"imageMessage",
		"message":{"imageMessage":{"mimetype":"image/jpeg","caption":"","contextInfo":{"mentionedJid":[]}}}
	}}`)
	g := &evolutionGateway{ownJIDs: []string{"34600111222@s.whatsapp.net"}}
	if _, ok := g.ParseInboundMessage(body); ok {
		t.Error("a photo that does not address the bot must not become a turn")
	}
}
