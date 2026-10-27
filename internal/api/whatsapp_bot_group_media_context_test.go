package api

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"preactvillacarmen/internal/config"
)

// Coordination id: wa_bot_group_media_context_v1
//
// A request in the privileged group travels with its photos ("crea una
// factura con estos datos" + the photo of the bill), and the mention rides
// on the caption, not on every photo. The photo that carries the data is a
// message the mention gate does not accept, so it is registered in the chat
// registry and the turn that follows reads it.

// A photo of a privileged group without a mention is kept as the context of
// the request that follows: it is registered in the chat registry with its
// ref, so the turn can read it.
func TestBotKeepGroupImageAsContext_RegistersTheImage(t *testing.T) {
	botChatImages.chats = map[string][]botChatImage{}
	botChatImages.inline = 0

	rid := 1
	chat := "120363430262328476@g.us"
	payload := base64.StdEncoding.EncodeToString([]byte("bytes-of-the-bill"))
	ref := botKeepGroupImageAsContext(rid, waInbound{
		MessageID: "IMG-BILL",
		ImageB64:  payload,
		ImageMime: "image/jpeg",
		ChatJID:   chat,
		MediaKind: "una imagen",
	})
	if ref != "IMG-BILL" {
		t.Fatalf("ref=%q, want the provider message id", ref)
	}
	img, ok := botChatImages.find(rid, chat, "")
	if !ok {
		t.Fatal("the image of the unmentioned photo was not registered")
	}
	if img.MessageID != "IMG-BILL" || len(img.Data) == 0 {
		t.Fatalf("registered image=%+v", img)
	}
}

// A message with no image registers nothing: a caption is not a photo.
func TestBotKeepGroupImageAsContext_NoImage(t *testing.T) {
	botChatImages.chats = map[string][]botChatImage{}
	botChatImages.inline = 0

	if ref := botKeepGroupImageAsContext(1, waInbound{MessageID: "TXT-1", ChatJID: "g@g.us", MediaKind: "una imagen"}); ref != "" {
		t.Fatalf("ref=%q, want no image registered", ref)
	}
	if len(botChatImages.chats) != 0 {
		t.Fatalf("the registry must stay empty: %+v", botChatImages.chats)
	}
}

// The recent images of a chat are the ones registered after `since`, newest
// first, and the cap holds.
func TestBotChatImages_RecentSelection(t *testing.T) {
	botChatImages.chats = map[string][]botChatImage{}
	botChatImages.inline = 0

	rid := 7
	chat := "chat-recent@g.us"
	old := botChatImage{MessageID: "OLD", At: time.Now().Add(-30 * time.Minute)}
	mid := botChatImage{MessageID: "MID", At: time.Now().Add(-2 * time.Minute)}
	newest := botChatImage{MessageID: "NEW", At: time.Now()}
	botChatImages.remember(rid, chat, old)
	botChatImages.remember(rid, chat, mid)
	botChatImages.remember(rid, chat, newest)

	got := botChatImages.recent(rid, chat, time.Now().Add(-5*time.Minute), 5)
	if len(got) != 2 {
		t.Fatalf("recent=%d, want the 2 images inside the window: %+v", len(got), got)
	}
	if got[0].MessageID != "NEW" || got[1].MessageID != "MID" {
		t.Errorf("recent order=%s,%s want NEW,MID", got[0].MessageID, got[1].MessageID)
	}
	if capped := botChatImages.recent(rid, chat, time.Now().Add(-5*time.Minute), 1); len(capped) != 1 || capped[0].MessageID != "NEW" {
		t.Errorf("capped=%+v", capped)
	}
	// Another chat never sees the images of this one.
	if other := botChatImages.recent(rid, "other@g.us", time.Now().Add(-time.Hour), 5); len(other) != 0 {
		t.Errorf("other chat saw %+v", other)
	}
}

// The turn note reads the other recent images of the chat: the model gets the
// data of the photo the request refers to, labelled with its ref so the tools
// can use its bytes.
func TestBotChatImagesTurnNote_ReadsTheOtherImagesOfTheChat(t *testing.T) {
	botChatImages.chats = map[string][]botChatImage{}
	botChatImages.inline = 0

	// No OCR engine in PATH: the extractor answers with its note instead of
	// running tesseract, which is enough to prove the image was picked up
	// from the registry and read into the turn.
	t.Setenv("PATH", "")

	rid := 3
	chat := "chat-note@g.us"
	botChatImages.remember(rid, chat, botChatImage{
		MessageID: "IMG-BILL",
		Mime:      "image/jpeg",
		Data:      []byte{0x89, 'P', 'N', 'G'},
		At:        time.Now(),
	})

	s := &Server{}
	msg := botWebhookMessage{MessageID: "IMG-CAPTION", ChatJID: chat, MediaKind: "una imagen"}
	note, read := s.botChatImagesTurnNote(context.Background(), rid, msg)
	if !read {
		t.Fatal("the image of the chat was not read into the note")
	}
	if !strings.Contains(note, "image_ref=IMG-BILL") {
		t.Errorf("note must carry the ref of the other image: %q", note)
	}
	if strings.Contains(note, "IMG-CAPTION") {
		t.Errorf("the note must not repeat the image of the message itself: %q", note)
	}
}

// The image of the message being served is not repeated as an extra one.
func TestBotChatImagesTurnNote_SkipsTheImageOfTheMessage(t *testing.T) {
	botChatImages.chats = map[string][]botChatImage{}
	botChatImages.inline = 0
	t.Setenv("PATH", "")

	rid := 4
	chat := "chat-same@g.us"
	botChatImages.remember(rid, chat, botChatImage{
		MessageID: "IMG-SAME",
		Mime:      "image/jpeg",
		Data:      []byte{0x89, 'P', 'N', 'G'},
		At:        time.Now(),
	})

	s := &Server{}
	note, read := s.botChatImagesTurnNote(context.Background(), rid, botWebhookMessage{MessageID: "IMG-SAME", ChatJID: chat})
	if read {
		t.Errorf("the image of the message must not be read twice: %q", note)
	}
}

// Coordination id: wa_bot_privileged_turn_budget_v1
//
// A privileged turn is a staff job: one model call may legitimately take
// longer than the fixed per-call cap, and cutting it killed the turn with
// budget left (prod 2026-10-27: the invoice turn was cut at 45s with 134s
// of the 3-minute turn left). A customer turn keeps the cap.
func TestBotCallTimeout_PrivilegedTurnUsesTheRemainingBudget(t *testing.T) {
	s := &Server{cfg: config.Config{BotTimeout: 45 * time.Second}}

	customer, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if got := s.botCallTimeout(customer); got != 45*time.Second {
		t.Errorf("customer turn timeout=%s, want the fixed cap", got)
	}

	privileged, cancel2 := context.WithTimeout(withBotTurnBudget(context.Background()), 3*time.Minute)
	defer cancel2()
	got := s.botCallTimeout(privileged)
	if got <= 45*time.Second {
		t.Errorf("privileged turn timeout=%s, want the remaining budget", got)
	}
	if got > 3*time.Minute {
		t.Errorf("privileged turn timeout=%s, more than the turn deadline", got)
	}
}

// A call with no turn deadline of its own keeps the fixed cap.
func TestBotCallTimeout_NoDeadlineKeepsTheCap(t *testing.T) {
	s := &Server{cfg: config.Config{BotTimeout: 45 * time.Second}}
	ctx := withBotTurnBudget(context.Background())
	if got := s.botCallTimeout(ctx); got != 45*time.Second {
		t.Errorf("timeout=%s, want the fixed cap", got)
	}
}
