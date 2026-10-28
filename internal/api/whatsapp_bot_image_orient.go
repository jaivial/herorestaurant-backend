package api

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"math/rand/v2"
	"strings"
	"time"
	"unicode"
)

// Coordination id: wa_bot_image_read_v1 (orientation)
//
// A note written across the page and photographed with the phone upright
// arrives sideways, and WhatsApp strips the EXIF orientation: the pixels are
// the only truth. MiniMax-M3.1-Flash does not read sideways handwriting: it
// rotates it in its reasoning with made-up coordinates and invents a text
// (prod note, 2026-10-28: 0 of 8 sideways reads, 4 of them a made-up English
// letter). Asked how many degrees turn it upright it sees that it is sideways
// but not which way (4 of 6 right, the rest upside down). Shown the four
// rotations and asked which one reads upright, it picks the right one every
// time in 2-4s (prod note and bill, random order). So the read shows it the
// four, turns the full image accordingly and transcribes that.

const botVisionOrientPrompt = "Son 4 versiones de la misma foto, cada una girada de una forma. ¿En cuál se lee el texto derecho (ni de lado ni del revés)? Responde solo el número: 1, 2, 3 o 4."

const (
	botVisionOrientMaxTokens = 3000
	botVisionOrientTimeout   = 20 * time.Second
	botVisionOrientEffort    = "low"
)

// botVisionUpright returns the image turned upright as JPEG. ok is false when
// the image is to be read as it came: undecodable (WebP), already upright, or
// the orientation could not be told.
func (s *Server) botVisionUpright(ctx context.Context, restaurantID int, data []byte) ([]byte, bool) {
	src, err := botDecodeRGBA(data)
	if err != nil {
		botCheckpoint("wa_bot_image_read_v1", "restaurant_id=%d orientation_skipped err=%v", restaurantID, err)
		return nil, false
	}
	content, order, err := botOrientVersions(src)
	if err != nil {
		botCheckpoint("wa_bot_image_read_v1", "restaurant_id=%d orientation_skipped err=%v", restaurantID, err)
		return nil, false
	}
	reply, err := s.minimaxMessagesText(ctx, restaurantID, "", content, botVisionOrientMaxTokens, botVisionOrientTimeout, botVisionOrientEffort)
	if err != nil {
		botCheckpoint("wa_bot_image_read_v1", "restaurant_id=%d orientation_failed err=%v", restaurantID, err)
		return nil, false
	}
	s.recordAIUsage(ctx, restaurantID, aiFeatureWhatsAppBot, botProviderMiniMax, reply.Model, reply.Usage, false)
	deg, ok := botOrientAnswer(order, reply.Text)
	botCheckpoint("wa_bot_image_read_v1", "restaurant_id=%d orientation=%d understood=%t", restaurantID, deg, ok)
	if !ok || deg == 0 {
		return nil, false
	}
	out, err := botEncodeJPEG(botRotateRGBA(src, deg), 90)
	if err != nil {
		return nil, false
	}
	return out, true
}

// botOrientVersions builds the question: the image at half size in its four
// clockwise rotations, in random order (a fixed order invites a position
// bias). order[i] is the rotation of version i+1.
func botOrientVersions(src *image.RGBA) ([]map[string]any, []int, error) {
	small := botHalfRGBA(src)
	order := []int{0, 90, 180, 270}
	rand.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	content := make([]map[string]any, 0, 2*len(order)+1)
	for i, deg := range order {
		version := small
		if deg != 0 {
			version = botRotateRGBA(small, deg)
		}
		data, err := botEncodeJPEG(version, 85)
		if err != nil {
			return nil, nil, err
		}
		content = append(content,
			map[string]any{"type": "text", "text": fmt.Sprintf("Versión %d:", i+1)},
			minimaxDocumentContent("image/jpeg", data, "")[0])
	}
	content = append(content, map[string]any{"type": "text", "text": botVisionOrientPrompt})
	return content, order, nil
}

// botOrientAnswer maps the version the model picked ("3", "Versión 3") to
// the clockwise rotation that turns the image upright.
func botOrientAnswer(order []int, text string) (int, bool) {
	i := strings.IndexFunc(text, unicode.IsDigit)
	if i < 0 {
		return 0, false
	}
	k := int(text[i] - '0')
	if k < 1 || k > len(order) || (i+1 < len(text) && unicode.IsDigit(rune(text[i+1]))) {
		return 0, false
	}
	return order[k-1], true
}

func botDecodeRGBA(data []byte) (*image.RGBA, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	return rgba, nil
}

func botEncodeJPEG(img image.Image, quality int) ([]byte, error) {
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// botHalfRGBA halves the image (2×2 box average): the four versions only have
// to show which way the text runs.
func botHalfRGBA(src *image.RGBA) *image.RGBA {
	w, h := src.Rect.Dx()/2, src.Rect.Dy()/2
	if w == 0 || h == 0 {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		r0 := src.Pix[2*y*src.Stride:]
		r1 := src.Pix[(2*y+1)*src.Stride:]
		d := dst.Pix[y*dst.Stride:]
		for x := 0; x < 4*w; x++ {
			i := 8*(x/4) + x%4
			d[x] = uint8((int(r0[i]) + int(r0[i+4]) + int(r1[i]) + int(r1[i+4]) + 2) / 4)
		}
	}
	return dst
}

// botRotateRGBA turns src clockwise by 90, 180 or 270 degrees.
func botRotateRGBA(src *image.RGBA, degrees int) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := h, w
	if degrees == 180 {
		dw, dh = w, h
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride : y*src.Stride+w*4]
		for x := 0; x < w; x++ {
			var dx, dy int
			switch degrees {
			case 90:
				dx, dy = h-1-y, x
			case 180:
				dx, dy = w-1-x, h-1-y
			default: // 270
				dx, dy = y, w-1-x
			}
			o := dy*dst.Stride + dx*4
			copy(dst.Pix[o:o+4], row[x*4:x*4+4])
		}
	}
	return dst
}
