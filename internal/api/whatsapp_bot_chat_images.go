package api

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Coordination id: campaign_tools_v1 (images from a WhatsApp chat)
//
// An image sent to a privileged chat can be used by the tools (today:
// manage_campaign action "image", which uploads it to the restaurant's
// BunnyCDN for a campaign email). The bytes only ride the webhook of the
// message that carried them, while the request to use them often comes in a
// later message ("pon la foto que te acabo de mandar en la portada"). This
// registry remembers the recent images of each privileged chat: the provider
// message id (the bytes can always be fetched again from the provider by it)
// plus the inline payload while it is small enough to keep.
//
// Process memory on purpose: the provider keeps the media, so a restart only
// costs one provider fetch; nothing here is the source of truth.

const (
	botChatImagesPerChat   = 5
	botChatImageTTL        = 2 * time.Hour
	botChatImageKeepBytes  = 6 << 20  // inline payloads above this are re-fetched by id
	botChatImagesMaxChats  = 200      // bound on chats remembered at once
	botChatImagesMaxInline = 96 << 20 // bound on inline bytes held at once
)

type botChatImage struct {
	MessageID string
	Mime      string
	Data      []byte // nil when not kept inline (fetch by MessageID)
	At        time.Time
}

type botChatImageRegistry struct {
	mu     sync.Mutex
	chats  map[string][]botChatImage
	inline int
}

var botChatImages = &botChatImageRegistry{chats: map[string][]botChatImage{}}

func botChatImageKey(rid int, chatJID string) string {
	return strconv.Itoa(rid) + "|" + strings.TrimSpace(chatJID)
}

// prune drops expired entries and, over the bounds, the oldest chats.
func (r *botChatImageRegistry) prune(now time.Time) {
	for key, list := range r.chats {
		kept := list[:0]
		for _, img := range list {
			if now.Sub(img.At) < botChatImageTTL {
				kept = append(kept, img)
			} else {
				r.inline -= len(img.Data)
			}
		}
		if len(kept) == 0 {
			delete(r.chats, key)
		} else {
			r.chats[key] = kept
		}
	}
	for len(r.chats) > botChatImagesMaxChats || r.inline > botChatImagesMaxInline {
		oldestKey, oldest := "", now
		for key, list := range r.chats {
			if at := list[len(list)-1].At; oldestKey == "" || at.Before(oldest) {
				oldestKey, oldest = key, at
			}
		}
		if oldestKey == "" {
			return
		}
		for _, img := range r.chats[oldestKey] {
			r.inline -= len(img.Data)
		}
		delete(r.chats, oldestKey)
	}
}

func (r *botChatImageRegistry) remember(rid int, chatJID string, img botChatImage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := botChatImageKey(rid, chatJID)
	list := append(r.chats[key], img)
	if len(list) > botChatImagesPerChat {
		for _, old := range list[:len(list)-botChatImagesPerChat] {
			r.inline -= len(old.Data)
		}
		list = list[len(list)-botChatImagesPerChat:]
	}
	r.chats[key] = list
	r.inline += len(img.Data)
	r.prune(img.At)
}

// find returns the image of ref in the chat, or its newest image when ref is
// empty. ok=false when the chat has none (or not that one).
func (r *botChatImageRegistry) find(rid int, chatJID, ref string) (botChatImage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(time.Now())
	list := r.chats[botChatImageKey(rid, chatJID)]
	for i := len(list) - 1; i >= 0; i-- {
		if ref == "" || list[i].MessageID == ref {
			return list[i], true
		}
	}
	return botChatImage{}, false
}

// recent returns the images of the chat registered after `since`, newest
// first, capped at `max`.
//
// Coordination id: wa_bot_group_media_context_v1
//
// The request and its photos travel together ("crea una factura con estos
// datos" + the photo of the bill), and the mention is on the caption, not on
// every photo: the photo that carries the data can be a message the mention
// gate did not accept. The gate registers those photos here, so the turn that
// follows can read the data the request refers to.
func (r *botChatImageRegistry) recent(rid int, chatJID string, since time.Time, max int) []botChatImage {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(time.Now())
	if max <= 0 {
		return nil
	}
	list := r.chats[botChatImageKey(rid, chatJID)]
	out := make([]botChatImage, 0, max)
	for i := len(list) - 1; i >= 0 && len(out) < max; i-- {
		if list[i].At.Before(since) {
			continue
		}
		out = append(out, list[i])
	}
	return out
}

// botChatImageRef is the ref of the image a message carries ("" when none).
func botChatImageRef(msg botWebhookMessage) string {
	hasImage := strings.TrimSpace(msg.ImageB64) != "" || strings.Contains(strings.ToLower(msg.MediaKind), "imagen")
	if !hasImage || strings.TrimSpace(msg.ChatJID) == "" {
		return ""
	}
	return strings.TrimSpace(msg.MessageID)
}

// botRememberChatImage registers the image a privileged message carries and
// returns its ref ("" when the message has no image).
func botRememberChatImage(rid int, msg botWebhookMessage) string {
	if botChatImageRef(msg) == "" {
		return ""
	}
	img := botChatImage{MessageID: strings.TrimSpace(msg.MessageID), Mime: strings.TrimSpace(msg.ImageMime), At: time.Now()}
	if raw := strings.TrimSpace(msg.ImageB64); raw != "" && len(raw) <= botChatImageKeepBytes*4/3 {
		if data, err := base64.StdEncoding.DecodeString(raw); err == nil {
			img.Data = data
		}
	}
	botChatImages.remember(rid, msg.ChatJID, img)
	botCheckpoint("campaign_tools_v1", "restaurant_id=%d chat_image_remembered ref=%s inline=%t", rid, img.MessageID, img.Data != nil)
	return img.MessageID
}

// botChatImageSource returns the bytes of an image of this chat: ref = the
// provider message id, or "" for the newest one. The current message's inline
// payload, the registry and the provider are tried in that order.
func (s *Server) botChatImageSource(ctx context.Context, rid int, msg botWebhookMessage, ref string) (campaignImageSource, error) {
	ref = strings.TrimSpace(ref)
	if (ref == "" || ref == msg.MessageID) && strings.TrimSpace(msg.ImageB64) != "" {
		if data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(msg.ImageB64)); err == nil {
			return campaignCheckImage("imagen-whatsapp", data, msg.ImageMime)
		}
	}
	img, ok := botChatImages.find(rid, msg.ChatJID, ref)
	if !ok {
		if ref == "" {
			return campaignImageSource{}, errors.New("no hay ninguna imagen reciente en este chat: pide que la envíen por aquí")
		}
		img = botChatImage{MessageID: ref}
	}
	data := img.Data
	if data == nil {
		raw := s.botFetchMediaBase64(ctx, rid, img.MessageID)
		if raw == "" {
			return campaignImageSource{}, errors.New("no se ha podido recuperar la imagen del chat: pide que la vuelvan a enviar")
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
		if err != nil {
			return campaignImageSource{}, errors.New("la imagen del chat no se puede leer: pide que la vuelvan a enviar")
		}
		data = decoded
	}
	return campaignCheckImage("imagen-whatsapp", data, img.Mime)
}
