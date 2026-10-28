package api

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Coordination id: wa_bot_image_read_v1

func resetChatImages(t *testing.T) {
	t.Helper()
	botChatImages.mu.Lock()
	botChatImages.chats = map[string][]botChatImage{}
	botChatImages.inline = 0
	botChatImages.db = nil
	botChatImages.mu.Unlock()
}

// stubVision replaces the vision call: read maps the bytes of an image to
// what the model reads from it; an image missing from the map is a read the
// model never finishes (hard handwriting, prod 2026-10-27). PATH is emptied so
// the tesseract fallback never runs and the outcome does not depend on the
// host. It returns the number of vision calls made.
func stubVision(t *testing.T, read map[string]string) *int32 {
	t.Helper()
	t.Setenv("PATH", "")
	var calls int32
	prev := botVisionRead
	botVisionRead = func(_ *Server, _ context.Context, _ int, _ string, data []byte) (string, error) {
		atomic.AddInt32(&calls, 1)
		if text, ok := read[string(data)]; ok {
			return text, nil
		}
		return "", errors.New("context deadline exceeded")
	}
	t.Cleanup(func() { botVisionRead = prev })
	return &calls
}

const botTestBillText = "4 MENU FIN DE SEMANA 30,00 120,00\nTOTAL 185,00"

// The 21:08 request: the photo of the bill sent WITHOUT the mention, then the
// photo of a handwritten note whose caption mentions the bot. The turn reads
// the bill (it is the data of the request) and says plainly that the note
// could not be read, instead of passing garbage on as text.
func TestBotImagesTurnNote_ReadsTheBillAndAdmitsTheUnreadableNote(t *testing.T) {
	resetChatImages(t)
	stubVision(t, map[string]string{"bill": botTestBillText})
	rid, chat := 1, "120363430262328476@g.us"

	botKeepGroupImageAsContext(rid, waInbound{
		MessageID: "IMG-BILL", ChatJID: chat, MediaKind: "una imagen", ImageMime: "image/jpeg",
		ImageB64: base64.StdEncoding.EncodeToString([]byte("bill")),
	})
	msg := botWebhookMessage{
		MessageID: "IMG-NOTE", ChatJID: chat, MediaKind: "una imagen", ImageMime: "image/jpeg",
		ImageB64: base64.StdEncoding.EncodeToString([]byte("note")), PrivilegedGroup: true,
	}
	botRememberChatImage(rid, msg)

	note, read := (&Server{}).botImagesTurnNote(context.Background(), rid, msg)
	if !read {
		t.Fatalf("the bill was not read: %q", note)
	}
	if !strings.Contains(note, "image_ref=IMG-BILL · transcripción]\n"+botTestBillText) {
		t.Errorf("the note must carry the transcribed bill with its ref: %q", note)
	}
	if !strings.Contains(note, "image_ref=IMG-NOTE: no se ha podido leer") {
		t.Errorf("the unreadable note must be reported as unread: %q", note)
	}
	if strings.Index(note, "IMG-NOTE") > strings.Index(note, "IMG-BILL") {
		t.Errorf("the image of the message goes first: %q", note)
	}
}

// The client's data is typed in a later message: that text turn still sees
// the bill, which only lives in the note (the transcript does not keep it).
func TestBotImagesTurnNote_TextTurnStillSeesTheBill(t *testing.T) {
	resetChatImages(t)
	stubVision(t, map[string]string{"bill": botTestBillText})
	rid, chat := 1, "120363430262328476@g.us"
	botKeepGroupImageAsContext(rid, waInbound{
		MessageID: "IMG-BILL", ChatJID: chat, ImageB64: base64.StdEncoding.EncodeToString([]byte("bill")),
	})

	msg := botWebhookMessage{MessageID: "TXT-1", ChatJID: chat, Text: "Ricart S.L.U, CIF B98942270", PrivilegedGroup: true}
	if !botTurnHasRequestImages(rid, msg) {
		t.Fatal("a text turn right after the bill must read the bill")
	}
	note, read := (&Server{}).botImagesTurnNote(context.Background(), rid, msg)
	if !read || !strings.Contains(note, botTestBillText) {
		t.Fatalf("the text turn lost the bill: read=%t note=%q", read, note)
	}
}

// Each image is read once: the turns that follow reuse the reading instead of
// paying (and waiting for) the vision call again — including the failed one,
// which would otherwise burn the read timeout on every turn.
func TestBotImagesTurnNote_ReadsEachImageOnce(t *testing.T) {
	resetChatImages(t)
	calls := stubVision(t, map[string]string{"bill": botTestBillText})
	rid, chat := 1, "chat-once@g.us"
	for ref, data := range map[string]string{"IMG-BILL": "bill", "IMG-NOTE": "note"} {
		botKeepGroupImageAsContext(rid, waInbound{MessageID: ref, ChatJID: chat, ImageB64: base64.StdEncoding.EncodeToString([]byte(data))})
	}
	msg := botWebhookMessage{MessageID: "TXT-1", ChatJID: chat, PrivilegedGroup: true}

	first, _ := (&Server{}).botImagesTurnNote(context.Background(), rid, msg)
	second, _ := (&Server{}).botImagesTurnNote(context.Background(), rid, msg)
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("vision calls=%d, want one per image", got)
	}
	if first != second {
		t.Errorf("the reused reading must give the same note:\n%q\n%q", first, second)
	}
}

// A restart (a deploy) keeps the photos of the chat and what was read from
// them: the turn that brings the client's data an hour later still has the
// bill, without reading it again. prod 2026-10-28: the bill of 06:07 was lost
// to the 06:47 deploy and no later turn could create the invoice.
func TestBotChatImages_ARestartKeepsThePhotosAndTheirReading(t *testing.T) {
	resetChatImages(t)
	store, err := newBotConversationStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := botChatImages.attach(store.DB()); err != nil {
		t.Fatal(err)
	}
	calls := stubVision(t, map[string]string{"bill": botTestBillText})
	rid, chat := 1, "chat-restart@g.us"
	botChatImages.remember(rid, chat, botChatImage{MessageID: "IMG-BILL", Data: []byte("bill"), At: time.Now().Add(-time.Hour)})
	msg := botWebhookMessage{MessageID: "TXT-1", ChatJID: chat, PrivilegedGroup: true}
	if _, read := (&Server{}).botImagesTurnNote(context.Background(), rid, msg); !read {
		t.Fatal("the bill was not read before the restart")
	}

	// The restart: a fresh process memory over the same SQLite.
	resetChatImages(t)
	if err := botChatImages.attach(store.DB()); err != nil {
		t.Fatal(err)
	}
	note, read := (&Server{}).botImagesTurnNote(context.Background(), rid, botWebhookMessage{MessageID: "TXT-2", ChatJID: chat, PrivilegedGroup: true})
	if !read || !strings.Contains(note, "185,00") || !strings.Contains(note, "image_ref=IMG-BILL") {
		t.Fatalf("after the restart the turn must still carry the bill, got %q", note)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("vision calls=%d, want the bill read once across the restart", got)
	}
}

// An image older than the request window is not part of the request.
func TestBotRequestImages_LeavesOldImagesAlone(t *testing.T) {
	resetChatImages(t)
	rid, chat := 1, "chat-window@g.us"
	botChatImages.remember(rid, chat, botChatImage{MessageID: "OLD", Data: []byte("x"), At: time.Now().Add(-botChatImageRequestWindow - time.Minute)})
	msg := botWebhookMessage{MessageID: "TXT-1", ChatJID: chat}
	if botTurnHasRequestImages(rid, msg) {
		t.Fatal("an image older than the window must not be read into the turn")
	}
	botChatImages.remember(rid, chat, botChatImage{MessageID: "NEW", Data: []byte("y"), At: time.Now()})
	if got := botRequestImages(rid, msg); len(got) != 1 || got[0].MessageID != "NEW" {
		t.Fatalf("request images=%+v, want only the recent one", got)
	}
}

// A provider retry of the same webhook must not register the photo twice:
// the copy would push a real photo out of the per-chat bound.
func TestBotChatImages_SameMessageIsOneImage(t *testing.T) {
	resetChatImages(t)
	rid, chat := 1, "chat-dup@g.us"
	for range 3 {
		botChatImages.remember(rid, chat, botChatImage{MessageID: "IMG-1", Data: []byte("x"), At: time.Now()})
	}
	if got := botChatImages.recent(rid, chat, time.Now().Add(-time.Hour), 10); len(got) != 1 {
		t.Fatalf("registered %d copies of one message", len(got))
	}
}
