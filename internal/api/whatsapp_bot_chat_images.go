package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"log"
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
// The id, the time and the READING of each image are also written to the
// bot's SQLite (chat_images), so a restart keeps them: prod 2026-10-28 the
// deploy at 06:47 wiped the photo of the bill sent at 06:07, and every later
// invoice turn hunted for its lines in the till until it ran out of steps.
// The bytes are not stored: the provider serves them again by message id.

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
	// Reading is what was read from the image, once: a later turn that still
	// refers to it ("con los datos de la cuenta de antes") reuses it instead
	// of paying the vision call again. Coordination id: wa_bot_image_read_v1
	Reading *botImageReading
}

type botChatImageRegistry struct {
	mu     sync.Mutex
	chats  map[string][]botChatImage
	inline int
	db     *sql.DB // bot context SQLite; nil keeps the registry in memory only
}

var botChatImages = &botChatImageRegistry{chats: map[string][]botChatImage{}}

func botChatImageKey(rid int, chatJID string) string {
	return strconv.Itoa(rid) + "|" + strings.TrimSpace(chatJID)
}

// attach makes the registry durable in db: it creates the table, drops what
// is past the TTL and loads the rest (with their readings) into memory.
func (r *botChatImageRegistry) attach(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS chat_images (
            restaurant_id INTEGER NOT NULL,
            chat_jid TEXT NOT NULL,
            message_id TEXT NOT NULL,
            mime TEXT NOT NULL DEFAULT '',
            at_ms INTEGER NOT NULL,
            reading_source TEXT,
            reading_text TEXT,
            PRIMARY KEY (restaurant_id, chat_jid, message_id)
        )`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM chat_images WHERE at_ms < ?`, time.Now().Add(-botChatImageTTL).UnixMilli()); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT restaurant_id, chat_jid, message_id, mime, at_ms, reading_source, reading_text FROM chat_images ORDER BY at_ms`)
	if err != nil {
		return err
	}
	defer rows.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.db = db
	for rows.Next() {
		var rid int
		var chat string
		var img botChatImage
		var atMS int64
		var source, text sql.NullString
		if err := rows.Scan(&rid, &chat, &img.MessageID, &img.Mime, &atMS, &source, &text); err != nil {
			return err
		}
		img.At = time.UnixMilli(atMS)
		if source.Valid {
			img.Reading = &botImageReading{Source: source.String, Text: text.String}
		}
		r.add(botChatImageKey(rid, chat), img)
	}
	return rows.Err()
}

// persist runs one write on the durable copy. A failure only costs the copy
// (the image stays in memory and with the provider), so it is logged.
func (r *botChatImageRegistry) persist(query string, args ...any) {
	if r.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		log.Printf("[bot] chat_images persist error: %v", err)
	}
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
	if !r.add(botChatImageKey(rid, chatJID), img) {
		return
	}
	r.prune(img.At)
	r.persist(`INSERT OR IGNORE INTO chat_images (restaurant_id, chat_jid, message_id, mime, at_ms) VALUES (?,?,?,?,?)`,
		rid, strings.TrimSpace(chatJID), img.MessageID, img.Mime, img.At.UnixMilli())
	r.persist(`DELETE FROM chat_images WHERE at_ms < ?`, img.At.Add(-botChatImageTTL).UnixMilli())
}

// add appends img to the chat under the per-chat bound; false when the chat
// already has it.
func (r *botChatImageRegistry) add(key string, img botChatImage) bool {
	// The same message registered twice (a provider retry of the webhook)
	// stays one image: the second copy would push a real one out of the
	// per-chat bound and be read twice.
	if img.MessageID != "" {
		for _, old := range r.chats[key] {
			if old.MessageID == img.MessageID {
				return false
			}
		}
	}
	list := append(r.chats[key], img)
	if len(list) > botChatImagesPerChat {
		for _, old := range list[:len(list)-botChatImagesPerChat] {
			r.inline -= len(old.Data)
		}
		list = list[len(list)-botChatImagesPerChat:]
	}
	r.chats[key] = list
	r.inline += len(img.Data)
	return true
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

// storeReading keeps what was read from an image of the chat, so the turns
// that follow reuse it. A no-op when the image is no longer registered.
// Coordination id: wa_bot_image_read_v1
func (r *botChatImageRegistry) storeReading(rid int, chatJID, ref string, rd botImageReading) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.chats[botChatImageKey(rid, chatJID)]
	for i := range list {
		if list[i].MessageID == ref {
			cp := rd
			list[i].Reading = &cp
			r.persist(`UPDATE chat_images SET reading_source = ?, reading_text = ? WHERE restaurant_id = ? AND chat_jid = ? AND message_id = ?`,
				rd.Source, rd.Text, rid, strings.TrimSpace(chatJID), ref)
			return
		}
	}
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
