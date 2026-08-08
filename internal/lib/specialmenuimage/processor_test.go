package specialmenuimage

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os/exec"
	"testing"
)

// TestNormalizeToWebPWithLimit verifies the parameterized variant enforces the
// caller-provided byte cap. Self-skips if ImageMagick is not on PATH.
func TestNormalizeToWebPWithLimit(t *testing.T) {
	if _, err := exec.LookPath("convert"); err != nil {
		if _, err := exec.LookPath("magick"); err != nil {
			t.Skip("imagemagick (magick/convert) not available")
		}
	}

	// Generate a small 64x64 RGBA PNG in memory.
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	out, err := NormalizeToWebPWithLimit(context.Background(), buf.Bytes(), "tiny.png", "image/png", 50*1024)
	if err != nil {
		t.Fatalf("NormalizeToWebPWithLimit: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("output is empty")
	}
	if len(out) > 50*1024 {
		t.Fatalf("output %d bytes exceeds 50KB cap", len(out))
	}
	// WebP signature: "RIFF" .... "WEBP"
	if !bytes.HasPrefix(out, []byte("RIFF")) || len(out) < 12 || string(out[8:12]) != "WEBP" {
		t.Fatalf("output is not a WebP file (header=%q)", string(out[:min(12, len(out))]))
	}
}
