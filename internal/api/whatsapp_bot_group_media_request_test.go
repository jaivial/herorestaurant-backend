package api

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// The note of the turn that serves the request carries the data of the other
// photos of the chat: the model reads the bill, not only the photo the
// caption was written on.
//
// This is the behaviour the request depends on, and it is the one that was
// missing: before the fix the note only ever held the image of the message
// being served.
func TestBotImageTurnNote_CarriesTheOtherImagesOfTheChat(t *testing.T) {
	botChatImages.chats = map[string][]botChatImage{}
	botChatImages.inline = 0
	t.Setenv("PATH", "") // no OCR engine: the extractor answers with its note

	rid := 5
	chat := "chat-request@g.us"
	// The photo of the bill, sent without a mention: the gate registered it.
	botChatImages.remember(rid, chat, botChatImage{
		MessageID: "IMG-BILL",
		Mime:      "image/jpeg",
		Data:      []byte{0x89, 'P', 'N', 'G'},
		At:        time.Now(),
	})

	s := &Server{}
	msg := botWebhookMessage{
		MessageID: "IMG-CAPTION",
		ChatJID:   chat,
		MediaKind: "una imagen",
		ImageB64:  base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G'}),
		ImageMime: "image/jpeg",
	}
	note, ok := s.botImageTurnNote(context.Background(), rid, msg)
	if !ok {
		t.Fatal("the image of the message was not read")
	}
	if !strings.Contains(note, "image_ref=IMG-BILL") {
		t.Errorf("the note must carry the data of the other photo of the chat: %q", note)
	}
}
