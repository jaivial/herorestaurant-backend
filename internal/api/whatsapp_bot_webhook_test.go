package api

import (
	"testing"
)

func TestParseBotWebhookMessage_Basic(t *testing.T) {
	body := []byte(`{
		"EventType": "messages",
		"token": "inst-token-1",
		"owner": "34999888777",
		"message": {
			"chatid": "34612345678@s.whatsapp.net",
			"text": "hola, quiero reservar",
			"fromMe": false,
			"messageid": "MSG-1",
			"pushname": "Jaime",
			"messageTimestamp": 1700000000
		}
	}`)
	msg, ok := parseBotWebhookMessage(body)
	if !ok {
		t.Fatal("expected ok")
	}
	if msg.Sender != "34612345678" {
		t.Errorf("sender = %q", msg.Sender)
	}
	if msg.Text != "hola, quiero reservar" {
		t.Errorf("text = %q", msg.Text)
	}
	if msg.PushName != "Jaime" {
		t.Errorf("pushname = %q", msg.PushName)
	}
	if msg.MessageID != "MSG-1" {
		t.Errorf("messageid = %q", msg.MessageID)
	}
	if msg.InstanceToken != "inst-token-1" {
		t.Errorf("token = %q", msg.InstanceToken)
	}
	if msg.Owner != "34999888777" {
		t.Errorf("owner = %q", msg.Owner)
	}
	if msg.FromMe {
		t.Error("fromMe should be false")
	}
}

func TestParseBotWebhookMessage_FromMe(t *testing.T) {
	body := []byte(`{"message": {"chatid": "1@s.whatsapp.net", "text": "x", "fromMe": true}}`)
	msg, ok := parseBotWebhookMessage(body)
	if !ok {
		t.Fatal("expected ok")
	}
	if !msg.FromMe {
		t.Error("expected fromMe true")
	}
}

func TestParseBotWebhookMessage_NoMessage(t *testing.T) {
	if _, ok := parseBotWebhookMessage([]byte(`{"EventType": "connection"}`)); ok {
		t.Error("expected not ok when no message property")
	}
}

func TestParseBotWebhookMessage_ButtonResponse(t *testing.T) {
	body := []byte(`{
		"message": {
			"chatid": "34600000001@s.whatsapp.net",
			"text": "",
			"vote": "Confirmar",
			"messageType": "ButtonsResponseMessage"
		}
	}`)
	msg, ok := parseBotWebhookMessage(body)
	if !ok {
		t.Fatal("expected ok")
	}
	if msg.Text != "Confirmar" {
		t.Errorf("expected vote as text, got %q", msg.Text)
	}
}

func TestParseBotWebhookMessage_GroupIgnored(t *testing.T) {
	body := []byte(`{"message": {"chatid": "123456-789@g.us", "text": "hola"}}`)
	if _, ok := parseBotWebhookMessage(body); ok {
		t.Error("expected group chats to be ignored")
	}
}

func TestBotDedup(t *testing.T) {
	s := &Server{botSeen: map[string]int64{}}
	if s.botSeenBefore("34600000001", "M1") {
		t.Error("first time should not be seen")
	}
	if !s.botSeenBefore("34600000001", "M1") {
		t.Error("second time should be seen")
	}
	if s.botSeenBefore("34600000001", "M2") {
		t.Error("different message id should not be seen")
	}
}


func TestParseBotConnectionEvent_QREvent(t *testing.T) {
	body := []byte(`{"event":"qrcode","token":"inst-1","qrcode":"data:image/png;base64,AAAA","status":"connecting"}`)
	ev, ok := parseBotConnectionEvent(body)
	if !ok {
		t.Fatal("expected connection event")
	}
	if ev.InstanceToken != "inst-1" {
		t.Errorf("token = %q", ev.InstanceToken)
	}
	if ev.QR == "" {
		t.Errorf("expected qr payload")
	}
}

func TestParseBotConnectionEvent_ConnectedEvent(t *testing.T) {
	body := []byte(`{"EventType":"connection","token":"inst-2","status":"connected","phone":"34612345678"}`)
	ev, ok := parseBotConnectionEvent(body)
	if !ok {
		t.Fatal("expected connection event")
	}
	if normalizeUAZAPIConnectionStatus(ev.Status) != "connected" {
		t.Errorf("status = %q", ev.Status)
	}
	if ev.ConnectedPhone != "34612345678" {
		t.Errorf("phone = %q", ev.ConnectedPhone)
	}
}

func TestParseBotConnectionEvent_IgnoresMessages(t *testing.T) {
	body := []byte(`{"message":{"chatid":"34600000000@s.whatsapp.net","text":"hola"},"token":"inst-3"}`)
	if _, ok := parseBotConnectionEvent(body); ok {
		t.Fatal("message payload must not be treated as connection event")
	}
}

func TestParseBotConnectionEvent_RequiresIdentity(t *testing.T) {
	body := []byte(`{"event":"qrcode","qrcode":"x"}`)
	if _, ok := parseBotConnectionEvent(body); ok {
		t.Fatal("connection event without token/owner must be rejected")
	}
}
