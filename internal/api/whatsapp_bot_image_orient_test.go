package api

import (
	"image"
	"image/color"
	"testing"
)

// Coordination id: wa_bot_image_read_v1 (orientation)

// The model picks the version that reads upright; the full image has to turn
// the same way that version was turned, or the note ends upside down. A 3×2
// image whose pixels are tagged with their source position checks where
// every corner lands.
func TestBotRotateRGBA_TurnsClockwise(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			src.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 7, A: 255})
		}
	}
	at := func(img *image.RGBA, x, y int) [2]uint8 {
		c := img.RGBAAt(x, y)
		return [2]uint8{c.R, c.G}
	}
	cases := []struct {
		deg  int
		w, h int
		// where the source top-left (0,0) and top-right (2,0) land
		topLeft, topRight [2]int
	}{
		{90, 2, 3, [2]int{1, 0}, [2]int{1, 2}},
		{180, 3, 2, [2]int{2, 1}, [2]int{0, 1}},
		{270, 2, 3, [2]int{0, 2}, [2]int{0, 0}},
	}
	for _, c := range cases {
		dst := botRotateRGBA(src, c.deg)
		if dst.Rect.Dx() != c.w || dst.Rect.Dy() != c.h {
			t.Fatalf("%d°: size %dx%d, want %dx%d", c.deg, dst.Rect.Dx(), dst.Rect.Dy(), c.w, c.h)
		}
		if got := at(dst, c.topLeft[0], c.topLeft[1]); got != [2]uint8{0, 0} {
			t.Errorf("%d°: source top-left not at %v (found source %v)", c.deg, c.topLeft, got)
		}
		if got := at(dst, c.topRight[0], c.topRight[1]); got != [2]uint8{2, 0} {
			t.Errorf("%d°: source top-right not at %v (found source %v)", c.deg, c.topRight, got)
		}
	}
}

// The versions go in random order: the picked number maps back to the
// rotation that version carries, and an answer that is not one of the
// versions leaves the image as it came.
func TestBotOrientAnswer_MapsTheVersionToItsRotation(t *testing.T) {
	order := []int{180, 0, 270, 90}
	for text, want := range map[string]int{"3": 270, "Versión 1": 180, " 2\n": 0, "4.": 90} {
		if got, ok := botOrientAnswer(order, text); !ok || got != want {
			t.Errorf("%q: %d (understood %t), want %d", text, got, ok, want)
		}
	}
	for _, text := range []string{"", "ninguna", "5", "0", "12"} {
		if _, ok := botOrientAnswer(order, text); ok {
			t.Errorf("%q must not be understood as a version", text)
		}
	}
}
